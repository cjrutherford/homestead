package apiclient

import (
	"context"
)

// ServiceUpdate is one service's version move in review.
type ServiceUpdate struct {
	Service string `json:"Service"`
	Current string `json:"Current"`
	Target  string `json:"Target"`
	Source  string `json:"Source"`
}

// UpdatePlan is the reviewable change set for one package.
type UpdatePlan struct {
	Package   string          `json:"Package"`
	Services  []ServiceUpdate `json:"Services"`
	Backup    bool            `json:"Backup"`
	BackupWhy string          `json:"BackupWhy"`
	Affected  []string        `json:"Affected"`
	Warnings  []string        `json:"Warnings"`
}

// CraftUpdate is the build state of one craft move in an update plan.
type CraftUpdate struct {
	Service string        `json:"service"`
	App     string        `json:"app"`
	Version string        `json:"version"`
	Tag     string        `json:"tag"`
	Built   bool          `json:"built"`
	Verdict *CraftVerdict `json:"verdict,omitempty"`
}

// PackageUpdate is one review row: the plan plus whether anything runs.
type PackageUpdate struct {
	Package   string     `json:"package"`
	Installed bool       `json:"installed"`
	Plan      UpdatePlan `json:"plan"`
	// Craft is the build status of any locally built craft image the
	// plan moves (S279).
	Craft []CraftUpdate `json:"craft,omitempty"`
}

// UpdatesList returns per-package review rows (S243): drift plans
// against running containers with registry maxima.
func (c *Client) UpdatesList(ctx context.Context) ([]PackageUpdate, error) {
	var out []PackageUpdate
	if err := c.get(ctx, "/api/v1/updates", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []PackageUpdate{}
	}
	return out, nil
}

// ApplyUpdate starts an approved update, returning the operation id
// for progress streams. Backup-gated plans refuse without backup
// requested; current packages report id zero with a detail string.
func (c *Client) ApplyUpdate(ctx context.Context, name string, backup bool, dbPassword string) (int64, string, error) {
	return c.ApplyUpdateCraft(ctx, name, backup, dbPassword, nil)
}

// CraftApply carries the optional inputs of an update that rebuilds a
// locally built craft image (S279): a manifest and signature other than
// the shipped ones, an owner-supplied artifact (offline use), and the
// audited counter override.
type CraftApply struct {
	Manifest   string `json:"manifest,omitempty"`
	Signature  string `json:"signature,omitempty"`
	Artifact   string `json:"artifact,omitempty"`
	AllowOlder bool   `json:"allow_older,omitempty"`
}

// ApplyUpdateCraft is ApplyUpdate with craft inputs (nil for none).
func (c *Client) ApplyUpdateCraft(ctx context.Context, name string, backup bool, dbPassword string, craft *CraftApply) (int64, string, error) {
	var out struct {
		ID     int64  `json:"id"`
		Detail string `json:"detail"`
	}
	body := map[string]any{"backup": backup, "db_password": dbPassword}
	if craft != nil {
		body["craft"] = craft
	}
	if err := c.postJSON(ctx, "/api/v1/updates/"+name+"/apply", body, &out); err != nil {
		return 0, "", err
	}
	return out.ID, out.Detail, nil
}
