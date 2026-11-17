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
		_, csrf, ok := src.Session(r)
		if !ok {
			deny(w, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"csrf": csrf})
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

// wasmCode marks JavaScript that compiles WebAssembly itself. A module the
// build inlines into a script (the 3D app's physics engine carries its
// kernel as base64) needs the same permission as a .wasm file (S377).
var wasmCode = [][]byte{[]byte("WebAssembly.instantiate"), []byte("WebAssembly.compile"), []byte("WebAssembly.Module(")}

// maxScanBytes bounds how much of one script is read when looking for wasmCode.
const maxScanBytes = 16 << 20

// hasWasm reports whether the built app ships any WebAssembly module: a
// .wasm file, or script that instantiates or compiles one it carries inline.
func hasWasm(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.HasSuffix(name, ".wasm") {
			found = true
			return fs.SkipAll
		}
		if strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".mjs") {
			if inlineWasm(p) {
				found = true
				return fs.SkipAll
			}
		}
		return nil
	})
	return found
}

func inlineWasm(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxScanBytes))
	if err != nil {
		return false
	}
	for _, marker := range wasmCode {
		if bytes.Contains(body, marker) {
			return true
		}
	}
	return false
}

// WorkerEvalEnv (S464) lets an app's dedicated Worker script use eval and
// new Function. Parametric's geometry engine (emscripten embind) builds its
// call stubs that way. Only the response for a worker script carries it
// (Sec-Fetch-Dest: worker); the page keeps refusing eval.
const WorkerEvalEnv = "HOMESTEAD_EVERYTHING_WORKER_EVAL"

// SandboxPageEnv (S464) names one file of the built app, relative to the
// base path, that is a script sandbox page. API Client runs the person's
// pre-request scripts in a sandboxed (opaque origin) iframe on this page; a
// srcdoc frame would inherit the app page's policy, so the page is its own
// response with its own policy: scripts and blob workers may use eval, and
// nothing may connect anywhere. The app page only gains frame-src 'self'.
const SandboxPageEnv = "HOMESTEAD_EVERYTHING_SANDBOX_PAGE"

// sandboxPolicy is the policy of the sandbox page and of the Blob workers
// it creates (they inherit it). The frame is cut off from the network, the
// app's DOM, storage and cookies; eval is the whole point of the page.
const sandboxPolicy = "default-src 'none'; script-src 'self' 'unsafe-eval' blob:; worker-src blob:; " +
	"child-src blob:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; " +
	"form-action 'none'; frame-ancestors 'self'"

type appServer struct {
	workerEval  bool
	sandboxPage string // "/<file>" under the base, or ""
	wasm        bool
	base, dir   string
	src         SessionSource
	files       http.Handler
	offer       *sourceOffer // nil unless the image carries a source offer (S423)
}

func registerApp(mux *http.ServeMux, base, dir string, src SessionSource) {
	srcDir := os.Getenv(SourceDirEnv)
	if srcDir == "" {
		srcDir = DefaultSourceDir
	}
	a := &appServer{base: base, dir: dir, src: src, wasm: hasWasm(dir), offer: loadSourceOffer(srcDir)}
	a.workerEval = os.Getenv(WorkerEvalEnv) == "1"
	if f := strings.Trim(os.Getenv(SandboxPageEnv), "/"); f != "" && !strings.Contains(f, "..") {
		a.sandboxPage = "/" + f
	}
	a.files = http.FileServer(noListingFS{http.Dir(dir)})
	if base != "" {
		a.files = http.StripPrefix(base, a.files)
	}
	mux.HandleFunc(base+"/", a.serve)
}

// policy is the page policy for this app, with frame-src 'self' when the
// app has a sandbox page to frame.
func (a *appServer) policy(index []byte) string {
	p := contentSecurityPolicy(index, a.wasm)
	if a.sandboxPage != "" {
		p += "; frame-src 'self'"
	}
	return p
}

// withEval adds 'unsafe-eval' to the script-src of policy.
func withEval(policy string) string {
	return strings.Replace(policy, "script-src ", "script-src 'unsafe-eval' ", 1)
}

func (a *appServer) serve(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, a.base)
	policy := a.policy(nil)
	switch {
	case a.sandboxPage != "" && p == a.sandboxPage:
		policy = sandboxPolicy
	case a.workerEval && (r.Header.Get("Sec-Fetch-Dest") == "worker" || r.Header.Get("Sec-Fetch-Dest") == "sharedworker"):
		policy = withEval(policy)
	}
	w.Header().Set("Content-Security-Policy", policy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch {
	case p == SourcePath || strings.HasPrefix(p, SourcePath+"/"):
		a.source(w, r, p)
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
	if a.offer != nil {
		// The Source link goes right after the opening <body> tag (S423).
		if b := bodyTag.FindIndex(out); b != nil {
			out = append(append(append([]byte{}, out[:b[1]]...), a.sourceLink()...), out[b[1]:]...)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", a.policy(page))
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
