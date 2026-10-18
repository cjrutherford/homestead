package craftfiles

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCleanRel(t *testing.T) {
	ok := map[string]string{
		"": "", "/": "", "a": "a", "/a/b/": "a/b", "a//b": "a/b", "dir/file name.xlsx": "dir/file name.xlsx",
	}
	for in, want := range ok {
		got, err := CleanRel(in)
		if err != nil || got != want {
			t.Errorf("CleanRel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"..", "a/../b", "a/./b", "./a", "a/..", "a\\b", "a\x00b", "a\nb", strings.Repeat("x", 300), strings.Repeat("a/", 40)} {
		if _, err := CleanRel(in); err == nil {
			t.Errorf("CleanRel(%q) accepted", in)
		}
	}
}

func TestAllMethodsReachRootFolder(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	do := func(method, path string, body string, opts ...reqOpt) *http.Response {
		return e.do(method, path, strings.NewReader(body), append(opts, withCookie(cookie))...)
	}
	if r := do("MKCOL", "/_homestead/files/f/docs", ""); r.StatusCode != 201 {
		t.Fatalf("MKCOL = %d", r.StatusCode)
	}
	if r := do("PUT", "/_homestead/files/f/docs/a%20b.xlsx", "workbook-bytes"); r.StatusCode != 201 {
		t.Fatalf("PUT = %d", r.StatusCode)
	}
	r := do("GET", "/_homestead/files/f/docs/a%20b.xlsx", "")
	if body := readAll(t, r); r.StatusCode != 200 || body != "workbook-bytes" {
		t.Fatalf("GET = %d %q", r.StatusCode, body)
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("GET content type %q: user files must never be a page on the craft origin", ct)
	}
	if r.Header.Get("Content-Disposition") != "attachment" || !strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("GET lacks attachment/sandbox headers: %v", r.Header)
	}
	if r.Header.Get("Etag") != `"abc123"` {
		t.Fatalf("ETag not forwarded: %v", r.Header)
	}
	r = do("HEAD", "/_homestead/files/f/docs/a%20b.xlsx", "")
	if r.StatusCode != 200 || r.Header.Get("Content-Length") != "14" {
		t.Fatalf("HEAD = %d %v", r.StatusCode, r.Header)
	}
	r = do("PROPFIND", "/_homestead/files/f/docs", `<propfind xmlns="DAV:"><allprop/></propfind>`, withHeader("Depth", "1"))
	body := readAll(t, r)
	if r.StatusCode != 207 || !strings.Contains(body, "/_homestead/files/f/docs") {
		t.Fatalf("PROPFIND = %d %s", r.StatusCode, body)
	}
	if strings.Contains(body, "remote.php") || strings.Contains(body, "alice") {
		t.Fatalf("PROPFIND leaks the upstream layout: %s", body)
	}
	if r := do("COPY", "/_homestead/files/f/docs/a%20b.xlsx", "", withHeader("Destination", "https://"+testHost+"/_homestead/files/f/docs/copy.xlsx")); r.StatusCode != 201 {
		t.Fatalf("COPY = %d", r.StatusCode)
	}
	if r := do("MOVE", "/_homestead/files/f/docs/copy.xlsx", "", withHeader("Destination", "/_homestead/files/f/docs/moved.xlsx")); r.StatusCode != 201 {
		t.Fatalf("MOVE = %d", r.StatusCode)
	}
	if r := do("DELETE", "/_homestead/files/f/docs/moved.xlsx", ""); r.StatusCode != 204 {
		t.Fatalf("DELETE = %d", r.StatusCode)
	}
	for _, s := range e.dav.seen() {
		if !strings.HasPrefix(s.Path, davRoot) {
			t.Errorf("upstream request outside the root folder: %s %s", s.Method, s.Path)
		}
	}
	if _, ok := e.dav.files["docs/a b.xlsx"]; !ok {
		t.Fatalf("file missing upstream: %v", e.dav.files)
	}
	// Methods outside the subset are refused by the mux.
	for _, m := range []string{"PATCH", "LOCK", "PROPPATCH", "POST", "TRACE"} {
		r := do(m, "/_homestead/files/f/x", "")
		if r.StatusCode != 405 && r.StatusCode != 404 {
			t.Errorf("%s = %d, want 405", m, r.StatusCode)
		}
	}
}

func TestRootIsListable(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	r := e.do("PROPFIND", "/_homestead/files/f/", strings.NewReader(""), withCookie(cookie), withHeader("Depth", "infinity"))
	if r.StatusCode != 207 {
		t.Fatalf("root PROPFIND = %d", r.StatusCode)
	}
	if got := e.dav.seen(); got[len(got)-1].Header.Get("Depth") != "1" {
		t.Fatalf("Depth infinity must be clamped, got %q", got[len(got)-1].Header.Get("Depth"))
	}
}

func TestTraversalIsRefused(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	before := len(e.dav.seen())
	for _, p := range []string{
		"/_homestead/files/f/../../etc/passwd",
		"/_homestead/files/f/a/../../b",
		"/_homestead/files/f/%2e%2e/x",
		"/_homestead/files/f/%2E%2E%2Fx",
		"/_homestead/files/f/a/%2e%2e/%2e%2e/x",
		"/_homestead/files/f/./a",
		"/_homestead/files/f/a%5Cb",
		"/_homestead/files/f/a%00b",
		"/_homestead/files/f//a",
		"/_homestead/files/f/a/..",
	} {
		for _, m := range []string{"GET", "PUT", "DELETE"} {
			r := e.do(m, p, strings.NewReader("x"), withCookie(cookie))
			if r.StatusCode != 400 {
				t.Errorf("%s %s = %d, want 400", m, p, r.StatusCode)
			}
		}
	}
	for _, dest := range []string{"/_homestead/files/f/../x", "/other/path", "https://evil.example/_homestead/files/f/x", "/_homestead/files/f/a/%2e%2e/%2e%2e/x"} {
		r := e.do("MOVE", "/_homestead/files/f/a", nil, withCookie(cookie), withHeader("Destination", dest))
		if r.StatusCode != 400 {
			t.Errorf("MOVE to %q = %d, want 400", dest, r.StatusCode)
		}
	}
	if after := len(e.dav.seen()); after != before {
		t.Fatalf("refused paths still reached Nextcloud (%d requests)", after-before)
	}
}

func TestCredentialNeverReachesClientOrComesFromClient(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	e.dav.mu.Lock()
	e.dav.files["a.txt"] = []byte("hello")
	e.dav.mu.Unlock()
	r := e.do("GET", "/_homestead/files/f/a.txt", nil, withCookie(cookie),
		withHeader("Authorization", "Bearer attacker"), withHeader("X-Forwarded-For", "1.2.3.4"))
	hdr := fmt.Sprint(r.Header)
	body := readAll(t, r)
	if strings.Contains(hdr+body, testPassword) {
		t.Fatalf("app password reached the client: %s %s", hdr, body)
	}
	up := e.dav.seen()
	last := up[len(up)-1]
	if last.Header.Get("Cookie") != "" || last.Header.Get("X-Forwarded-For") != "" || last.Header.Get("Origin") != "" {
		t.Fatalf("browser headers leaked upstream: %v", last.Header)
	}
	user, pass, ok := (&http.Request{Header: last.Header}).BasicAuth()
	if !ok || user != "alice" || pass != testPassword {
		t.Fatalf("upstream auth = %v %q %q; the proxy must authenticate itself", ok, user, pass)
	}
	// Upstream rejection: nothing of the upstream answer reaches the client.
	e.dav.mu.Lock()
	e.dav.revoked = true
	e.dav.mu.Unlock()
	r = e.do("GET", "/_homestead/files/f/a.txt", nil, withCookie(cookie))
	hdr, body = fmt.Sprint(r.Header), readAll(t, r)
	if r.StatusCode != 502 || r.Header.Get("X-Homestead-Upstream") != "auth-rejected" {
		t.Fatalf("revoked app password: %d %v", r.StatusCode, r.Header)
	}
	if !strings.Contains(body, "relink") {
		t.Fatalf("no clear message: %q", body)
	}
	for _, leak := range []string{testPassword, "upstream-secret", "bad credentials", "WWW-Authenticate", "nextcloud"} {
		if strings.Contains(hdr+body, leak) {
			t.Fatalf("upstream detail %q leaked: %s %s", leak, hdr, body)
		}
	}
}

func TestOversizeUploadRefused(t *testing.T) {
	e := newEnv(t, Limits{MaxUpload: 1024})
	cookie := e.login()
	before := len(e.dav.seen())
	r := e.do("PUT", "/_homestead/files/f/big.bin", bytes.NewReader(make([]byte, 2048)), withCookie(cookie))
	if r.StatusCode != 413 {
		t.Fatalf("declared oversize = %d, want 413", r.StatusCode)
	}
	if len(e.dav.seen()) != before {
		t.Fatal("declared-oversize upload reached Nextcloud")
	}
	// Unknown length (chunked): refused as the stream crosses the limit.
	pr, pw := io.Pipe()
	go func() { pw.Write(make([]byte, 4096)); pw.Close() }()
	r = e.do("PUT", "/_homestead/files/f/chunked.bin", pr, withCookie(cookie))
	if r.StatusCode != 413 {
		t.Fatalf("chunked oversize = %d, want 413", r.StatusCode)
	}
	if _, ok := e.dav.files["chunked.bin"]; ok {
		t.Fatal("oversize chunked upload was stored")
	}
	// Exactly at the limit succeeds.
	r = e.do("PUT", "/_homestead/files/f/exact.bin", bytes.NewReader(make([]byte, 1024)), withCookie(cookie))
	if r.StatusCode != 201 {
		t.Fatalf("at-limit upload = %d, want 201", r.StatusCode)
	}
}

func TestStreamingLargerThanMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("streams hundreds of MiB")
	}
	const size = 256 << 20
	e := newEnv(t, Limits{MaxUpload: 1 << 30, Timeout: time.Minute})
	cookie := e.login()
	bigSize.Store(size)
	bigSeen.Store(0)

	var peak uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				runtime.ReadMemStats(&m)
				if m.HeapInuse > peak {
					peak = m.HeapInuse
				}
			}
		}
	}()
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	r := e.do("PUT", "/_homestead/files/f/big", io.LimitReader(zeroReader{}, size), withCookie(cookie), func(r *http.Request) { r.ContentLength = size })
	if r.StatusCode != 201 {
		t.Fatalf("large PUT = %d", r.StatusCode)
	}
	if got := bigSeen.Load(); got != size {
		t.Fatalf("upstream received %d bytes, want %d", got, size)
	}
	r = e.do("GET", "/_homestead/files/f/big", nil, withCookie(cookie))
	n, err := io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if err != nil || n != size {
		t.Fatalf("large GET = %d bytes, %v", n, err)
	}
	close(stop)
	wg.Wait()
	if grew := int64(peak) - int64(base.HeapInuse); grew > 96<<20 {
		t.Fatalf("heap grew %d MiB moving %d MiB each way: bodies are being buffered", grew>>20, size>>20)
	}
}

func TestUpstreamErrors(t *testing.T) {
	e := newEnv(t, Limits{Timeout: 300 * time.Millisecond})
	cookie := e.login()
	get := func() *http.Response {
		return e.do("GET", "/_homestead/files/f/a.txt", nil, withCookie(cookie))
	}
	// Upstream 5xx and 4xx pass through (without relaying their text).
	e.dav.mu.Lock()
	e.dav.status = 507
	e.dav.mu.Unlock()
	if r := get(); r.StatusCode != 507 {
		t.Fatalf("upstream 507 = %d", r.StatusCode)
	}
	e.dav.mu.Lock()
	e.dav.status = 0
	e.dav.mu.Unlock()
	if r := get(); r.StatusCode != 404 {
		t.Fatalf("missing file = %d, want 404", r.StatusCode)
	}
	if r := e.do("PUT", "/_homestead/files/f/a.txt", strings.NewReader("x"), withCookie(cookie), withHeader("If-Match", `"stale"`)); r.StatusCode != 412 {
		t.Fatalf("stale If-Match = %d, want 412", r.StatusCode)
	}
	// A redirect could leave the linked Nextcloud: never followed.
	e.dav.mu.Lock()
	e.dav.redirect = true
	e.dav.mu.Unlock()
	if r := get(); r.StatusCode != 502 {
		t.Fatalf("redirect = %d, want 502", r.StatusCode)
	}
	e.dav.mu.Lock()
	e.dav.redirect = false
	e.dav.delay = time.Second
	e.dav.mu.Unlock()
	if r := get(); r.StatusCode != 504 {
		t.Fatalf("slow upstream = %d, want 504", r.StatusCode)
	}
	// Unreachable upstream.
	e.dav.mu.Lock()
	e.dav.delay = 0
	e.dav.mu.Unlock()
	e.davSrv.Close()
	if r := get(); r.StatusCode != 502 {
		t.Fatalf("unreachable upstream = %d, want 502", r.StatusCode)
	}
	// Not linked: fails closed.
	e.linked = false
	if r := get(); r.StatusCode != 409 {
		t.Fatalf("unlinked = %d, want 409", r.StatusCode)
	}
}

func TestPerSessionConcurrencyCap(t *testing.T) {
	e := newEnv(t, Limits{PerSession: 2, Timeout: 10 * time.Second})
	cookie := e.login()
	e.dav.hold = make(chan struct{})
	e.dav.entered = make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := e.do("PUT", fmt.Sprintf("/_homestead/files/f/held%d", i), strings.NewReader("x"), withCookie(cookie))
			if r.StatusCode != 201 {
				t.Errorf("held PUT = %d", r.StatusCode)
			}
		}(i)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-e.dav.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("uploads did not start")
		}
	}
	r := e.do("GET", "/_homestead/files/f/x", nil, withCookie(cookie))
	if r.StatusCode != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("third concurrent request = %d, want 429 with Retry-After", r.StatusCode)
	}
	// Another session is unaffected.
	other := e.login()
	if r := e.do("GET", "/_homestead/files/f/x", nil, withCookie(other)); r.StatusCode == 429 {
		t.Fatal("cap leaked across sessions")
	}
	close(e.dav.hold)
	wg.Wait()
	if r := e.do("GET", "/_homestead/files/f/x", nil, withCookie(cookie)); r.StatusCode == 429 {
		t.Fatal("slots not released")
	}
}
