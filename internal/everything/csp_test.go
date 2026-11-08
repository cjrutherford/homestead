package everything

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixedSession struct{ ok bool }

func (f fixedSession) Session(*http.Request) (string, string, bool) { return "sub", "tok", f.ok }

const cspIndex = `<html><head><script>
  window.theme = 1;
</script><script src="https://js.puter.com/v2/"></script><script type="module" src="./a.js"></script></head></html>`

// TestContentSecurityPolicyIsFailClosed: ADR-057. The policy admits only the
// app's own origin and the hash of the one inline script the build carries.
func TestContentSecurityPolicyIsFailClosed(t *testing.T) {
	sum := sha256.Sum256([]byte("\n  window.theme = 1;\n"))
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	policy := ContentSecurityPolicy([]byte(cspIndex))
	for _, want := range []string{"script-src 'self' " + hash + ";", "connect-src 'self';", "object-src 'none'",
		"base-uri 'none'", "frame-ancestors 'none'", "default-src 'none'", "form-action 'self'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("policy lacks %q: %s", want, policy)
		}
	}
	// A script with a src is never hashed; no wildcard, remote host or eval.
	for _, bad := range []string{"unsafe-eval", "*", "http:", "https:", "unsafe-inline'; script", "js.puter.com"} {
		if strings.Contains(strings.Split(policy, "style-src")[0], bad) {
			t.Errorf("script policy contains %q: %s", bad, policy)
		}
	}
	if strings.Count(policy, "sha256-") != 1 {
		t.Errorf("want exactly one inline script hash: %s", policy)
	}
	if p := ContentSecurityPolicy(nil); strings.Contains(p, "sha256-") || !strings.Contains(p, "script-src 'self';") {
		t.Errorf("empty page policy = %s", p)
	}
}

func TestAppResponsesCarryThePolicy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(cspIndex), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sw.js"), []byte("//"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerApp(mux, "/apps/tasks", dir, fixedSession{ok: true})
	for path, hashed := range map[string]bool{"/apps/tasks/": true, "/apps/tasks/sw.js": false, "/apps/tasks/missing": false} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		got := rec.Header().Get("Content-Security-Policy")
		if got == "" || !strings.Contains(got, "frame-ancestors 'none'") || strings.Contains(got, "sha256-") != hashed {
			t.Errorf("%s: policy %q (hashed want %v)", path, got, hashed)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: no nosniff", path)
		}
	}
	// The redirect to sign-in (no session) is not a document either.
	mux = http.NewServeMux()
	registerApp(mux, "/apps/tasks", dir, fixedSession{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/apps/tasks/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("unauthenticated index = %d", rec.Code)
	}
}

// S375: an app that ships a .wasm file may compile WebAssembly (and only
// that); one without keeps the strict policy.
func TestPolicyAllowsWebAssemblyOnlyWhenTheBuildShipsAModule(t *testing.T) {
	plain, withWasm := t.TempDir(), t.TempDir()
	for _, d := range []string{plain, withWasm} {
		if err := os.WriteFile(filepath.Join(d, "index.html"), []byte(cspIndex), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(withWasm, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(withWasm, "assets", "kernel-AbCd1234.wasm"), []byte("\x00asm"), 0o600); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]bool{plain: false, withWasm: true} {
		mux := http.NewServeMux()
		registerApp(mux, "/apps/c", dir, fixedSession{ok: true})
		for _, path := range []string{"/apps/c/", "/apps/c/assets/kernel-AbCd1234.wasm", "/apps/c/missing"} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
			got := rec.Header().Get("Content-Security-Policy")
			scripts := strings.Split(got, "style-src")[0]
			if strings.Contains(scripts, "'wasm-unsafe-eval'") != want || strings.Contains(scripts, "unsafe-eval'") && !strings.Contains(scripts, "wasm-unsafe-eval") {
				t.Errorf("%s (wasm %v): %s", path, want, got)
			}
			if strings.Contains(strings.ReplaceAll(scripts, "'wasm-unsafe-eval'", ""), "eval") {
				t.Errorf("%s: JavaScript eval allowed: %s", path, got)
			}
		}
	}
}

// S377: a build whose script carries its WebAssembly inline (the 3D app's
// physics engine) gets the same permission as one with a .wasm file; a script
// that merely mentions the word, or a file that is not script, does not.
func TestPolicyAllowsWebAssemblyForScriptThatInstantiatesAnInlineModule(t *testing.T) {
	cases := map[string]struct {
		name, body string
		want       bool
	}{
		"inline instantiate":  {"rapier-AbCd1234.js", `const m=await WebAssembly.instantiate(bytes,imports);`, true},
		"inline compile":      {"engine-AbCd1234.mjs", `WebAssembly.compile(b)`, true},
		"only a feature test": {"vendor-AbCd1234.js", `if(typeof WebAssembly==="object"&&WebAssembly.validate)ok()`, false},
		"not script":          {"notes-AbCd1234.txt", `WebAssembly.instantiate`, false},
	}
	for label, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(cspIndex), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "assets", c.name), []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		registerApp(mux, "/apps/c", dir, fixedSession{ok: true})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/apps/c/", nil))
		got := rec.Header().Get("Content-Security-Policy")
		scripts := strings.Split(got, "style-src")[0]
		if strings.Contains(scripts, "'wasm-unsafe-eval'") != c.want {
			t.Errorf("%s: want wasm %v: %s", label, c.want, got)
		}
		if strings.Contains(strings.ReplaceAll(scripts, "'wasm-unsafe-eval'", ""), "eval") {
			t.Errorf("%s: JavaScript eval allowed: %s", label, got)
		}
	}
}

// S464: eval is allowed only for a worker script of an app that asks for it;
// the page never gets it.
func TestWorkerEvalOnlyForWorkerScriptsOfAppsThatAsk(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": cspIndex, "worker.js": "//"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, on := range []bool{false, true} {
		if on {
			t.Setenv(WorkerEvalEnv, "1")
		}
		mux := http.NewServeMux()
		registerApp(mux, "/apps/p", dir, fixedSession{ok: true})
		for _, c := range []struct{ path, dest string }{
			{"/apps/p/", "document"}, {"/apps/p/worker.js", "script"}, {"/apps/p/worker.js", "worker"}, {"/apps/p/worker.js", ""},
		} {
			req := httptest.NewRequest("GET", c.path, nil)
			req.Header.Set("Sec-Fetch-Dest", c.dest)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			scripts := strings.Split(rec.Header().Get("Content-Security-Policy"), "style-src")[0]
			if want := on && c.dest == "worker"; strings.Contains(scripts, "'unsafe-eval'") != want {
				t.Errorf("env=%v %s dest=%q: %s", on, c.path, c.dest, scripts)
			}
		}
	}
}

// S464: the sandbox page has its own policy (eval and blob workers, no
// network); the app page gains frame-src 'self' and nothing looser.
func TestSandboxPageHasItsOwnPolicy(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": cspIndex, "sandbox.html": "<html></html>", "other.html": "<html></html>"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(SandboxPageEnv, "sandbox.html")
	mux := http.NewServeMux()
	registerApp(mux, "/apps/a", dir, fixedSession{ok: true})
	get := func(path string) string {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Header().Get("Content-Security-Policy")
	}
	box := get("/apps/a/sandbox.html")
	for _, want := range []string{"script-src 'self' 'unsafe-eval' blob:", "worker-src blob:", "connect-src 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(box, want) {
			t.Errorf("sandbox policy lacks %q: %s", want, box)
		}
	}
	for _, path := range []string{"/apps/a/", "/apps/a/other.html"} {
		got := get(path)
		if !strings.Contains(got, "frame-src 'self'") || strings.Contains(got, "eval") || strings.Contains(got, "blob:'") {
			t.Errorf("%s: %s", path, got)
		}
	}
}
