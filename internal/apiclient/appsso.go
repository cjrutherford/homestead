package apiclient

import (
	"context"
	"net/url"

	"homestead/internal/appsso"
	"homestead/internal/sso"
)

// AppSignInRequest is a plan or apply body (S298). Token is the Authentik
// API token for one apply; it is sent once and never stored.
type AppSignInRequest struct {
	Enabled *bool  `json:"enabled"`
	Digest  string `json:"digest,omitempty"`
	Token   string `json:"token,omitempty"`
}

func signInPath(pkg, suffix string) string {
	return "/api/v1/catalog/" + url.PathEscape(pkg) + "/sign-in" + suffix
}

// AppSignIn reads whether an app signs in through Authentik.
func (c *Client) AppSignIn(ctx context.Context, pkg string) (appsso.Status, error) {
	var out appsso.Status
	err := c.get(ctx, signInPath(pkg, ""), &out)
	return out, err
}

// AppSignInPlan previews turning sign-in on or off; nothing changes.
func (c *Client) AppSignInPlan(ctx context.Context, pkg string, enabled bool) (appsso.Plan, error) {
	var out appsso.Plan
	err := c.postJSON(ctx, signInPath(pkg, "/plan"), AppSignInRequest{Enabled: &enabled}, &out)
	return out, err
}

// AppSignInApply starts the operation for the reviewed plan.
func (c *Client) AppSignInApply(ctx context.Context, pkg string, req AppSignInRequest) (int64, error) {
	var out OperationStarted
	err := c.postJSON(ctx, signInPath(pkg, "/apply"), req, &out)
	return out.ID, err
}

// AppSignInAccess reads back the groups and bindings Homestead made for the
// app's sign-in (S314). Nothing changes.
func (c *Client) AppSignInAccess(ctx context.Context, pkg, token string) (sso.AccessReport, error) {
	var out sso.AccessReport
	err := c.postJSON(ctx, signInPath(pkg, "/access"), AccessRequest{Token: token}, &out)
	return out, err
}
