package cert

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"strings"
	"time"
)

// acmeClient is the minimal RFC 8555 subset Homestead needs: dns-01
// issuance only. Direct challenges stay Caddy-native (S101); this client
// exists because stock Caddy ships no DNS provider plugins, so DNS-01
// must run outside it. HTTP-01/TLS-ALPN-01 are out of scope here by
// design, not by omission.
const (
	acmeTimeout      = 30 * time.Second
	acmeKeyType      = "EC"
	acmeKeyCurve     = "P-256"
	acmeBadNonceType = "urn:ietf:params:acme:error:badNonce"
)

// b64url is ACME's unpadded base64url.
func b64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// Account holds the ACME account key and location. Keys persist as PEM
// (GenerateAccountKey/EncodeAccountKey/ParseAccountKey) in the secret
// store at issuance layers; this client never stores.
type Account struct {
	Key *ecdsa.PrivateKey
	KID string
}

// GenerateAccountKey creates a fresh P-256 account key.
func GenerateAccountKey() (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("cert: account key: %w", err)
	}
	return key, nil
}

// EncodeAccountKey renders a key for secret storage.
func EncodeAccountKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("cert: encode key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// ParseAccountKey reads a stored key, refusing non-P-256 curves.
func ParseAccountKey(raw []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("cert: account key is not PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cert: account key: %w", err)
	}
	if key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("cert: account key must be P-256")
	}
	return key, nil
}

// jwk renders the public key in JWK form.
func jwk(key *ecdsa.PrivateKey) map[string]string {
	size := (key.Curve.Params().BitSize + 7) / 8
	x := key.X.Bytes()
	y := key.Y.Bytes()
	xPadded := make([]byte, size)
	yPadded := make([]byte, size)
	copy(xPadded[size-len(x):], x)
	copy(yPadded[size-len(y):], y)
	return map[string]string{
		"kty": acmeKeyType, "crv": acmeKeyCurve,
		"x": b64url(xPadded), "y": b64url(yPadded),
	}
}

// thumbprint is the RFC 7638 JWK thumbprint authorizing challenges.
func thumbprint(key *ecdsa.PrivateKey) (string, error) {
	ordered, err := json.Marshal(map[string]string{
		"crv": acmeKeyCurve, "kty": acmeKeyType,
		"x": jwk(key)["x"], "y": jwk(key)["y"],
	})
	if err != nil {
		return "", fmt.Errorf("cert: thumbprint: %w", err)
	}
	sum := sha256.Sum256(ordered)
	return b64url(sum[:]), nil
}

// keyAuthorization builds "<token>.<thumbprint>" for a challenge.
func keyAuthorization(key *ecdsa.PrivateKey, token string) (string, error) {
	print, err := thumbprint(key)
	if err != nil {
		return "", err
	}
	return token + "." + print, nil
}

// dns01Value hashes the key authorization into the TXT value.
func dns01Value(keyAuthz string) string {
	sum := sha256.Sum256([]byte(keyAuthz))
	return b64url(sum[:])
}

// acmeDirectory is the discovered endpoint set.
type acmeDirectory struct {
	NewNonce   string `json:"newNonce"`
	NewAccount string `json:"newAccount"`
	NewOrder   string `json:"newOrder"`
	Meta       struct {
		TermsOfService string `json:"termsOfService"`
	} `json:"meta"`
}

// acmeError is a problem document.
type acmeError struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

// acmeIdentifier, challenge, authorization, order: the order subset.
type acmeIdentifier struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type acmeChallenge struct {
	Type  string     `json:"type"`
	URL   string     `json:"url"`
	Token string     `json:"token"`
	Error *acmeError `json:"error"`
}

type acmeAuthorization struct {
	Identifier acmeIdentifier  `json:"identifier"`
	Status     string          `json:"status"`
	Challenges []acmeChallenge `json:"challenges"`
}

type acmeOrder struct {
	Status         string     `json:"status"`
	Authorizations []string   `json:"authorizations"`
	Finalize       string     `json:"finalize"`
	Certificate    string     `json:"certificate"`
	Error          *acmeError `json:"error"`
}

// Client speaks ACME over plain HTTPS with stdlib only.
type Client struct {
	DirectoryURL string
	HTTP         *http.Client
	nonce        string
}

// NewClient builds a client for a directory URL (production or test).
func NewClient(directoryURL string) (*Client, error) {
	u, err := urlpkg.Parse(directoryURL)
	if err != nil {
		return nil, fmt.Errorf("cert: directory URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("cert: directory URL must be http or https")
	}
	return &Client{DirectoryURL: directoryURL, HTTP: &http.Client{Timeout: acmeTimeout}}, nil
}

// postJWS sends one signed request, refreshing nonces and retrying a
// single badNonce exactly once. Responses decode into out when non-nil;
// the fresh nonce always updates first.
func (c *Client) postJWS(ctx context.Context, acct *Account, url string, payload any, out any) error {
	body, err := json.Marshal(payloadOrEmpty(payload))
	if err != nil {
		return fmt.Errorf("cert: acme encode: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if c.nonce == "" {
			if err := c.fetchNonce(ctx); err != nil {
				return err
			}
		}
		signed, err := signJWS(acct, url, c.nonce, body)
		if err != nil {
			return err
		}
		var problem acmeError
		status, nonce, raw, err := c.postJSON(ctx, url, signed)
		if err != nil {
			return err
		}
		c.nonce = nonce
		if status == http.StatusOK || status == http.StatusCreated {
			if out != nil {
				if err := json.Unmarshal(raw, out); err != nil {
					return fmt.Errorf("cert: acme decode: %w", err)
				}
			}
			return nil
		}
		_ = json.Unmarshal(raw, &problem)
		if problem.Type == acmeBadNonceType {
			lastErr = fmt.Errorf("cert: acme bad nonce, retrying")
			c.nonce = ""
			continue
		}
		if problem.Detail != "" {
			return fmt.Errorf("cert: acme %s: %s", problem.Type, problem.Detail)
		}
		return fmt.Errorf("cert: acme %s: status %d", url, status)
	}
	return lastErr
}

// payloadOrEmpty renders nil payloads as {} per JWS conventions.
func payloadOrEmpty(payload any) any {
	if payload == nil {
		return map[string]any{}
	}
	return payload
}

// signJWS builds the flattened JWS with jwk (no KID yet) or kid.
func signJWS(acct *Account, url, nonce string, body []byte) ([]byte, error) {
	protected := map[string]any{"alg": "ES256", "nonce": nonce, "url": url}
	if acct.KID == "" {
		protected["jwk"] = jwk(acct.Key)
	} else {
		protected["kid"] = acct.KID
	}
	rawProtected, err := json.Marshal(protected)
	if err != nil {
		return nil, fmt.Errorf("cert: acme protected: %w", err)
	}
	signingInput := b64url(rawProtected) + "." + b64url(body)
	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, acct.Key, hash[:])
	if err != nil {
		return nil, fmt.Errorf("cert: acme sign: %w", err)
	}
	size := (acct.Key.Curve.Params().BitSize + 7) / 8
	sig := make([]byte, 2*size)
	r.FillBytes(sig[:size])
	s.FillBytes(sig[size:])
	return json.Marshal(map[string]string{
		"protected": b64url(rawProtected),
		"payload":   b64url(body),
		"signature": b64url(sig),
	})
}

// fetchNonce primes the replay nonce from newNonce.
func (c *Client) fetchNonce(ctx context.Context) error {
	dir, err := c.discover(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, dir.NewNonce, nil)
	if err != nil {
		return fmt.Errorf("cert: acme nonce request: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cert: acme nonce: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	nonce := resp.Header.Get("Replay-Nonce")
	if nonce == "" {
		return fmt.Errorf("cert: acme server sent no nonce")
	}
	c.nonce = nonce
	return nil
}

// discover fetches the directory document.
func (c *Client) discover(ctx context.Context) (acmeDirectory, error) {
	var dir acmeDirectory
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DirectoryURL, nil)
	if err != nil {
		return dir, fmt.Errorf("cert: acme directory request: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return dir, fmt.Errorf("cert: acme directory: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return dir, fmt.Errorf("cert: acme directory read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return dir, fmt.Errorf("cert: acme directory: status %s", resp.Status)
	}
	if err := json.Unmarshal(raw, &dir); err != nil {
		return dir, fmt.Errorf("cert: acme directory decode: %w", err)
	}
	if dir.NewAccount == "" || dir.NewOrder == "" || dir.NewNonce == "" {
		return dir, fmt.Errorf("cert: acme directory missing endpoints")
	}
	return dir, nil
}

// postJSON posts a JWS body, returning status, fresh nonce, and raw body.
func (c *Client) postJSON(ctx context.Context, url string, signed []byte) (int, string, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(signed))
	if err != nil {
		return 0, "", nil, fmt.Errorf("cert: acme request: %w", err)
	}
	req.Header.Set("Content-Type", "application/jose+json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, "", nil, fmt.Errorf("cert: acme post: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, "", nil, fmt.Errorf("cert: acme read: %w", err)
	}
	return resp.StatusCode, resp.Header.Get("Replay-Nonce"), raw, nil
}

// get fetches an ACME resource (authorizations poll unsigned GETs).
func (c *Client) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("cert: acme get request: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cert: acme get: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("cert: acme get read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("cert: acme get %s: status %s", url, resp.Status)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("cert: acme get decode: %w", err)
	}
	return nil
}

// pollAuthz waits for one authorization to leave pending, polling the
// unsigned URL until ctx ends. Invalid authorizations report the
// challenge error, never a bare status.
func (c *Client) pollAuthz(ctx context.Context, url string, interval time.Duration) (acmeAuthorization, error) {
	var authz acmeAuthorization
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := c.get(ctx, url, &authz); err != nil {
			return authz, err
		}
		switch authz.Status {
		case "valid":
			return authz, nil
		case "invalid":
			for _, challenge := range authz.Challenges {
				if challenge.Error != nil {
					return authz, fmt.Errorf("cert: challenge %s: %s", challenge.Error.Type, challenge.Error.Detail)
				}
			}
			return authz, fmt.Errorf("cert: authorization invalid")
		}
		select {
		case <-ctx.Done():
			return authz, fmt.Errorf("cert: authorization pending: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// pollOrder waits for finalization, returning the certificate URL.
func (c *Client) pollOrder(ctx context.Context, acct *Account, url string, interval time.Duration) (string, error) {
	var order acmeOrder
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		var fetch acmeOrder
		if err := c.postJWS(ctx, acct, url, nil, &fetch); err != nil {
			return "", err
		}
		order = fetch
		switch order.Status {
		case "valid":
			if order.Certificate == "" {
				return "", fmt.Errorf("cert: order valid without certificate URL")
			}
			return order.Certificate, nil
		case "invalid":
			if order.Error != nil {
				return "", fmt.Errorf("cert: order %s: %s", order.Error.Type, order.Error.Detail)
			}
			return "", fmt.Errorf("cert: order invalid")
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("cert: order pending: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// ensureAccount registers the account when no KID exists, requiring
// explicit terms agreement when the directory publishes terms. Existing
// KIDs skip registration — agreement happened at creation. The KID
// comes from the Location header, which RFC 8555 mandates.
func (c *Client) ensureAccount(ctx context.Context, acct *Account, agreeTerms bool) error {
	if acct.KID != "" {
		return nil
	}
	dir, err := c.discover(ctx)
	if err != nil {
		return err
	}
	if dir.Meta.TermsOfService != "" && !agreeTerms {
		return fmt.Errorf("cert: terms of service require owner agreement")
	}
	body, err := json.Marshal(map[string]any{"termsOfServiceAgreed": true})
	if err != nil {
		return fmt.Errorf("cert: acme encode: %w", err)
	}
	if c.nonce == "" {
		if err := c.fetchNonce(ctx); err != nil {
			return err
		}
	}
	signed, err := signJWS(acct, dir.NewAccount, c.nonce, body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dir.NewAccount, bytes.NewReader(signed))
	if err != nil {
		return fmt.Errorf("cert: acme request: %w", err)
	}
	req.Header.Set("Content-Type", "application/jose+json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cert: acme newAccount: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("cert: acme read: %w", err)
	}
	if nonce := resp.Header.Get("Replay-Nonce"); nonce != "" {
		c.nonce = nonce
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("cert: acme newAccount: status %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	if acct.KID = resp.Header.Get("Location"); acct.KID == "" {
		return fmt.Errorf("cert: acme newAccount returned no key location")
	}
	return nil
}

// finalizeCSR builds a CSR for names under a fresh P-256 key, returning
// DER plus the key PEM for storage alongside the chain.
func finalizeCSR(names []string) (der, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("cert: certificate key: %w", err)
	}
	template := x509.CertificateRequest{DNSNames: names}
	der, err = x509.CreateCertificateRequest(rand.Reader, &template, key)
	if err != nil {
		return nil, nil, fmt.Errorf("cert: CSR: %w", err)
	}
	keyPEM, err = EncodeAccountKey(key)
	if err != nil {
		return nil, nil, err
	}
	return der, keyPEM, nil
}

// download fetches the PEM chain, validating at least one certificate
// block so truncated bodies never pass as chains.
func (c *Client) download(ctx context.Context, acct *Account, url string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("cert: acme encode: %w", err)
	}
	if c.nonce == "" {
		if err := c.fetchNonce(ctx); err != nil {
			return nil, err
		}
	}
	signed, err := signJWS(acct, url, c.nonce, body)
	if err != nil {
		return nil, err
	}
	status, nonce, raw, err := c.postJSON(ctx, url, signed)
	if err != nil {
		return nil, err
	}
	if nonce != "" {
		c.nonce = nonce
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("cert: acme download: status %d", status)
	}
	block, _ := pem.Decode(raw)
	if block == nil || !strings.HasSuffix(block.Type, "CERTIFICATE") {
		return nil, fmt.Errorf("cert: acme download is not a PEM chain")
	}
	return raw, nil
}
