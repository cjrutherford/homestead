package apiclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"homestead/internal/router"
)

// TunnelStatus is discovery plus tunnels plus connector state.
type TunnelStatus struct {
	Preferred string               `json:"preferred"`
	Gateway   string               `json:"gateway"`
	Interface string               `json:"interface"`
	Protocols []string             `json:"protocols"`
	Tunnels   []TunnelEntry        `json:"tunnels"`
	Routes    []router.TunnelRoute `json:"routes"`
	Connector TunnelConnector      `json:"connector"`
	Accounts  int                  `json:"accounts"`
	Discovery string               `json:"discovery_error,omitempty"`
}

// TunnelEntry is one recorded tunnel with provider health.
type TunnelEntry struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Status      string                    `json:"status"`
	Connections []router.TunnelConnection `json:"connections"`
}

// TunnelConnector reports the local connector binary and service.
type TunnelConnector struct {
	Installed bool `json:"installed"`
	Active    bool `json:"active"`
}

// TunnelStatus reports the preferred remote-access path, recorded
// tunnels with health, routes, and connector state.
func (c *Client) TunnelStatus(ctx context.Context) (TunnelStatus, error) {
	var out TunnelStatus
	if err := c.get(ctx, "/api/v1/tunnel/status", &out); err != nil {
		return TunnelStatus{}, err
	}
	if out.Protocols == nil {
		out.Protocols = []string{}
	}
	if out.Tunnels == nil {
		out.Tunnels = []TunnelEntry{}
	}
	if out.Routes == nil {
		out.Routes = []router.TunnelRoute{}
	}
	return out, nil
}

// TunnelSetup seals the API token server-side, creates the tunnel,
// enables its connector, and records the route, returning the
// operation id for progress streams.
func (c *Client) TunnelSetup(ctx context.Context, name, hostname, service, apiToken, accountID string) (int64, error) {
	var out OperationStarted
	body := map[string]any{
		"name": name, "hostname": hostname, "service": service,
		"api_token": apiToken, "account_id": accountID,
	}
	if err := c.postJSON(ctx, "/api/v1/tunnel/setup", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// TunnelTeardown deletes the provider tunnel, removes its connector,
// and forgets its routes, returning the operation id.
func (c *Client) TunnelTeardown(ctx context.Context, id, accountID string) (int64, error) {
	var out OperationStarted
	path := "/api/v1/tunnel/tunnels/" + url.PathEscape(id) + "?account_id=" + url.QueryEscape(accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+path, nil)
	if err != nil {
		return 0, fmt.Errorf("api: %w", err)
	}
	if err := c.roundTrip(req, http.StatusOK, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
