package apiclient

import (
	"context"

	"homestead/internal/catalog"
)

// DataMigrations is the detection response: per-app placement plus the
// old volumes migrations left behind.
type DataMigrations struct {
	Apps       []catalog.AppDataStatus   `json:"apps"`
	OldVolumes []catalog.OldVolumeRecord `json:"old_volumes"`
}

// BackupAvailability says whether the pre-migration backup can run.
type BackupAvailability struct {
	Possible    bool   `json:"possible"`
	Destination string `json:"destination"`
	Reason      string `json:"reason"`
}

// DataMigrationPlan is the reviewable migration plan with its backup
// verdict.
type DataMigrationPlan struct {
	catalog.MigrationPlan
	Backup BackupAvailability `json:"backup"`
}

// DataMigrationStatus lists detection results (S277).
func (c *Client) DataMigrationStatus(ctx context.Context) (DataMigrations, error) {
	var out DataMigrations
	if err := c.get(ctx, "/api/v1/data-migrations", &out); err != nil {
		return DataMigrations{}, err
	}
	return out, nil
}

// DataMigrationPlan previews one package's migration.
func (c *Client) DataMigrationPlan(ctx context.Context, name string) (DataMigrationPlan, error) {
	var out DataMigrationPlan
	if err := c.postJSON(ctx, "/api/v1/data-migrations/"+name+"/plan", map[string]any{}, &out); err != nil {
		return DataMigrationPlan{}, err
	}
	return out, nil
}

// DataMigrationApprove approves the quoted plan digest and starts the
// migration, returning the operation id.
func (c *Client) DataMigrationApprove(ctx context.Context, name, digest string, acknowledgeNoBackup bool) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	body := map[string]any{"digest": digest, "acknowledge_no_backup": acknowledgeNoBackup}
	if err := c.postJSON(ctx, "/api/v1/data-migrations/"+name+"/approve", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// DataMigrationRemoveVolumes removes named old volumes of a migrated
// package, returning the operation id.
func (c *Client) DataMigrationRemoveVolumes(ctx context.Context, name string, volumes []string) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.postJSON(ctx, "/api/v1/data-migrations/"+name+"/remove-volumes", map[string]any{"volumes": volumes}, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
