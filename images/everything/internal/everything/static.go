package everything

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SessionPath is the small authenticated endpoint the shim uses to refresh
// its CSRF token without reloading the page (and without any cached HTML).
const SessionPath = "/_homestead/session"

// SessionSource is implemented by identities that can describe the caller's
// current session. NewServerAt serves SessionPath when the identity has it.
type SessionSource interface {
	Session(*http.Request) (subject, csrf string, ok bool)
}

func sessionHandler(src SessionSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			deny(w, http.StatusMethodNotAllowed)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			deny(w, http.StatusForbidden)
			return
		}
		subject, csrf, ok := src.Session(r)
		if !ok {
			deny(w, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"csrf": csrf, "subject": subject})
	}
}

var headTag = regexp.MustCompile(`(?i)<head(\s[^>]*)?>`)

// inlineScript matches a script element with no src attribute: its body is
// the only inline script the policy admits, by SHA-256 hash.
var inlineScript = regexp.MustCompile(`(?is)<script(\s[^>]*)?>(.*?)</script>`)
var scriptSrcAttr = regexp.MustCompile(`(?i)\ssrc\s*=`)

// ContentSecurityPolicy is the bridge's fail-closed policy (ADR-057): code
// only from the app's own origin, plus the hashes of the inline scripts the
// built index.html itself carries; no framing, no plugins, no <base>, and
// network access only back to the bridge. Styles may be inline because the
// app sets element styles from script. Computed from the page being served,
// so a changed inline script is refused until the build is reviewed again.
func ContentSecurityPolicy(index []byte) string { return contentSecurityPolicy(index, false) }

// contentSecurityPolicy adds 'wasm-unsafe-eval' when wasm is true (S375): an
// app whose build ships a WebAssembly module (Circuits' geometry kernel) must
// be allowed to compile it. That keyword permits WebAssembly compilation only;
// JavaScript eval stays refused, and apps without a .wasm file are unchanged.
func contentSecurityPolicy(index []byte, wasm bool) string {
	scripts := "'self'"
	for _, m := range inlineScript.FindAllSubmatch(index, -1) {
		if scriptSrcAttr.Match(m[1]) {
			continue
		}
		sum := sha256.Sum256(m[2])
		scripts += " 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	if wasm {
		scripts += " 'wasm-unsafe-eval'"
	}
	return "default-src 'none'; script-src " + scripts + "; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; font-src 'self' data:; media-src 'self' blob: data:; " +
		"connect-src 'self'; manifest-src 'self'; worker-src 'self'; form-action 'self'; " +
		"object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
}

// hashedAsset matches build output like assets/index-AbC123xy.js: a content
// hash in the file name makes the URL immutable, so it may be cached long.
var hashedAsset = regexp.MustCompile(`^/assets/[^/]+-[A-Za-z0-9_]{8,}\.[A-Za-z0-9]+$`)

// hasWasm reports whether the built app ships any WebAssembly module.
func hasWasm(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".wasm") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

type appServer struct {
	wasm      bool
	base, dir string
	src       SessionSource
	files     http.Handler
}

func registerApp(mux *http.ServeMux, base, dir string, src SessionSource) {
	a := &appServer{base: base, dir: dir, src: src, wasm: hasWasm(dir)}
	a.files = http.FileServer(noListingFS{http.Dir(dir)})
	if base != "" {
		a.files = http.StripPrefix(base, a.files)
	}
	mux.HandleFunc(base+"/", a.serve)
}

func (a *appServer) serve(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, a.base)
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy(nil, a.wasm))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch {
	case p == "/" || p == "/index.html":
		a.index(w, r)
	case strings.HasPrefix(p, "/_homestead/") && p != "/_homestead/puter-bridge.js":
		// Unregistered bridge paths never fall through to the file server.
		w.Header().Set("Cache-Control", "no-store")
		deny(w, http.StatusNotFound)
	default:
		if hashedAsset.MatchString(p) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		a.files.ServeHTTP(w, r)
	}
}

// index serves index.html with the per-session CSRF token injected. It is
// never cacheable: a stored copy would carry one person's token or a stale
// sign-in redirect.
func (a *appServer) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, csrf, ok := a.src.Session(r)
	if !ok {
		http.Redirect(w, r, a.base+LoginPath, http.StatusFound)
		return
	}
	page, err := os.ReadFile(a.dir + "/index.html")
	loc := headTag.FindIndex(page)
	if err != nil || loc == nil {
		deny(w, http.StatusInternalServerError)
		return
	}
	meta := `<meta name="homestead-csrf" content="` + csrf + `">`
	out := append(append(append([]byte{}, page[:loc[1]]...), meta...), page[loc[1]:]...)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy(page, a.wasm))
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = io.Copy(w, bytes.NewReader(out))
}

type noListingFS struct{ http.FileSystem }

func (n noListingFS) Open(name string) (http.File, error) {
	f, err := n.FileSystem.Open(name)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err != nil || st.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}
