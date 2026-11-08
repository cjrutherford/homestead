package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/docker"
	"homestead/internal/sso"
	"homestead/internal/store"
)

const ssoToken = "ak-token-DO-NOT-LEAK"

// ssoWorld is Authentik in one test server: the admin API Homestead
// configures it through plus the OIDC endpoints it signs in against.
type ssoWorld struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu       sync.Mutex
	secret   string // set by the provider POST
	provider map[string]any
	app      map[string]any
	pending  map[string]ssoFlow
	sub      string
	down     bool
	// authAge is how long ago the person signed in at Authentik.
	authAge time.Duration
	// access state (S314)
	groups   map[string]map[string]any
	bindings []map[string]any
	mappings map[string]map[string]any
	// people (S317)
	users map[int]map[string]any
	calls []string
	// recovery flow on the default brand (S333)
	recovery string
}

type ssoFlow struct{ nonce, challenge, redirect string }

func newSSOWorld(t *testing.T) *ssoWorld {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	w := &ssoWorld{t: t, key: k, pending: map[string]ssoFlow{}, sub: "owner-sub",
		groups: map[string]map[string]any{}, mappings: map[string]map[string]any{}}
	w.srv = httptest.NewServer(http.HandlerFunc(w.serve))
	t.Cleanup(w.srv.Close)
	return w
}

func (w *ssoWorld) issuer() string { return w.srv.URL + "/application/o/homestead/" }

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (w *ssoWorld) serve(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	enc := json.NewEncoder(rw)
	if w.down {
		http.Error(rw, "down", http.StatusBadGateway)
		return
	}
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/.well-known/openid-configuration"):
		_ = enc.Encode(map[string]any{"issuer": w.issuer(), "authorization_endpoint": w.srv.URL + "/application/o/authorize/",
			"token_endpoint": w.srv.URL + "/application/o/token/", "jwks_uri": w.srv.URL + "/application/o/homestead/jwks/"})
	case strings.HasSuffix(p, "/jwks/"):
		_ = enc.Encode(map[string]any{"keys": []map[string]any{{"kty": "RSA", "use": "sig", "kid": "k", "alg": "RS256",
			"n": b64u(w.key.N.Bytes()), "e": b64u(big.NewInt(int64(w.key.E)).Bytes())}}})
	case strings.HasSuffix(p, "/token/"):
		_ = r.ParseForm()
		flow, ok := w.pending[r.Form.Get("code")]
		delete(w.pending, r.Form.Get("code"))
		if !ok || r.Form.Get("client_secret") != w.secret || w.secret == "" || sso.Challenge(r.Form.Get("code_verifier")) != flow.challenge ||
			r.Form.Get("redirect_uri") != flow.redirect {
			http.Error(rw, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		claims := map[string]any{"iss": w.issuer(), "sub": w.sub, "aud": "homestead", "nonce": flow.nonce,
			"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"auth_time": time.Now().Add(-w.authAge).Unix()}
		hb, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k"})
		cb, _ := json.Marshal(claims)
		in := b64u(hb) + "." + b64u(cb)
		sum := sha256.Sum256([]byte(in))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, w.key, crypto.SHA256, sum[:])
		_ = enc.Encode(map[string]any{"id_token": in + "." + b64u(sig)})
	case strings.HasPrefix(p, "/api/v3/"):
		w.admin(rw, r, enc)
	default:
		http.NotFound(rw, r)
	}
}

func (w *ssoWorld) admin(rw http.ResponseWriter, r *http.Request, enc *json.Encoder) {
	if r.Header.Get("Authorization") != "Bearer "+ssoToken {
		http.Error(rw, "no", http.StatusForbidden)
		return
	}
	var body map[string]any
	if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	res := func(items ...any) { _ = enc.Encode(map[string]any{"results": items}) }
	w.calls = append(w.calls, r.Method+" "+r.URL.Path)
	if w.peopleAPI(rw, r, body, enc) {
		return
	}
	switch p := r.URL.Path; {
	case p == "/api/v3/admin/version/":
		_ = enc.Encode(map[string]any{"version_current": "2026.8.3"})
	case p == "/api/v3/flows/instances/" && r.Method == http.MethodPost:
		_ = enc.Encode(map[string]any{"pk": "f-" + body["slug"].(string), "slug": body["slug"]})
	case p == "/api/v3/flows/instances/default-authentication-flow/":
		_ = enc.Encode(body)
	case p == "/api/v3/flows/instances/":
		res(map[string]any{"pk": "f", "slug": r.URL.Query().Get("slug")})
	case p == "/api/v3/core/brands/":
		res(map[string]any{"brand_uuid": "b1", "domain": "authentik-default", "default": true, "flow_recovery": w.recovery})
	case p == "/api/v3/core/brands/b1/":
		w.recovery, _ = body["flow_recovery"].(string)
		_ = enc.Encode(body)
	case strings.HasPrefix(p, "/api/v3/stages/") || p == "/api/v3/flows/bindings/" || p == "/api/v3/policies/expression/":
		if r.Method == http.MethodPost {
			body["pk"] = "s-" + fmt.Sprint(body["name"])
			_ = enc.Encode(body)
			return
		}
		res()
	case p == "/api/v3/crypto/certificatekeypairs/":
		res(map[string]any{"pk": "kp", "name": r.URL.Query().Get("name")})
	case p == "/api/v3/propertymappings/provider/scope/" && r.Method == http.MethodPost:
		body["pk"] = "m-" + body["name"].(string)
		w.mappings[body["name"].(string)] = body
		rw.WriteHeader(http.StatusCreated)
		_ = enc.Encode(body)
	case p == "/api/v3/propertymappings/provider/scope/" && r.URL.Query().Get("name") != "":
		if m, ok := w.mappings[r.URL.Query().Get("name")]; ok {
			res(m)
		} else {
			res()
		}
	case p == "/api/v3/core/groups/" && r.Method == http.MethodGet:
		var out []any
		for _, g := range w.groups {
			if g["name"] == r.URL.Query().Get("name") {
				out = append(out, g)
			}
		}
		res(out...)
	case p == "/api/v3/core/groups/" && r.Method == http.MethodPost:
		g := map[string]any{"pk": "g-" + body["name"].(string), "name": body["name"], "users": []any{}}
		w.groups[body["name"].(string)] = g
		rw.WriteHeader(http.StatusCreated)
		_ = enc.Encode(g)
	case strings.HasSuffix(p, "/add_user/"):
		for _, g := range w.groups {
			if "/api/v3/core/groups/"+g["pk"].(string)+"/add_user/" == p {
				g["users"] = append(g["users"].([]any), body["pk"])
			}
		}
		rw.WriteHeader(http.StatusNoContent)
	case p == "/api/v3/core/users/":
		res(map[string]any{"pk": 7, "uid": w.sub, "type": "internal", "is_active": true})
	case p == "/api/v3/policies/bindings/" && r.Method == http.MethodGet:
		var out []any
		for _, b := range w.bindings {
			if t := r.URL.Query().Get("target"); t != "" && b["target"] != t {
				continue
			}
			out = append(out, b)
		}
		res(out...)
	case p == "/api/v3/policies/bindings/" && r.Method == http.MethodPost && body["policy"] != nil:
		rw.WriteHeader(http.StatusCreated)
		_ = enc.Encode(body)
	case p == "/api/v3/policies/bindings/" && r.Method == http.MethodPost:
		w.bindings = append(w.bindings, body)
		rw.WriteHeader(http.StatusCreated)
		_ = enc.Encode(body)
	case p == "/api/v3/propertymappings/provider/scope/":
		res(map[string]any{"pk": "1", "scope_name": "openid"}, map[string]any{"pk": "2", "scope_name": "profile"}, map[string]any{"pk": "3", "scope_name": "email"})
	case p == "/api/v3/providers/oauth2/" && r.Method == http.MethodGet:
		if w.provider != nil {
			res(w.provider)
		} else {
			res()
		}
	case strings.HasPrefix(p, "/api/v3/providers/oauth2/"):
		if r.Method == http.MethodPost {
			body["pk"] = 1
			w.provider = body
		} else {
			for k, v := range body {
				w.provider[k] = v
			}
		}
		w.secret, _ = w.provider["client_secret"].(string)
		_ = enc.Encode(w.provider)
	case strings.HasPrefix(p, "/api/v3/core/applications/") && r.Method == http.MethodGet:
		if w.app == nil {
			http.NotFound(rw, r)
			return
		}
		_ = enc.Encode(w.app)
	case strings.HasPrefix(p, "/api/v3/core/applications/"):
		if r.Method == http.MethodPost {
			body["pk"] = "app-pk"
			w.app = body
		} else {
			for k, v := range body {
				w.app[k] = v
			}
		}
		_ = enc.Encode(w.app)
	default:
		http.NotFound(rw, r)
	}
}

// authorize plays the browser at Authentik and returns the callback URL
// Authentik would send it back to.
func (w *ssoWorld) authorize(authURL string) string {
	u, err := url.Parse(authURL)
	if err != nil {
		w.t.Fatal(err)
	}
	q := u.Query()
	code := fmt.Sprintf("c%d", time.Now().UnixNano())
	w.mu.Lock()
	w.pending[code] = ssoFlow{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	w.mu.Unlock()
	back, _ := url.Parse(q.Get("redirect_uri"))
	bq := back.Query()
	bq.Set("code", code)
	bq.Set("state", q.Get("state"))
	back.RawQuery = bq.Encode()
	return back.String()
}

func (w *ssoWorld) setDown(v bool) { w.mu.Lock(); w.down = v; w.mu.Unlock() }

// ssoApp is an enforced server with an owner and a configured Authentik.
type ssoApp struct {
	*enforcedApp
	w     *ssoWorld
	owner struct{ token, csrf string }
}

const ssoLocal = "127.0.0.1:8080"

func (a *ssoApp) req(method, path, body string, token string, opts ...func(*http.Request)) *httptest.ResponseRecorder {
	return a.do(method, path, body, func(r *http.Request) {
		r.Host = ssoLocal
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		for _, o := range opts {
			o(r)
		}
	})
}

func reauth(pw string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(ReauthHeader, pw) }
}

const ownerPassword = "long-enough-password"

func newSSOApp(t *testing.T) *ssoApp {
	t.Helper()
	old := authentikInstalled
	authentikInstalled = func(*store.Store) bool { return true }
	t.Cleanup(func() { authentikInstalled = old })
	a := &ssoApp{enforcedApp: newEnforcedApp(t), w: newSSOWorld(t)}
	setup := a.setup(t)
	var resp struct {
		Token, CSRF string
	}
	var raw map[string]any
	_ = json.Unmarshal(setup.Body.Bytes(), &raw)
	resp.Token, _ = raw["token"].(string)
	a.owner.token = resp.Token
	return a
}

// configure runs plan, approve and apply through the API.
func (a *ssoApp) configure(t *testing.T) {
	t.Helper()
	a.configureKeeping(t, false)
}

func (a *ssoApp) configureKeeping(t *testing.T, keep bool) {
	t.Helper()
	plan := a.req("POST", "/api/v1/sso/plan", fmt.Sprintf(`{"authentik_url":%q,"origins":["http://127.0.0.1:8080","https://homestead.example.test"],"api_token":%q}`,
		a.w.srv.URL, ssoToken), a.owner.token)
	if plan.Code != 200 {
		t.Fatalf("plan = %d %s", plan.Code, plan.Body)
	}
	var p struct {
		Digest string `json:"digest"`
	}
	_ = json.Unmarshal(plan.Body.Bytes(), &p)
	ap := a.req("POST", "/api/v1/sso/approve", fmt.Sprintf(`{"digest":%q,"api_token":%q,"keep_token":%t}`, p.Digest, ssoToken, keep), a.owner.token)
	if ap.Code != 200 {
		t.Fatalf("approve = %d %s", ap.Code, ap.Body)
	}
	run := a.req("POST", "/api/v1/sso/apply", "", a.owner.token)
	if run.Code != 200 {
		t.Fatalf("apply = %d %s", run.Code, run.Body)
	}
	var op struct{ ID int64 }
	_ = json.Unmarshal([]byte(strings.Replace(run.Body.String(), `"id"`, `"ID"`, 1)), &op)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rec := a.req("GET", fmt.Sprintf("/api/v1/operations/%d", op.ID), "", a.owner.token)
		var o struct{ Status, Error string }
		_ = json.Unmarshal(rec.Body.Bytes(), &o)
		switch strings.ToLower(o.Status) {
		case "succeeded":
			return
		case "failed", "cancelled":
			t.Fatalf("apply operation %s: %s", o.Status, o.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("apply operation did not finish")
}

// linkOwner runs the whole link ceremony and returns nothing on success.
func (a *ssoApp) linkOwner(t *testing.T) {
	t.Helper()
	rec := a.req("POST", "/api/v1/sso/link", "", a.owner.token, reauth(ownerPassword))
	if rec.Code != 200 {
		t.Fatalf("link = %d %s", rec.Code, rec.Body)
	}
	var out struct{ URL string }
	_ = json.Unmarshal([]byte(strings.Replace(rec.Body.String(), `"url"`, `"URL"`, 1)), &out)
	back := a.w.authorize(out.URL)
	cb := a.followCallback(back, "")
	if cb.Code != http.StatusFound || !strings.HasPrefix(cb.Header().Get("Location"), "/sign-in?linked=1") {
		t.Fatalf("link callback = %d %s", cb.Code, cb.Header().Get("Location"))
	}
}

// followCallback hits the callback the way the browser returns from
// Authentik: no session cookie (SameSite=Strict is not sent), optionally the
// state cookie.
func (a *ssoApp) followCallback(rawURL, stateCookie string) *httptest.ResponseRecorder {
	u, _ := url.Parse(rawURL)
	return a.do("GET", u.RequestURI(), "", func(r *http.Request) {
		r.Host = u.Host
		if stateCookie != "" {
			r.AddCookie(&http.Cookie{Name: "homestead_sso", Value: stateCookie})
		}
	})
}

// browserLogin runs the sign-in flow and returns the callback response and
// the minted cookies.
func (a *ssoApp) browserLogin(t *testing.T, host string) *httptest.ResponseRecorder {
	t.Helper()
	start := a.do("GET", "/api/v1/sso/login", "", func(r *http.Request) { r.Host = host })
	if start.Code != http.StatusFound {
		t.Fatalf("login start = %d %s", start.Code, start.Body)
	}
	state := ""
	for _, c := range start.Result().Cookies() {
		if c.Name == "homestead_sso" {
			state = c.Value
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/api/v1/sso" {
				t.Fatalf("state cookie not HttpOnly/Lax/scoped: %+v", c)
			}
		}
	}
	back := a.w.authorize(start.Header().Get("Location"))
	return a.followCallback(back, state)
}

func cookiesOf(rec *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		out[c.Name] = c
	}
	return out
}

func (a *ssoApp) ssoSession(t *testing.T) (token, csrf string) {
	t.Helper()
	rec := a.browserLogin(t, ssoLocal)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?sso=done" {
		t.Fatalf("callback = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	c := cookiesOf(rec)
	if c["homestead_session"] == nil || c["homestead_csrf"] == nil {
		t.Fatal("callback did not mint the standard session cookies")
	}
	return c["homestead_session"].Value, c["homestead_csrf"].Value
}

func cookieAuth(token, csrf string) func(*http.Request) {
	return func(r *http.Request) {
		// Both names: the daemon reads only the one that fits the scheme (S470).
		r.AddCookie(&http.Cookie{Name: "homestead_session", Value: token})
		r.AddCookie(&http.Cookie{Name: "__Host-homestead_session", Value: token})
		if csrf != "" {
			r.Header.Set(CSRFHeader, csrf)
		}
	}
}

func TestSSORoutesAre404BeforeClaimAndUntilConfiguredAndLinked(t *testing.T) {
	a := &ssoApp{enforcedApp: newEnforcedApp(t), w: newSSOWorld(t)}
	for _, p := range []string{"/api/v1/sso/options", "/api/v1/sso/login", "/api/v1/sso/callback?state=x&code=y"} {
		if rec := a.req("GET", p, "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("unclaimed %s = %d, want 404", p, rec.Code)
		}
	}
	a.setup(t)
	for _, p := range []string{"/api/v1/sso/options", "/api/v1/sso/login", "/api/v1/sso/callback?state=x&code=y"} {
		if rec := a.req("GET", p, "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("claimed but unconfigured %s = %d, want 404", p, rec.Code)
		}
	}
}

func TestSSOConfiguredButUnlinkedIsStill404ForOptionsAndLogin(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	for _, p := range []string{"/api/v1/sso/options", "/api/v1/sso/login"} {
		if rec := a.req("GET", p, "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("unlinked %s = %d, want 404", p, rec.Code)
		}
	}
	// A callback with no live link intent is not found either.
	if rec := a.req("GET", "/api/v1/sso/callback?state=nope&code=x", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("callback without an intent = %d, want 404", rec.Code)
	}
}

func TestSSOEndToEndLinkLoginSessionAndUnlink(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)

	opt := a.req("GET", "/api/v1/sso/options", "", "")
	if opt.Code != 200 || strings.TrimSpace(opt.Body.String()) != `{"enabled":true,"password":true}` {
		t.Fatalf("options = %d %s", opt.Code, opt.Body)
	}
	// On a public dashboard name only Authentik signs in (ADR-037 §4).
	if pub := a.publicDo(dashPublic, "GET", "/api/v1/sso/options", nil); !strings.Contains(pub.Body.String(), `"password":false`) {
		t.Fatalf("public options = %d %s", pub.Code, pub.Body)
	}

	token, csrf := a.ssoSession(t)
	// It is the standard session: it reads, mutating calls need CSRF, and
	// logout revokes it.
	if rec := a.req("GET", "/api/v1/sso", "", "", cookieAuth(token, "")); rec.Code != 200 {
		t.Fatalf("oidc session read = %d", rec.Code)
	} else if !strings.Contains(rec.Body.String(), `"session_method":"oidc"`) {
		t.Fatalf("status must say the session is oidc: %s", rec.Body)
	}
	if rec := a.req("POST", "/api/v1/sso/plan", "{}", "", cookieAuth(token, "")); rec.Code != 403 || rec.Header().Get(ErrorCodeHeader) != "" {
		t.Fatalf("oidc session without CSRF = %d (%s), want a plain CSRF 403", rec.Code, rec.Header().Get(ErrorCodeHeader))
	}
	if rec := a.req("DELETE", "/api/v1/sessions", "", "", cookieAuth(token, csrf)); rec.Code != http.StatusNoContent {
		t.Fatalf("oidc logout = %d", rec.Code)
	}
	if rec := a.req("GET", "/api/v1/sso", "", "", cookieAuth(token, "")); rec.Code != 401 {
		t.Fatalf("revoked oidc session = %d, want 401", rec.Code)
	}

	// Unlink needs the fresh password and revokes oidc sessions only.
	token2, _ := a.ssoSession(t)
	if rec := a.req("DELETE", "/api/v1/sso/link", "", a.owner.token); rec.Code != 403 || rec.Header().Get(ErrorCodeHeader) != ErrorCodeReauthRequired {
		t.Fatalf("unlink without a password = %d", rec.Code)
	}
	if rec := a.req("DELETE", "/api/v1/sso/link", "", a.owner.token, reauth("wrong-password-here")); rec.Code != 403 || rec.Header().Get(ErrorCodeHeader) != ErrorCodeReauthInvalid {
		t.Fatalf("unlink with a wrong password = %d", rec.Code)
	}
	if rec := a.req("DELETE", "/api/v1/sso/link", "", a.owner.token, reauth(ownerPassword)); rec.Code != 200 {
		t.Fatalf("unlink = %d %s", rec.Code, rec.Body)
	}
	if rec := a.req("GET", "/api/v1/sso", "", "", cookieAuth(token2, "")); rec.Code != 401 {
		t.Fatalf("oidc session after unlink = %d, want 401", rec.Code)
	}
	if rec := a.req("GET", "/api/v1/sso", "", a.owner.token); rec.Code != 200 {
		t.Fatalf("the password session must survive unlink: %d", rec.Code)
	}
	if rec := a.req("GET", "/api/v1/sso/options", "", ""); rec.Code != 404 {
		t.Fatalf("options after unlink = %d, want 404", rec.Code)
	}
}

func TestSSOLinkNeedsAFreshPasswordFromEverySession(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	for name, opt := range map[string]func(*http.Request){
		"missing": nil, "wrong": reauth("not-the-password"),
	} {
		var opts []func(*http.Request)
		if opt != nil {
			opts = append(opts, opt)
		}
		if rec := a.req("POST", "/api/v1/sso/link", "", a.owner.token, opts...); rec.Code != 403 {
			t.Errorf("link with a %s password = %d, want 403", name, rec.Code)
		}
	}
	// No link intent was created by the refusals: a callback finds nothing.
	if rec := a.req("GET", "/api/v1/sso/callback?state=x&code=y", "", ""); rec.Code != 404 {
		t.Fatalf("callback = %d", rec.Code)
	}
}

func TestSSOStepUpForOIDCSessions(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	token, csrf := a.ssoSession(t)
	cookie := cookieAuth(token, csrf)
	routes := []struct {
		method, path, body string
		destructive        bool
	}{
		{"POST", "/api/v1/sso/plan", "{}", false},
		{"POST", "/api/v1/sso/approve", `{"digest":"x"}`, false},
		{"POST", "/api/v1/sso/apply", "", false},
		{"POST", "/api/v1/sso/link", "", false},
		{"POST", "/api/v1/secrets/rekey", `{}`, false},
		{"POST", "/api/v1/backups/identity/export", `{}`, false},
		{"POST", "/api/v1/catalog/nextcloud/secrets/admin_password/reveal", `{"confirm":true}`, false},
		{"PUT", "/api/v1/people/1/apps/immich/admin", `{"confirm":true}`, false},
		{"POST", "/api/v1/people/1/password-reset", `{"confirm":true}`, false},
		{"POST", "/api/v1/people/1/mfa-reset", `{"confirm":true}`, false},
		{"POST", "/api/v1/people/1/remove", `{"confirm":true}`, false},
		{"POST", "/api/v1/people/1/resolve", `{"confirm":true,"action":"keep"}`, false},
		{"DELETE", "/api/v1/sso/link", "", true},
		{"DELETE", "/api/v1/sso", "", true},
	}
	for i, rt := range routes {
		rec := a.req(rt.method, rt.path, rt.body, "", cookie)
		if rec.Code != 403 || rec.Header().Get(ErrorCodeHeader) != ErrorCodeReauthRequired {
			t.Errorf("%s %s without the password = %d (%s), want 403 reauth-required", rt.method, rt.path, rec.Code, rec.Header().Get(ErrorCodeHeader))
		}
		// Wrong guesses feed the local lockout, so only a few are tried.
		if i < 2 {
			rec = a.req(rt.method, rt.path, rt.body, "", cookie, reauth("wrong-password-xx"))
			if rec.Code != 403 || rec.Header().Get(ErrorCodeHeader) != ErrorCodeReauthInvalid {
				t.Errorf("%s %s with a wrong password = %d (%s), want 403 reauth-invalid", rt.method, rt.path, rec.Code, rec.Header().Get(ErrorCodeHeader))
			}
		}
		if rt.destructive {
			continue
		}
		// With the right password the request reaches its handler: any
		// answer without a re-auth code proves the boundary let it by.
		rec = a.req(rt.method, rt.path, rt.body, "", cookie, reauth(ownerPassword))
		if code := rec.Header().Get(ErrorCodeHeader); code == ErrorCodeReauthRequired || code == ErrorCodeReauthInvalid {
			t.Errorf("%s %s with the right password was refused as %s", rt.method, rt.path, code)
		}
	}
	// A plain read needs nothing extra.
	if rec := a.req("GET", "/api/v1/sso", "", "", cookie); rec.Code != 200 {
		t.Fatalf("read = %d", rec.Code)
	}
	// The two destructive routes, with the right password, in order: unlink
	// ends the oidc session itself, so each gets a fresh one.
	if rec := a.req("DELETE", "/api/v1/sso/link", "", "", cookie, reauth(ownerPassword)); rec.Code != 200 {
		t.Fatalf("oidc unlink with the password = %d %s", rec.Code, rec.Body)
	}
	a.linkOwner(t)
	token, csrf = a.ssoSession(t)
	if rec := a.req("DELETE", "/api/v1/sso", "", "", cookieAuth(token, csrf), reauth(ownerPassword)); rec.Code != 200 {
		t.Fatalf("oidc disable with the password = %d %s", rec.Code, rec.Body)
	}
}

func TestSSOStepUpListIsExactAndClassified(t *testing.T) {
	want := map[string]stepUpMode{
		"POST /api/v1/sso/link": stepUpHandler, "DELETE /api/v1/sso/link": stepUpHandler,
		"POST /api/v1/sso/plan": stepUpBoundary, "POST /api/v1/sso/approve": stepUpBoundary,
		"POST /api/v1/sso/apply": stepUpBoundary, "DELETE /api/v1/sso": stepUpBoundary,
		"POST /api/v1/secrets/rekey": stepUpBoundary, "POST /api/v1/backups/identity/export": stepUpBoundary,
		"POST /api/v1/catalog/{name}/secrets/{secret}/reveal": stepUpBoundary,
		"PUT /api/v1/people/{id}/apps/{app}/admin":            stepUpBoundary,
		"POST /api/v1/catalog/{name}/install-admin":           stepUpBoundary,
		"POST /api/v1/catalog/{name}/setup/resume-admin":      stepUpBoundary,
		"POST /api/v1/people/{id}/password-reset":             stepUpBoundary,
		"POST /api/v1/people/{id}/mfa-reset":                  stepUpBoundary,
		"POST /api/v1/people/{id}/remove":                     stepUpBoundary,
		"POST /api/v1/people/{id}/resolve":                    stepUpBoundary,
	}
	if len(stepUpRoutes) != len(want) {
		t.Fatalf("step-up list has %d routes, want %d", len(stepUpRoutes), len(want))
	}
	srv, _ := fullServer(t)
	registered := map[string]bool{}
	for _, p := range srv.Routes() {
		registered[p] = true
	}
	for p, mode := range want {
		if stepUpRoutes[p] != mode {
			t.Errorf("%s step-up mode = %v, want %v", p, stepUpRoutes[p], mode)
		}
		if !registered[p] {
			t.Errorf("step-up route %q is not registered", p)
		}
		if c, _ := Classify(p); c != ClassSession {
			t.Errorf("step-up route %q is %s, want session", p, c)
		}
	}
}

func TestPasswordSessionsAreNotSteppedUp(t *testing.T) {
	a := newSSOApp(t)
	for _, rt := range []struct{ method, path string }{
		{"POST", "/api/v1/sso/plan"}, {"DELETE", "/api/v1/sso"},
	} {
		rec := a.req(rt.method, rt.path, "{}", a.owner.token)
		if rec.Header().Get(ErrorCodeHeader) != "" {
			t.Errorf("password session on %s %s got %s", rt.method, rt.path, rec.Header().Get(ErrorCodeHeader))
		}
	}
}

func TestSSORefusesForgedHostAndIgnoresForwardedHeaders(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	opt := a.do("GET", "/api/v1/sso/options", "", func(r *http.Request) { r.Host = "evil.example" })
	if opt.Code != 200 || !strings.Contains(opt.Body.String(), `"enabled":false`) {
		t.Fatalf("options at a forged host = %d %s, want enabled:false", opt.Code, opt.Body)
	}
	rec := a.do("GET", "/api/v1/sso/login", "", func(r *http.Request) { r.Host = "evil.example" })
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?sso_error=origin" {
		t.Fatalf("login at a forged host = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// Forwarded headers never choose the redirect URI.
	rec = a.do("GET", "/api/v1/sso/login", "", func(r *http.Request) {
		r.Host = ssoLocal
		r.Header.Set("X-Forwarded-Host", "evil.example")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Forwarded", "host=evil.example;proto=https")
	})
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusFound || !strings.Contains(loc, "redirect_uri=http%3A%2F%2F127.0.0.1%3A8080%2Fapi%2Fv1%2Fsso%2Fcallback") {
		t.Fatalf("redirect built from forwarded headers? %s", loc)
	}
	if strings.Contains(loc, "evil.example") {
		t.Fatalf("forwarded host reached the redirect: %s", loc)
	}
	// The public hostname behind a TLS-terminating tunnel uses the planned https origin.
	rec = a.do("GET", "/api/v1/sso/login", "", func(r *http.Request) { r.Host = "homestead.example.test" })
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "redirect_uri=https%3A%2F%2Fhomestead.example.test%2Fapi%2Fv1%2Fsso%2Fcallback") {
		t.Fatalf("public host redirect = %s", loc)
	}
	var secure bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "homestead_sso" {
			secure = c.Secure
		}
	}
	if !secure {
		t.Fatal("the state cookie for an https origin must be Secure")
	}
}

func TestSSOCallbackReplayAndForgedStateAreRefused(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	start := a.do("GET", "/api/v1/sso/login", "", func(r *http.Request) { r.Host = ssoLocal })
	state := cookiesOf(start)["homestead_sso"].Value
	back := a.w.authorize(start.Header().Get("Location"))
	if rec := a.followCallback(back, state); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?sso=done" {
		t.Fatalf("first callback = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec := a.followCallback(back, state)
	if rec.Header().Get("Location") != "/login?sso_error=state" || cookiesOf(rec)["homestead_session"] != nil {
		t.Fatalf("replayed callback = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// A callback without the browser's state cookie (login CSRF) fails.
	start = a.do("GET", "/api/v1/sso/login", "", func(r *http.Request) { r.Host = ssoLocal })
	back = a.w.authorize(start.Header().Get("Location"))
	rec = a.followCallback(back, "")
	if rec.Header().Get("Location") != "/login?sso_error=state" || cookiesOf(rec)["homestead_session"] != nil {
		t.Fatalf("callback without the cookie = %s", rec.Header().Get("Location"))
	}
}

func TestSSOUnlinkedSubjectGetsNoSessionAndCreatesNothing(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	a.w.mu.Lock()
	a.w.sub = "someone-else"
	a.w.mu.Unlock()
	rec := a.browserLogin(t, ssoLocal)
	if rec.Header().Get("Location") != "/login?sso_error=unlinked" || cookiesOf(rec)["homestead_session"] != nil {
		t.Fatalf("unlinked subject = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	var n int
	_ = a.st.DB().QueryRow(`SELECT COUNT(*) FROM identities`).Scan(&n)
	if n != 1 {
		t.Fatalf("identities = %d, want only the owner", n)
	}
	audit := a.req("GET", "/api/v1/audit?limit=200", "", a.owner.token).Body.String()
	if !strings.Contains(audit, "sso.login") || !strings.Contains(audit, "refused: unlinked subject=someone-else") {
		t.Fatalf("the refusal was not audited: %s", audit)
	}
}

func TestLocalLoginAndLoginPageSurviveAuthentikBeingDown(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	a.w.setDown(true)
	// The button is still offered: it comes from local state.
	if opt := a.req("GET", "/api/v1/sso/options", "", ""); opt.Code != 200 || !strings.Contains(opt.Body.String(), "true") {
		t.Fatalf("options with Authentik down = %d %s", opt.Code, opt.Body)
	}
	// Following it returns to the login page with a fixed code.
	rec := a.do("GET", "/api/v1/sso/login", "", func(r *http.Request) { r.Host = ssoLocal })
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?sso_error=unavailable" {
		t.Fatalf("login with Authentik down = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// The local password login is untouched.
	login := a.req("POST", "/api/v1/sessions", `{"username":"owner","password":"`+ownerPassword+`"}`, "")
	if login.Code != 200 {
		t.Fatalf("local login with Authentik down = %d %s", login.Code, login.Body)
	}
	// And so is the disable switch.
	if rec := a.req("DELETE", "/api/v1/sso", "", a.owner.token); rec.Code != 200 {
		t.Fatalf("disable = %d %s", rec.Code, rec.Body)
	}
	if rec := a.req("GET", "/api/v1/sso/options", "", ""); rec.Code != 404 {
		t.Fatalf("options after disable = %d", rec.Code)
	}
}

func TestSSOAndLocalLockoutDoNotInterfere(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	for i := 0; i < 5; i++ {
		a.req("POST", "/api/v1/sessions", `{"username":"owner","password":"nope-nope-nope-nope"}`, "")
	}
	if rec := a.req("POST", "/api/v1/sessions", `{"username":"owner","password":"`+ownerPassword+`"}`, ""); rec.Code != 429 {
		t.Fatalf("local login while locked = %d, want 429", rec.Code)
	}
	// SSO still works for the locked account...
	token, _ := a.ssoSession(t)
	// ...and does not clear the local counters.
	lock := a.req("GET", "/api/v1/admins/lockout", "", "", cookieAuth(token, ""))
	if lock.Code != 200 || !strings.Contains(lock.Body.String(), `"locked":true`) {
		t.Fatalf("lockout after SSO login = %d %s", lock.Code, lock.Body)
	}
}

func TestSSOSecretsNeverAppearInResponsesOrAudit(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	a.ssoSession(t)
	secret := a.w.secret
	if secret == "" {
		t.Fatal("no client secret was configured in Authentik")
	}
	st := a.req("GET", "/api/v1/sso", "", a.owner.token).Body.String()
	auditBody := a.req("GET", "/api/v1/audit?limit=500", "", a.owner.token).Body.String()
	export := a.req("GET", "/api/v1/config/export", "", a.owner.token).Body.String()
	for name, blob := range map[string]string{"status": st, "audit": auditBody, "config export": export} {
		for _, bad := range []string{secret, ssoToken} {
			if strings.Contains(blob, bad) {
				t.Errorf("%s contains a secret", name)
			}
		}
	}
	for _, want := range []string{"sso.plan", "sso.approve", "sso.apply", "sso.link", "sso.login", "sso.token"} {
		if !strings.Contains(auditBody, want) {
			t.Errorf("audit lacks %s", want)
		}
	}
	// Default: the API token was used once and forgotten.
	if strings.Contains(st, `"api_token_kept":true`) {
		t.Fatal("the API token was kept without an opt-in")
	}
}

func TestSSOLoginRateLimitIsPerRemoteAddrNotForwardedFor(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	flood := func(r *http.Request) {
		r.Host = ssoLocal
		r.RemoteAddr = "198.51.100.9:4000"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("10.9.%d.1", time.Now().UnixNano()%250)) // must be ignored
	}
	limited := false
	for i := 0; i < 40; i++ {
		rec := a.do("GET", "/api/v1/sso/login", "", flood)
		if strings.Contains(rec.Header().Get("Location"), "sso_error=rate") {
			limited = true
		}
	}
	if !limited {
		t.Fatal("the flooding address was never limited")
	}
	rec := a.do("GET", "/api/v1/sso/login", "", func(r *http.Request) { r.Host = ssoLocal; r.RemoteAddr = "192.0.2.50:5000" })
	if rec.Code != http.StatusFound || strings.Contains(rec.Header().Get("Location"), "sso_error") {
		t.Fatalf("another address was blocked: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSSODefaultOriginsHonourRouteTLS(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), store.DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: []docker.Route{
		{Host: "plain.lan", Target: docker.Target{Host: "127.0.0.1", Port: 8080}, TLS: docker.TLSOff},
		{Host: "secure.lan", Target: docker.Target{Host: "127.0.0.1", Port: 8080}, TLS: docker.TLSInternal},
	}}); err != nil {
		t.Fatal(err)
	}
	d := ssoDefaults(st, 8080)
	joined := strings.Join(d.Origins, " ")
	if !strings.Contains(joined, "http://plain.lan") || strings.Contains(joined, "https://plain.lan") || !strings.Contains(joined, "https://secure.lan") {
		t.Fatalf("origins = %v", d.Origins)
	}
}

// TestSSODefaultsWireNames pins the JSON names the dashboard reads
// (defaults.authentik_url, defaults.origins); without tags the form showed
// "undefined" (S292).
func TestSSODefaultsWireNames(t *testing.T) {
	raw, err := json.Marshal(sso.Defaults{AuthentikURL: "https://authentik.lan", Origins: []string{"https://homestead.lan"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `{"authentik_url":"https://authentik.lan","origins":["https://homestead.lan"]}`; got != want {
		t.Fatalf("defaults JSON = %s, want %s", got, want)
	}
}

func peopleAccess() *sso.Access {
	return &sso.Access{Groups: []string{sso.OwnerGroup, sso.PeopleGroup}, Bindings: []string{sso.OwnerGroup, sso.PeopleGroup}}
}

// S338: a sign-in applied before the people group asks for a re-plan.
func TestAccessReplanNamesTheMissingPeopleGroup(t *testing.T) {
	old := sso.Status{Configured: true, Spec: &sso.Spec{Access: &sso.Access{Groups: []string{sso.OwnerGroup}, Bindings: []string{sso.OwnerGroup}}, RecoveryFlow: sso.RecoveryFlowSlug, LoginHint: sso.LoginHint}}
	if r := accessReplanReason(old); !strings.Contains(r, sso.PeopleGroup) {
		t.Fatalf("no re-plan for a missing people group: %q", r)
	}
}

// S336: a sign-in with a recovery flow but no login hint asks for a re-plan.
func TestAccessReplanNamesTheMissingLoginHint(t *testing.T) {
	st := sso.Status{Configured: true, Spec: &sso.Spec{Access: peopleAccess(), RecoveryFlow: sso.RecoveryFlowSlug}}
	if r := accessReplanReason(st); !strings.Contains(r, "ask the owner") {
		t.Fatalf("re-plan reason = %q", r)
	}
}

// S333: a sign-in applied before the recovery flow asks for a re-plan;
// one that has it does not.
func TestAccessReplanNamesTheMissingRecoveryFlow(t *testing.T) {
	old := sso.Status{Configured: true, Spec: &sso.Spec{Access: peopleAccess()}}
	if r := accessReplanReason(old); !strings.Contains(r, "recovery flow") {
		t.Fatalf("no re-plan for a missing recovery flow: %q", r)
	}
	cur := sso.Status{Configured: true, Spec: &sso.Spec{Access: peopleAccess(), RecoveryFlow: sso.RecoveryFlowSlug, LoginHint: sso.LoginHint}}
	if r := accessReplanReason(cur); r != "" {
		t.Fatalf("re-plan reason = %q", r)
	}
}

// S338 (ADR-050 §1): a person signs in through the same callback, matched by
// the Authentik user Homestead recorded, and gets a person session.
func TestSSOPersonSignsInAsAPersonAndIsAudited(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	if _, err := a.st.DB().Exec(`INSERT INTO people (id, display_name, username, email, authentik_pk, authentik_uid, state, origin, created_by, created_at)
		VALUES (7, 'Sam Person', 'sam', '', 9, 'person-sub', 'active', 'created', 'o', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	a.w.mu.Lock()
	a.w.sub = "person-sub"
	a.w.mu.Unlock()
	token, csrf := a.ssoSession(t)
	rec := a.req("GET", "/api/v1/me", "", "", cookieAuth(token, ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"role":"person"`) {
		t.Fatalf("me = %d %s", rec.Code, rec.Body)
	}
	if rec := a.req("GET", "/api/v1/sso", "", "", cookieAuth(token, "")); rec.Code != 403 || rec.Header().Get(ErrorCodeHeader) != ErrorCodeOwnerOnly {
		t.Fatalf("owner route as a person = %d (%s)", rec.Code, rec.Header().Get(ErrorCodeHeader))
	}
	audit := a.req("GET", "/api/v1/audit?limit=200", "", a.owner.token).Body.String()
	if !strings.Contains(audit, "session.login") || !strings.Contains(audit, "person=7") {
		t.Fatalf("person sign-in not audited: %s", audit)
	}
	if rec := a.req("DELETE", "/api/v1/sessions", "", "", cookieAuth(token, csrf)); rec.Code != http.StatusNoContent {
		t.Fatalf("sign-out = %d", rec.Code)
	}
	audit = a.req("GET", "/api/v1/audit?limit=200", "", a.owner.token).Body.String()
	if !strings.Contains(audit, "session.logout") {
		t.Fatalf("person sign-out not audited: %s", audit)
	}
	// A disabled person is refused as unlinked.
	_, _ = a.st.DB().Exec(`UPDATE people SET state = 'disabled' WHERE id = 7`)
	rec = a.browserLogin(t, ssoLocal)
	if rec.Header().Get("Location") != "/login?sso_error=unlinked" {
		t.Fatalf("disabled person = %s", rec.Header().Get("Location"))
	}
}
