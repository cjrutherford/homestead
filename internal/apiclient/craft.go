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

// CraftPlan is what a craft plan checked (S279).
type CraftPlan struct {
	App            string `json:"app"`
	Version        string `json:"version"`
	Counter        int64  `json:"counter"`
	Tag            string `json:"tag"`
	KeyID          string `json:"key_id"`
	ArtifactName   string `json:"artifact_name"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	ArtifactSize   int64  `json:"artifact_size"`
	Source         string `json:"source"`
	ContextDigest  string `json:"context_digest"`
	Override       bool   `json:"override"`
	DiskRequired   int64  `json:"disk_required"`
	Baseline       string `json:"baseline"`
}

// CraftVerdict is the latest verification or build outcome for an app.
type CraftVerdict struct {
	App        string    `json:"app"`
	Version    string    `json:"version"`
	Counter    int64     `json:"counter"`
	State      string    `json:"state"`
	Refusal    string    `json:"refusal"`
	Detail     string    `json:"detail"`
	KeyID      string    `json:"key_id"`
	Override   bool      `json:"override"`
	Actor      string    `json:"actor"`
	VerifiedAt time.Time `json:"verified_at"`
}

// CraftImage is a locally built image with its live comparison.
type CraftImage struct {
	App            string    `json:"app"`
	Version        string    `json:"version"`
	Tag            string    `json:"tag"`
	ImageID        string    `json:"image_id"`
	Counter        int64     `json:"counter"`
	KeyID          string    `json:"key_id"`
	ArtifactSHA256 string    `json:"artifact_sha256"`
	ContextDigest  string    `json:"context_digest"`
	BuiltAt        time.Time `json:"built_at"`
	Present        bool      `json:"present"`
	LiveID         string    `json:"live_id"`
	Match          bool      `json:"match"`
}

// CraftStatus is GET /api/v1/craft.
type CraftStatus struct {
	Images        []CraftImage                 `json:"images"`
	Verdicts      []CraftVerdict               `json:"verdicts"`
	Baselines     map[string]PlatformInstalled `json:"baselines"`
	ManifestPath  string                       `json:"manifest_path"`
	SignaturePath string                       `json:"signature_path"`
	ContextDir    string                       `json:"context_dir"`
}

// CraftApp is one installed craft app on the start page (S281).
type CraftApp struct {
	Name      string `json:"name"`
	Title     string `json:"title"`
	Version   string `json:"version"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	Host      string `json:"host"`
	TLS       string `json:"tls"`
	Exposure  string `json:"exposure"`
	URLReason string `json:"url_reason"`
}

// CraftApps reads the installed craft apps with their local URLs.
func (c *Client) CraftApps(ctx context.Context) ([]CraftApp, error) {
	var out struct {
		Apps []CraftApp `json:"apps"`
	}
	if err := c.get(ctx, "/api/v1/craft/apps", &out); err != nil {
		return nil, err
	}
	return out.Apps, nil
}

// CraftRequest names the app and, optionally, the manifest, signature and
// an owner-supplied local artifact (offline use). AllowOlder is the
// explicit, audited counter override.
type CraftRequest struct {
	App        string `json:"app"`
	Version    string `json:"version,omitempty"`
	Manifest   string `json:"manifest,omitempty"`
	Signature  string `json:"signature,omitempty"`
	Artifact   string `json:"artifact,omitempty"`
	AllowOlder bool   `json:"allow_older,omitempty"`
}

// CraftPlanResult is the plan with its saved verdict.
type CraftPlanResult struct {
	Plan    CraftPlan    `json:"plan"`
	Verdict CraftVerdict `json:"verdict"`
}

// CraftStatus reads recorded builds, verdicts and baselines.
func (c *Client) CraftStatus(ctx context.Context) (CraftStatus, error) {
	var out CraftStatus
	if err := c.get(ctx, "/api/v1/craft", &out); err != nil {
		return CraftStatus{}, err
	}
	return out, nil
}

// CraftPlan verifies a craft manifest (signature, counter, local artifact,
// build-context digest, disk space) without building. A refusal returns a
// result whose verdict state is "failed", not an error.
func (c *Client) CraftPlan(ctx context.Context, req CraftRequest) (CraftPlanResult, error) {
	var out CraftPlanResult
	if err := c.postJSON(ctx, "/api/v1/craft/plan", req, &out); err != nil {
		return CraftPlanResult{}, err
	}
	return out, nil
}

// CraftBuild starts the verify-then-build operation and returns its id.
func (c *Client) CraftBuild(ctx context.Context, req CraftRequest) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.postJSON(ctx, "/api/v1/craft/build", req, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// CraftTrustKeys lists trusted craft signing keys.
func (c *Client) CraftTrustKeys(ctx context.Context) ([]TrustKey, error) {
	var out []TrustKey
	if err := c.get(ctx, "/api/v1/craft/trust-keys", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []TrustKey{}
	}
	return out, nil
}

// CraftTrustKeyAdd trusts a craft signing key.
func (c *Client) CraftTrustKeyAdd(ctx context.Context, publicKey, label string) (TrustKey, error) {
	var out TrustKey
	if err := c.postJSON(ctx, "/api/v1/craft/trust-keys", map[string]any{"public_key": publicKey, "label": label}, &out); err != nil {
		return TrustKey{}, err
	}
	return out, nil
}

// CraftTrustKeyReplace swaps a trusted craft key for another.
func (c *Client) CraftTrustKeyReplace(ctx context.Context, keyID, publicKey, label string) (TrustKey, error) {
	raw, err := json.Marshal(map[string]any{"public_key": publicKey, "label": label})
	if err != nil {
		return TrustKey{}, fmt.Errorf("api: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/api/v1/craft/trust-keys/"+url.PathEscape(keyID), bytes.NewReader(raw))
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

// CraftTrustKeyRemove stops trusting a craft key.
func (c *Client) CraftTrustKeyRemove(ctx context.Context, keyID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/api/v1/craft/trust-keys/"+url.PathEscape(keyID), nil)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	return c.roundTrip(req, http.StatusNoContent, nil)
}
