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
	Digest   string             `json:"digest"`
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
