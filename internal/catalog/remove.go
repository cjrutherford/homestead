package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"homestead/internal/operation"
)

// DataDir names one persistent-data root for explicit removal: the
// mountpoint plus the package-relative path beneath it. Removal
// validates both halves independently because rm -rf forgives nothing.
type DataDir struct {
	Mountpoint string
	Path       string
}

// Validate checks removal safety: absolute mountpoints, relative
// contained paths, never the mount root itself (wiping a whole mount
// is not "removing application data").
func (d DataDir) Validate() error {
	if d.Mountpoint == "" || d.Mountpoint[0] != '/' {
		return fmt.Errorf("catalog: data mountpoint must be absolute")
	}
	if d.Path == "" || d.Path[0] == '/' || d.Path == "." {
		return fmt.Errorf("catalog: data path must be a relative subdirectory")
	}
	cleaned := filepath.Clean(d.Path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return fmt.Errorf("catalog: data path %q must stay inside its mount", d.Path)
	}
	return nil
}

// Removal scopes one uninstall: the package and stack directory to
// retire, whether persistent data goes with it, and exactly which data
// roots that means. Data stays by default — removal deletes the
// application, never surprises — and explicit data removal names every
// root rather than discovering them.
type Removal struct {
	Package    string
	DataDirs   []DataDir
	RemoveData bool
}

// Validate checks removal shape: named package, valid data roots,
// non-empty roots when data goes, and no data roots when it stays
// (stray roots with preservation on mean ambiguous intent).
func (r Removal) Validate() error {
	if r.Package == "" {
		return fmt.Errorf("catalog: removal needs a package")
	}
	for i := range r.DataDirs {
		if err := r.DataDirs[i].Validate(); err != nil {
			return fmt.Errorf("catalog: data #%d: %w", i, err)
		}
	}
	if r.RemoveData && len(r.DataDirs) == 0 {
		return fmt.Errorf("catalog: data removal needs explicit roots")
	}
	if !r.RemoveData && len(r.DataDirs) > 0 {
		return fmt.Errorf("catalog: data roots need explicit removal")
	}
	return nil
}

// RemoveDefinition plans uninstallation: stop and remove containers
// and networks, delete generated stack files, and — only when
// explicitly asked with explicit roots — delete persistent data.
// Missing compose files refuse before anything runs: removing what was
// never installed is a typo, not a no-op.
func RemoveDefinition(removal Removal, dir string) (operation.Definition, error) {
	if err := removal.Validate(); err != nil {
		return operation.Definition{}, err
	}
	if dir == "" {
		return operation.Definition{}, fmt.Errorf("catalog: removal needs a directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err != nil {
		return operation.Definition{}, fmt.Errorf("catalog: %q is not installed: %w", removal.Package, err)
	}
	def := operation.Definition{Type: "catalog.remove"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "down",
		Run: func(ctx context.Context) error {
			return composeDown(ctx, dir, removal.Package)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "remove-files",
		Run: func(ctx context.Context) error {
			for _, name := range []string{"compose.yaml", OverrideFileName} {
				path := filepath.Join(dir, name)
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("catalog: remove %s: %w", name, err)
				}
			}
			return nil
		},
	})
	if removal.RemoveData {
		dirs := removal.DataDirs
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "remove-data",
			Run: func(ctx context.Context) error {
				for _, data := range dirs {
					full := filepath.Join(data.Mountpoint, data.Path)
					info, err := os.Stat(full)
					if err != nil {
						return fmt.Errorf("catalog: data %q: %w", full, err)
					}
					if !info.IsDir() {
						return fmt.Errorf("catalog: data %q is not a directory", full)
					}
					if err := os.RemoveAll(full); err != nil {
						return fmt.Errorf("catalog: remove data %q: %w", full, err)
					}
				}
				return nil
			},
		})
	}
	return def, nil
}
