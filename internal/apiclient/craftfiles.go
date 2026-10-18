package apiclient

import (
	"context"

	"homestead/internal/craftfiles"
)

// CraftFilesStatus reads the Nextcloud link and per-app state (S254). It
// never carries the app password.
func (c *Client) CraftFilesStatus(ctx context.Context) (craftfiles.Status, error) {
	var out craftfiles.Status
	err := c.get(ctx, "/api/v1/craft/files", &out)
	return out, err
}

// CraftFilesPlan asks for a link, rotate or unlink plan.
func (c *Client) CraftFilesPlan(ctx context.Context, req craftfiles.PlanRequest) (craftfiles.Plan, error) {
	var out craftfiles.Plan
	err := c.postJSON(ctx, "/api/v1/craft/files/plan", req, &out)
	return out, err
}

// CraftFilesApprove approves a plan. The secrets travel in the request
// body only and are never returned.
func (c *Client) CraftFilesApprove(ctx context.Context, req craftfiles.ApproveRequest) (craftfiles.Status, error) {
	var out craftfiles.Status
	err := c.postJSON(ctx, "/api/v1/craft/files/approve", req, &out)
	return out, err
}

// CraftFilesLocalOnly sets or clears the per-app browser-local opt-out.
func (c *Client) CraftFilesLocalOnly(ctx context.Context, app string, localOnly bool) (craftfiles.Status, error) {
	var out craftfiles.Status
	err := c.postJSON(ctx, "/api/v1/craft/files/local-only", map[string]any{"app": app, "local_only": localOnly}, &out)
	return out, err
}

// CraftFilesLaunch mints a one-time launch URL for a craft app.
func (c *Client) CraftFilesLaunch(ctx context.Context, app string) (craftfiles.LaunchResult, error) {
	var out craftfiles.LaunchResult
	err := c.postJSON(ctx, "/api/v1/craft/files/launch", map[string]string{"app": app}, &out)
	return out, err
}
