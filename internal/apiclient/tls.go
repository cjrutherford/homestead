package apiclient

import (
	"context"

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
func (c *Client) TLSIssuanceApprove(ctx context.Context, certificate, digest string, agreeTerms, confirmProduction bool) (cert.Approval, error) {
	var out cert.Approval
	err := c.postJSON(ctx, "/api/v1/tls/issuance/approvals", map[string]any{
		"certificate": certificate, "digest": digest,
		"agree_terms": agreeTerms, "confirm_production": confirmProduction,
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
func (c *Client) TLSIssuanceCredentials(ctx context.Context, provider string, values map[string]string) ([]string, error) {
	var out struct {
		Stored []string `json:"stored"`
	}
	if err := c.postJSON(ctx, "/api/v1/tls/issuance/credentials",
		map[string]any{"provider": provider, "values": values}, &out); err != nil {
		return nil, err
	}
	return out.Stored, nil
}
