package apiclient

import (
	"context"
	"net/url"

	"homestead/internal/appsso"
)

// AppSetupStep is one step of the setup approved with an install (S324).
type AppSetupStep struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Digest    string `json:"digest"`
	Reason    string `json:"reason,omitempty"`
	Operation int64  `json:"operation,omitempty"`
}

// AppSetupReview is a stopped step's plan as it stands now; approving
// its digest resumes the setup.
type AppSetupReview struct {
	Step      string         `json:"step"`
	Digest    string         `json:"digest,omitempty"`
	Problem   string         `json:"problem,omitempty"`
	Addresses []string       `json:"addresses,omitempty"`
	Public    *AppPublicPlan `json:"public,omitempty"`
	SignIn    *appsso.Plan   `json:"sign_in,omitempty"`
	People    []string       `json:"people,omitempty"`
	TokenKept *bool          `json:"token_kept,omitempty"`
}

// AppSetup is an app's setup: none, waiting, running, stopped, done or
// skipped.
type AppSetup struct {
	App       string          `json:"app"`
	Status    string          `json:"status"`
	Install   int64           `json:"install_operation,omitempty"`
	Operation int64           `json:"operation,omitempty"`
	Steps     []AppSetupStep  `json:"steps"`
	Reason    string          `json:"reason,omitempty"`
	Review    *AppSetupReview `json:"review,omitempty"`
}

// AppSetupResumeRequest approves the stopped step's current plan. Token
// is an Authentik API token for this run only; it is never stored.
type AppSetupResumeRequest struct {
	Digest string `json:"digest"`
	Token  string `json:"token,omitempty"`
}

func setupPath(pkg, suffix string) string {
	return "/api/v1/catalog/" + url.PathEscape(pkg) + "/setup" + suffix
}

// AppSetupStatus reads an app's setup and, when stopped, the stopped
// step's current plan.
func (c *Client) AppSetupStatus(ctx context.Context, pkg string) (AppSetup, error) {
	var out AppSetup
	err := c.get(ctx, setupPath(pkg, ""), &out)
	return out, err
}

// AppSetupResume runs the stopped step and the rest.
func (c *Client) AppSetupResume(ctx context.Context, pkg string, req AppSetupResumeRequest) (int64, error) {
	var out OperationStarted
	err := c.postJSON(ctx, setupPath(pkg, "/resume"), req, &out)
	return out.ID, err
}

// AppSetupSkip ends a stopped setup.
func (c *Client) AppSetupSkip(ctx context.Context, pkg string) (AppSetup, error) {
	var out AppSetup
	err := c.postJSON(ctx, setupPath(pkg, "/skip"), struct{}{}, &out)
	return out, err
}
