package dashboard

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// DefaultAddr is the bind address used when no --addr is given: loopback
// only. Exposing the dashboard off-loopback is always an explicit operator
// choice.
const DefaultAddr = "127.0.0.1:8080"

// Records paging bounds: garbage or out-of-range limits fall back to the
// default, and no request can pull more than maxRecordsLimit rows.
const (
	defaultRecordsLimit = 50
	maxRecordsLimit     = 200
)

// Options configures the dashboard HTTP server.
type Options struct {
	// Addr is the bind address. Empty means DefaultAddr (loopback).
	Addr string
	// RevealSecrets disables redaction in API responses. There is
	// deliberately no query parameter that can unmask: only this
	// server-side flag, so a shared URL can never self-reveal.
	RevealSecrets bool
	// SourceLabel names what the dashboard is watching, e.g. the ingested
	// log files or "live capture". Surfaced in the UI header.
	SourceLabel string
	// Live marks the dashboard as attached to a running capture (the
	// --dashboard-addr mode). The UI header phrases the status differently
	// for live vs standalone watching.
	Live bool
	// SourceError describes a problem with the watched source — e.g. a
	// seed log that could not be read. When set, the UI keeps any last
	// known data on screen and names the problem in the header.
	SourceError string
}

// NewServer builds the dashboard HTTP server. The store is read on every
// request, so the UI picks up new captures on each poll. The returned
// server is not running; callers start it with ListenAndServe.
func NewServer(store *Store, opts Options) (*http.Server, error) {
	addr := opts.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("invalid bind address %q: %w", addr, err)
	}
	return &http.Server{
		Addr:              addr,
		Handler:           newHandler(store, opts),
		ReadHeaderTimeout: 5 * time.Second,
	}, nil
}

// newHandler builds the route table.
func newHandler(store *Store, opts Options) http.Handler {
	h := &handler{store: store, opts: opts}

	mux := http.NewServeMux()
	registerUI(mux)
	mux.HandleFunc("GET /api/summary", h.handleSummary)
	mux.HandleFunc("GET /api/records", h.handleRecords)
	return mux
}

type handler struct {
	store *Store
	opts  Options
}

func (h *handler) handleSummary(w http.ResponseWriter, r *http.Request) {
	sum := h.store.Summary()
	sum.Source = h.opts.SourceLabel
	sum.Live = h.opts.Live
	sum.Reveal = h.opts.RevealSecrets
	sum.Error = h.opts.SourceError
	writeJSON(w, http.StatusOK, sum)
}

// recordsResponse is /api/records: one page of redacted records, newest
// first.
type recordsResponse struct {
	Total   int                 `json:"total"`
	Limit   int                 `json:"limit"`
	Offset  int                 `json:"offset"`
	Records []map[string]string `json:"records"`
}

func (h *handler) handleRecords(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"))
	offset := parseOffset(r.URL.Query().Get("offset"))

	raw, total := h.store.RecordsPage(limit, offset)
	records := make([]map[string]string, 0, len(raw))
	for _, fields := range raw {
		records = append(records, RedactRecord(fields, h.opts.RevealSecrets))
	}

	writeJSON(w, http.StatusOK, recordsResponse{
		Total:   total,
		Limit:   limit,
		Offset:  offset,
		Records: records,
	})
}

// parseLimit clamps the limit query parameter: 1..maxRecordsLimit, with the
// default applied when missing, zero, negative, or unparseable.
func parseLimit(raw string) int {
	limit := defaultRecordsLimit
	if v, err := strconv.Atoi(raw); err == nil && v > 0 {
		limit = v
	}
	return min(limit, maxRecordsLimit)
}

// parseOffset clamps the offset query parameter: >= 0, with 0 applied when
// missing, negative, or unparseable.
func parseOffset(raw string) int {
	if v, err := strconv.Atoi(raw); err == nil && v > 0 {
		return v
	}
	return 0
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// The page polls every 5 s; never let an intermediary cache a snapshot.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	// Headers are already sent; a failing encode leaves a truncated body,
	// which the poller treats like any transport error.
	_ = json.NewEncoder(w).Encode(body)
}
