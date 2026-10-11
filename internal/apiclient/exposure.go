package apiclient

import (
	"context"

	"homestead/internal/exposure"
)

// ExposureReview recomputes the reviewed plan from saved wizard
// inputs (S245). Nothing saved reads as an error, never an empty
// plan.
func (c *Client) ExposureReview(ctx context.Context) (exposure.Plan, error) {
	var out exposure.Plan
	if err := c.get(ctx, "/api/v1/exposure/review", &out); err != nil {
		return exposure.Plan{}, err
	}
	return out, nil
}

// ExposureApproval is one recorded ownership decision.
type ExposureApproval struct {
	Host  string `json:"host"`
	Actor string `json:"actor"`
}

// ExposureApprovals lists recorded approvals in host order.
func (c *Client) ExposureApprovals(ctx context.Context) ([]ExposureApproval, error) {
	var out []ExposureApproval
	if err := c.get(ctx, "/api/v1/exposure/approvals", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []ExposureApproval{}
	}
	return out, nil
}

// ExposureApprove approves the reviewed plan under the acting owner
// with explicit CIDRs for wide sources.
func (c *Client) ExposureApprove(ctx context.Context, actor string, networks []string) (ExposureApproval, error) {
	var out ExposureApproval
	body := map[string]any{"actor": actor, "networks": networks}
	if err := c.postJSON(ctx, "/api/v1/exposure/approvals", body, &out); err != nil {
		return ExposureApproval{}, err
	}
	return out, nil
}

// ExposureApply starts application for an approved host, returning
// the operation id for progress streams.
func (c *Client) ExposureApply(ctx context.Context, host string) (int64, error) {
	var out OperationStarted
	if err := c.postJSON(ctx, "/api/v1/exposure/apply", map[string]any{"host": host}, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
