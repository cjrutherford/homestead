package apiclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"homestead/internal/cert"
)

// TLSRenewal returns the certificate renewal report (S263): per-
// certificate statuses plus the explicit recorded-issuance count.
func (c *Client) TLSRenewal(ctx context.Context) (cert.RenewalReport, error) {
	var out cert.RenewalReport
	if err := c.get(ctx, "/api/v1/tls/renewal", &out); err != nil {
		return cert.RenewalReport{}, err
	}
	if out.Certificates == nil {
		out.Certificates = []cert.RenewalStatus{}
	}
	return out, nil
}

// TLSIssuancePlans previews first issuance (S278) for every ACME dns-01
// certificate in the saved strategy. No CA is contacted.
func (c *Client) TLSIssuancePlans(ctx context.Context) ([]cert.IssuancePlan, error) {
	var out struct {
		Plans []cert.IssuancePlan `json:"plans"`
	}
	if err := c.get(ctx, "/api/v1/tls/issuance/plans", &out); err != nil {
		return nil, err
	}
	if out.Plans == nil {
		out.Plans = []cert.IssuancePlan{}
	}
	return out.Plans, nil
}

// TLSIssuanceApprovals lists the recorded issuance approvals.
func (c *Client) TLSIssuanceApprovals(ctx context.Context) ([]cert.Approval, error) {
	var out struct {
		Approvals []cert.Approval `json:"approvals"`
	}
	if err := c.get(ctx, "/api/v1/tls/issuance/approvals", &out); err != nil {
		return nil, err
	}
	if out.Approvals == nil {
		out.Approvals = []cert.Approval{}
	}
	return out.Approvals, nil
}

// TLSIssuanceApprove approves one reviewed plan digest.
func (c *Client) TLSIssuanceApprove(ctx context.Context, certificate, digest string, agreeTerms, confirmProduction bool, renewal string) (cert.Approval, error) {
	var out cert.Approval
	err := c.postJSON(ctx, "/api/v1/tls/issuance/approvals", map[string]any{
		"certificate": certificate, "digest": digest,
		"agree_terms": agreeTerms, "confirm_production": confirmProduction, "renewal": renewal,
	}, &out)
	return out, err
}

// TLSIssuanceApply starts the approved issuance, returning the
// operation id for progress streams.
func (c *Client) TLSIssuanceApply(ctx context.Context, certificate string) (int64, error) {
	var out OperationStarted
	if err := c.postJSON(ctx, "/api/v1/tls/issuance/apply", map[string]any{"certificate": certificate}, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// TLSIssuanceCredentials stores a DNS provider's credential values in the
// daemon's secret store (S278). Write-only: values are never read back.
// The warning is set when a stored Cloudflare token cannot see the setup
// domain's zone (S294); the credentials are stored either way.
func (c *Client) TLSIssuanceCredentials(ctx context.Context, provider string, values map[string]string) ([]string, string, error) {
	var out struct {
		Stored  []string `json:"stored"`
		Warning string   `json:"warning"`
	}
	if err := c.postJSON(ctx, "/api/v1/tls/issuance/credentials",
		map[string]any{"provider": provider, "values": values}, &out); err != nil {
		return nil, "", err
	}
	return out.Stored, out.Warning, nil
}

// TLSReregisterAccount re-registers the ACME account of a certificate's
// approved CA directory (S272): the owner action for a CA that forgot the
// account, required for production directories.
// Production directories need agreeTerms: a fresh agreement to the CA's
// current terms (TLSAccountTerms shows them).
func (c *Client) TLSReregisterAccount(ctx context.Context, certificate string, agreeTerms bool) error {
	return c.postJSON(ctx, "/api/v1/tls/issuance/account/reregister",
		map[string]any{"certificate": certificate, "agree_terms": agreeTerms}, nil)
}

// TLSAccountTerms returns the terms of service the approved CA publishes
// now next to the one recorded at registration.
func (c *Client) TLSAccountTerms(ctx context.Context, certificate string) (cert.AccountTerms, error) {
	var out cert.AccountTerms
	err := c.get(ctx, "/api/v1/tls/issuance/account/terms?certificate="+url.QueryEscape(certificate), &out)
	return out, err
}

// LocalCAReport returns the internal CA with its fingerprint and every
// local leaf graded (S280, ADR-030). Public material only.
func (c *Client) LocalCAReport(ctx context.Context) (cert.Report, error) {
	var out cert.Report
	if err := c.get(ctx, "/api/v1/tls/local-ca", &out); err != nil {
		return cert.Report{}, err
	}
	if out.Leaves == nil {
		out.Leaves = []cert.Leaf{}
	}
	return out, nil
}

// LocalCACreate creates the internal CA if absent (idempotent).
func (c *Client) LocalCACreate(ctx context.Context) (cert.CAInfo, bool, error) {
	var out struct {
		CA      cert.CAInfo `json:"ca"`
		Created bool        `json:"created"`
	}
	if err := c.postJSON(ctx, "/api/v1/tls/local-ca", map[string]any{}, &out); err != nil {
		return cert.CAInfo{}, false, err
	}
	return out.CA, out.Created, nil
}

// LocalCALeaves lists the graded local leaves.
func (c *Client) LocalCALeaves(ctx context.Context) ([]cert.Leaf, error) {
	var out struct {
		Leaves []cert.Leaf `json:"leaves"`
	}
	if err := c.get(ctx, "/api/v1/tls/local-ca/leaves", &out); err != nil {
		return nil, err
	}
	if out.Leaves == nil {
		out.Leaves = []cert.Leaf{}
	}
	return out.Leaves, nil
}

// LocalCAReissue re-issues local leaves (one host, or all when empty) and
// returns the operation id.
func (c *Client) LocalCAReissue(ctx context.Context, host string) (int64, error) {
	var out OperationStarted
	if err := c.postJSON(ctx, "/api/v1/tls/local-ca/leaves/reissue", map[string]any{"host": host}, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// LocalCARotationPlan previews a root rotation.
func (c *Client) LocalCARotationPlan(ctx context.Context) (cert.RotationPlan, error) {
	var out cert.RotationPlan
	err := c.get(ctx, "/api/v1/tls/local-ca/rotation", &out)
	return out, err
}

// LocalCARotate approves the reviewed plan digest and returns the
// operation id.
func (c *Client) LocalCARotate(ctx context.Context, digest string) (int64, error) {
	var out OperationStarted
	if err := c.postJSON(ctx, "/api/v1/tls/local-ca/rotation", map[string]any{"digest": digest}, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// LocalCARoot downloads the public root certificate ("pem" or "der"),
// the same unauthenticated bytes a device fetches (ADR-030).
func (c *Client) LocalCARoot(ctx context.Context, format string) ([]byte, error) {
	if format != "pem" && format != "der" {
		return nil, fmt.Errorf("api: root format must be pem or der")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/tls/local-ca/root."+format, nil)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		excerpt, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, &StatusError{Method: req.Method, Path: req.URL.Path, Status: res.StatusCode, Body: strings.TrimSpace(string(excerpt))}
	}
	return io.ReadAll(io.LimitReader(res.Body, 1<<20))
}
