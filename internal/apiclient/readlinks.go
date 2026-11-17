package apiclient

import (
	"context"
	"net/url"
)

// ReadLinksStatus is whether an Everything app's read and view links are
// open on the public name (S433, ADR-058).
type ReadLinksStatus struct {
	App       string   `json:"app"`
	Title     string   `json:"title"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
	Exposed   bool     `json:"exposed"`
	Origin    string   `json:"origin,omitempty"`
	Patterns  []string `json:"patterns,omitempty"`
	Links     int      `json:"links"`
	SignIn    bool     `json:"sign_in"`
}

// ReadLinksPlan is the reviewed change; nothing changes until approved.
type ReadLinksPlan struct {
	App           string   `json:"app"`
	Title         string   `json:"title"`
	Action        string   `json:"action"`
	Origin        string   `json:"origin"`
	Route         string   `json:"route"`
	Patterns      []string `json:"patterns"`
	Refused       []string `json:"refused"`
	Links         int      `json:"links"`
	ExpiryCapDays int      `json:"expiry_cap_days"`
	Warnings      []string `json:"warnings"`
	Digest        string   `json:"digest"`
}

// ReadLinksRequest is a plan or approve body.
type ReadLinksRequest struct {
	Enabled *bool  `json:"enabled"`
	Digest  string `json:"digest,omitempty"`
}

func readLinksPath(app, suffix string) string {
	return "/api/v1/apps/" + url.PathEscape(app) + "/read-links" + suffix
}

// ReadLinks reads whether the app's read links are on the public name.
func (c *Client) ReadLinks(ctx context.Context, app string) (ReadLinksStatus, error) {
	var out ReadLinksStatus
	err := c.get(ctx, readLinksPath(app, ""), &out)
	return out, err
}

// ReadLinksPlan previews exposing or removing the public paths.
func (c *Client) ReadLinksPlan(ctx context.Context, app string, req ReadLinksRequest) (ReadLinksPlan, error) {
	var out ReadLinksPlan
	err := c.postJSON(ctx, readLinksPath(app, "/plan"), req, &out)
	return out, err
}

// ReadLinksApprove starts the operation for the reviewed plan.
func (c *Client) ReadLinksApprove(ctx context.Context, app string, req ReadLinksRequest) (int64, error) {
	var out OperationStarted
	err := c.postJSON(ctx, readLinksPath(app, "/approve"), req, &out)
	return out.ID, err
}
