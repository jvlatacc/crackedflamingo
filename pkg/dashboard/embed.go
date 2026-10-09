package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

// staticFS holds the single-page UI. Embedded so the dashboard ships in
// the one static binary — no CDN assets, no npm, nothing to serve alongside
// the binary.
//
//go:embed static
var staticFS embed.FS

// registerUI mounts the embedded single-page UI. / serves index.html;
// /static/ serves the stylesheet and script referenced by it.
func registerUI(mux *http.ServeMux) {
	mux.HandleFunc("GET /", handleIndex)

	staticRoot, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Cannot happen: the embed directive guarantees the directory.
		panic("dashboard: embedded static tree missing: " + err.Error())
	}
	mux.Handle("GET /static/", http.FileServerFS(staticRoot))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	// The route table sends non-root paths to /static/ or a 404; anything
	// that still lands here gets the page.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, err := fs.ReadFile(staticFS, "static/index.html")
	if err != nil {
		// Cannot happen for embedded files; fail loudly rather than serve
		// an empty page.
		http.Error(w, "dashboard: embedded index missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(page)
}
