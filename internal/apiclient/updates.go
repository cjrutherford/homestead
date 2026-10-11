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

// PackageUpdate is one review row: the plan plus whether anything runs.
type PackageUpdate struct {
	Package   string     `json:"package"`
	Installed bool       `json:"installed"`
	Plan      UpdatePlan `json:"plan"`
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
	var out struct {
		ID     int64  `json:"id"`
		Detail string `json:"detail"`
	}
	body := map[string]any{"backup": backup, "db_password": dbPassword}
	if err := c.postJSON(ctx, "/api/v1/updates/"+name+"/apply", body, &out); err != nil {
		return 0, "", err
	}
	return out.ID, out.Detail, nil
}
