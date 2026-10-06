// Package dashboard serves the production Angular assets (S031).
//
// The serving logic here works over any fs.FS and always builds, so Go
// iteration stays hermetic. Production bytes arrive through embed.go, which
// compiles only with -tags embeddash after make dashboard-embed refreshes
// internal/dashboard/dist from the pnpm build; plain builds use the
// noembed stub and run API-only with a startup notice.
package dashboard

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler serves production assets with SPA fallback: existing files
// verbatim, every other path serves index.html so client-side routes work
// on refresh and deep links. index.html is never cached so shell updates
// propagate; hashed asset filenames carry their own cache identity.
type Handler struct {
	files fs.FS
	serve http.Handler
}

// New returns a Handler over files (embedded dist in production).
func New(files fs.FS) *Handler {
	return &Handler{files: files, serve: http.FileServer(http.FS(files))}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" && fs.ValidPath(name) {
		if f, err := h.files.Open(name); err == nil {
			f.Close()
			if name == "index.html" {
				w.Header().Set("Cache-Control", "no-cache")
			}
			h.serve.ServeHTTP(w, r)
			return
		}
	}
	h.serveIndex(w, r)
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(h.files, "index.html")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
