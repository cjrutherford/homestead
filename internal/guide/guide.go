// Package guide serves the built owner guide at /guide/ (S494).
//
// scripts/guide/build.py turns docs/guide/*.md into static HTML under
// internal/guide/dist. Like the dashboard, the bytes arrive through embed.go
// (-tags embeddash); plain builds have no guide and the route answers 404.
// The handler works over any fs.FS so tests stay hermetic.
package guide

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Prefix is the URL path the guide is mounted at.
const Prefix = "/guide/"

// csp is sent with every page: the pages need nothing but their own inline
// style (allowed by the hash inside each page's meta tag) and same-origin
// images, and must never be framed.
const csp = "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Handler serves the guide files read-only.
type Handler struct {
	files fs.FS
}

// New returns a Handler over files (the embedded dist in production).
func New(files fs.FS) *Handler { return &Handler{files: files} }

// Available reports whether files holds a built guide (an index page).
func Available(files fs.FS) bool {
	_, err := fs.Stat(files, "index.html")
	return err == nil
}

// ServeHTTP implements http.Handler. Only GET and HEAD, no directory
// listings, no fallback: an unknown page is a 404.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(r.URL.Path, Prefix)), "/")
	if name == "" || strings.HasSuffix(r.URL.Path, "/") {
		name = path.Join(name, "index.html")
	}
	if !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	f, err := h.files.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	hd := w.Header()
	hd.Set("Cache-Control", "no-cache")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "no-referrer")
	if strings.HasSuffix(name, ".html") {
		hd.Set("Content-Security-Policy", csp)
	}
	rs, ok := f.(readSeeker)
	if !ok {
		data, err := fs.ReadFile(h.files, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(name, ".html") {
			hd.Set("Content-Type", "text/html; charset=utf-8")
		}
		_, _ = w.Write(data)
		return
	}
	http.ServeContent(w, r, name, st.ModTime(), rs)
}

type readSeeker interface {
	Read([]byte) (int, error)
	Seek(int64, int) (int64, error)
}
