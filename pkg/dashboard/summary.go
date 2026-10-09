package dashboard

import (
	"sort"
	"time"
)

// TopN caps the top_hosts and top_usernames lists.
const TopN = 10

// Window is the observed capture-time span, RFC3339 in UTC. Both fields are
// empty when no record carries a parseable _etime.
type Window struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

// HostCount is one entry of the top source hosts list.
type HostCount struct {
	Host  string `json:"host"`
	Count int    `json:"count"`
}

// UserCount is one entry of the top usernames list.
type UserCount struct {
	Username string `json:"username"`
	Count    int    `json:"count"`
}

// Bucket is one timeline bar.
type Bucket struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

// Summary is the GET /api/summary response: the whole-store picture.
// Source, Live, Reveal, and Error are injected from server Options on each
// request, not aggregated from records.
type Summary struct {
	Total             int            `json:"total"`
	SkippedLines      int            `json:"skipped_lines"`
	Source            string         `json:"source"`
	Live              bool           `json:"live"`
	Reveal            bool           `json:"reveal"`
	Error             string         `json:"error,omitempty"`
	Window            Window         `json:"window"`
	ByProtocol        map[string]int `json:"by_protocol"`
	ByType            map[string]int `json:"by_type"`
	ByMethod          map[string]int `json:"by_method"`
	DistinctHosts     int            `json:"distinct_hosts"`
	DistinctUsernames int            `json:"distinct_usernames"`
	TopHosts          []HostCount    `json:"top_hosts"`
	TopUsernames      []UserCount    `json:"top_usernames"`
	Timeline          []Bucket       `json:"timeline"`
}

// countPair is the internal shape for top-N ranking.
type countPair struct {
	name  string
	count int
}

// topPairs ranks counts by count descending, name ascending, capped at n.
// The name tiebreak keeps ordering deterministic across polls.
func topPairs(counts map[string]int, n int) []countPair {
	pairs := make([]countPair, 0, len(counts))
	for k, v := range counts {
		pairs = append(pairs, countPair{name: k, count: v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].name < pairs[j].name
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	return pairs
}

// bucketWidth picks the timeline bucket size for the observed span:
// minute buckets up to 2 h, hourly up to 72 h, daily beyond.
func bucketWidth(span time.Duration) time.Duration {
	switch {
	case span <= 2*time.Hour:
		return time.Minute
	case span <= 72*time.Hour:
		return time.Hour
	default:
		return 24 * time.Hour
	}
}

// bucketStart truncates t to the start of its bucket, in UTC. Day boundaries
// are UTC midnights; there is no DST in UTC.
func bucketStart(t time.Time, width time.Duration) time.Time {
	t = t.UTC()
	switch width {
	case time.Minute:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	case time.Hour:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
	default:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
}

// formatBucket renders a bucket label: date with hour+minute for minute and
// hourly buckets ("2026-10-09T13:00Z"), bare date for daily buckets.
func formatBucket(t time.Time, width time.Duration) string {
	t = t.UTC()
	if width == 24*time.Hour {
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01-02T15:04") + "Z"
}

// Summary computes the aggregate view over the store's current records.
func (s *Store) Summary() Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return summarize(s.records, s.skipped)
}

// summarize is the pure aggregation core, split from Store.Summary for
// direct testing.
func summarize(recs []record, skipped int) Summary {
	sum := Summary{
		Total:        len(recs),
		SkippedLines: skipped,
		ByProtocol:   map[string]int{},
		ByType:       map[string]int{},
		ByMethod:     map[string]int{},
		TopHosts:     []HostCount{},
		TopUsernames: []UserCount{},
		Timeline:     []Bucket{},
	}

	hosts := map[string]int{}
	users := map[string]int{}

	var first, last time.Time
	var timedCount int

	for _, r := range recs {
		sum.ByType[r.rtype]++
		if proto := r.fields["_proto"]; proto != "" {
			sum.ByProtocol[proto]++
		}
		if method := r.fields["method"]; method != "" {
			sum.ByMethod[method]++
		}
		if host := r.fields["_host"]; host != "" {
			hosts[host]++
		}
		if user := r.fields["username"]; user != "" {
			users[user]++
		}
		if r.timed {
			timedCount++
			if timedCount == 1 || r.when.Before(first) {
				first = r.when
			}
			if timedCount == 1 || r.when.After(last) {
				last = r.when
			}
		}
	}

	for _, p := range topPairs(hosts, TopN) {
		sum.TopHosts = append(sum.TopHosts, HostCount{Host: p.name, Count: p.count})
	}
	for _, p := range topPairs(users, TopN) {
		sum.TopUsernames = append(sum.TopUsernames, UserCount{Username: p.name, Count: p.count})
	}
	sum.DistinctHosts = len(hosts)
	sum.DistinctUsernames = len(users)

	if timedCount > 0 {
		sum.Window = Window{
			First: first.Format(time.RFC3339),
			Last:  last.Format(time.RFC3339),
		}
		sum.Timeline = buildTimeline(recs, first, last)
	}
	return sum
}

// buildTimeline renders contiguous buckets from the first to the last timed
// record, zero-filling gaps so the histogram shows attack lulls honestly.
func buildTimeline(recs []record, first, last time.Time) []Bucket {
	width := bucketWidth(last.Sub(first))
	counts := map[time.Time]int{}
	for _, r := range recs {
		if r.timed {
			counts[bucketStart(r.when, width)]++
		}
	}

	out := []Bucket{}
	for t := bucketStart(first, width); !t.After(bucketStart(last, width)); t = t.Add(width) {
		out = append(out, Bucket{Bucket: formatBucket(t, width), Count: counts[t]})
	}
	return out
}
