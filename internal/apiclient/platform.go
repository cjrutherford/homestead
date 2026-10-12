package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// PlatformComponentUpdate is one planned component move with its
// artifact verification state (S264).
type PlatformComponentUpdate struct {
	Component    string `json:"Component"`
	Current      string `json:"Current"`
	Target       string `json:"Target"`
	Verification string `json:"Verification"`
}

// PlatformPlan is the reviewable platform change set.
type PlatformPlan struct {
	Components []PlatformComponentUpdate `json:"Components"`
	Backup     bool                      `json:"Backup"`
	BackupWhy  string                    `json:"BackupWhy"`
	Warnings   []string                  `json:"Warnings"`
}

// PlatformRelease is the latest verification record for a component.
type PlatformRelease struct {
	Component  string    `json:"component"`
	Version    string    `json:"version"`
	Counter    int64     `json:"counter"`
	State      string    `json:"state"`
	Refusal    string    `json:"refusal"`
	Detail     string    `json:"detail"`
	KeyID      string    `json:"key_id"`
	SHA256     string    `json:"sha256"`
	Override   bool      `json:"override"`
	Actor      string    `json:"actor"`
	VerifiedAt time.Time `json:"verified_at"`
	PlanError  string    `json:"plan_error"`
}

// PlatformInstalled is the recorded installed release of a component.
type PlatformInstalled struct {
	Version string `json:"Version"`
	Counter int64  `json:"Counter"`
}

// PlatformUpdates is GET /api/v1/platform/updates.
type PlatformUpdates struct {
	Running   map[string]string            `json:"running"`
	Plan      PlatformPlan                 `json:"plan"`
	Releases  []PlatformRelease            `json:"releases"`
	Refused   []PlatformRelease            `json:"refused"`
	Installed map[string]PlatformInstalled `json:"installed"`
}

// TrustKey is one trusted release-signing key.
type TrustKey struct {
	KeyID     string    `json:"key_id"`
	PublicKey string    `json:"public_key"`
	Label     string    `json:"label"`
	Source    string    `json:"source"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
	// Placeholder marks the embedded key whose private half was discarded.
	Placeholder bool `json:"placeholder"`
}

// PlatformUpdates reads the platform plan with per-release
// verification state.
func (c *Client) PlatformUpdates(ctx context.Context) (PlatformUpdates, error) {
	var out PlatformUpdates
	if err := c.get(ctx, "/api/v1/platform/updates", &out); err != nil {
		return PlatformUpdates{}, err
	}
	return out, nil
}

// PlatformVerify verifies owner-supplied local artifact files on the
// appliance. A failed verification returns a record with state
// "failed", not an error.
func (c *Client) PlatformVerify(ctx context.Context, component, artifact, checksums, signature string, allowOlder bool) (PlatformRelease, error) {
	var out PlatformRelease
	body := map[string]any{
		"component": component, "artifact": artifact, "checksums": checksums,
		"signature": signature, "allow_older": allowOlder,
	}
	if err := c.postJSON(ctx, "/api/v1/platform/updates/verify", body, &out); err != nil {
		return PlatformRelease{}, err
	}
	return out, nil
}

// TrustKeys lists trusted keys.
func (c *Client) TrustKeys(ctx context.Context) ([]TrustKey, error) {
	var out []TrustKey
	if err := c.get(ctx, "/api/v1/platform/trust-keys", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []TrustKey{}
	}
	return out, nil
}

// TrustKeyAdd trusts a minisign public key.
func (c *Client) TrustKeyAdd(ctx context.Context, publicKey, label string) (TrustKey, error) {
	var out TrustKey
	if err := c.postJSON(ctx, "/api/v1/platform/trust-keys", map[string]any{"public_key": publicKey, "label": label}, &out); err != nil {
		return TrustKey{}, err
	}
	return out, nil
}

// TrustKeyReplace swaps a trusted key for another.
func (c *Client) TrustKeyReplace(ctx context.Context, keyID, publicKey, label string) (TrustKey, error) {
	raw, err := json.Marshal(map[string]any{"public_key": publicKey, "label": label})
	if err != nil {
		return TrustKey{}, fmt.Errorf("api: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/api/v1/platform/trust-keys/"+url.PathEscape(keyID), bytes.NewReader(raw))
	if err != nil {
		return TrustKey{}, fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	var out TrustKey
	if err := c.roundTrip(req, http.StatusOK, &out); err != nil {
		return TrustKey{}, err
	}
	return out, nil
}

// TrustKeyRemove stops trusting a key.
func (c *Client) TrustKeyRemove(ctx context.Context, keyID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/api/v1/platform/trust-keys/"+url.PathEscape(keyID), nil)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	return c.roundTrip(req, http.StatusNoContent, nil)
}
