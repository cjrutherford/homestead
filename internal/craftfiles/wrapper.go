package craftfiles

import (
	"embed"
	"net/http"
	"strings"
)

// The wrapper page for craft apps served by path (ADR-045). The image's
// copy (images/craft/homestead) uses absolute paths and is part of the
// signed craft build context, so the daemon serves this copy, which
// resolves its paths against the page itself, under every app path.
//
//go:embed wrapper/open.html wrapper/open.js wrapper/open.css
var wrapperFiles embed.FS

// WrapperPrefix is where the wrapper page lives under an app's path.
const WrapperPrefix = "/_homestead/"

var wrapperTypes = map[string]string{
	"open.html": "text/html; charset=utf-8",
	"open.js":   "text/javascript; charset=utf-8",
	"open.css":  "text/css; charset=utf-8",
}

// wrapperCSP is the policy the image's nginx sets on the wrapper.
const wrapperCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// isWrapper reports a read of one of the wrapper's own files.
func isWrapper(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	name, ok := strings.CutPrefix(r.URL.Path, WrapperPrefix)
	_, known := wrapperTypes[name]
	return ok && known
}

func (h *Handler) wrapper(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, WrapperPrefix)
	body, err := wrapperFiles.ReadFile("wrapper/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", wrapperTypes[name])
	hd.Set("Cache-Control", "no-cache")
	hd.Set("Content-Security-Policy", wrapperCSP)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}
