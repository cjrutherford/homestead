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
