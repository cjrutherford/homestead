package dns

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Route53Endpoint is the production API origin. Tests override it.
const Route53Endpoint = "https://route53.amazonaws.com"

// route53Version pins the API version path.
const route53Version = "2013-04-01"

// Route 53 credential fields: static IAM key pair. STS session tokens
// stay out: short-lived credentials need a refresh flow no slice owns yet.
const (
	route53KeyField    = "access_key_id"
	route53SecretField = "secret_access_key"
)

// Route53 is the S091 provider for AWS-managed public zones: record
// management plus ACME DNS-01 through the Route 53 query API, signed
// with SigV4 from stdlib only (no SDK). Only simple routing-policy
// records in intent-covered types map; weighted/latency/geolocation,
// alias targets, and foreign types stay unmanaged and never surface as
// observed state.
type Route53 struct {
	base *url.URL
	http *http.Client
	now  func() time.Time
}

// NewRoute53 builds the provider against baseURL (Route53Endpoint in
// production, httptest doubles in tests).
func NewRoute53(baseURL string) (*Route53, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("dns: route53 base URL: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("dns: route53 base URL must be http or https")
	}
	if base.Host == "" {
		return nil, fmt.Errorf("dns: route53 base URL must have a host")
	}
	return &Route53{base: base, http: &http.Client{Timeout: 15 * time.Second}, now: time.Now}, nil
}

// Name identifies the Route 53 provider.
func (r *Route53) Name() string { return "route53" }

// Capabilities reports record management and DNS-01.
func (r *Route53) Capabilities() []Capability {
	return []Capability{CapabilityRecords, CapabilityDNS01}
}

// RequiredCredentials reports the IAM key pair fields.
func (r *Route53) RequiredCredentials() []CredentialField {
	return []CredentialField{
		{Name: route53KeyField, Label: "AWS access key ID"},
		{Name: route53SecretField, Label: "AWS secret access key", Secret: true},
	}
}

// sha256Hex hashes payload for signing and transport.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hmacSHA256 chains one SigV4 key-derivation or signature step.
func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// signV4 signs req with SigV4 (service route53, region us-east-1: the
// partition-global endpoint). Paths and queries here are provider-built
// ASCII, so canonicalization is exact encoding plus key sorting.
func signV4(req *http.Request, payloadHash, accessKey, secret string, now time.Time) {
	const region, service = "us-east-1", "route53"
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	var canonicalQuery []string
	for key, values := range req.URL.Query() {
		for _, value := range values {
			canonicalQuery = append(canonicalQuery,
				url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	sort.Strings(canonicalQuery)
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		req.URL.Host, payloadHash, amzDate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonical := strings.Join([]string{
		req.Method, req.URL.EscapedPath(), strings.Join(canonicalQuery, "&"),
		canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")
	scope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	key := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, signedHeaders, signature))
}

// route53call performs one signed call with an optional XML body,
// returning the raw XML response. Service errors (4xx/5xx) are errors
// carrying status.
func (r *Route53) route53call(ctx context.Context, creds Credentials, method, path string, query url.Values, xmlBody []byte) ([]byte, error) {
	accessKey, err := creds.Get(route53KeyField)
	if err != nil {
		return nil, err
	}
	secret, err := creds.Get(route53SecretField)
	if err != nil {
		return nil, err
	}
	target := *r.base
	target.Path = "/" + route53Version + path
	target.RawQuery = query.Encode()
	var reader io.Reader
	if xmlBody != nil {
		reader = bytes.NewReader(xmlBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("dns: route53 request: %w", err)
	}
	if xmlBody != nil {
		req.Header.Set("Content-Type", "application/xml")
	}
	hash := sha256Hex(xmlBody)
	signV4(req, hash, accessKey, secret, r.now())
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dns: route53 %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("dns: route53 read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("dns: route53 %s %s: status %s: %s", method, path, resp.Status, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}
