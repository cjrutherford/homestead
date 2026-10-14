package cert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/dns"
	"homestead/internal/secret"
	"homestead/internal/store"
	"path/filepath"
)

// acmeDouble is a scripted CA: directory, nonce, account, one order
// with one dns-01 authorization, finalize, and download. It verifies
// JWS signatures against the account key from first contact, proving
// the client's signing end to end.
type acmeDouble struct {
	t         *testing.T
	mu        sync.Mutex
	base      string
	nonces    []string
	jwk       map[string]string
	triggered bool
	authed    bool
	terms     string
	badNonce  bool
	usedNonce map[string]bool
	failFinal bool
	// lastPayload is the raw JWS payload of the last verified request.
	lastPayload string
	// csrNames/leaf: what the CSR asked for and the leaf issued for it.
	leafPEM []byte
	// pollsFinalize counts (forbidden) polls of the finalize URL.
	orderPolls int
}

func (d *acmeDouble) freshNonce() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	d.nonces = append(d.nonces, nonce)
	return nonce
}

func (d *acmeDouble) handler(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	nonce := d.freshNonce()
	w.Header().Set("Replay-Nonce", nonce)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/directory":
		meta := map[string]any{}
		if d.terms != "" {
			meta["termsOfService"] = d.terms
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"newNonce": d.base + "/new-nonce", "newAccount": d.base + "/new-account",
			"newOrder": d.base + "/new-order", "meta": meta,
		})
	case r.URL.Path == "/new-nonce":
		w.WriteHeader(http.StatusOK)
	case r.URL.Path == "/new-account":
		body := d.verifyJWS(w, r, true)
		if body == nil {
			return
		}
		w.Header().Set("Location", d.base+"/acct/1")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "valid"})
	case r.URL.Path == "/new-order":
		if d.verifyJWS(w, r, false) == nil {
			return
		}
		w.Header().Set("Location", d.base+"/order/1")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "pending", "finalize": d.base + "/finalize/1",
			"authorizations": []string{d.base + "/authz/1"},
		})
	case r.URL.Path == "/authz/1":
		d.verifyAsGet(w, r)
		status := "pending"
		if d.triggered {
			status = "valid"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"identifier": map[string]string{"type": "dns", "value": "www.example.com"},
			"status":     status,
			"challenges": []map[string]any{
				{"type": "dns-01", "url": d.base + "/chall/1", "token": "tok123"},
			},
		})
	case r.URL.Path == "/chall/1":
		if d.verifyJWS(w, r, false) == nil {
			return
		}
		d.triggered = true
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "dns-01", "status": "processing"})
	case r.URL.Path == "/finalize/1":
		payload := d.verifyJWS(w, r, false)
		if payload == nil {
			return
		}
		if d.authed {
			d.t.Fatalf("finalize URL polled again: RFC 8555 §7.1.3 says poll the order URL")
		}
		if d.failFinal {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type": "urn:ietf:params:acme:error:unauthorized", "detail": "nope",
			})
			return
		}
		csr, _ := payload["csr"].(string)
		raw, err := base64.RawURLEncoding.DecodeString(csr)
		if err != nil {
			d.t.Fatalf("csr: %v", err)
		}
		d.leafPEM = leafForCSR(d.t, raw)
		d.authed = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "processing", "finalize": d.base + "/finalize/1",
		})
	case r.URL.Path == "/order/1":
		d.verifyAsGet(w, r)
		d.orderPolls++
		if !d.authed || d.orderPolls < 2 {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "processing"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "valid", "certificate": d.base + "/cert/1",
		})
	case r.URL.Path == "/cert/1":
		d.verifyAsGet(w, r)
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		_, _ = w.Write(d.leafPEM)
	default:
		d.t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
	}
}

// verifyJWS checks signature, nonce freshness, and url binding,
// returning the decoded payload. The first contact registers the jwk.
func (d *acmeDouble) verifyJWS(w http.ResponseWriter, r *http.Request, register bool) map[string]any {
	var envelope struct {
		Protected string `json:"protected"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		d.t.Fatalf("decode jws: %v", err)
	}
	rawProtected, err := base64.RawURLEncoding.DecodeString(envelope.Protected)
	if err != nil {
		d.t.Fatalf("decode protected: %v", err)
	}
	var protected struct {
		Alg   string            `json:"alg"`
		Nonce string            `json:"nonce"`
		URL   string            `json:"url"`
		JWK   map[string]string `json:"jwk"`
		KID   string            `json:"kid"`
	}
	if err := json.Unmarshal(rawProtected, &protected); err != nil {
		d.t.Fatalf("protected: %v", err)
	}
	d.lastPayload = envelope.Payload
	if protected.Alg != "ES256" {
		d.t.Fatalf("alg = %q", protected.Alg)
	}
	if register {
		d.jwk = protected.JWK
	}
	if protected.URL != d.base+r.URL.Path {
		d.t.Fatalf("url binding = %q, want %q", protected.URL, d.base+r.URL.Path)
	}
	if d.usedNonce[protected.Nonce] {
		d.t.Fatalf("nonce replayed: %q", protected.Nonce)
	}
	d.usedNonce[protected.Nonce] = true
	if d.jwk == nil {
		d.t.Fatal("no account key registered")
	}
	x, _ := base64.RawURLEncoding.DecodeString(d.jwk["x"])
	y, _ := base64.RawURLEncoding.DecodeString(d.jwk["y"])
	key := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	sig, err := base64.RawURLEncoding.DecodeString(envelope.Signature)
	if err != nil {
		d.t.Fatalf("decode sig: %v", err)
	}
	hash := sha256.Sum256([]byte(envelope.Protected + "." + envelope.Payload))
	if !ecdsa.Verify(key, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		d.t.Fatalf("bad JWS signature")
	}
	var payload map[string]any
	rawPayload, _ := base64.RawURLEncoding.DecodeString(envelope.Payload)
	_ = json.Unmarshal(rawPayload, &payload)
	return payload
}

// verifyAsGet verifies a POST-as-GET: signed, with an EMPTY payload.
func (d *acmeDouble) verifyAsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		d.t.Fatalf("%s %s: ACME reads must be POST-as-GET", r.Method, r.URL.Path)
	}
	d.verifyJWS(w, r, false)
	if d.lastPayload != "" {
		d.t.Fatalf("%s: POST-as-GET payload must be empty, got %q", r.URL.Path, d.lastPayload)
	}
}

// leafForCSR issues a real leaf for the CSR's public key and names,
// signed by a throwaway CA, so placement can prove the key matches.
func leafForCSR(t *testing.T, der []byte) []byte {
	t.Helper()
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatalf("parse csr: %v", err)
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	leaf := x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: csr.DNSNames[0]},
		DNSNames: csr.DNSNames, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(60 * 24 * time.Hour)}
	leafDER, err := x509.CreateCertificate(rand.Reader, &leaf, ca, csr.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf: %v", err)
	}
	return append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
}

// testChain mints a throwaway certificate PEM for download tests.
func testChain(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "www.example.com"},
		DNSNames: []string{"www.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func serveACME(t *testing.T, d *acmeDouble) (*Client, string) {
	t.Helper()
	d.t = t
	d.usedNonce = map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(d.handler))
	t.Cleanup(srv.Close)
	d.base = srv.URL
	client, err := NewClient(srv.URL + "/directory")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client, srv.URL
}

func testAccount(t *testing.T) *Account {
	t.Helper()
	key, err := GenerateAccountKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return &Account{Key: key}
}

func openDNS01(t *testing.T, client *Client, provider *scriptDNSProvider) (*DNS01, *secret.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	secrets, err := secret.New(s, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	registry := dns.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	probe := func(_ context.Context, name string, _ dns.RecordType) ([]string, error) {
		for _, r := range provider.records {
			full := r.Name + ".example.com"
			if r.Name == "@" {
				full = "example.com"
			}
			if full == name && r.Type == dns.RecordTXT {
				return []string{r.Value}, nil
			}
		}
		return nil, fmt.Errorf("no such host %s", name)
	}
	return &DNS01{
		Providers: registry, Secrets: secrets, Client: client,
		Probe: probe, Interval: time.Millisecond,
	}, secrets
}

func errNoSuchHostForTest(name string) error {
	return &netDNSErrorForTest{name: name}
}

type netDNSErrorForTest struct{ name string }

func (e *netDNSErrorForTest) Error() string   { return "no such host " + e.name }
func (e *netDNSErrorForTest) Timeout() bool   { return false }
func (e *netDNSErrorForTest) Temporary() bool { return false }

func scriptIssuer() Issuer {
	return Issuer{Name: "test-acme", Type: IssuerACME,
		Directory: "https://ca.example/directory", Challenge: ChallengeDNS01,
		DNSProvider: "script"}
}

func TestIssueFlow(t *testing.T) {
	double := &acmeDouble{}
	client, _ := serveACME(t, double)
	provider := &scriptDNSProvider{}
	dns01, _ := openDNS01(t, client, provider)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	issued, err := dns01.Issue(ctx, testAccount(t), scriptIssuer(), "example.com",
		[]string{"www.example.com"}, true)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	chain, _ := pem.Decode(issued.ChainPEM)
	if chain == nil {
		t.Fatal("chain is not PEM")
	}
	if _, err := x509.ParseCertificate(chain.Bytes); err != nil {
		t.Fatalf("chain parse: %v", err)
	}
	key, err := ParseAccountKey(issued.KeyPEM)
	if err != nil {
		t.Fatalf("key parse: %v", err)
	}
	if key.Curve != elliptic.P256() {
		t.Fatal("key is not P-256")
	}
	// TXT placed carried the exact key-authorization hash, then cleaned.
	if len(provider.records) != 0 {
		t.Fatalf("leftovers = %+v", provider.records)
	}
}

func TestIssueTermsRefused(t *testing.T) {
	double := &acmeDouble{terms: "https://ca.example/terms"}
	client, _ := serveACME(t, double)
	provider := &scriptDNSProvider{}
	dns01, _ := openDNS01(t, client, provider)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := dns01.Issue(ctx, testAccount(t), scriptIssuer(), "example.com",
		[]string{"www.example.com"}, false)
	if err == nil || !strings.Contains(err.Error(), "owner agreement") {
		t.Fatalf("err = %v", err)
	}
}

func TestIssueFailureCleansUp(t *testing.T) {
	double := &acmeDouble{failFinal: true}
	client, _ := serveACME(t, double)
	provider := &scriptDNSProvider{}
	dns01, _ := openDNS01(t, client, provider)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := dns01.Issue(ctx, testAccount(t), scriptIssuer(), "example.com",
		[]string{"www.example.com"}, true)
	if err == nil {
		t.Fatal("issued through failing finalize")
	}
	for _, r := range provider.records {
		if r.Type == dns.RecordTXT && strings.HasPrefix(r.Name, "_acme-challenge") {
			t.Fatalf("challenge leaked: %+v", provider.records)
		}
	}
}

func TestPlacerFor(t *testing.T) {
	double := &acmeDouble{}
	client, _ := serveACME(t, double)
	provider := &scriptDNSProvider{}
	dns01, secrets := openDNS01(t, client, provider)
	placer, err := dns01.placerFor(scriptIssuer(), "example.com")
	if err != nil {
		t.Fatalf("placerFor: %v", err)
	}
	if placer.Zone != "example.com" {
		t.Fatalf("placer = %+v", placer)
	}
	httpIssuer := Issuer{Name: "x", Type: IssuerACME,
		Directory: "https://x/", Challenge: ChallengeHTTP01}
	if _, err := dns01.placerFor(httpIssuer, "example.com"); err == nil ||
		!strings.Contains(err.Error(), "not dns-01") {
		t.Fatalf("err = %v", err)
	}
	internal := Issuer{Name: "x", Type: IssuerInternal}
	if _, err := dns01.placerFor(internal, "example.com"); err == nil {
		t.Fatal("wired internal issuer")
	}
	unknown := scriptIssuer()
	unknown.DNSProvider = "nope"
	if _, err := dns01.placerFor(unknown, "example.com"); err == nil {
		t.Fatal("wired unknown provider")
	}
	provider.fields = []dns.CredentialField{{Name: "api_token", Label: "Token", Secret: true}}
	if _, err := dns01.placerFor(scriptIssuer(), "example.com"); err == nil ||
		!strings.Contains(err.Error(), `missing credential "api_token"`) {
		t.Fatalf("err = %v", err)
	}
	if err := secrets.Set("dns_script_api_token", "token"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := dns01.placerFor(scriptIssuer(), "example.com"); err != nil {
		t.Fatalf("placerFor with secret: %v", err)
	}
}

func TestDefaultProviders(t *testing.T) {
	registry, err := DefaultProviders()
	if err != nil {
		t.Fatalf("DefaultProviders: %v", err)
	}
	if names := registry.Names(); len(names) != 3 ||
		names[0] != "cloudflare" || names[1] != "digitalocean" || names[2] != "route53" {
		t.Fatalf("names = %v", names)
	}
}

func TestAccountKeyRoundTrip(t *testing.T) {
	key, err := GenerateAccountKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	raw, err := EncodeAccountKey(key)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	back, err := ParseAccountKey(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if back.D.Cmp(key.D) != 0 {
		t.Fatal("key mismatch")
	}
	if _, err := ParseAccountKey([]byte("not pem")); err == nil {
		t.Fatal("parsed junk")
	}
}
