package apiclient

import "context"

// AppNames is one app route's local names (S295).
type AppNames struct {
	Host  string   `json:"host"`
	Names []string `json:"names"`
	// Path is set for an app served by path (ADR-044).
	Path string `json:"path,omitempty"`
}

// ResolverStatus is the optional `.lan` resolver's state.
type ResolverStatus struct {
	Enabled    bool     `json:"enabled"`
	Running    bool     `json:"running"`
	Listening  []string `json:"listening"`
	Error      string   `json:"error,omitempty"`
	Names      []string `json:"names"`
	RouterHint string   `json:"router_hint"`
}

// NamesStatus lists each app's local names and the resolver state.
type NamesStatus struct {
	Resolver ResolverStatus `json:"resolver"`
	Apps     []AppNames     `json:"apps"`
}

// ResolverFirewallRule is one port 53 rule the resolver plan names.
type ResolverFirewallRule struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// ResolverPlan is what turning the resolver on or off changes.
type ResolverPlan struct {
	Enabled    bool                   `json:"enabled"`
	Changes    []string               `json:"changes"`
	Listen     []string               `json:"listen"`
	Names      []string               `json:"names"`
	Firewall   []ResolverFirewallRule `json:"firewall"`
	Warnings   []string               `json:"warnings"`
	RouterHint string                 `json:"router_hint"`
	Digest     string                 `json:"digest"`
}

// Names reads the app names and the resolver state.
func (c *Client) Names(ctx context.Context) (NamesStatus, error) {
	var out NamesStatus
	if err := c.get(ctx, "/api/v1/names", &out); err != nil {
		return NamesStatus{}, err
	}
	return out, nil
}

// ResolverPlan previews turning the resolver on or off; it changes nothing.
func (c *Client) ResolverPlan(ctx context.Context, enabled bool) (ResolverPlan, error) {
	var out ResolverPlan
	if err := c.postJSON(ctx, "/api/v1/names/resolver/plan", map[string]any{"enabled": enabled}, &out); err != nil {
		return ResolverPlan{}, err
	}
	return out, nil
}

// ResolverApply approves a reviewed plan by its digest.
func (c *Client) ResolverApply(ctx context.Context, enabled bool, digest string) (NamesStatus, error) {
	var out NamesStatus
	body := map[string]any{"enabled": enabled, "digest": digest}
	if err := c.postJSON(ctx, "/api/v1/names/resolver/apply", body, &out); err != nil {
		return NamesStatus{}, err
	}
	return out, nil
}
