package apiclient

import (
	"context"

	"homestead/internal/docker"
)

// EdgeDeploy deploys the Caddy edge proxy, returning the operation
// id for progress streams.
func (c *Client) EdgeDeploy(ctx context.Context) (int64, error) {
	var out OperationStarted
	if err := c.postJSON(ctx, "/api/v1/edge/deploy", map[string]any{}, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// EdgeReconcile replaces the proxy route table, returning the
// operation id for progress streams.
func (c *Client) EdgeReconcile(ctx context.Context, routes []docker.Route) (int64, error) {
	var out OperationStarted
	body := map[string]any{"routes": routes}
	if routes == nil {
		body["routes"] = []docker.Route{}
	}
	if err := c.postJSON(ctx, "/api/v1/edge/routes", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
