package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// AdGuard paths on the published web port (S084). The adapter speaks the
// stable control API with basic auth; initial install wizard automation
// (POST /control/install/configure) stays out: its schema is version
// brittle and unverifiable here, recorded in DEFERRED for the slice that
// needs end-to-end setup (S086).
const (
	adguardStatusPath      = "/control/status"
	adguardRewriteListPath = "/control/rewrite/list"
	adguardRewriteAddPath  = "/control/rewrite/add"
	adguardRewriteDelPath  = "/control/rewrite/delete"
)

// defaultTimeout bounds every adapter call; DNS management must fail fast,
// never hang operations.
const defaultTimeout = 15 * time.Second

var validDomainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// validateDomain enforces canonical lowercase DNS for rewrite names, the
// same spelling the route model (S082) requires, so names agree across
// proxy and DNS.
func validateDomain(domain string) error {
	if len(domain) == 0 || len(domain) > 253 {
		return fmt.Errorf("dns: domain must be 1-253 characters")
	}
	if strings.ToLower(domain) != domain {
		return fmt.Errorf("dns: domain must be lowercase")
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return fmt.Errorf("dns: domain must not have empty labels")
	}
	for _, label := range strings.Split(domain, ".") {
		if !validDomainLabel.MatchString(label) {
			return fmt.Errorf("dns: invalid domain label %q", label)
		}
	}
	return nil
}

// Client is the local DNS provider adapter backed by AdGuard Home. It
// owns the HTTP conversation only: no serving, no persistence, no
// desired-state wiring (S086/S090 compose it into features).
type Client struct {
	base     *url.URL
	username string
	password string
	http     *http.Client
}

// NewAdGuard builds the adapter for the published web origin, for
// example http://127.0.0.1:3000. Credentials are the AdGuard administrator
// from initial setup; empty credentials mean unauthenticated calls, which
// a configured instance rejects.
func NewAdGuard(baseURL, username, password string) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("dns: base URL: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("dns: base URL must be http or https")
	}
	if base.Host == "" {
		return nil, fmt.Errorf("dns: base URL must have a host")
	}
	return &Client{
		base:     base,
		username: username,
		password: password,
		http:     &http.Client{Timeout: defaultTimeout},
	}, nil
}

// Status is the observed AdGuard state. Only needed fields decode; the
// API carries more across versions and the adapter must not pin it.
type Status struct {
	Running      bool
	Version      string
	DNSAddresses []string
}

// Rewrite is one custom DNS answer: domain resolves to answer.
type Rewrite struct {
	Domain string
	Answer string
}

// Validate checks rewrite shape: canonical domain, IP answer.
func (r Rewrite) Validate() error {
	if err := validateDomain(r.Domain); err != nil {
		return err
	}
	if _, err := netip.ParseAddr(r.Answer); err != nil {
		return fmt.Errorf("dns: answer must be an IP address")
	}
	return nil
}

// call performs one authenticated control-API call, decoding JSON into
// out when non-nil. Non-2xx responses are errors carrying status.
func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("dns: encode: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	target := *c.base
	target.Path = strings.TrimSuffix(c.base.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return fmt.Errorf("dns: request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("dns: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("dns: read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("dns: %s %s: status %s", method, path, resp.Status)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("dns: decode: %w", err)
		}
	}
	return nil
}

// adguardStatus is the /control/status shape subset the adapter reads.
type adguardStatus struct {
	Running      bool     `json:"running"`
	Version      string   `json:"version"`
	DNSAddresses []string `json:"dns_addresses"`
}

// adguardRewrite is the rewrite entry shape shared by list/add/delete.
type adguardRewrite struct {
	Domain string `json:"domain"`
	Answer string `json:"answer"`
}

// FetchStatus proves the DNS service answers its control API and reports
// whether its DNS server runs.
func (c *Client) FetchStatus(ctx context.Context) (Status, error) {
	var raw adguardStatus
	if err := c.call(ctx, http.MethodGet, adguardStatusPath, nil, &raw); err != nil {
		return Status{}, err
	}
	return Status{Running: raw.Running, Version: raw.Version, DNSAddresses: raw.DNSAddresses}, nil
}

// ListRewrites returns the custom DNS answers currently configured.
func (c *Client) ListRewrites(ctx context.Context) ([]Rewrite, error) {
	var raw []adguardRewrite
	if err := c.call(ctx, http.MethodGet, adguardRewriteListPath, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Rewrite, 0, len(raw))
	for _, entry := range raw {
		out = append(out, Rewrite{Domain: entry.Domain, Answer: entry.Answer})
	}
	return out, nil
}

// AddRewrite creates a custom DNS answer, idempotent at the server for
// identical entries.
func (c *Client) AddRewrite(ctx context.Context, r Rewrite) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, adguardRewriteAddPath,
		adguardRewrite{Domain: r.Domain, Answer: r.Answer}, nil)
}

// DeleteRewrite removes a custom DNS answer.
func (c *Client) DeleteRewrite(ctx context.Context, r Rewrite) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, adguardRewriteDelPath,
		adguardRewrite{Domain: r.Domain, Answer: r.Answer}, nil)
}
