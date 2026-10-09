package dashboard

import (
	"testing"
	"time"
)

// loadFixture ingests the committed sample.log fixture and returns the
// store plus its summary. The fixture's expected aggregates are asserted
// in TestSummaryFixtureAggregates.
func loadFixture(t *testing.T) (*Store, Summary) {
	t.Helper()
	store := NewStore()
	if _, _, err := store.IngestFile("testdata/sample.log"); err != nil {
		t.Fatalf("IngestFile(sample.log): %v", err)
	}
	return store, store.Summary()
}

// TestSummaryFixtureAggregates pins the exact numbers the committed
// fixture must produce: 13 JSON records, 4 skipped lines (2 blank,
// 2 malformed), across protocols/types/methods with one access record
// each for http and dns.
func TestSummaryFixtureAggregates(t *testing.T) {
	_, sum := loadFixture(t)

	if sum.Total != 13 {
		t.Errorf("total = %d, want 13", sum.Total)
	}
	if sum.SkippedLines != 4 {
		t.Errorf("skipped_lines = %d, want 4", sum.SkippedLines)
	}

	wantProtocols := map[string]int{"ssh": 4, "http": 4, "ldap": 1, "ftp": 2, "snmp": 1, "dns": 1}
	for proto, want := range wantProtocols {
		if got := sum.ByProtocol[proto]; got != want {
			t.Errorf("by_protocol[%s] = %d, want %d", proto, got, want)
		}
	}
	if len(sum.ByProtocol) != len(wantProtocols) {
		t.Errorf("by_protocol has unexpected entries: %v", sum.ByProtocol)
	}

	if sum.ByType["credential"] != 11 || sum.ByType["access"] != 2 {
		t.Errorf("by_type = %v, want credential 11 / access 2", sum.ByType)
	}

	wantMethods := map[string]int{"password": 3, "pubkey": 1, "basic": 2, "NTLMSSP": 1}
	for method, want := range wantMethods {
		if got := sum.ByMethod[method]; got != want {
			t.Errorf("by_method[%s] = %d, want %d", method, got, want)
		}
	}

	if sum.Window.First != "2026-10-09T13:02:11Z" || sum.Window.Last != "2026-10-09T13:09:00Z" {
		t.Errorf("window = %+v, want first 13:02:11Z / last 13:09:00Z", sum.Window)
	}

	// Timeline: minute buckets from 13:02 to 13:09, zero-filled, covering
	// only the 11 records with parseable _etime.
	wantCounts := []int{1, 1, 0, 2, 2, 2, 2, 1}
	if len(sum.Timeline) != len(wantCounts) {
		t.Fatalf("timeline has %d buckets, want %d", len(sum.Timeline), len(wantCounts))
	}
	for i, want := range wantCounts {
		if got := sum.Timeline[i].Count; got != want {
			t.Errorf("timeline[%d] = %d, want %d", i, got, want)
		}
	}
	if first := sum.Timeline[0].Bucket; first != "2026-10-09T13:02Z" {
		t.Errorf("first bucket = %s, want 2026-10-09T13:02Z", first)
	}
}

func TestSummaryTopLists(t *testing.T) {
	_, sum := loadFixture(t)

	// Hosts: 10.0.0.7:9992 (3) beats 1.2.3.4:1361 and 10.0.0.7:51204 (2 each),
	// name-ascending tiebreak; 9.9.9.9 hosts appear despite bad timestamps.
	if len(sum.TopHosts) < 3 {
		t.Fatalf("top_hosts has %d entries, want at least 3", len(sum.TopHosts))
	}
	if sum.TopHosts[0].Host != "10.0.0.7:9992" || sum.TopHosts[0].Count != 3 {
		t.Errorf("top_hosts[0] = %+v, want 10.0.0.7:9992 x3", sum.TopHosts[0])
	}
	if sum.TopHosts[1].Host != "1.2.3.4:1361" || sum.TopHosts[2].Host != "10.0.0.7:51204" {
		t.Errorf("tie order wrong: %+v", sum.TopHosts)
	}

	// Usernames: root (3), admin (2), then the five singles in name order.
	if len(sum.TopUsernames) < 3 {
		t.Fatalf("top_usernames has %d entries, want at least 3", len(sum.TopUsernames))
	}
	if sum.TopUsernames[0].Username != "root" || sum.TopUsernames[0].Count != 3 {
		t.Errorf("top_usernames[0] = %+v, want root x3", sum.TopUsernames[0])
	}
	if sum.TopUsernames[1].Username != "admin" || sum.TopUsernames[1].Count != 2 {
		t.Errorf("top_usernames[1] = %+v, want admin x2", sum.TopUsernames[1])
	}
}

func TestSummaryDistinctCounts(t *testing.T) {
	_, sum := loadFixture(t)
	// 9 distinct hosts, 7 distinct usernames on the fixture — including
	// hosts seen only on records with bad timestamps.
	if sum.DistinctHosts != 9 {
		t.Errorf("distinct_hosts = %d, want 9", sum.DistinctHosts)
	}
	if sum.DistinctUsernames != 7 {
		t.Errorf("distinct_usernames = %d, want 7", sum.DistinctUsernames)
	}
}

func TestTopNCapTen(t *testing.T) {
	// 12 distinct hosts: the top list must cap at 10, highest first.
	store := NewStore()
	for i := 0; i < 12; i++ {
		store.Add(map[string]string{
			"_etime": "2026-10-09T13:00:00Z", "_proto": "ssh",
			"_host": "10.0.0." + string(rune('a'+i)) + ":1",
		})
	}
	sum := store.Summary()
	if len(sum.TopHosts) != TopN {
		t.Errorf("top_hosts capped at %d entries, want %d", len(sum.TopHosts), TopN)
	}
	for i := 1; i < len(sum.TopHosts); i++ {
		if sum.TopHosts[i-1].Count < sum.TopHosts[i].Count {
			t.Errorf("top_hosts not count-descending at %d: %+v", i, sum.TopHosts)
		}
	}
}

func TestTopNMostlyZeroCounts(t *testing.T) {
	// Regression: a cap larger than the entry count must not pad.
	store := NewStore()
	store.Add(map[string]string{"_etime": "2026-10-09T13:00:00Z", "_proto": "ssh", "_host": "h1"})
	store.Add(map[string]string{"_etime": "2026-10-09T13:00:00Z", "_proto": "ssh", "_host": "h2"})
	sum := store.Summary()
	if len(sum.TopHosts) != 2 {
		t.Errorf("top_hosts = %d entries, want 2", len(sum.TopHosts))
	}
}

// TestBucketWidthEdges pins the adaptive bucket boundaries: minute buckets
// up to a 2h span, hourly up to 72h, daily beyond.
func TestBucketWidthEdges(t *testing.T) {
	tests := []struct {
		span time.Duration
		want time.Duration
	}{
		{span: 0, want: time.Minute},
		{span: time.Minute, want: time.Minute},
		{span: 2 * time.Hour, want: time.Minute},           // exactly at the edge
		{span: 2*time.Hour + time.Second, want: time.Hour}, // one second past
		{span: 72 * time.Hour, want: time.Hour},            // exactly at the edge
		{span: 72*time.Hour + time.Second, want: 24 * time.Hour},
	}
	for _, tt := range tests {
		if got := bucketWidth(tt.span); got != tt.want {
			t.Errorf("bucketWidth(%v) = %v, want %v", tt.span, got, tt.want)
		}
	}
}

// TestBucketBoundariesInTimeline drives the edges through the real
// aggregation path: a 2h00m span yields minute buckets, one second more
// yields hour buckets.
func TestBucketBoundariesInTimeline(t *testing.T) {
	mk := func(first, last time.Time) Summary {
		recs := []record{
			newRecord(map[string]string{"_etime": first.Format(time.RFC3339), "_proto": "ssh"}),
			newRecord(map[string]string{"_etime": last.Format(time.RFC3339), "_proto": "ssh"}),
		}
		return summarize(recs, 0)
	}

	base := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

	atEdge := mk(base, base.Add(2*time.Hour))
	if got := len(atEdge.Timeline); got != 121 { // 12:00..14:00 inclusive, minute buckets
		t.Errorf("2h00m span: %d minute buckets, want 121", got)
	}

	pastEdge := mk(base, base.Add(2*time.Hour+time.Second))
	if got := len(pastEdge.Timeline); got != 3 { // 12:00, 13:00, 14:00 hourly
		t.Errorf("2h00m01s span: %d hourly buckets, want 3", got)
	}
}

func TestSummarizeUntimedOnly(t *testing.T) {
	// Records with no parseable _etime: totals count, window and timeline
	// stay empty.
	recs := []record{
		newRecord(map[string]string{"_proto": "ssh"}),
		newRecord(map[string]string{"_etime": "garbage", "_proto": "ftp"}),
	}
	sum := summarize(recs, 0)
	if sum.Total != 2 {
		t.Errorf("total = %d, want 2", sum.Total)
	}
	if sum.Window.First != "" || sum.Window.Last != "" {
		t.Errorf("window = %+v, want empty", sum.Window)
	}
	if len(sum.Timeline) != 0 {
		t.Errorf("timeline = %d buckets, want 0", len(sum.Timeline))
	}
}
