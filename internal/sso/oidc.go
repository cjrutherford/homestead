// Package sso implements dashboard sign-in with the appliance's own
// Authentik (S286, ADR-033): OIDC authorization code with PKCE, ID token
// verification, the single owner link, and the plan, approve and apply
// flow that configures the Authentik side.
//
// Verification uses the standard library only (crypto/rsa, crypto/ecdsa
// and a small JWKS cache): no OIDC dependency is added to the daemon.
package sso

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Skew is the clock allowance for exp, iat and nbf (ADR-033).
const Skew = 2 * time.Minute

// maxBody bounds every response read from the identity provider.
const maxBody = 1 << 20

// Verification failures. They are internal reasons for the audit log;
// the browser only ever sees a fixed code.
var (
	ErrDiscovery   = errors.New("sso: provider discovery failed")
	ErrUnreachable = errors.New("sso: provider unreachable")
	ErrToken       = errors.New("sso: invalid ID token")
)

// Discovery is the subset of the provider metadata Homestead uses.
type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// Provider talks to one fixed issuer. The issuer string is set at
// configuration time; nothing a response says can change it.
type Provider struct {
	Issuer string
	// Client is the back-channel HTTP client: it trusts the appliance CA
	// and dials the local edge when Authentik is local. Required.
	Client func() *http.Client
	Now    func() time.Time

	mu        sync.Mutex
	disc      *Discovery
	discAt    time.Time
	keys      map[string]jwk
	keysAt    time.Time
	lastFetch time.Time
}

const (
	discoveryTTL = 10 * time.Minute
	// jwksMinRefresh stops an unknown kid from turning every bad token
	// into a JWKS fetch.
	jwksMinRefresh = 5 * time.Second
)

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Provider) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.Client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, urlErr(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered %d", ErrDiscovery, req.URL.Path, resp.StatusCode)
	}
	return body, nil
}

func urlErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// sameOrigin reports whether endpoint is on the issuer's scheme, host and
// port. Discovery cannot send the daemon, or the client secret, anywhere
// else.
func sameOrigin(issuer, endpoint string) bool {
	a, err1 := url.Parse(issuer)
	b, err2 := url.Parse(endpoint)
	if err1 != nil || err2 != nil || b.Scheme == "" || b.Host == "" {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

// Discover returns the provider metadata, cached briefly. The document's
// issuer must equal the configured string exactly and every endpoint must
// sit on the issuer's origin.
func (p *Provider) Discover(ctx context.Context) (Discovery, error) {
	return p.discover(ctx, false)
}

// DiscoverFresh is Discover without the cache: starting a sign-in reads the
// metadata again, so a dead Authentik is found before the browser is sent
// there.
func (p *Provider) DiscoverFresh(ctx context.Context) (Discovery, error) {
	return p.discover(ctx, true)
}

func (p *Provider) discover(ctx context.Context, fresh bool) (Discovery, error) {
	p.mu.Lock()
	if !fresh && p.disc != nil && p.now().Sub(p.discAt) < discoveryTTL {
		d := *p.disc
		p.mu.Unlock()
		return d, nil
	}
	p.mu.Unlock()
	body, err := p.get(ctx, strings.TrimSuffix(p.Issuer, "/")+"/.well-known/openid-configuration")
	if err != nil {
		return Discovery{}, err
	}
	var d Discovery
	if err := json.Unmarshal(body, &d); err != nil {
		return Discovery{}, fmt.Errorf("%w: not JSON", ErrDiscovery)
	}
	if d.Issuer != p.Issuer {
		return Discovery{}, fmt.Errorf("%w: issuer mismatch", ErrDiscovery)
	}
	for _, e := range []string{d.AuthorizationEndpoint, d.TokenEndpoint, d.JWKSURI} {
		if !sameOrigin(p.Issuer, e) {
			return Discovery{}, fmt.Errorf("%w: an endpoint is not on the issuer's origin", ErrDiscovery)
		}
	}
	p.mu.Lock()
	p.disc, p.discAt = &d, p.now()
	p.mu.Unlock()
	return d, nil
}

// jwk is one parsed verification key.
type jwk struct {
	kid string
	alg string // optional pin from the JWKS entry
	pub crypto.PublicKey
}

func b64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func parseJWKS(body []byte) (map[string]jwk, error) {
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: JWKS is not JSON", ErrDiscovery)
	}
	out := map[string]jwk{}
	for _, k := range doc.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch k.Kty {
		case "RSA":
			n, err1 := b64(k.N)
			e, err2 := b64(k.E)
			if err1 != nil || err2 != nil || len(n) < 256 || len(e) == 0 || len(e) > 4 {
				continue // under 2048 bits or malformed
			}
			exp := 0
			for _, b := range e {
				exp = exp<<8 | int(b)
			}
			out[k.Kid] = jwk{kid: k.Kid, alg: k.Alg, pub: &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}}
		case "EC":
			if k.Crv != "P-256" {
				continue
			}
			x, err1 := b64(k.X)
			y, err2 := b64(k.Y)
			if err1 != nil || err2 != nil {
				continue
			}
			pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
			if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
				continue
			}
			out[k.Kid] = jwk{kid: k.Kid, alg: k.Alg, pub: pub}
		}
	}
	return out, nil
}

// key returns the verification key for kid, refreshing the JWKS once when
// the kid is unknown (rotation), no more often than jwksMinRefresh.
func (p *Provider) key(ctx context.Context, kid string) (jwk, error) {
	p.mu.Lock()
	k, ok := p.keys[kid]
	stale := p.keys == nil || p.now().Sub(p.lastFetch) >= jwksMinRefresh
	p.mu.Unlock()
	if ok {
		return k, nil
	}
	if !stale {
		return jwk{}, fmt.Errorf("%w: unknown signing key", ErrToken)
	}
	d, err := p.Discover(ctx)
	if err != nil {
		return jwk{}, err
	}
	body, err := p.get(ctx, d.JWKSURI)
	if err != nil {
		return jwk{}, err
	}
	keys, err := parseJWKS(body)
	if err != nil {
		return jwk{}, err
	}
	p.mu.Lock()
	p.keys, p.keysAt, p.lastFetch = keys, p.now(), p.now()
	k, ok = p.keys[kid]
	p.mu.Unlock()
	if !ok {
		return jwk{}, fmt.Errorf("%w: unknown signing key", ErrToken)
	}
	return k, nil
}

// Claims are the verified ID token claims Homestead reads. Email, name and
// groups are deliberately absent: they never map or grant anything.
type Claims struct {
	Issuer  string
	Subject string
}

type rawClaims struct {
	Iss   string          `json:"iss"`
	Sub   string          `json:"sub"`
	Aud   json.RawMessage `json:"aud"`
	Azp   string          `json:"azp"`
	Nonce string          `json:"nonce"`
	Exp   *float64        `json:"exp"`
	Iat   *float64        `json:"iat"`
	Nbf   *float64        `json:"nbf"`
}

func audiences(raw json.RawMessage) ([]string, bool) {
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}, true
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
		return many, true
	}
	return nil, false
}

// Verify checks an ID token end to end (ADR-033): signature against the
// provider's JWKS with RS256 or ES256 only (none and HMAC refused), exact
// iss, aud containing the client id and azp equal to it when present, the
// stored nonce, and exp, iat and nbf with a two minute skew.
func (p *Provider) Verify(ctx context.Context, idToken, clientID, nonce string) (Claims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return Claims{}, fmt.Errorf("%w: not a compact JWS", ErrToken)
	}
	headerRaw, err := b64(parts[0])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: bad header", ErrToken)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return Claims{}, fmt.Errorf("%w: bad header", ErrToken)
	}
	if header.Alg != "RS256" && header.Alg != "ES256" {
		return Claims{}, fmt.Errorf("%w: algorithm %q is not allowed", ErrToken, safeAlg(header.Alg))
	}
	sig, err := b64(parts[2])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: bad signature encoding", ErrToken)
	}
	k, err := p.key(ctx, header.Kid)
	if err != nil {
		return Claims{}, err
	}
	if k.alg != "" && k.alg != header.Alg {
		return Claims{}, fmt.Errorf("%w: key and algorithm disagree", ErrToken)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch header.Alg {
	case "RS256":
		pub, ok := k.pub.(*rsa.PublicKey)
		if !ok || rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig) != nil {
			return Claims{}, fmt.Errorf("%w: bad signature", ErrToken)
		}
	case "ES256":
		pub, ok := k.pub.(*ecdsa.PublicKey)
		if !ok || len(sig) != 64 ||
			!ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return Claims{}, fmt.Errorf("%w: bad signature", ErrToken)
		}
	}
	payload, err := b64(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: bad payload", ErrToken)
	}
	var c rawClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, fmt.Errorf("%w: bad payload", ErrToken)
	}
	if c.Iss != p.Issuer {
		return Claims{}, fmt.Errorf("%w: wrong issuer", ErrToken)
	}
	aud, ok := audiences(c.Aud)
	if !ok {
		return Claims{}, fmt.Errorf("%w: no audience", ErrToken)
	}
	has := false
	for _, a := range aud {
		if a == clientID {
			has = true
		}
	}
	if !has {
		return Claims{}, fmt.Errorf("%w: wrong audience", ErrToken)
	}
	if (len(aud) > 1 || c.Azp != "") && c.Azp != clientID {
		return Claims{}, fmt.Errorf("%w: wrong authorized party", ErrToken)
	}
	if nonce == "" || c.Nonce != nonce {
		return Claims{}, fmt.Errorf("%w: wrong nonce", ErrToken)
	}
	if c.Sub == "" {
		return Claims{}, fmt.Errorf("%w: no subject", ErrToken)
	}
	now := p.now()
	if c.Exp == nil || now.After(unix(*c.Exp).Add(Skew)) {
		return Claims{}, fmt.Errorf("%w: expired", ErrToken)
	}
	if c.Iat != nil && unix(*c.Iat).After(now.Add(Skew)) {
		return Claims{}, fmt.Errorf("%w: issued in the future", ErrToken)
	}
	if c.Nbf != nil && unix(*c.Nbf).After(now.Add(Skew)) {
		return Claims{}, fmt.Errorf("%w: not valid yet", ErrToken)
	}
	return Claims{Issuer: c.Iss, Subject: c.Sub}, nil
}

func unix(f float64) time.Time { return time.Unix(int64(f), 0) }

func safeAlg(a string) string {
	if len(a) > 16 {
		a = a[:16]
	}
	return a
}

// Endpoints for the browser and the back channel.

// AuthURLParams are the inputs of the authorize redirect.
type AuthURLParams struct {
	ClientID    string
	RedirectURI string
	State       string
	Nonce       string
	Challenge   string
	// Fresh forces a new Authentik login (prompt=login, max_age=0): the
	// link ceremony proves the owner is present now.
	Fresh bool
}

// AuthURL builds the browser-facing authorize URL from discovery.
func AuthURL(d Discovery, p AuthURLParams) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", "openid profile email")
	q.Set("state", p.State)
	q.Set("nonce", p.Nonce)
	q.Set("code_challenge", p.Challenge)
	q.Set("code_challenge_method", "S256")
	if p.Fresh {
		q.Set("prompt", "login")
		q.Set("max_age", "0")
	}
	sep := "?"
	if strings.Contains(d.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return d.AuthorizationEndpoint + sep + q.Encode()
}

// ExchangeCode redeems the authorization code at the token endpoint from
// the daemon (the back channel) and returns the raw ID token.
func (p *Provider) ExchangeCode(ctx context.Context, clientID, clientSecret, code, verifier, redirectURI string) (string, error) {
	d, err := p.Discover(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.Client().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnreachable, urlErr(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if resp.StatusCode != http.StatusOK {
		// The body is not echoed: it may quote request values.
		if resp.StatusCode >= 500 {
			return "", fmt.Errorf("%w: token endpoint answered %d", ErrUnreachable, resp.StatusCode)
		}
		return "", fmt.Errorf("%w: token endpoint answered %d", ErrToken, resp.StatusCode)
	}
	var out struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.IDToken == "" {
		return "", fmt.Errorf("%w: token endpoint returned no ID token", ErrToken)
	}
	return out.IDToken, nil
}

// Challenge is the PKCE S256 challenge of a verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
