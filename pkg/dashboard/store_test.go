package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

// ingestString writes a JSON-lines blob to a temp file and ingests it —
// the same code path production file ingestion uses.
func ingestString(t *testing.T, store *Store, blob string) (n, skipped int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.log")
	if err := os.WriteFile(path, []byte(blob), 0o600); err != nil {
		t.Fatalf("write temp log: %v", err)
	}
	n, skipped, err := store.IngestFile(path)
	if err != nil {
		t.Fatalf("IngestFile: %v", err)
	}
	return n, skipped
}

func TestIngestParserTolerance(t *testing.T) {
	tests := []struct {
		name        string
		blob        string
		wantN       int
		wantSkipped int
	}{
		{
			name:  "valid record",
			blob:  `{"_etime":"2026-10-09T13:00:00Z","_type":"credential","_host":"1.2.3.4:1","_proto":"ssh","username":"root"}` + "\n",
			wantN: 1,
		},
		{
			name:        "blank lines skipped and counted",
			blob:        "\n\n" + `{"_etime":"2026-10-09T13:00:00Z","_proto":"ssh"}` + "\n\n",
			wantN:       1,
			wantSkipped: 3,
		},
		{
			name:        "malformed JSON skipped and counted, never fatal",
			blob:        `{"_etime":"2026-10-09T13:00:00Z"}` + "\n" + `{"this is not json` + "\n" + `[also broken` + "\n",
			wantN:       1,
			wantSkipped: 2,
		},
		{
			name:  "missing _etime counts in totals",
			blob:  `{"_type":"credential","_proto":"ssh","username":"root"}` + "\n",
			wantN: 1,
		},
		{
			name:  "unparseable _etime counts in totals",
			blob:  `{"_etime":"yesterday","_proto":"ssh"}` + "\n",
			wantN: 1,
		},
		{
			name:  "missing _type defaults to credential",
			blob:  `{"_etime":"2026-10-09T13:00:00Z","_proto":"ftp"}` + "\n",
			wantN: 1,
		},
		{
			name:  "unknown keys are preserved",
			blob:  `{"_etime":"2026-10-09T13:00:00Z","_proto":"dns","questions":"example.com/A","future_key":"v"}` + "\n",
			wantN: 1,
		},
		{
			name:  "empty file ingests cleanly",
			blob:  "",
			wantN: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore()
			n, skipped := ingestString(t, store, tt.blob)
			if n != tt.wantN {
				t.Errorf("ingested %d records, want %d", n, tt.wantN)
			}
			if skipped != tt.wantSkipped {
				t.Errorf("skipped %d lines, want %d", skipped, tt.wantSkipped)
			}
			if got := store.Summary().Total; got != tt.wantN {
				t.Errorf("summary total %d, want %d", got, tt.wantN)
			}
		})
	}
}

func TestIngestMissingEtimeExcludedFromTimeline(t *testing.T) {
	store := NewStore()
	ingestString(t, store,
		`{"_etime":"2026-10-09T13:00:00Z","_proto":"ssh"}`+"\n"+
			`{"_etime":"nope","_proto":"ssh"}`+"\n"+
			`{"_proto":"ssh"}`+"\n")
	sum := store.Summary()
	if sum.Total != 3 {
		t.Fatalf("total = %d, want 3 (untimed records still count)", sum.Total)
	}
	sumBuckets := 0
	for _, b := range sum.Timeline {
		sumBuckets += b.Count
	}
	if sumBuckets != 1 {
		t.Errorf("timeline counts %d records, want 1 (only parseable _etime)", sumBuckets)
	}
	if len(sum.Timeline) != 1 {
		t.Errorf("timeline has %d buckets, want 1", len(sum.Timeline))
	}
}

func TestIngestUnknownKeysPreserved(t *testing.T) {
	store := NewStore()
	ingestString(t, store, `{"_etime":"2026-10-09T13:00:00Z","_proto":"dns","questions":"example.com/A"}`+"\n")
	records, _ := store.RecordsPage(1, 0)
	if len(records) != 1 || records[0]["questions"] != "example.com/A" {
		t.Errorf("unknown/custom key not preserved: %v", records)
	}
}

func TestMultipleFilesConcatenateOrderedByEtime(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, blob string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(blob), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}

	// Ingest out of chronological order on purpose.
	a := writeFile("a.log", `{"_etime":"2026-10-09T13:00:00Z","_proto":"ssh","username":"first"}`+"\n"+
		`{"_etime":"2026-10-09T13:05:00Z","_proto":"ssh","username":"third"}`+"\n")
	b := writeFile("b.log", `{"_etime":"2026-10-09T13:02:00Z","_proto":"ftp","username":"second"}`+"\n")

	store := NewStore()
	for _, path := range []string{a, b} {
		if _, _, err := store.IngestFile(path); err != nil {
			t.Fatalf("IngestFile(%s): %v", path, err)
		}
	}

	records, total := store.RecordsPage(10, 0)
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	wantOrder := []string{"third", "second", "first"} // newest first
	for i, want := range wantOrder {
		if got := records[i]["username"]; got != want {
			t.Errorf("records[%d] = %s, want %s", i, got, want)
		}
	}
}

func TestIngestFileError(t *testing.T) {
	store := NewStore()
	if _, _, err := store.IngestFile(filepath.Join(t.TempDir(), "missing.log")); err == nil {
		t.Error("IngestFile on a missing path should return an error")
	}
}

func TestLiveSinkAdd(t *testing.T) {
	// The live-mode wiring appends store.Add as an OutputWriter; exercise
	// that path directly.
	store := NewStore()
	store.Add(map[string]string{
		"_etime": "2026-10-09T13:00:00Z", "_type": "credential",
		"_host": "1.2.3.4:5", "_proto": "ssh", "username": "root", "password": "pw",
	})
	sum := store.Summary()
	if sum.Total != 1 || sum.ByProtocol["ssh"] != 1 {
		t.Errorf("live sink add: total = %d, ssh = %d", sum.Total, sum.ByProtocol["ssh"])
	}
}

func TestConcurrentAddAndRead(t *testing.T) {
	// Run with -race: live capture writes while the API polls.
	store := NewStore()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			store.Add(map[string]string{
				"_etime": "2026-10-09T13:00:00Z", "_proto": "ssh", "username": "root",
			})
		}
	}()
	for i := 0; i < 50; i++ {
		_ = store.Summary()
		_, _ = store.RecordsPage(10, 0)
	}
	<-done
}
