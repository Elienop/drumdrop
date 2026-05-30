package server

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// notBuiltNotice is served for SPA routes when no UI is embedded (default !webui
// build): a clear instruction rather than a confusing 404.
const notBuiltNotice = "drumdrop web UI is not built into this binary.\n" +
	"Build it with: make web && make build   (go build -tags webui)\n"

// spaHandler serves the embedded single-page app from dist. Hashed assets are
// served directly; any other client route falls back to index.html so the
// browser router can handle it. It guards the API/health prefixes itself —
// net/http ServeMux precedence only protects exact registered method+path
// tuples, so an unregistered /api/... path or a method-mismatched API call would
// otherwise be captured by the "/" catch-all and served index.html (200 HTML
// where a client expects JSON/404). When dist has no index.html (no UI embedded)
// it serves a plain "not built" notice.
func spaHandler(dist fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/") || p == "/healthz" || p == "/readyz" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, err := fs.Stat(dist, "index.html"); errors.Is(err, fs.ErrNotExist) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, notBuiltNotice)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+p), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(dist, name); err != nil {
			name = "index.html" // unknown path -> SPA route -> index.html
		}
		// Hashed assets are immutable; the HTML shell must always re-validate.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, dist, name)
	}
}
