package apiclient

import (
	"context"
	"net/url"
)

// AppPublicRequest is a public app name plan or apply body (S290).
type AppPublicRequest struct {
	Enabled  *bool  `json:"enabled"`
	Hostname string `json:"hostname,omitempty"`
	Route    string `json:"route,omitempty"`
	Digest   string `json:"digest,omitempty"`
	// BeforeInstall plans a name for an app the same install creates
	// (S313); the plan is applied without it once the app is installed.
	// Managed installs request the anticipated post-sign-in state.
	BeforeInstall    bool `json:"before_install,omitempty"`
	AnticipateSignIn bool `json:"anticipate_sign_in,omitempty"`
}

// AppPublicStatus is an app's public names on the tunnel.
type AppPublicStatus struct {
	App       string `json:"app"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	TunnelID  string `json:"tunnel_id,omitempty"`
	Tunnel    string `json:"tunnel,omitempty"`
	Routes    []struct {
		Route  string   `json:"route"`
		Public []string `json:"public"`
		// Path and URL are set for an app served by path (ADR-044).
		Path string `json:"path,omitempty"`
		URL  string `json:"url,omitempty"`
	} `json:"routes"`
	Proposed string `json:"proposed,omitempty"`
	// ProposedURL is a path app's public address (ADR-044).
	ProposedURL string         `json:"proposed_url,omitempty"`
	SignIn      bool           `json:"sign_in"`
	Coverage    []NameCoverage `json:"coverage"`
}

// AppPublicPlan is one public name added to or removed from an app.
type AppPublicPlan struct {
	App      string `json:"app"`
	Action   string `json:"action"`
	Hostname string `json:"hostname"`
	Route    string `json:"route"`
	// Path and URL are set for an app served by path (ADR-044): it is
	// published on the dashboard's public name, with no DNS change.
	Path     string             `json:"path,omitempty"`
	URL      string             `json:"url"`
	Tunnel   string             `json:"tunnel"`
	TunnelID string             `json:"tunnel_id"`
	Zone     string             `json:"zone"`
	DNS      []TunnelPlanChange `json:"dns"`
	Before   []string           `json:"connector_before"`
	After    []string           `json:"connector_after"`
	SignIn   string             `json:"sign_in,omitempty"`
	Coverage []NameCoverage     `json:"coverage"`
	Warnings []string           `json:"warnings"`
	// AppSettings are the app's own host settings the plan changes (S313).
	AppSettings []string `json:"app_settings"`
	Digest      string   `json:"digest"`
}

func publicPath(pkg, suffix string) string {
	return "/api/v1/catalog/" + url.PathEscape(pkg) + "/public" + suffix
}

// AppPublic reads an app's public names.
func (c *Client) AppPublic(ctx context.Context, pkg string) (AppPublicStatus, error) {
	var out AppPublicStatus
	err := c.get(ctx, publicPath(pkg, ""), &out)
	return out, err
}

// AppPublicPlan previews adding or removing a public name; nothing changes.
func (c *Client) AppPublicPlan(ctx context.Context, pkg string, req AppPublicRequest) (AppPublicPlan, error) {
	var out AppPublicPlan
	err := c.postJSON(ctx, publicPath(pkg, "/plan"), req, &out)
	return out, err
}

// AppPublicApply starts the operation for the reviewed plan.
func (c *Client) AppPublicApply(ctx context.Context, pkg string, req AppPublicRequest) (int64, error) {
	var out OperationStarted
	err := c.postJSON(ctx, publicPath(pkg, "/apply"), req, &out)
	return out.ID, err
}

// ReachAddress is one address of an app and whether it answers (S313).
type ReachAddress struct {
	Address string `json:"address"`
	Host    string `json:"host"`
	Kind    string `json:"kind"`
	Answers bool   `json:"answers"`
	Reason  string `json:"reason,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// AppReachStatus is the appliance's check of each address of an app.
type AppReachStatus struct {
	App       string         `json:"app"`
	Resolver  bool           `json:"resolver"`
	Addresses []ReachAddress `json:"addresses"`
}

// AppReach checks from the appliance whether each address answers. It
// is advisory: nothing changes.
func (c *Client) AppReach(ctx context.Context, pkg string) (AppReachStatus, error) {
	var out AppReachStatus
	err := c.get(ctx, "/api/v1/catalog/"+url.PathEscape(pkg)+"/reach", &out)
	return out, err
}

// AppMoveStatus is whether an installed app can move to its path under
// the appliance's names (S315, ADR-044 §5).
type AppMoveStatus struct {
	App     string   `json:"app"`
	Offered bool     `json:"offered"`
	Reason  string   `json:"reason,omitempty"`
	From    string   `json:"from,omitempty"`
	Path    string   `json:"path,omitempty"`
	To      []string `json:"to"`
}

// AppMovePlan is the reviewed move of an app to its path.
type AppMovePlan struct {
	App         string   `json:"app"`
	Route       string   `json:"route"`
	From        string   `json:"from"`
	Path        string   `json:"path"`
	To          []string `json:"to"`
	StripPrefix bool     `json:"strip_prefix"`
	Redirects   []string `json:"redirects"`
	AppSettings []string `json:"app_settings"`
	SignIn      []string `json:"sign_in"`
	Warnings    []string `json:"warnings"`
	Digest      string   `json:"digest"`
}

func movePath(pkg, suffix string) string {
	return "/api/v1/catalog/" + url.PathEscape(pkg) + "/move" + suffix
}

// AppMove reads whether the move is offered.
func (c *Client) AppMove(ctx context.Context, pkg string) (AppMoveStatus, error) {
	var out AppMoveStatus
	err := c.get(ctx, movePath(pkg, ""), &out)
	return out, err
}

// AppMovePlan previews the move; nothing changes.
func (c *Client) AppMovePlan(ctx context.Context, pkg string) (AppMovePlan, error) {
	var out AppMovePlan
	err := c.postJSON(ctx, movePath(pkg, "/plan"), struct{}{}, &out)
	return out, err
}

// AppMoveApply starts the move the owner approved by digest.
func (c *Client) AppMoveApply(ctx context.Context, pkg, digest string) (int64, error) {
	var out OperationStarted
	err := c.postJSON(ctx, movePath(pkg, "/apply"), map[string]string{"digest": digest}, &out)
	return out.ID, err
}
