package apiclient

import (
	"context"

	"homestead/internal/cert"
)

// TLSRenewal lists certificate renewal statuses (S263).
func (c *Client) TLSRenewal(ctx context.Context) ([]cert.RenewalStatus, error) {
	var out []cert.RenewalStatus
	if err := c.get(ctx, "/api/v1/tls/renewal", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []cert.RenewalStatus{}
	}
	return out, nil
}
