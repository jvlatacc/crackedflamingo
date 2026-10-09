// Package dashboard renders flamingo capture logs as a local web dashboard.
//
// The Store is an in-memory, thread-safe view over capture records, either
// ingested from JSON-lines log files (the `flamingo dashboard` subcommand)
// or fed live through an OutputWriter sink during capture. Records are kept
// exactly as captured; redaction happens at read time (see redact.go) so the
// log file stays the single source of truth.
package dashboard

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"
)

// defaultRecordType mirrors RecordWriter.Record: records are credentials
// unless the listener says otherwise.
const defaultRecordType = "credential"

// record is one capture: the raw fields as logged, plus the parsed capture
// time. A record with a missing or unparseable _etime still counts toward
// totals but is excluded from the timeline.
type record struct {
	fields map[string]string
	when   time.Time // UTC; zero value unless timed
	timed  bool
	rtype  string // _type with the defaultRecordType default applied
}

func newRecord(fields map[string]string) record {
	r := record{fields: fields, rtype: fields["_type"]}
	if r.rtype == "" {
		r.rtype = defaultRecordType
	}
	if et, err := time.Parse(time.RFC3339, fields["_etime"]); err == nil {
		r.when = et.UTC()
		r.timed = true
	}
	return r
}

// Store holds capture records in memory and computes aggregates on read.
// All methods are safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	records []record
	skipped int
}

// NewStore returns an empty capture store.
func NewStore() *Store { return &Store{} }

// Add stores one live record. It is the sink half of an OutputWriter and is
// cheap enough to call from the capture pipeline; it never blocks on I/O.
func (s *Store) Add(rec map[string]string) {
	r := newRecord(rec)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
}

// IngestFile parses a JSON-lines capture log, one JSON object per line, as
// written by flamingo's file outputs. Blank and unparseable lines are
// skipped and counted; a corrupt line never aborts ingestion. A file that
// cannot be opened returns an error so the caller decides whether that is
// fatal (the standalone command treats it as fatal, live seeding does not).
func (s *Store) IngestFile(path string) (n int, skipped int, err error) {
	fd, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer fd.Close()

	scanner := bufio.NewScanner(fd)
	// Captured values (long URLs, authorization headers) can exceed the
	// default 64 KiB token size.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			skipped++
			continue
		}
		fields := map[string]string{}
		if e := json.Unmarshal(line, &fields); e != nil {
			skipped++
			continue
		}
		s.Add(fields)
		n++
	}

	s.mu.Lock()
	s.skipped += skipped
	s.mu.Unlock()

	if e := scanner.Err(); e != nil {
		return n, skipped, e
	}
	return n, skipped, nil
}

// RecordsPage returns one page of raw records, newest first by _etime.
// Records without a parseable _etime sort last, arrival order preserved
// among ties. The returned maps are the store's own raw records — callers
// must not mutate them; apply RedactRecord before serving them.
func (s *Store) RecordsPage(limit, offset int) ([]map[string]string, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sorted := make([]record, len(s.records))
	copy(sorted, s.records)
	// Stable sort keeps arrival order among records that share a timestamp
	// and lets untimed records (zero time) sink to the end.
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].when.After(sorted[j].when)
	})

	if offset > len(sorted) {
		offset = len(sorted)
	}
	end := min(offset+limit, len(sorted))

	out := make([]map[string]string, 0, end-offset)
	for _, r := range sorted[offset:end] {
		out = append(out, r.fields)
	}
	return out, len(sorted)
}
