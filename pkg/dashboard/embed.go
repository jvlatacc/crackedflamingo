package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticFS holds the single-page UI. Embedded so the dashboard ships in
// the one static binary — no CDN assets, no npm, nothing to serve alongside
// the binary.
//
//go:embed static
var staticFS embed.FS

// staticTypes pins Content-Type by extension. http.FileServer would
// consult the host's /etc/mime.types, which minimal containers (where
// honeypots run) often lack — the files then degrade to sniffed
// text/plain and browsers refuse the stylesheet and script. The type map
// makes serving deterministic everywhere.
var staticTypes = map[string]string{
	".css": "text/css; charset=utf-8",
	".js":  "text/javascript; charset=utf-8",
	".svg": "image/svg+xml",
	".png": "image/png",
	".ico": "image/x-icon",
}

// registerUI mounts the embedded single-page UI. / serves index.html;
// /static/ serves the stylesheet and script referenced by it.
func registerUI(mux *http.ServeMux) {
	mux.HandleFunc("GET /", handleIndex)

	staticRoot, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Cannot happen: the embed directive guarantees the directory.
		panic("dashboard: embedded static tree missing: " + err.Error())
	}
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(strings.TrimPrefix(r.URL.Path, "/static/"))
		if name == "." || name == ".." || strings.HasPrefix(name, "../") {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(staticRoot, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		ctype, ok := staticTypes[path.Ext(name)]
		if !ok {
			// The embedded set is fixed; an unknown extension means the
			// tree gained a file without a matching type mapping.
			http.Error(w, "dashboard: unmapped static type", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	}))
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
