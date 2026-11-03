package everything

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// OIDCConfig configures the bridge as its own OpenID Connect client
// (ADR-053 amendment 2026-11-02). The client secret is passed in memory by
// the caller, which reads it from a file; it is never read from argv.
type OIDCConfig struct {
	App          string // application name scoping stored data
	Issuer       string // exactly the issuer string in the provider's ID tokens
	ClientID     string
	ClientSecret string
	RedirectURL  string // https://host/<base>/_homestead/callback
	Origin       string // the one allowed browser origin, https://host
	BasePath     string // "" or "/apps/tasks"
	StaticDir    string // prepared application files; index.html required
	HTTPClient   *http.Client
	IdleTTL      time.Duration // default 12h
	AbsoluteTTL  time.Duration // default 7d
	// Groups, when set, is a defence-in-depth check: a token that carries a
	// groups claim must list one of them (ADR-058).
	Groups []string

	now func() time.Time
}

const (
	LoginPath    = "/_homestead/login"
	CallbackPath = "/_homestead/callback"
	LogoutPath   = "/_homestead/logout"
	sessionName  = "hs_everything_session"
	loginName    = "hs_everything_login"
	CSRFHeader   = "X-Homestead-CSRF"
	maxStates    = 5000
)

var basePathRe = regexp.MustCompile(`^(/[a-z0-9][a-z0-9._-]*)*$`)

// OIDC implements Identity with Authentik (or any OIDC provider) sign-in.
type OIDC struct {
	cfg   OIDCConfig
	store *Store
	hc    *http.Client

	mu        sync.Mutex
	disco     *discovery
	keys      map[string]crypto.PublicKey
	keysFetch time.Time
}

type discovery struct {
	Issuer   string `json:"issuer"`
	AuthURL  string `json:"authorization_endpoint"`
	TokenURL string `json:"token_endpoint"`
	JWKSURI  string `json:"jwks_uri"`
}

func checkEndpoint(raw, issuer string) error {
	u, err := url.Parse(raw)
	iu, ierr := url.Parse(issuer)
	if err != nil || ierr != nil || u.Host == "" || u.User != nil || u.Scheme != iu.Scheme {
		return errors.New("everything: invalid provider endpoint")
	}
	return nil
}

func isLoopback(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// NewOIDC validates the configuration; any missing or unsafe value is an error.
func NewOIDC(cfg OIDCConfig, store *Store) (*OIDC, error) {
	bad := func(what string) (*OIDC, error) { return nil, fmt.Errorf("everything: oidc config: %s", what) }
	if store == nil {
		return bad("no store")
	}
	if !appName.MatchString(cfg.App) {
		return bad("invalid app name")
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return bad("client id and secret are required")
	}
	iu, err := url.Parse(cfg.Issuer)
	if err != nil || iu.Host == "" || iu.User != nil || iu.Fragment != "" || iu.RawQuery != "" ||
		(iu.Scheme != "https" && !(iu.Scheme == "http" && isLoopback(iu.Host))) {
		return bad("issuer must be an https URL (http only for loopback)")
	}
	ou, err := url.Parse(cfg.Origin)
	if err != nil || (ou.Scheme != "https" && !(ou.Scheme == "http" && isLoopback(ou.Host))) || ou.Host == "" ||
		ou.User != nil || ou.Path != "" || ou.RawQuery != "" || ou.Fragment != "" {
		return bad("origin must be an https origin")
	}
	if !basePathRe.MatchString(cfg.BasePath) || strings.Contains(cfg.BasePath, "..") {
		return bad("invalid base path")
	}
	ru, err := url.Parse(cfg.RedirectURL)
	if err != nil || ru.Scheme != ou.Scheme || ru.Host != ou.Host || ru.Path != cfg.BasePath+CallbackPath || ru.RawQuery != "" || ru.Fragment != "" {
		return bad("redirect URL must be <origin><base path>" + CallbackPath)
	}
	if cfg.StaticDir == "" {
		return bad("static dir is required")
	}
	if info, err := os.Stat(cfg.StaticDir + "/index.html"); err != nil || !info.Mode().IsRegular() {
		return bad("static dir has no index.html")
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 12 * time.Hour
	}
	if cfg.AbsoluteTTL <= 0 {
		cfg.AbsoluteTTL = 7 * 24 * time.Hour
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	// Never follow redirects while talking to the provider.
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &OIDC{cfg: cfg, store: store, hc: &c}, nil
}

// Origins returns the allowlist for the data handler.
func (o *OIDC) Origins() []string { return []string{o.cfg.Origin} }

// ---- provider access ----

func (o *OIDC) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("provider returned %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func (o *OIDC) discovery(ctx context.Context) (*discovery, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.disco != nil {
		return o.disco, nil
	}
	var d discovery
	if err := o.getJSON(ctx, strings.TrimRight(o.cfg.Issuer, "/")+"/.well-known/openid-configuration", &d); err != nil {
		return nil, err
	}
	if d.Issuer != o.cfg.Issuer {
		return nil, errors.New("everything: provider issuer mismatch")
	}
	for _, e := range []string{d.AuthURL, d.TokenURL, d.JWKSURI} {
		if err := checkEndpoint(e, o.cfg.Issuer); err != nil {
			return nil, err
		}
	}
	o.disco = &d
	return &d, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func b64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func parseJWK(k jwk) (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, e1 := b64(k.N)
		e, e2 := b64(k.E)
		if e1 != nil || e2 != nil || len(n) < 256 || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("bad rsa key")
		}
		ev := 0
		for _, b := range e {
			ev = ev<<8 | int(b)
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: ev}, nil
	case "EC":
		if k.Crv != "P-256" {
			return nil, errors.New("unsupported curve")
		}
		x, e1 := b64(k.X)
		y, e2 := b64(k.Y)
		if e1 != nil || e2 != nil {
			return nil, errors.New("bad ec key")
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return nil, errors.New("ec point not on curve")
		}
		return pub, nil
	}
	return nil, errors.New("unsupported key type")
}

// key returns the signing key for kid, refreshing the JWKS at most every 10
// seconds when the kid is unknown (key rotation).
func (o *OIDC) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	d, err := o.discovery(ctx)
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if k, ok := o.keys[kid]; ok {
		return k, nil
	}
	if o.keys != nil && o.cfg.now().Sub(o.keysFetch) < 10*time.Second {
		return nil, errors.New("everything: unknown signing key")
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err = o.getJSON(ctx, d.JWKSURI, &set); err != nil {
		return nil, err
	}
	keys := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if pub, err := parseJWK(k); err == nil {
			keys[k.Kid] = pub
		}
	}
	o.keys, o.keysFetch = keys, o.cfg.now()
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, errors.New("everything: unknown signing key")
}

// ---- ID token verification ----

type idClaims struct {
	Iss   string          `json:"iss"`
	Sub   string          `json:"sub"`
	Aud   json.RawMessage `json:"aud"`
	Azp   string          `json:"azp"`
	Exp   float64         `json:"exp"`
	Nbf   float64         `json:"nbf"`
	Nonce string          `json:"nonce"`
	// profile scope (ADR-058): the name a sharer types and the groups the
	// optional defence-in-depth check compares.
	PreferredUsername string          `json:"preferred_username"`
	Groups            json.RawMessage `json:"groups"`
}

// verifiedClaims is what a successful sign-in hands on.
type verifiedClaims struct{ Sub, Username string }

// groupAllowed applies the optional group check. A token with no groups
// claim passes (Authentik's own binding already decided); a token that
// carries one must list at least one configured group.
func groupAllowed(raw json.RawMessage, allowed []string) bool {
	if len(allowed) == 0 || len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var have []string
	if json.Unmarshal(raw, &have) != nil {
		return false
	}
	for _, h := range have {
		for _, a := range allowed {
			if h == a {
				return true
			}
		}
	}
	return false
}

const clockSkew = 30 * time.Second

func (o *OIDC) verifyIDToken(ctx context.Context, token, nonce string) (verifiedClaims, error) {
	fail := errors.New("everything: invalid id token")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return verifiedClaims{}, fail
	}
	hb, e1 := b64(parts[0])
	pb, e2 := b64(parts[1])
	sig, e3 := b64(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return verifiedClaims{}, fail
	}
	var hdr struct{ Alg, Kid string }
	if json.Unmarshal(hb, &hdr) != nil {
		return verifiedClaims{}, fail
	}
	pub, err := o.key(ctx, hdr.Kid)
	if err != nil {
		return verifiedClaims{}, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch k := pub.(type) {
	case *rsa.PublicKey:
		if hdr.Alg != "RS256" || rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) != nil {
			return verifiedClaims{}, fail
		}
	case *ecdsa.PublicKey:
		if hdr.Alg != "ES256" || len(sig) != 64 ||
			!ecdsa.Verify(k, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return verifiedClaims{}, fail
		}
	default:
		return verifiedClaims{}, fail
	}
	var c idClaims
	if json.Unmarshal(pb, &c) != nil {
		return verifiedClaims{}, fail
	}
	now := o.cfg.now()
	if c.Iss != o.cfg.Issuer || c.Exp == 0 || !now.Before(time.Unix(int64(c.Exp), 0).Add(clockSkew)) ||
		(c.Nbf != 0 && now.Add(clockSkew).Before(time.Unix(int64(c.Nbf), 0))) {
		return verifiedClaims{}, fail
	}
	var auds []string
	if json.Unmarshal(c.Aud, &auds) != nil {
		var one string
		if json.Unmarshal(c.Aud, &one) != nil {
			return verifiedClaims{}, fail
		}
		auds = []string{one}
	}
	okAud := false
	for _, a := range auds {
		okAud = okAud || a == o.cfg.ClientID
	}
	if !okAud || (len(auds) > 1 && c.Azp != o.cfg.ClientID) || (c.Azp != "" && c.Azp != o.cfg.ClientID) {
		return verifiedClaims{}, fail
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1 {
		return verifiedClaims{}, fail
	}
	if !validText(c.Sub, 255) {
		return verifiedClaims{}, fail
	}
	if !groupAllowed(c.Groups, o.cfg.Groups) {
		return verifiedClaims{}, fail
	}
	return verifiedClaims{Sub: c.Sub, Username: c.PreferredUsername}, nil
}

func (o *OIDC) exchange(ctx context.Context, d *discovery, code, verifier string) (string, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {o.cfg.RedirectURL}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(o.cfg.ClientID), url.QueryEscape(o.cfg.ClientSecret))
	resp, err := o.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		IDToken string `json:"id_token"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out) != nil || out.IDToken == "" {
		return "", errors.New("everything: token exchange failed")
	}
	return out.IDToken, nil
}

// ---- Identity ----

func (o *OIDC) session(r *http.Request) (sessionInfo, string, bool) {
	c, err := r.Cookie(sessionName)
	if err != nil || c.Value == "" || len(c.Value) > 128 {
		return sessionInfo{}, "", false
	}
	info, ok := o.store.lookupSession(r.Context(), c.Value, o.cfg.App, o.cfg.now(), o.cfg.IdleTTL, o.cfg.AbsoluteTTL)
	return info, c.Value, ok
}

func (o *OIDC) Authenticate(r *http.Request) (Scope, error) {
	info, _, ok := o.session(r)
	if !ok {
		return Scope{}, ErrInvalid
	}
	return Scope{App: info.app, Subject: info.subject}, nil
}

// SessionAge reports how old the caller's session is (ADR-058: recipient-side
// shared operations need a session under 24 hours).
func (o *OIDC) SessionAge(r *http.Request) (time.Duration, bool) {
	info, _, ok := o.session(r)
	if !ok {
		return 0, false
	}
	return o.cfg.now().Sub(time.Unix(info.created, 0)), true
}

func (o *OIDC) CheckCSRF(r *http.Request) bool {
	info, _, ok := o.session(r)
	got := r.Header.Get(CSRFHeader)
	return ok && got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(info.csrf)) == 1
}

// ---- routes ----

func (o *OIDC) cookiePath() string {
	if o.cfg.BasePath == "" {
		return "/"
	}
	return o.cfg.BasePath + "/"
}

func (o *OIDC) setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: o.cookiePath(), MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func deny(w http.ResponseWriter, code int) { http.Error(w, http.StatusText(code), code) }

func (o *OIDC) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		deny(w, http.StatusMethodNotAllowed)
		return
	}
	d, err := o.discovery(r.Context())
	if err != nil {
		deny(w, http.StatusBadGateway)
		return
	}
	state, verifier := randomToken(32), randomToken(48)
	ls := loginState{nonce: randomToken(32), verifier: verifier}
	now := o.cfg.now()
	if err = o.store.putLoginState(r.Context(), state, ls, now.Add(10*time.Minute), now); err != nil {
		deny(w, http.StatusServiceUnavailable)
		return
	}
	chal := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {o.cfg.ClientID}, "redirect_uri": {o.cfg.RedirectURL},
		"scope": {"openid profile"}, "state": {state}, "nonce": {ls.nonce},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(chal[:])}, "code_challenge_method": {"S256"}}
	o.setCookie(w, loginName, state, 600)
	au, _ := url.Parse(d.AuthURL)
	if au.RawQuery != "" {
		au.RawQuery += "&"
	}
	au.RawQuery += q.Encode()
	http.Redirect(w, r, au.String(), http.StatusFound)
}

func (o *OIDC) callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		deny(w, http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	state, code := q.Get("state"), q.Get("code")
	c, cerr := r.Cookie(loginName)
	o.setCookie(w, loginName, "", -1)
	if state == "" || cerr != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		deny(w, http.StatusForbidden)
		return
	}
	// The state is single use, consumed before anything else can fail.
	ls, ok := o.store.takeLoginState(r.Context(), state, o.cfg.now())
	if !ok || q.Get("error") != "" || code == "" || len(code) > 4096 {
		deny(w, http.StatusForbidden)
		return
	}
	d, err := o.discovery(r.Context())
	if err != nil {
		deny(w, http.StatusBadGateway)
		return
	}
	tok, err := o.exchange(r.Context(), d, code, ls.verifier)
	if err != nil {
		deny(w, http.StatusForbidden)
		return
	}
	vc, err := o.verifyIDToken(r.Context(), tok, ls.nonce)
	if err != nil {
		deny(w, http.StatusForbidden)
		return
	}
	if err = o.store.RecordSignIn(r.Context(), o.cfg.App, vc.Sub, vc.Username, o.cfg.now()); err != nil {
		deny(w, http.StatusServiceUnavailable)
		return
	}
	id, _, err := o.store.createSession(r.Context(), o.cfg.App, vc.Sub, o.cfg.now())
	if err != nil {
		deny(w, http.StatusServiceUnavailable)
		return
	}
	o.setCookie(w, sessionName, id, int(o.cfg.AbsoluteTTL/time.Second))
	http.Redirect(w, r, o.cfg.BasePath+"/", http.StatusFound)
}

func (o *OIDC) logout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		deny(w, http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Origin") != o.cfg.Origin {
		deny(w, http.StatusForbidden)
		return
	}
	_, id, ok := o.session(r)
	if !ok {
		deny(w, http.StatusUnauthorized)
		return
	}
	if !o.CheckCSRF(r) {
		deny(w, http.StatusForbidden)
		return
	}
	if err := o.store.deleteSession(r.Context(), id); err != nil {
		deny(w, http.StatusServiceUnavailable)
		return
	}
	o.setCookie(w, sessionName, "", -1)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// Session implements SessionSource.
func (o *OIDC) Session(r *http.Request) (string, string, bool) {
	info, _, ok := o.session(r)
	return info.subject, info.csrf, ok
}

// Routes registers sign-in, logout, the personalised index and the static
// application files. It is the `extra` argument of NewServerAt.
func (o *OIDC) Routes(mux *http.ServeMux) {
	b := o.cfg.BasePath
	mux.HandleFunc(b+LoginPath, o.login)
	mux.HandleFunc(b+CallbackPath, o.callback)
	mux.HandleFunc(b+LogoutPath, o.logout)
	registerApp(mux, b, o.cfg.StaticDir, o)
}

// ReadSecretFile reads a client secret from a regular file, never a symlink
// target of unexpected type, trimming one trailing newline.
func ReadSecretFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 4096 || info.Mode().Perm()&0002 != 0 {
		return "", errors.New("everything: client secret file must be a small non-empty regular file that is not world-writable")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimRight(string(b), "\r\n")
	if s == "" {
		return "", errors.New("everything: client secret file is empty")
	}
	return s, nil
}

// ClientWithCA returns an HTTP client trusting only the given PEM CA bundle;
// an empty path means the system roots.
func ClientWithCA(path string) (*http.Client, error) {
	hc := &http.Client{Timeout: 15 * time.Second}
	if path == "" {
		return hc, nil
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("everything: CA bundle has no certificates")
	}
	hc.Transport = &http.Transport{TLSClientConfig: newTLS(pool), Proxy: http.ProxyFromEnvironment}
	return hc, nil
}
