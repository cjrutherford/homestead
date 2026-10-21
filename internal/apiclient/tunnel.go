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
	Proposed  TunnelProposal       `json:"proposed"`
	Discovery string               `json:"discovery_error,omitempty"`
}

// TunnelProposal is the default plan: public names from the setup domain
// and whether sign-in with Authentik is linked, which the dashboard name
// needs (ADR-037 §4).
type TunnelProposal struct {
	DashboardHostname string `json:"dashboard_hostname"`
	AuthHostname      string `json:"auth_hostname"`
	SignInLinked      bool   `json:"sign_in_linked"`
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

// TunnelRequest names the tunnel and, optionally, the public names of the
// dashboard and Authentik; empty names default to homestead.<domain> and
// auth.<domain>.
type TunnelRequest struct {
	Name              string `json:"name"`
	DashboardHostname string `json:"dashboard_hostname,omitempty"`
	AuthHostname      string `json:"auth_hostname,omitempty"`
}

// TunnelPlan is the reviewed tunnel change (ADR-037 §4): the public names,
// the DNS records it creates or replaces, the names it leaves out and why,
// warnings, and the digest that approves exactly this plan. It never
// carries the account or zone IDs.
type TunnelPlan struct {
	Name              string             `json:"name"`
	DashboardHostname string             `json:"dashboard_hostname"`
	AuthHostname      string             `json:"auth_hostname"`
	Zone              string             `json:"zone"`
	Ingress           []TunnelPlanRoute  `json:"ingress"`
	DNS               []TunnelPlanChange `json:"dns"`
	Omitted           []TunnelOmission   `json:"omitted"`
	SignIn            string             `json:"sign_in,omitempty"`
	Warnings          []string           `json:"warnings"`
	Digest            string             `json:"digest"`
}

// TunnelPlanRoute is one public name, its role, and the local edge route
// that serves it through the edge's tunnel entry.
type TunnelPlanRoute struct {
	Hostname string `json:"hostname"`
	Role     string `json:"role"`
	Route    string `json:"route"`
	Service  string `json:"service"`
}

// TunnelOmission is a proposed public name the plan leaves out.
type TunnelOmission struct {
	Hostname string `json:"hostname"`
	Role     string `json:"role"`
	Reason   string `json:"reason"`
}

// TunnelPlanChange is one DNS change: create, or replace the listed
// records.
type TunnelPlanChange struct {
	Name     string             `json:"name"`
	Type     string             `json:"type"`
	Content  string             `json:"content"`
	Proxied  bool               `json:"proxied"`
	Action   string             `json:"action"`
	Replaces []router.DNSRecord `json:"replaces"`
}

// TunnelPlan reads Cloudflare and returns the plan; it changes nothing.
func (c *Client) TunnelPlan(ctx context.Context, req TunnelRequest) (TunnelPlan, error) {
	var out TunnelPlan
	if err := c.postJSON(ctx, "/api/v1/tunnel/plan", req, &out); err != nil {
		return TunnelPlan{}, err
	}
	return out, nil
}

// TunnelSetup approves a reviewed plan by its digest: the daemon plans
// again, refuses if anything changed, then creates the tunnel, its DNS
// records, the edge's public names and the connector, returning the
// operation id.
func (c *Client) TunnelSetup(ctx context.Context, req TunnelRequest, digest string) (int64, error) {
	var out OperationStarted
	body := struct {
		TunnelRequest
		Digest string `json:"digest"`
	}{req, digest}
	if err := c.postJSON(ctx, "/api/v1/tunnel/setup", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// TunnelTeardown stops the connector, deletes the provider tunnel and the
// DNS records Homestead created for it, and forgets its routes, returning
// the operation id. The account is the one stored at setup.
func (c *Client) TunnelTeardown(ctx context.Context, id string) (int64, error) {
	var out OperationStarted
	path := "/api/v1/tunnel/tunnels/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+path, nil)
	if err != nil {
		return 0, fmt.Errorf("api: %w", err)
	}
	if err := c.roundTrip(req, http.StatusOK, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
