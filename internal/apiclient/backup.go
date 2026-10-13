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

// BackupRecipient is one backup recipient (public data only; S271).
type BackupRecipient struct {
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	Actor       string `json:"actor"`
}

// BackupRecipients is GET /api/v1/backups/recipients.
type BackupRecipients struct {
	Recipients []BackupRecipient `json:"recipients"`
	OwnerHeld  bool              `json:"owner_held"`
	// ApplianceCreated is false until the first backup or export.
	ApplianceCreated bool   `json:"appliance_created"`
	Note             string `json:"note"`
}

// BackupRecipients lists the appliance and owner recipients.
func (c *Client) BackupRecipients(ctx context.Context) (BackupRecipients, error) {
	var out BackupRecipients
	err := c.get(ctx, "/api/v1/backups/recipients", &out)
	return out, err
}

// BackupRecipientAdd adds one owner age recipient.
func (c *Client) BackupRecipientAdd(ctx context.Context, publicKey, label string) (BackupRecipient, error) {
	var out BackupRecipient
	err := c.postJSON(ctx, "/api/v1/backups/recipients", map[string]string{"public_key": publicKey, "label": label}, &out)
	return out, err
}

// BackupRecipientRemove removes one owner recipient by fingerprint.
func (c *Client) BackupRecipientRemove(ctx context.Context, fingerprint string) (BackupRecipient, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/api/v1/backups/recipients/"+url.PathEscape(fingerprint), nil)
	if err != nil {
		return BackupRecipient{}, fmt.Errorf("api: %w", err)
	}
	var out BackupRecipient
	err = c.roundTrip(req, http.StatusOK, &out)
	return out, err
}

// BackupIdentityStatus is GET /api/v1/backups/identity.
type BackupIdentityStatus struct {
	Created        bool   `json:"created"`
	Recipient      string `json:"recipient"`
	Fingerprint    string `json:"fingerprint"`
	Exported       bool   `json:"exported"`
	ExportedAt     string `json:"exported_at"`
	ExportedBy     string `json:"exported_by"`
	Acknowledged   bool   `json:"acknowledged"`
	AcknowledgedAt string `json:"acknowledged_at"`
}

// BackupIdentityAck confirms the exported recovery identity is stored
// offline; only this clears the doctor warning.
func (c *Client) BackupIdentityAck(ctx context.Context) (BackupIdentityStatus, error) {
	var out BackupIdentityStatus
	err := c.postJSON(ctx, "/api/v1/backups/identity/ack", map[string]any{}, &out)
	return out, err
}

// BackupIdentity reports the appliance recipient and export record.
func (c *Client) BackupIdentity(ctx context.Context) (BackupIdentityStatus, error) {
	var out BackupIdentityStatus
	err := c.get(ctx, "/api/v1/backups/identity", &out)
	return out, err
}

// BackupIdentityExport is the one response carrying the secret key.
type BackupIdentityExport struct {
	Identity    string `json:"identity"`
	Recipient   string `json:"recipient"`
	Fingerprint string `json:"fingerprint"`
	ExportedAt  string `json:"exported_at"`
	Actor       string `json:"actor"`
}

// BackupIdentityExport exports the recovery identity (audited, explicit).
func (c *Client) BackupIdentityExport(ctx context.Context) (BackupIdentityExport, error) {
	var out BackupIdentityExport
	err := c.postJSON(ctx, "/api/v1/backups/identity/export", map[string]bool{"confirm": true}, &out)
	return out, err
}
