package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"homestead/internal/sso"
)

// SSOState is the sign-in state the daemon reports (S286, ADR-033). It
// carries no secret and no token.
type SSOState struct {
	sso.Status
	Defaults  sso.Defaults `json:"defaults"`
	Installed bool         `json:"authentik_installed"`
	Method    string       `json:"session_method"`
	// Replan says the tunnel published a dashboard name the setup does
	// not cover yet (S296); ReplanReason says what a re-plan adds.
	Replan       bool   `json:"replan,omitempty"`
	ReplanReason string `json:"replan_reason,omitempty"`
	// OpenApps are apps whose Authentik sign-in binds no group: every
	// Authentik user can sign in to them (S314, ADR-043).
	OpenApps []string `json:"open_apps,omitempty"`
}

// AccessRequest asks for a read-back; Token is used for this call only.
type AccessRequest struct {
	Token string `json:"token,omitempty"`
}

// SSOAccess reads back the groups and bindings of the dashboard sign-in.
func (c *Client) SSOAccess(ctx context.Context, token string) (sso.AccessReport, error) {
	var out sso.AccessReport
	err := c.postJSON(ctx, "/api/v1/sso/access", AccessRequest{Token: token}, &out)
	return out, err
}

// ReauthHeader carries the local password for fresh re-authentication.
const ReauthHeader = "X-Homestead-Reauth"

// SSOStatus reads the sign-in state.
func (c *Client) SSOStatus(ctx context.Context) (SSOState, error) {
	var out SSOState
	err := c.get(ctx, "/api/v1/sso", &out)
	return out, err
}

// SSOPlan builds the plan to configure Authentik for sign-in.
func (c *Client) SSOPlan(ctx context.Context, req sso.PlanRequest) (sso.Plan, error) {
	var out sso.Plan
	err := c.postJSON(ctx, "/api/v1/sso/plan", req, &out)
	return out, err
}

// SSOApprove approves a plan by digest; the API token travels in the body
// only and is never returned.
func (c *Client) SSOApprove(ctx context.Context, req sso.ApproveRequest) (SSOState, error) {
	var out SSOState
	err := c.postJSON(ctx, "/api/v1/sso/approve", req, &out)
	return out, err
}

// SSOApply starts the operation that configures Authentik.
func (c *Client) SSOApply(ctx context.Context) (int64, error) {
	var out OperationStarted
	req, err := c.post(ctx, "/api/v1/sso/apply", nil)
	if err != nil {
		return 0, err
	}
	if err := c.roundTrip(req, http.StatusOK, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// SSOLinkIntent is the single-use link URL to open in a browser.
type SSOLinkIntent struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SSOLink mints the link intent. password is the owner's local password,
// sent for this request only (fresh re-authentication).
func (c *Client) SSOLink(ctx context.Context, password, origin string) (SSOLinkIntent, error) {
	var out SSOLinkIntent
	raw, err := json.Marshal(map[string]string{"origin": origin})
	if err != nil {
		return out, err
	}
	req, err := c.post(ctx, "/api/v1/sso/link", raw)
	if err != nil {
		return out, err
	}
	req.Header.Set(ReauthHeader, password)
	err = c.roundTrip(req, http.StatusOK, &out)
	return out, err
}

func (c *Client) ssoDelete(ctx context.Context, path, password string) (SSOState, error) {
	var out SSOState
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+path, bytes.NewReader(nil))
	if err != nil {
		return out, fmt.Errorf("api: %w", err)
	}
	if password != "" {
		req.Header.Set(ReauthHeader, password)
	}
	err = c.roundTrip(req, http.StatusOK, &out)
	return out, err
}

// SSOUnlink removes the link; password is the fresh local password.
func (c *Client) SSOUnlink(ctx context.Context, password string) (SSOState, error) {
	return c.ssoDelete(ctx, "/api/v1/sso/link", password)
}

// SSODisable turns sign-in with Authentik off.
func (c *Client) SSODisable(ctx context.Context) (SSOState, error) {
	return c.ssoDelete(ctx, "/api/v1/sso", "")
}
