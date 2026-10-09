package dashboard

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// handlerFor builds a test handler over the committed fixture.
func handlerFor(t *testing.T, opts Options) http.Handler {
	t.Helper()
	store := NewStore()
	if _, _, err := store.IngestFile("testdata/sample.log"); err != nil {
		t.Fatalf("IngestFile(sample.log): %v", err)
	}
	return newHandler(store, opts)
}

func TestIndexServesEmbeddedPage(t *testing.T) {
	srv := httptest.NewServer(handlerFor(t, Options{SourceLabel: "testdata/sample.log"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, resp.Body); err != nil {
		t.Fatalf("read index: %v", err)
	}
	page := buf.String()
	// The page is embedded in the binary and wired to the API endpoints.
	for _, want := range []string{"flamingo", "app.js", "style.css"} {
		if !strings.Contains(page, want) {
			t.Errorf("index page missing %q", want)
		}
	}
}

func TestSummaryShapeAndInjection(t *testing.T) {
	srv := httptest.NewServer(handlerFor(t, Options{
		Addr:        "127.0.0.1:9999",
		SourceLabel: "fixture.log",
		Live:        true,
		SourceError: "fixture.log: boom",
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/summary")
	if err != nil {
		t.Fatalf("GET /api/summary: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	for _, key := range []string{
		"total", "skipped_lines", "source", "live", "reveal", "window",
		"by_protocol", "by_type", "by_method", "top_hosts", "top_usernames",
		"timeline", "distinct_hosts", "distinct_usernames", "error",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("summary missing key %q", key)
		}
	}
	if body["total"].(float64) != 13 {
		t.Errorf("total = %v, want 13", body["total"])
	}
	if body["skipped_lines"].(float64) != 4 {
		t.Errorf("skipped_lines = %v, want 4", body["skipped_lines"])
	}
	if body["source"] != "fixture.log" || body["live"] != true || body["error"] != "fixture.log: boom" {
		t.Errorf("injected status wrong: source=%v live=%v source_error=%v", body["source"], body["live"], body["source_error"])
	}
	if body["reveal"] != false {
		t.Errorf("reveal should default false, got %v", body["reveal"])
	}
}

func TestRecordsPagingNewestFirst(t *testing.T) {
	srv := httptest.NewServer(handlerFor(t, Options{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/records?limit=2&offset=0")
	if err != nil {
		t.Fatalf("GET /api/records: %v", err)
	}
	defer resp.Body.Close()

	var body struct {
		Total   int                 `json:"total"`
		Limit   int                 `json:"limit"`
		Offset  int                 `json:"offset"`
		Records []map[string]string `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode records: %v", err)
	}
	if body.Total != 13 || body.Limit != 2 || body.Offset != 0 {
		t.Errorf("paging envelope = %d/%d/%d, want 13/2/0", body.Total, body.Limit, body.Offset)
	}
	if len(body.Records) != 2 {
		t.Fatalf("got %d records, want 2", len(body.Records))
	}
	// Newest first: 13:09:00Z (test/http), then 13:08:30Z (admin/ftp).
	if body.Records[0]["_etime"] != "2026-10-09T13:09:00Z" || body.Records[1]["_etime"] != "2026-10-09T13:08:30Z" {
		t.Errorf("page order wrong: %s, %s", body.Records[0]["_etime"], body.Records[1]["_etime"])
	}
}

func TestRecordsLimitClamping(t *testing.T) {
	srv := httptest.NewServer(handlerFor(t, Options{}))
	defer srv.Close()

	tests := []struct {
		raw      string
		wantPage int // records returned
		wantLim  int // limit echoed
	}{
		{raw: "", wantPage: 13, wantLim: defaultRecordsLimit},
		{raw: "limit=0", wantPage: 13, wantLim: defaultRecordsLimit},
		{raw: "limit=-3", wantPage: 13, wantLim: defaultRecordsLimit},
		{raw: "limit=abc", wantPage: 13, wantLim: defaultRecordsLimit},
		{raw: "limit=99999", wantPage: 13, wantLim: maxRecordsLimit},
		{raw: "limit=2", wantPage: 2, wantLim: 2},
	}
	for _, tt := range tests {
		t.Run("q="+tt.raw, func(t *testing.T) {
			url := srv.URL + "/api/records"
			if tt.raw != "" {
				url += "?" + tt.raw
			}
			resp, err := http.Get(url)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()
			var body struct {
				Total   int `json:"total"`
				Limit   int `json:"limit"`
				Records []struct {
					Password string `json:"password"`
				} `json:"records"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Limit != tt.wantLim {
				t.Errorf("limit = %d, want %d", body.Limit, tt.wantLim)
			}
			if len(body.Records) != tt.wantPage {
				t.Errorf("records = %d, want %d", len(body.Records), tt.wantPage)
			}
		})
	}
}

func TestRecordsOffsetClamping(t *testing.T) {
	srv := httptest.NewServer(handlerFor(t, Options{}))
	defer srv.Close()

	// Negative offset clamps to 0; offset past the end yields an empty page.
	resp, _ := http.Get(srv.URL + "/api/records?limit=5&offset=-7")
	var body struct {
		Offset  int `json:"offset"`
		Records []struct {
			Password string `json:"password"`
		} `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if body.Offset != 0 || len(body.Records) != 5 {
		t.Errorf("negative offset: offset = %d, records = %d, want 0/5", body.Offset, len(body.Records))
	}

	resp2, _ := http.Get(srv.URL + "/api/records?limit=5&offset=999")
	var body2 struct {
		Records []map[string]string `json:"records"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&body2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp2.Body.Close()
	if len(body2.Records) != 0 {
		t.Errorf("offset past end returned %d records, want 0", len(body2.Records))
	}
}

func TestAPIRedactsByDefaultAndRevealsOnFlag(t *testing.T) {
	// Masked: the plaintext secrets must not appear anywhere in the payload.
	masked := httptest.NewServer(handlerFor(t, Options{}))
	resp, _ := http.Get(masked.URL + "/api/records?limit=100")
	maskedBody := readAllString(t, resp)
	resp.Body.Close()
	for _, secret := range []string{"hunter2", "letmein", "letmein123", "ldapsecret", "NtOw.", "YWRtaW46YWRtaW4="} {
		if strings.Contains(maskedBody, secret) {
			t.Errorf("masked API response contains plaintext secret %q", secret)
		}
	}
	if !strings.Contains(maskedBody, MaskedValue) {
		t.Error("masked API response does not contain the placeholder")
	}

	// Revealed: the same secret appears verbatim.
	revealed := httptest.NewServer(handlerFor(t, Options{RevealSecrets: true}))
	resp2, _ := http.Get(revealed.URL + "/api/records?limit=100")
	revealedBody := readAllString(t, resp2)
	resp2.Body.Close()
	if !strings.Contains(revealedBody, "hunter2") {
		t.Error("revealed API response does not contain the original secret")
	}
}

func TestSummaryRedactsTopUsernamesConsistently(t *testing.T) {
	// Top lists carry no secret material, but the summary payload as a
	// whole must never leak plaintext either.
	srv := httptest.NewServer(handlerFor(t, Options{}))
	resp, _ := http.Get(srv.URL + "/api/summary")
	body := readAllString(t, resp)
	resp.Body.Close()
	if strings.Contains(body, "hunter2") || strings.Contains(body, "letmein") {
		t.Error("summary response contains plaintext secrets")
	}
}

func TestUnknownAPIPathIs404(t *testing.T) {
	srv := httptest.NewServer(handlerFor(t, Options{}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/nonexistent")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestParseLimitUnit pins the clamping rules at the parser level.
func TestParseLimitUnit(t *testing.T) {
	tests := map[string]int{
		"":       defaultRecordsLimit,
		"0":      defaultRecordsLimit,
		"-1":     defaultRecordsLimit,
		"abc":    defaultRecordsLimit,
		"2":      2,
		"999999": maxRecordsLimit,
	}
	for raw, want := range tests {
		if got := parseLimit(raw); got != want {
			t.Errorf("parseLimit(%q) = %d, want %d", raw, got, want)
		}
	}
}

// TestStaticContentTypes pins the served Content-Type for every embedded
// asset. The types must not depend on the host's /etc/mime.types, which
// minimal containers (where honeypots run) lack — files would degrade to
// sniffed text/plain and browsers would refuse the stylesheet and script.
func TestStaticContentTypes(t *testing.T) {
	ts := httptest.NewServer(handlerFor(t, Options{}))
	defer ts.Close()

	for _, tt := range []struct {
		file     string
		wantType string
	}{
		{"static/style.css", "text/css; charset=utf-8"},
		{"static/app.js", "text/javascript; charset=utf-8"},
	} {
		resp, err := http.Get(ts.URL + "/" + tt.file)
		if err != nil {
			t.Fatalf("GET %s: %v", tt.file, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", tt.file, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != tt.wantType {
			t.Errorf("GET %s Content-Type = %q, want %q (host mime.types must not leak in)", tt.file, got, tt.wantType)
		}
		if len(body) == 0 {
			t.Errorf("GET %s: empty body", tt.file)
		}
	}

	// Unknown paths under /static/ 404 rather than leak the index.
	resp, err := http.Get(ts.URL + "/static/nope.css")
	if err != nil {
		t.Fatalf("GET /static/nope.css: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /static/nope.css status = %d, want 404", resp.StatusCode)
	}
}

// TestDefaultBindIsLoopback covers criterion 6: with no explicit address the
// server targets the loopback default, and an actual bind proves loopback.
func TestDefaultBindIsLoopback(t *testing.T) {
	store := NewStore()
	srv, err := NewServer(store, Options{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv.Addr != DefaultAddr {
		t.Fatalf("default addr = %q, want %q", srv.Addr, DefaultAddr)
	}

	// Bind on a kernel-chosen port and prove the listener is loopback.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	proof, err := NewServer(store, Options{Addr: ln.Addr().String()})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go proof.Serve(ln)

	client := &http.Client{}
	resp, err := client.Get("http://" + ln.Addr().String() + "/api/summary")
	if err != nil {
		t.Fatalf("GET over loopback listener: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("loopback bind status = %d, want 200", resp.StatusCode)
	}
	host, _, _ := net.SplitHostPort(ln.Addr().String())
	if host != "127.0.0.1" {
		t.Errorf("bound host = %q, want 127.0.0.1", host)
	}
}

// --- small helpers -------------------------------------------------------

func readAllString(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.String()
}
