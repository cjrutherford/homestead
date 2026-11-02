package everything

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testOrigin = "https://home.example"
	testBase   = "/apps/tasks"
	testClient = "tasks-client"
	testSecret = "s3cret-value"
)

// fakeProvider is a minimal OIDC provider: discovery, JWKS, token endpoint
// with PKCE and client authentication checks.
type fakeProvider struct {
	srv   *httptest.Server
	key   *rsa.PrivateKey
	other *rsa.PrivateKey
	mu    sync.Mutex
	codes map[string]fakeCode
	clock func() time.Time
	// per-test mutation of the next token
	claims      func(map[string]any)
	sign        func(header map[string]any) string // replaces normal signing; may edit the header
	lastNonce   string
	kidOverride string
	tamper      bool
}

type fakeCode struct {
	sub, nonce, challenge string
}

func newFakeProvider(t *testing.T, clock func() time.Time) *fakeProvider {
	t.Helper()
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	k2, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeProvider{key: k, other: k2, codes: map[string]fakeCode{}, clock: clock}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": f.issuer(), "authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": f.kid(), "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes())}}})
	})
	mux.HandleFunc("/token", f.token)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeProvider) kid() string {
	if f.kidOverride != "" {
		return f.kidOverride
	}
	return "k1"
}

func (f *fakeProvider) issuer() string { return f.srv.URL + "/" }

func (f *fakeProvider) token(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, sec, ok := r.BasicAuth()
	if !ok || id != testClient || sec != testSecret {
		http.Error(w, "bad client", 401)
		return
	}
	r.ParseForm()
	c, ok := f.codes[r.PostForm.Get("code")]
	delete(f.codes, r.PostForm.Get("code"))
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || r.PostForm.Get("grant_type") != "authorization_code" ||
		r.PostForm.Get("redirect_uri") != testOrigin+testBase+CallbackPath ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
		http.Error(w, `{"error":"invalid_grant"}`, 400)
		return
	}
	now := f.clock()
	claims := map[string]any{"iss": f.issuer(), "sub": c.sub, "aud": testClient, "exp": now.Add(5 * time.Minute).Unix(),
		"iat": now.Unix(), "nonce": c.nonce}
	if f.claims != nil {
		f.claims(claims)
	}
	f.lastNonce = c.nonce
	header := map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"}
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	var tok string
	if f.sign != nil {
		tok = f.sign(header)
	} else {
		in := enc(header) + "." + enc(claims)
		d := sha256.Sum256([]byte(in))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, d[:])
		if f.tamper {
			sig[0] ^= 0xff
		}
		tok = in + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	json.NewEncoder(w).Encode(map[string]string{"id_token": tok, "token_type": "Bearer", "access_token": "x"})
}

// browser is a cookie holder that does not follow redirects.
type browser struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]*http.Cookie
}

func (b *browser) do(method, path string, hdr map[string]string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for _, c := range b.cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	return rec
}

type testEnv struct {
	t      *testing.T
	o      *OIDC
	h      http.Handler
	p      *fakeProvider
	store  *Store
	now    time.Time
	static string
}

func (e *testEnv) clock() time.Time { return e.now }

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	e := &testEnv{t: t, now: time.Unix(1_800_000_000, 0)}
	e.p = newFakeProvider(t, e.clock)
	dir := t.TempDir()
	e.static = filepath.Join(dir, "static")
	os.MkdirAll(filepath.Join(e.static, "assets"), 0o700)
	os.WriteFile(filepath.Join(e.static, "index.html"), []byte(`<!doctype html><html lang="en"><head><title>Tasks</title></head><body></body></html>`), 0o600)
	os.WriteFile(filepath.Join(e.static, "assets", "app.js"), []byte("console.log(1)"), 0o600)
	var err error
	e.store, err = OpenStore(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.store.Close() })
	e.o, err = NewOIDC(OIDCConfig{App: "tasks", Issuer: e.p.issuer(), ClientID: testClient, ClientSecret: testSecret,
		RedirectURL: testOrigin + testBase + CallbackPath, Origin: testOrigin, BasePath: testBase, StaticDir: e.static,
		HTTPClient: e.p.srv.Client(), IdleTTL: time.Hour, AbsoluteTTL: 24 * time.Hour, now: e.clock}, e.store)
	if err != nil {
		t.Fatal(err)
	}
	e.h, err = NewServerAt(testBase, e.store, e.o, e.o.Origins(), e.o.Routes)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *testEnv) browser() *browser {
	return &browser{t: e.t, h: e.h, cookies: map[string]*http.Cookie{}}
}

// startLogin performs GET /login and returns the authorize parameters.
func (e *testEnv) startLogin(b *browser) url.Values {
	rec := b.do("GET", testBase+LoginPath, nil, nil)
	if rec.Code != 302 {
		e.t.Fatalf("login status %d", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if !strings.HasPrefix(loc.String(), e.p.srv.URL+"/authorize") {
		e.t.Fatalf("redirect to %s", loc)
	}
	return loc.Query()
}

// approve is the provider's user consent: it issues a code for sub.
func (e *testEnv) approve(q url.Values, sub string) string {
	code := randomToken(12)
	e.p.mu.Lock()
	e.p.codes[code] = fakeCode{sub: sub, nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	e.p.mu.Unlock()
	return code
}

func (e *testEnv) callback(b *browser, state, code string) *httptest.ResponseRecorder {
	return b.do("GET", testBase+CallbackPath+"?"+url.Values{"state": {state}, "code": {code}}.Encode(), nil, nil)
}

// signIn runs a complete good login and returns the CSRF token.
func (e *testEnv) signIn(b *browser, sub string) string {
	q := e.startLogin(b)
	rec := e.callback(b, q.Get("state"), e.approve(q, sub))
	if rec.Code != 302 || rec.Header().Get("Location") != testBase+"/" {
		e.t.Fatalf("callback: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	return e.csrf(b)
}

func (e *testEnv) csrf(b *browser) string {
	rec := b.do("GET", testBase+"/", nil, nil)
	if rec.Code != 200 {
		e.t.Fatalf("index status %d", rec.Code)
	}
	m := `<meta name="homestead-csrf" content="`
	s := rec.Body.String()
	i := strings.Index(s, m)
	if i < 0 {
		e.t.Fatalf("no csrf meta in %s", s)
	}
	s = s[i+len(m):]
	return s[:strings.Index(s, `"`)]
}

func (e *testEnv) data(b *browser, csrf string, body string) *httptest.ResponseRecorder {
	h := map[string]string{"Origin": testOrigin, "Content-Type": "application/json"}
	if csrf != "" {
		h[CSRFHeader] = csrf
	}
	return b.do("POST", testBase+DataPath, h, []byte(body))
}

func (e *testEnv) whoami(b *browser, csrf string) (int, string) {
	rec := e.data(b, csrf, `{"operation":"whoami"}`)
	var out map[string]string
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out["subject"]
}

func TestOIDCGoodLogin(t *testing.T) {
	e := newEnv(t)
	b := e.browser()

	// Unauthenticated index load goes to sign-in and exposes no token.
	rec := b.do("GET", testBase+"/", nil, nil)
	if rec.Code != 302 || rec.Header().Get("Location") != testBase+LoginPath || strings.Contains(rec.Body.String(), "homestead-csrf") {
		t.Fatalf("unauthenticated index: %d %q", rec.Code, rec.Header().Get("Location"))
	}

	q := e.startLogin(b)
	if q.Get("response_type") != "code" || q.Get("client_id") != testClient || q.Get("redirect_uri") != testOrigin+testBase+CallbackPath ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("scope") != "openid" {
		t.Fatalf("authorize params: %v", q)
	}
	lc := b.cookies[loginName]
	if lc == nil || lc.Value != q.Get("state") {
		t.Fatal("state not bound to the browser")
	}
	rec = e.callback(b, q.Get("state"), e.approve(q, "sub-alice"))
	if rec.Code != 302 || rec.Header().Get("Location") != testBase+"/" {
		t.Fatalf("callback: %d", rec.Code)
	}
	var sc *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionName {
			sc = c
		}
	}
	if sc == nil || !sc.HttpOnly || !sc.Secure || sc.SameSite != http.SameSiteLaxMode || sc.Path != testBase+"/" || sc.MaxAge != 86400 {
		t.Fatalf("session cookie: %+v", sc)
	}
	if b.cookies[loginName] != nil {
		t.Fatal("login cookie not cleared")
	}
	csrf := e.csrf(b)
	if code, sub := e.whoami(b, csrf); code != 200 || sub != "sub-alice" {
		t.Fatalf("whoami: %d %q", code, sub)
	}
	if rec = e.data(b, csrf, `{"operation":"put","kind":"kv","key":"k","value":"dg=="}`); rec.Code != 200 {
		t.Fatalf("put: %d", rec.Code)
	}
	if rec = e.data(b, csrf, `{"operation":"get","kind":"kv","key":"k"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"dg=="`) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	// Index keeps the page, injects the token right after <head>, never caches.
	idx := b.do("GET", testBase+"/index.html", nil, nil)
	if !strings.Contains(idx.Body.String(), `<head><meta name="homestead-csrf" content="`+csrf+`"><title>Tasks</title>`) || idx.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("index: %s", idx.Body)
	}
	// Only a hash of the session id is stored.
	var n int
	e.store.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id_hash = ?`, b.cookies[sessionName].Value).Scan(&n)
	if n != 0 {
		t.Fatal("raw session id stored")
	}
	// Static assets are served; directories are not listed; the stored index is not raw-served.
	if a := b.do("GET", testBase+"/assets/app.js", nil, nil); a.Code != 200 || a.Body.String() != "console.log(1)" {
		t.Fatalf("asset: %d", a.Code)
	}
	if a := b.do("GET", testBase+"/assets/", nil, nil); a.Code != 404 {
		t.Fatalf("directory listing: %d", a.Code)
	}
	if a := b.do("GET", testBase+"/../../etc/passwd", nil, nil); a.Code == 200 {
		t.Fatal("traversal served")
	}
}

func TestOIDCStateChecks(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	q := e.startLogin(b)
	code := e.approve(q, "u")
	// A state the bridge never issued, even with a matching cookie.
	b.cookies[loginName] = &http.Cookie{Name: loginName, Value: "forged"}
	if rec := e.callback(b, "forged", code); rec.Code != 403 || b.cookies[sessionName] != nil {
		t.Fatalf("forged state: %d", rec.Code)
	}
	// Real state, but the callback arrives in a browser that never started it (login CSRF).
	other := e.browser()
	if rec := e.callback(other, q.Get("state"), code); rec.Code != 403 {
		t.Fatalf("state without browser binding: %d", rec.Code)
	}
	// Mismatched cookie.
	b.cookies[loginName] = &http.Cookie{Name: loginName, Value: "different"}
	if rec := e.callback(b, q.Get("state"), code); rec.Code != 403 {
		t.Fatalf("mismatched cookie: %d", rec.Code)
	}
	// Provider error, and missing code.
	q = e.startLogin(b)
	if rec := b.do("GET", testBase+CallbackPath+"?state="+q.Get("state")+"&error=access_denied", nil, nil); rec.Code != 403 {
		t.Fatalf("provider error: %d", rec.Code)
	}
	q = e.startLogin(b)
	if rec := b.do("GET", testBase+CallbackPath+"?state="+q.Get("state"), nil, nil); rec.Code != 403 {
		t.Fatalf("missing code: %d", rec.Code)
	}
	// Single use: replaying a consumed state fails.
	q = e.startLogin(b)
	state := q.Get("state")
	if rec := e.callback(b, state, e.approve(q, "u")); rec.Code != 302 {
		t.Fatalf("good: %d", rec.Code)
	}
	b.cookies[loginName] = &http.Cookie{Name: loginName, Value: state}
	if rec := e.callback(b, state, e.approve(q, "u")); rec.Code != 403 {
		t.Fatalf("replayed state: %d", rec.Code)
	}
	// An expired state fails.
	q = e.startLogin(b)
	e.now = e.now.Add(11 * time.Minute)
	if rec := e.callback(b, q.Get("state"), e.approve(q, "u")); rec.Code != 403 {
		t.Fatalf("expired state: %d", rec.Code)
	}
	// Only GET.
	if rec := b.do("POST", testBase+CallbackPath, nil, nil); rec.Code != 405 {
		t.Fatalf("post callback: %d", rec.Code)
	}
}

func TestOIDCRejectsBadIDTokens(t *testing.T) {
	cases := map[string]func(p *fakeProvider){
		"wrong nonce":         func(p *fakeProvider) { p.claims = func(c map[string]any) { c["nonce"] = "other" } },
		"no nonce":            func(p *fakeProvider) { p.claims = func(c map[string]any) { delete(c, "nonce") } },
		"wrong aud":           func(p *fakeProvider) { p.claims = func(c map[string]any) { c["aud"] = "someone-else" } },
		"aud list without us": func(p *fakeProvider) { p.claims = func(c map[string]any) { c["aud"] = []string{"a", "b"} } },
		"multi aud wrong azp": func(p *fakeProvider) {
			p.claims = func(c map[string]any) { c["aud"] = []string{testClient, "b"}; c["azp"] = "b" }
		},
		"wrong issuer": func(p *fakeProvider) { p.claims = func(c map[string]any) { c["iss"] = "https://evil.example/" } },
		"expired": func(p *fakeProvider) {
			p.claims = func(c map[string]any) { c["exp"] = time.Unix(1_800_000_000, 0).Add(-time.Hour).Unix() }
		},
		"no exp":             func(p *fakeProvider) { p.claims = func(c map[string]any) { delete(c, "exp") } },
		"no sub":             func(p *fakeProvider) { p.claims = func(c map[string]any) { c["sub"] = "" } },
		"tampered signature": func(p *fakeProvider) { p.tamper = true },
		"signed by another key": func(p *fakeProvider) {
			p.sign = func(h map[string]any) string { return p.forge(h, p.other) }
		},
		"alg none": func(p *fakeProvider) {
			p.sign = func(h map[string]any) string {
				h["alg"] = "none"
				return p.forgeUnsigned(h)
			}
		},
		"alg HS256 with public key as secret": func(p *fakeProvider) {
			p.sign = func(h map[string]any) string {
				h["alg"] = "HS256"
				return p.forgeUnsigned(h)
			}
		},
		"unknown kid": func(p *fakeProvider) {
			p.sign = func(h map[string]any) string { h["kid"] = "nope"; return p.forge(h, p.key) }
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			setup(e.p)
			b := e.browser()
			q := e.startLogin(b)
			rec := e.callback(b, q.Get("state"), e.approve(q, "sub-mallory"))
			if rec.Code != 403 || b.cookies[sessionName] != nil {
				t.Fatalf("accepted: %d", rec.Code)
			}
			if rec = b.do("GET", testBase+"/", nil, nil); rec.Code != 302 {
				t.Fatalf("index after failed login: %d", rec.Code)
			}
		})
	}
}

func (f *fakeProvider) forgeUnsigned(h map[string]any) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	return enc(h) + "." + enc(map[string]any{"iss": f.issuer(), "sub": "x", "aud": testClient, "exp": f.clock().Add(time.Hour).Unix(), "nonce": f.lastNonce}) + "."
}

// forge signs a token whose nonce matches the last issued code's nonce.
func (f *fakeProvider) forge(h map[string]any, key *rsa.PrivateKey) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	nonce := f.lastNonce
	in := enc(h) + "." + enc(map[string]any{"iss": f.issuer(), "sub": "sub-mallory", "aud": testClient, "exp": f.clock().Add(time.Hour).Unix(), "nonce": nonce})
	d := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, d[:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestOIDCForgedForgeHelpersAreValidWhenCorrectlySigned(t *testing.T) {
	// Control for the forged-token cases: the same construction signed with the
	// real key and the issued nonce is accepted, so the failures above are
	// caused by the key/alg/kid, not by a malformed helper.
	e := newEnv(t)
	e.p.sign = func(h map[string]any) string { return e.p.forge(h, e.p.key) }
	b := e.browser()
	q := e.startLogin(b)
	code := e.approve(q, "ignored")
	if rec := e.callback(b, q.Get("state"), code); rec.Code != 302 {
		t.Fatalf("control token rejected: %d", rec.Code)
	}
	if _, sub := e.whoami(b, e.csrf(b)); sub != "sub-mallory" {
		t.Fatalf("subject %q", sub)
	}
}

func TestOIDCSessionExpiryAndLogout(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	csrf := e.signIn(b, "sub-a")

	// Activity inside the idle window keeps the session alive, but never
	// past the absolute limit (24h from sign-in).
	start := e.now
	for {
		e.now = e.now.Add(50 * time.Minute)
		code, _ := e.whoami(b, csrf)
		if code == 401 {
			break
		}
		if e.now.Sub(start) >= 24*time.Hour || code != 200 {
			t.Fatalf("session outlived the absolute limit: %d at %v", code, e.now.Sub(start))
		}
	}
	if e.now.Sub(start) < 24*time.Hour {
		t.Fatalf("absolute expiry too early: %v", e.now.Sub(start))
	}
	if rec := b.do("GET", testBase+"/", nil, nil); rec.Code != 302 {
		t.Fatalf("index after absolute expiry: %d", rec.Code)
	}

	// Idle expiry.
	b = e.browser()
	csrf = e.signIn(b, "sub-a")
	e.now = e.now.Add(61 * time.Minute)
	if code, _ := e.whoami(b, csrf); code != 401 {
		t.Fatalf("idle expiry: %d", code)
	}

	// Logout revokes server-side: a copied cookie stops working.
	b = e.browser()
	csrf = e.signIn(b, "sub-a")
	stolen := *b.cookies[sessionName]
	if rec := b.do("POST", testBase+LogoutPath, map[string]string{"Origin": testOrigin}, nil); rec.Code != 403 {
		t.Fatalf("logout without csrf: %d", rec.Code)
	}
	if rec := b.do("POST", testBase+LogoutPath, map[string]string{"Origin": "https://evil.example", CSRFHeader: csrf}, nil); rec.Code != 403 {
		t.Fatalf("logout foreign origin: %d", rec.Code)
	}
	if rec := b.do("GET", testBase+LogoutPath, nil, nil); rec.Code != 405 {
		t.Fatalf("logout via GET: %d", rec.Code)
	}
	if rec := b.do("POST", testBase+LogoutPath, map[string]string{"Origin": testOrigin, CSRFHeader: csrf}, nil); rec.Code != 200 {
		t.Fatalf("logout: %d", rec.Code)
	}
	replay := e.browser()
	replay.cookies[sessionName] = &stolen
	if code, _ := e.whoami(replay, csrf); code != 401 {
		t.Fatalf("revoked session accepted: %d", code)
	}
}

func TestOIDCCSRFAndOriginOnMutations(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	csrf := e.signIn(b, "sub-a")
	put := `{"operation":"put","kind":"kv","key":"k","value":"dg=="}`
	if rec := e.data(b, "", put); rec.Code != 403 {
		t.Fatalf("missing csrf: %d", rec.Code)
	}
	if rec := e.data(b, "wrong", put); rec.Code != 403 {
		t.Fatalf("wrong csrf: %d", rec.Code)
	}
	// Another person's valid token does not work for this session.
	b2 := e.browser()
	csrf2 := e.signIn(b2, "sub-b")
	if csrf2 == csrf {
		t.Fatal("csrf tokens are shared")
	}
	if rec := e.data(b, csrf2, put); rec.Code != 403 {
		t.Fatalf("other session's csrf: %d", rec.Code)
	}
	for _, origin := range []string{"", "https://evil.example", testOrigin + ".evil.example", "http://home.example", testOrigin + "/"} {
		rec := b.do("POST", testBase+DataPath, map[string]string{"Origin": origin, CSRFHeader: csrf, "Content-Type": "application/json"}, []byte(put))
		if rec.Code != 403 {
			t.Fatalf("origin %q: %d", origin, rec.Code)
		}
	}
	if rec := b.do("POST", testBase+DataPath, map[string]string{"Origin": testOrigin, CSRFHeader: csrf, "Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"}, []byte(put)); rec.Code != 403 {
		t.Fatalf("cross-site fetch: %d", rec.Code)
	}
	// No cookie at all.
	if rec := e.data(e.browser(), csrf, put); rec.Code != 401 {
		t.Fatalf("no session: %d", rec.Code)
	}
	if rec := e.data(b, csrf, put); rec.Code != 200 {
		t.Fatalf("valid request refused: %d", rec.Code)
	}
}

func TestOIDCSubjectIsolation(t *testing.T) {
	e := newEnv(t)
	a, b := e.browser(), e.browser()
	ca, cb := e.signIn(a, "sub-alice"), e.signIn(b, "sub-bob")
	if rec := e.data(a, ca, `{"operation":"put","kind":"kv","key":"secret","value":"YQ=="}`); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := e.data(a, ca, `{"operation":"put","kind":"file","key":"f.txt","value":"YQ=="}`); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := e.data(b, cb, `{"operation":"get","kind":"kv","key":"secret"}`); rec.Code != 404 {
		t.Fatalf("bob read alice's kv: %d", rec.Code)
	}
	if rec := e.data(b, cb, `{"operation":"list","kind":"file","key":""}`); strings.Contains(rec.Body.String(), "f.txt") {
		t.Fatalf("bob listed alice's files: %s", rec.Body)
	}
	// A client-supplied subject is not a field the handler accepts.
	if rec := e.data(b, cb, `{"operation":"get","kind":"kv","key":"secret","subject":"sub-alice"}`); rec.Code != 400 {
		t.Fatalf("client subject accepted: %d", rec.Code)
	}
	// Signing in again as the same subject yields the same private data.
	a2 := e.browser()
	ca2 := e.signIn(a2, "sub-alice")
	if rec := e.data(a2, ca2, `{"operation":"get","kind":"kv","key":"secret"}`); rec.Code != 200 {
		t.Fatalf("alice lost her data: %d", rec.Code)
	}
}

func TestOIDCKeyRotationIsRefetchedAtMostEveryTenSeconds(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	e.signIn(b, "u") // primes the JWKS cache with k1
	// Provider rotates to a new key id; refetch is rate limited to 10s.
	newKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	e.p.key = newKey
	e.p.kidOverride = "k2"
	e.p.sign = func(h map[string]any) string { h["kid"] = "k2"; return e.p.forge(h, newKey) }
	b2 := e.browser()
	q := e.startLogin(b2)
	code := e.approve(q, "u")
	if rec := e.callback(b2, q.Get("state"), code); rec.Code != 403 {
		t.Fatalf("rotated key accepted before refetch window: %d", rec.Code)
	}
	// After the window the new key is fetched and the login succeeds.
	e.now = e.now.Add(11 * time.Second)
	q = e.startLogin(b2)
	if rec := e.callback(b2, q.Get("state"), e.approve(q, "u")); rec.Code != 302 {
		t.Fatalf("rotated key rejected after refetch window: %d", rec.Code)
	}
}

func TestNewOIDCValidatesConfig(t *testing.T) {
	e := newEnv(t)
	good := e.o.cfg
	good.HTTPClient = nil
	bad := map[string]func(*OIDCConfig){
		"no client id": func(c *OIDCConfig) { c.ClientID = "" },
		"no secret":    func(c *OIDCConfig) { c.ClientSecret = "" },
		"plain http":   func(c *OIDCConfig) { c.Issuer = "http://auth.example/" },
		"http origin": func(c *OIDCConfig) {
			c.Origin = "http://home.example"
			c.RedirectURL = "http://home.example" + testBase + CallbackPath
		},
		"origin path":      func(c *OIDCConfig) { c.Origin = testOrigin + "/x" },
		"bad base":         func(c *OIDCConfig) { c.BasePath = "/apps/../x" },
		"trailing slash":   func(c *OIDCConfig) { c.BasePath = "/apps/tasks/" },
		"foreign redirect": func(c *OIDCConfig) { c.RedirectURL = "https://evil.example" + testBase + CallbackPath },
		"redirect path":    func(c *OIDCConfig) { c.RedirectURL = testOrigin + "/callback" },
		"no static":        func(c *OIDCConfig) { c.StaticDir = "" },
		"empty static":     func(c *OIDCConfig) { c.StaticDir = t.TempDir() },
		"bad app":          func(c *OIDCConfig) { c.App = "Bad App" },
	}
	for name, m := range bad {
		c := good
		m(&c)
		if _, err := NewOIDC(c, e.store); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewOIDC(good, e.store); err != nil {
		t.Fatal(err)
	}
}

func TestReadSecretFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s")
	os.WriteFile(p, []byte("value\n"), 0o600)
	if v, err := ReadSecretFile(p); err != nil || v != "value" {
		t.Fatalf("%q %v", v, err)
	}
	os.WriteFile(p, nil, 0o600)
	if _, err := ReadSecretFile(p); err == nil {
		t.Fatal("empty accepted")
	}
	if _, err := ReadSecretFile(dir); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := ReadSecretFile(filepath.Join(dir, "none")); err == nil {
		t.Fatal("missing accepted")
	}
}

func TestClientWithCATrustsOnlyTheGivenBundle(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	dir := t.TempDir()
	pemFile := filepath.Join(dir, "ca.pem")
	cert := srv.Certificate()
	os.WriteFile(pemFile, pemEncode(cert.Raw), 0o600)
	hc, err := ClientWithCA(pemFile)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := hc.Get(srv.URL); err != nil {
		t.Fatalf("private CA not trusted: %v", err)
	} else {
		resp.Body.Close()
	}
	sys, _ := ClientWithCA("")
	if resp, err := sys.Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Fatal("system roots trusted an unknown CA")
	}
	os.WriteFile(pemFile, []byte("not a cert"), 0o600)
	if _, err := ClientWithCA(pemFile); err == nil {
		t.Fatal("garbage bundle accepted")
	}
}

func pemEncode(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestOIDCSessionEndpoint(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	url := testBase + SessionPath
	rec := b.do("GET", url, nil, nil)
	if rec.Code != 401 || rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), "csrf") {
		t.Fatalf("unauthenticated session: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	csrf := e.signIn(b, "sub-alice")
	rec = b.do("GET", url, nil, nil)
	var out map[string]string
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out["csrf"] != csrf || out["subject"] != "sub-alice" {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("headers: %v", rec.Header())
	}
	for h := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(h), "access-control-") {
			t.Fatalf("CORS header %s on session endpoint", h)
		}
	}
	if rec = b.do("POST", url, map[string]string{"Origin": testOrigin}, nil); rec.Code != 405 {
		t.Fatalf("POST: %d", rec.Code)
	}
	if rec = b.do("GET", url, map[string]string{"Sec-Fetch-Site": "cross-site"}, nil); rec.Code != 403 {
		t.Fatalf("cross-site: %d", rec.Code)
	}
	// Another person gets their own token, never this one.
	b2 := e.browser()
	e.signIn(b2, "sub-bob")
	rec = b2.do("GET", url, nil, nil)
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["csrf"] == csrf || out["subject"] != "sub-bob" {
		t.Fatalf("bob session: %s", rec.Body)
	}
	// After logout the endpoint refuses and the old token is gone.
	if rec = b.do("POST", testBase+LogoutPath, map[string]string{"Origin": testOrigin, CSRFHeader: csrf}, nil); rec.Code != 200 {
		t.Fatalf("logout: %d", rec.Code)
	}
	if rec = b.do("GET", url, nil, nil); rec.Code != 401 {
		t.Fatalf("after logout: %d", rec.Code)
	}
}

func TestOIDCCacheHeaders(t *testing.T) {
	e := newEnv(t)
	os.WriteFile(filepath.Join(e.static, "assets", "index-AbC123xy.js"), []byte("x"), 0o600)
	os.MkdirAll(filepath.Join(e.static, "_homestead"), 0o700)
	os.WriteFile(filepath.Join(e.static, "_homestead", "puter-bridge.js"), []byte("y"), 0o600)
	os.WriteFile(filepath.Join(e.static, "_homestead", "secret.json"), []byte("z"), 0o600)
	b := e.browser()
	cc := func(path string, want int) string {
		rec := b.do("GET", testBase+path, nil, nil)
		if rec.Code != want {
			t.Fatalf("%s: %d want %d", path, rec.Code, want)
		}
		return rec.Header().Get("Cache-Control")
	}
	// Signed out: the sign-in redirect and the login redirect are never stored.
	if got := cc("/", 302); got != "no-store" {
		t.Fatalf("redirect cache: %q", got)
	}
	if got := cc("/index.html", 302); got != "no-store" {
		t.Fatalf("redirect cache: %q", got)
	}
	e.signIn(b, "sub-a")
	if got := cc("/", 200); got != "no-store" {
		t.Fatalf("index cache: %q", got)
	}
	if got := cc("/index.html", 200); got != "no-store" {
		t.Fatalf("index.html cache: %q", got)
	}
	if got := cc("/assets/index-AbC123xy.js", 200); got != "public, max-age=31536000, immutable" {
		t.Fatalf("hashed asset: %q", got)
	}
	if got := cc("/assets/app.js", 200); got != "no-cache" {
		t.Fatalf("unhashed asset: %q", got)
	}
	if got := cc("/_homestead/puter-bridge.js", 200); got != "no-cache" {
		t.Fatalf("bridge shim: %q", got)
	}
	// Other _homestead/ files are not served and the refusal is not stored.
	if got := cc("/_homestead/secret.json", 404); got != "no-store" {
		t.Fatalf("_homestead file: %q", got)
	}
	for _, p := range []string{LoginPath, CallbackPath, LogoutPath, DataPath} {
		if got := b.do("GET", testBase+p, nil, nil).Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s cache: %q", p, got)
		}
	}
}
