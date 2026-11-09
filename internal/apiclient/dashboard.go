package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// DashboardAddress is the dashboard's local network address (S289,
// ADR-035): the owner-set name, the appliance's mDNS name, and the URLs
// with the .local one first.
type DashboardAddress struct {
	Name       string   `json:"name"`
	Default    string   `json:"default"`
	Configured bool     `json:"configured"`
	Exposure   string   `json:"exposure"`
	MDNSName   string   `json:"mdns_name"`
	Names      []string `json:"names"`
	URLs       []string `json:"urls"`
	RootPath   string   `json:"root_path"`
}

// DashboardAddress reads the dashboard address.
func (c *Client) DashboardAddress(ctx context.Context) (DashboardAddress, error) {
	var out DashboardAddress
	if err := c.get(ctx, "/api/v1/dashboard/address", &out); err != nil {
		return DashboardAddress{}, err
	}
	return out, nil
}

// SetDashboardAddress sets the local name (and exposure, local or
// overlay), returning the operation id. Public exposure is refused by the
// daemon: the tunnel is the public path.
func (c *Client) SetDashboardAddress(ctx context.Context, name, exposure string) (int64, error) {
	raw, err := json.Marshal(map[string]string{"name": name, "exposure": exposure})
	if err != nil {
		return 0, fmt.Errorf("api: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/api/v1/dashboard/address", bytes.NewReader(raw))
	if err != nil {
		return 0, fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	var out OperationStarted
	if err := c.roundTrip(req, http.StatusOK, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
