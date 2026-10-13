package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
}

// InstallPlanReport is the reviewed change set subset the CLI prints:
// counts and names, never full documents (plan files carry those).
type InstallPlanReport struct {
	Package      string `json:"Package"`
	Requirements struct {
		Passed bool `json:"Passed"`
	} `json:"Requirements"`
	Routes []struct {
		Host     string `json:"Host"`
		Exposure string `json:"Exposure"`
	} `json:"Routes"`
	DNS []struct {
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
	Notes   []string `json:"Notes"`
	Actions []string `json:"Actions"`
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
func (c *Client) RegisterAppSecret(ctx context.Context, name, secret, value string, skipVerify bool) (string, error) {
	var out struct {
		StoreName string `json:"store_name"`
	}
	body := map[string]any{"value": value, "skip_verify": skipVerify}
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
