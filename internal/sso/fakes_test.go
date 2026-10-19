package sso

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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

	"homestead/internal/store"
)

const testSecret = "the-client-secret-value"

// memSecrets is an in-memory SecretStore.
type memSecrets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSecrets) SetSystem(n, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[n] = v
	return nil
}

func (s *memSecrets) Get(n string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[n]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (s *memSecrets) Delete(n string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, n)
	return nil
}

func (s *memSecrets) has(n string) bool { _, err := s.Get(n); return err == nil }

// idpKey is one signing key of the fake provider.
type idpKey struct {
	kid string
	rsa *rsa.PrivateKey
	ec  *ecdsa.PrivateKey
}

// fakeIDP is an in-process OIDC provider: discovery, JWKS and a token
// endpoint that checks the confidential client and PKCE.
type fakeIDP struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	keys   []idpKey
	active int
	// pending maps authorization code to the flow it was issued for.
	pending map[string]idpFlow
	// mutate edits the claims (or replaces the token) just before signing.
	mutate    func(c map[string]any)
	tokenHook func(header map[string]any) // edits the JWS header
	alg       string                      // "RS256" (default), "ES256"
	sign      func(signingInput string, k idpKey) []byte
	down      bool
	jwksHits  int
	issuerOvr string
	tokenCode int
	discoverX func(d map[string]any)
	secretOvr string
}

func (f *fakeIDP) wantSecret() string {
	if f.secretOvr != "" {
		return f.secretOvr
	}
	return testSecret
}

type idpFlow struct {
	nonce, challenge, redirect string
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	f := &fakeIDP{t: t, pending: map[string]idpFlow{}}
	f.addKey("k1")
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIDP) addKey(kid string) {
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		f.t.Fatal(err)
	}
	ek, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	f.keys = append(f.keys, idpKey{kid: kid, rsa: rk, ec: ek})
	f.mu.Unlock()
}

func (f *fakeIDP) issuer() string {
	if f.issuerOvr != "" {
		return f.issuerOvr
	}
	return f.srv.URL + "/application/o/homestead/"
}

func (f *fakeIDP) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	down := f.down
	f.mu.Unlock()
	if down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
		d := map[string]any{
			"issuer":                 f.issuer(),
			"authorization_endpoint": f.srv.URL + "/application/o/authorize/",
			"token_endpoint":         f.srv.URL + "/application/o/token/",
			"jwks_uri":               f.srv.URL + "/application/o/homestead/jwks/",
		}
		if f.discoverX != nil {
			f.discoverX(d)
		}
		_ = json.NewEncoder(w).Encode(d)
	case strings.HasSuffix(r.URL.Path, "/jwks/"):
		f.mu.Lock()
		f.jwksHits++
		var keys []map[string]any
		for _, k := range f.keys {
			keys = append(keys, map[string]any{
				"kty": "RSA", "use": "sig", "kid": k.kid, "alg": "RS256",
				"n": b64e(k.rsa.N.Bytes()), "e": b64e(big.NewInt(int64(k.rsa.E)).Bytes()),
			}, map[string]any{
				"kty": "EC", "use": "sig", "kid": k.kid + "-ec", "alg": "ES256", "crv": "P-256",
				"x": b64e(pad32(k.ec.X)), "y": b64e(pad32(k.ec.Y)),
			})
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	case strings.HasSuffix(r.URL.Path, "/token/"):
		f.token(w, r)
	default:
		http.NotFound(w, r)
	}
}

func pad32(n *big.Int) []byte {
	b := n.Bytes()
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

func b64e(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (f *fakeIDP) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	flow, ok := f.pending[r.Form.Get("code")]
	delete(f.pending, r.Form.Get("code"))
	code := f.tokenCode
	f.mu.Unlock()
	if code != 0 {
		http.Error(w, "boom", code)
		return
	}
	if !ok || r.Form.Get("client_id") != ClientID || r.Form.Get("client_secret") != f.wantSecret() ||
		r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != flow.redirect ||
		Challenge(r.Form.Get("code_verifier")) != flow.challenge {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		return
	}
	claims := map[string]any{
		"iss": f.issuer(), "sub": "user-1", "aud": ClientID, "nonce": flow.nonce,
		"exp": time.Now().Add(10 * time.Minute).Unix(), "iat": time.Now().Unix(),
		"email": "owner@example.test", "groups": []string{"admins"},
	}
	if f.mutate != nil {
		f.mutate(claims)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id_token": f.sign_(claims), "access_token": "at", "token_type": "Bearer"})
}

func (f *fakeIDP) sign_(claims map[string]any) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.keys[f.active]
	alg := f.alg
	if alg == "" {
		alg = "RS256"
	}
	header := map[string]any{"alg": alg, "typ": "JWT", "kid": k.kid}
	if alg == "ES256" {
		header["kid"] = k.kid + "-ec"
	}
	if f.tokenHook != nil {
		f.tokenHook(header)
	}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	input := b64e(hb) + "." + b64e(cb)
	var sig []byte
	switch {
	case f.sign != nil:
		sig = f.sign(input, k)
	case alg == "ES256":
		sum := sha256.Sum256([]byte(input))
		rr, ss, _ := ecdsa.Sign(rand.Reader, k.ec, sum[:])
		sig = append(pad32(rr), pad32(ss)...)
	default:
		sum := sha256.Sum256([]byte(input))
		sig, _ = rsa.SignPKCS1v15(rand.Reader, k.rsa, crypto.SHA256, sum[:])
	}
	return input + "." + b64e(sig)
}

// authorize plays the browser's trip to the provider: it reads the authorize
// URL, registers the flow and returns the code and state the provider would
// send back to the callback.
func (f *fakeIDP) authorize(authURL string) (code, state string) {
	f.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("client_id") != ClientID ||
		q.Get("nonce") == "" || q.Get("state") == "" || q.Get("scope") != "openid profile email" {
		f.t.Fatalf("authorize URL malformed: %s", authURL)
	}
	code = fmt.Sprintf("code-%d", time.Now().UnixNano())
	f.mu.Lock()
	f.pending[code] = idpFlow{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	f.mu.Unlock()
	return code, q.Get("state")
}

func hs256(secret []byte) func(string, idpKey) []byte {
	return func(input string, _ idpKey) []byte {
		h := hmac.New(sha256.New, secret)
		h.Write([]byte(input))
		return h.Sum(nil)
	}
}

// harness wires a Manager over a real store and the fake provider.
type harness struct {
	t       *testing.T
	m       *Manager
	idp     *fakeIDP
	secrets *memSecrets
	now     time.Time
	audits  []string
	revoked []int64
	st      *store.Store
	spec    Spec
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), store.DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &harness{t: t, idp: newFakeIDP(t), secrets: &memSecrets{}, now: time.Now(), st: st}
	h.m = &Manager{
		DB: st.DB(), Secrets: h.secrets, Client: func() *http.Client { return http.DefaultClient },
		YAMLPath: filepath.Join(t.TempDir(), "sso.yaml"),
		Now:      func() time.Time { return h.now },
		Audit: func(actor, action, outcome, detail string) {
			h.audits = append(h.audits, actor+"|"+action+"|"+outcome+"|"+detail)
		},
		RevokeOIDC: func(id int64) error { h.revoked = append(h.revoked, id); return nil },
		Installed:  func() bool { return true },
		Version:    func() string { return "2026.8.3" },
	}
	return h
}

// configure applies a spec directly (the plan flow has its own tests).
func (h *harness) configure() {
	h.t.Helper()
	base := h.idp.srv.URL
	spec, err := h.m.BuildSpec(PlanRequest{AuthentikURL: base, Origins: []string{"http://127.0.0.1:8080", "https://homestead.example.test"}})
	if err != nil {
		h.t.Fatal(err)
	}
	h.spec = spec
	if spec.Issuer != h.idp.issuer() {
		h.t.Fatalf("issuer %q != fake %q", spec.Issuer, h.idp.issuer())
	}
	if err := h.m.recordConfig(spec, "owner"); err != nil {
		h.t.Fatal(err)
	}
	_ = h.secrets.SetSystem(ClientSecretName, testSecret)
}

// link runs the link ceremony to completion for identity 7.
func (h *harness) link() {
	h.t.Helper()
	start, err := h.m.CreateLinkIntent(ctxb(), 7, "owner", "127.0.0.1:8080", "")
	if err != nil {
		h.t.Fatal(err)
	}
	if !strings.Contains(start.URL, "prompt=login") {
		h.t.Fatalf("link flow must run with prompt=login: %s", start.URL)
	}
	code, state := h.idp.authorize(start.URL)
	out, err := h.m.Callback(ctxb(), state, code, "", "")
	if err != nil {
		h.t.Fatalf("link callback: %v", err)
	}
	if out.Kind != KindLink || out.Subject != "user-1" || out.IdentityID != 7 {
		h.t.Fatalf("link outcome = %+v", out)
	}
}

// login starts a login and returns the callback inputs.
func (h *harness) startLogin() (code, state string) {
	h.t.Helper()
	start, err := h.m.StartLogin(ctxb(), "127.0.0.1:8080")
	if err != nil {
		h.t.Fatalf("StartLogin: %v", err)
	}
	return h.idp.authorize(start.URL)
}

// signRSA signs with the first key (callers hold f.mu).
func (f *fakeIDP) signRSA(input string) []byte {
	sum := sha256.Sum256([]byte(input))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.keys[0].rsa, crypto.SHA256, sum[:])
	return sig
}
