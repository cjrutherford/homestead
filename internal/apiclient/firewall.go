package apiclient

import (
	"context"
	"fmt"
)

// FirewallStatus is the setup-state report (S056, extended S057).
type FirewallStatus struct {
	NFTAvailable      bool     `json:"nft_available"`
	RulesetAccessible bool     `json:"ruleset_accessible"`
	Managed           bool     `json:"managed"`
	RuleCount         int      `json:"rule_count"`
	DaemonPort        int      `json:"daemon_port"`
	LocalSubnets      []string `json:"local_subnets"`
	Mode              string   `json:"mode"`
	ExternalManagers  []string `json:"external_managers"`
}

// FirewallChange is one reviewed plan step.
type FirewallChange struct {
	Action string `json:"action"`
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

// FirewallPlan is the reviewed diff plus the script to apply.
type FirewallPlan struct {
	InSync  bool             `json:"in_sync"`
	Changes []FirewallChange `json:"changes"`
	Script  string           `json:"script"`
}

// FirewallStatus returns detection plus LAN identification.
func (c *Client) FirewallStatus(ctx context.Context) (FirewallStatus, error) {
	var status FirewallStatus
	if err := c.get(ctx, "/api/v1/firewall/status", &status); err != nil {
		return FirewallStatus{}, err
	}
	return status, nil
}

// FirewallPlan returns the reviewed diff for the daemon port.
func (c *Client) FirewallPlan(ctx context.Context) (FirewallPlan, error) {
	var plan FirewallPlan
	if err := c.get(ctx, "/api/v1/firewall/plan", &plan); err != nil {
		return FirewallPlan{}, err
	}
	return plan, nil
}

// FirewallApply commits the planned ruleset after the server-side guard.
// Guard failures answer 409; without privilege, 503.
func (c *Client) FirewallApply(ctx context.Context) error {
	req, err := c.post(ctx, "/api/v1/firewall/apply", nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return c.statusError(req, res)
	}
	return nil
}
