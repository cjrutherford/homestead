package apiclient

import (
	"context"

	"homestead/internal/backup"
)

// BackupPolicySkip is an installed app the nightly policy leaves out.
type BackupPolicySkip struct {
	Package string `json:"package"`
	Reason  string `json:"reason"`
}

// BackupPolicyStatus is the nightly backup policy (S297): the approved
// policy (nil before the first approval), the proposal, and its coverage.
type BackupPolicyStatus struct {
	Policy        *backup.Policy     `json:"policy,omitempty"`
	Proposed      backup.Policy      `json:"proposed"`
	Covered       []string           `json:"covered"`
	Skipped       []BackupPolicySkip `json:"skipped"`
	TimeZone      string             `json:"time_zone"`
	BackupStorage bool               `json:"backup_storage"`
}

// BackupPolicyPlan is what approving the request would store.
type BackupPolicyPlan struct {
	Policy   backup.Policy      `json:"policy"`
	Covered  []string           `json:"covered"`
	Skipped  []BackupPolicySkip `json:"skipped"`
	TimeZone string             `json:"time_zone"`
	Changes  []string           `json:"changes"`
	Digest   string             `json:"digest"`
}

// BackupPolicyRequest is a plan or apply body. Empty fields take the
// proposed defaults (02:00, keep 7, on).
type BackupPolicyRequest struct {
	TimeOfDay string `json:"time_of_day,omitempty"`
	Retain    int    `json:"retain,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

// BackupPolicy reads the nightly policy.
func (c *Client) BackupPolicy(ctx context.Context) (BackupPolicyStatus, error) {
	var out BackupPolicyStatus
	err := c.get(ctx, "/api/v1/backups/policy", &out)
	return out, err
}

// BackupPolicyPlan previews a policy; nothing changes.
func (c *Client) BackupPolicyPlan(ctx context.Context, req BackupPolicyRequest) (BackupPolicyPlan, error) {
	var out BackupPolicyPlan
	req.Digest = ""
	err := c.postJSON(ctx, "/api/v1/backups/policy/plan", req, &out)
	return out, err
}

// BackupPolicyApply stores the reviewed plan; req.Digest must match it.
func (c *Client) BackupPolicyApply(ctx context.Context, req BackupPolicyRequest) (BackupPolicyStatus, error) {
	var out BackupPolicyStatus
	err := c.postJSON(ctx, "/api/v1/backups/policy/apply", req, &out)
	return out, err
}
