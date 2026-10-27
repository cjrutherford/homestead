package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"homestead/internal/docker"
)

// CatalogPackage is one browsable package summary.
type CatalogPackage struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Title    string `json:"title"`
	Blurb    string `json:"blurb"`
	Category string `json:"category"`
	Icon     string `json:"icon"`
}

// CatalogService is one manifest service as served.
type CatalogService struct {
	Name    string `json:"Name"`
	Image   string `json:"Image"`
	Exposes string `json:"Exposes"`
}

// CatalogManifest is the curated manifest detail the CLI reviews.
type CatalogManifest struct {
	Name         string           `json:"Name"`
	Version      string           `json:"Version"`
	Title        string           `json:"Title"`
	Blurb        string           `json:"Blurb"`
	Description  string           `json:"Description"`
	Category     string           `json:"Category"`
	Services     []CatalogService `json:"Services"`
	Requirements struct {
		MemoryMB uint64 `json:"MemoryMB"`
		Ports    []int  `json:"Ports"`
	} `json:"Requirements"`
	BackupPaths []string `json:"BackupPaths"`
	// Browser lists advisory client capabilities (S252).
	Browser []BrowserAdvisory `json:"Browser"`
}

// BrowserAdvisory is one non-blocking browser capability warning with its
// plain-language reason (S252).
type BrowserAdvisory struct {
	Capability string `json:"Capability"`
	// Label is the API-supplied plain-language name (single source: Go).
	Label  string `json:"Label"`
	Reason string `json:"Reason"`
}

// InstallPlanReport is the reviewed change set subset the CLI prints:
// counts and names, never full documents (plan files carry those).
type InstallPlanReport struct {
	Package      string `json:"Package"`
	Requirements struct {
		Passed bool `json:"Passed"`
	} `json:"Requirements"`
	// Routes are the planned edge routes, whole, so the CLI can add them
	// to the edge table after the install (S313).
	Routes []docker.Route `json:"Routes"`
	// Path and Addresses are where the app answers locally (ADR-044):
	// its path under each of the appliance's names, or its own names.
	Path      string   `json:"path,omitempty"`
	Addresses []string `json:"addresses"`
	DNS       []struct {
		Name  string `json:"Name"`
		Type  string `json:"Type"`
		Value string `json:"Value"`
	} `json:"DNS"`
	TLS []struct {
		Host   string `json:"Host"`
		Issuer string `json:"Issuer"`
	} `json:"TLS"`
	Firewall []struct {
		Service struct {
			Name string `json:"Name"`
		} `json:"Service"`
	} `json:"Firewall"`
	Directories []struct {
		Mountpoint string `json:"Mountpoint"`
	} `json:"Directories"`
	Mounts []struct {
		Service   string `json:"Service"`
		Target    string `json:"Target"`
		Source    string `json:"Source"`
		Class     string `json:"Class"`
		Database  bool   `json:"Database"`
		Ephemeral bool   `json:"Ephemeral"`
		ReadOnly  bool   `json:"ReadOnly"`
		Declared  bool   `json:"Declared"`
	} `json:"Mounts"`
	BrowserAdvisories []BrowserAdvisory `json:"BrowserAdvisories"`
	Notes             []string          `json:"Notes"`
	Actions           []string          `json:"Actions"`
	// Digest binds the install to this plan (S313); install with it in
	// the body answers 409 when the plan changed.
	Digest string `json:"digest"`
}

// OperationStarted carries the started operation id.
type OperationStarted struct {
	ID int64 `json:"id"`
}

// CatalogPackages lists curated packages.
func (c *Client) CatalogPackages(ctx context.Context) ([]CatalogPackage, error) {
	var out []CatalogPackage
	if err := c.get(ctx, "/api/v1/catalog", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CatalogPackage fetches one manifest.
func (c *Client) CatalogPackage(ctx context.Context, name string) (CatalogManifest, error) {
	var out CatalogManifest
	if err := c.get(ctx, "/api/v1/catalog/"+name, &out); err != nil {
		return CatalogManifest{}, err
	}
	return out, nil
}

// postJSON posts a JSON document and decodes the response.
func (c *Client) postJSON(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("api: encode: %w", err)
	}
	req, err := c.post(ctx, path, raw)
	if err != nil {
		return err
	}
	return c.roundTrip(req, http.StatusOK, out)
}

// PlanInstall reviews an install body without executing.
func (c *Client) PlanInstall(ctx context.Context, name string, body map[string]any) (InstallPlanReport, error) {
	var out InstallPlanReport
	if err := c.postJSON(ctx, "/api/v1/catalog/"+name+"/plan", body, &out); err != nil {
		return InstallPlanReport{}, err
	}
	return out, nil
}

// InstallPackage executes an approved install body, returning the
// operation id for progress streams.
func (c *Client) InstallPackage(ctx context.Context, name string, body map[string]any) (int64, error) {
	var out OperationStarted
	if err := c.postJSON(ctx, "/api/v1/catalog/"+name+"/install", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// UninstallPackage removes an installed package, returning the
// operation id for progress streams. Data survives unless the body
// asks for explicit roots.
func (c *Client) UninstallPackage(ctx context.Context, name string, body map[string]any) (int64, error) {
	var out OperationStarted
	if err := c.postJSON(ctx, "/api/v1/catalog/"+name+"/uninstall", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// ConfigurePackageRotating applies variable changes and rotates the
// named secrets in one configure operation.
func (c *Client) ConfigurePackageRotating(ctx context.Context, name string, changes []map[string]any, rotate []string) (int64, error) {
	var out OperationStarted
	body := map[string]any{"changes": changes, "rotate_secrets": rotate}
	if err := c.postJSON(ctx, "/api/v1/catalog/"+name+"/configure", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// ConfigurePackage applies service variable changes, returning the
// operation id for progress streams.
func (c *Client) ConfigurePackage(ctx context.Context, name string, changes []map[string]any) (int64, error) {
	var out OperationStarted
	if err := c.postJSON(ctx, "/api/v1/catalog/"+name+"/configure", map[string]any{"changes": changes}, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// AppSecret is one declared package secret as served (S266): names and
// presence only, never a value.
type AppSecret struct {
	Name      string   `json:"name"`
	StoreName string   `json:"store_name"`
	Present   bool     `json:"present"`
	Immutable bool     `json:"immutable"`
	Services  []string `json:"services"`
	Database  bool     `json:"database_password"`
	Wired     bool     `json:"wired"`
	Installed bool     `json:"installed"`
	// Reveal marks a credential meant for the owner (S312), readable
	// with RevealAppSecret; Note says what it is for.
	Reveal bool   `json:"reveal"`
	Note   string `json:"note,omitempty"`
}

// RevealedSecret is a credential the owner asked to read (S312).
type RevealedSecret struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Note  string `json:"note,omitempty"`
}

// RevealAppSecret reads a secret the package marks reveal, such as
// Nextcloud's generated admin password. The appliance audits it.
func (c *Client) RevealAppSecret(ctx context.Context, name, secret string) (RevealedSecret, error) {
	var out RevealedSecret
	if err := c.postJSON(ctx, "/api/v1/catalog/"+name+"/secrets/"+secret+"/reveal", map[string]any{"confirm": true}, &out); err != nil {
		return RevealedSecret{}, err
	}
	return out, nil
}

// AppSecrets lists a package's declared secret-backed variables.
func (c *Client) AppSecrets(ctx context.Context, name string) ([]AppSecret, error) {
	var out []AppSecret
	if err := c.get(ctx, "/api/v1/catalog/"+name+"/secrets", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RegisterAppSecret registers an existing value for a declared secret
// (installs that predate S266). The value travels in the request body
// once; the database password is verified unless skipVerify is set.
func (c *Client) RegisterAppSecret(ctx context.Context, name, secret, value string, skipVerify, force bool) (string, error) {
	var out struct {
		StoreName string `json:"store_name"`
	}
	body := map[string]any{"value": value, "skip_verify": skipVerify, "force": force}
	if err := c.postJSON(ctx, "/api/v1/catalog/"+name+"/secrets/"+secret, body, &out); err != nil {
		return "", err
	}
	return out.StoreName, nil
}

// RotatePackageSecrets rotates declared secrets through the configure
// operation, returning its operation id.
func (c *Client) RotatePackageSecrets(ctx context.Context, name string, secrets []string) (int64, error) {
	var out OperationStarted
	body := map[string]any{"changes": []map[string]any{}, "rotate_secrets": secrets}
	if err := c.postJSON(ctx, "/api/v1/catalog/"+name+"/configure", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// AdoptPackageSecrets regenerates only the secret wiring of a stack
// installed before S266 (never data or passwords), returning the
// configure operation id.
func (c *Client) AdoptPackageSecrets(ctx context.Context, name string) (int64, error) {
	var out OperationStarted
	body := map[string]any{"changes": []map[string]any{}, "adopt_secrets": true}
	if err := c.postJSON(ctx, "/api/v1/catalog/"+name+"/configure", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
