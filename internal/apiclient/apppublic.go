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
	BeforeInstall bool `json:"before_install,omitempty"`
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
	} `json:"routes"`
	Proposed string         `json:"proposed,omitempty"`
	SignIn   bool           `json:"sign_in"`
	Coverage []NameCoverage `json:"coverage"`
}

// AppPublicPlan is one public name added to or removed from an app.
type AppPublicPlan struct {
	App      string             `json:"app"`
	Action   string             `json:"action"`
	Hostname string             `json:"hostname"`
	Route    string             `json:"route"`
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
