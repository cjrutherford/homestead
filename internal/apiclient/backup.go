package apiclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"homestead/internal/backup"
)

// BackupDestination resolves the backup root from the backup-class
// assignment and live facts (S238). Unassigned or vanished
// destinations error instead of guessing.
func (c *Client) BackupDestination(ctx context.Context) (string, error) {
	var out struct {
		Destination string `json:"destination"`
	}
	if err := c.get(ctx, "/api/v1/backups/destination", &out); err != nil {
		return "", err
	}
	return out.Destination, nil
}

// BackupDestinationInfo is the destination plus the encryption facts
// of runs written there (S259).
type BackupDestinationInfo struct {
	Destination   string `json:"destination"`
	Encrypted     bool   `json:"encrypted"`
	FormatVersion int    `json:"format_version"`
}

// BackupDestinationInfo resolves the backup root with encryption facts.
func (c *Client) BackupDestinationInfo(ctx context.Context) (BackupDestinationInfo, error) {
	var out BackupDestinationInfo
	err := c.get(ctx, "/api/v1/backups/destination", &out)
	return out, err
}

// BackupSchedules lists persisted schedules ordered by package.
func (c *Client) BackupSchedules(ctx context.Context) ([]backup.Schedule, error) {
	var out []backup.Schedule
	if err := c.get(ctx, "/api/v1/backups/schedules", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []backup.Schedule{}
	}
	return out, nil
}

// BackupSchedule creates or replaces one package schedule.
func (c *Client) BackupSchedule(ctx context.Context, schedule backup.Schedule) (backup.Schedule, error) {
	var out backup.Schedule
	body := map[string]any{
		"package": schedule.Package, "interval_seconds": schedule.IntervalSeconds,
		"retain": schedule.Retain, "enabled": schedule.Enabled, "actor": schedule.Actor,
	}
	if err := c.postJSON(ctx, "/api/v1/backups/schedules", body, &out); err != nil {
		return backup.Schedule{}, err
	}
	return out, nil
}

// BackupUnschedule drops one package schedule, idempotent.
func (c *Client) BackupUnschedule(ctx context.Context, packageName string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/api/v1/backups/schedules/"+url.PathEscape(packageName), nil)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return c.statusError(req, res)
	}
	return nil
}
