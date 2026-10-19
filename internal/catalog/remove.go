package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"homestead/internal/operation"
	"homestead/internal/store"
)

// ErrNotInstalled reports removal of a package with no installed stack.
var ErrNotInstalled = errors.New("catalog: not installed")

// StackInstalled reports whether dir holds an installed stack. When it does
// not, a leftover empty directory (from an uninstall before S288) is
// removed so it stops cluttering the stacks root; a directory with
// anything in it is left alone.
func StackInstalled(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err == nil {
		return true
	}
	_ = removeIfEmpty(dir)
	return false
}

// removeIfEmpty removes dir only when it is an empty directory. It never
// recurses, so data folders and stray files are always safe.
func removeIfEmpty(dir string) error {
	err := os.Remove(dir)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	var pe *os.PathError
	if errors.As(err, &pe) && (errors.Is(pe.Err, syscall.ENOTEMPTY) || errors.Is(pe.Err, syscall.EEXIST) || errors.Is(pe.Err, syscall.ENOTDIR)) {
		return nil
	}
	return err
}

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
// never installed is a typo, not a no-op. Fingerprint records clear
// with the files so deleted stacks stop reporting (S212).
func RemoveDefinition(removal Removal, dir string, st *store.Store) (operation.Definition, error) {
	if err := removal.Validate(); err != nil {
		return operation.Definition{}, err
	}
	if dir == "" {
		return operation.Definition{}, fmt.Errorf("catalog: removal needs a directory")
	}
	if err := RefuseDuringMigration(removal.Package); err != nil {
		return operation.Definition{}, err
	}
	if st == nil {
		return operation.Definition{}, fmt.Errorf("catalog: removal needs a store")
	}
	if !StackInstalled(dir) {
		return operation.Definition{}, fmt.Errorf("%w: %q has no installed stack", ErrNotInstalled, removal.Package)
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
			return removeStackFiles(st, removal.Package, dir)
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

// removeStackFiles clears the fingerprint records and deletes the
// generated stack files: never data, never the secret store.
func removeStackFiles(st *store.Store, pkg, dir string) error {
	// Records clear first: a failed clear leaves files (and
	// their records) in place for retry, while files removed
	// without clearing would report phantom drift.
	if err := ClearFingerprints(st, pkg); err != nil {
		return err
	}
	for _, name := range []string{"compose.yaml", OverrideFileName} {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("catalog: remove %s: %w", name, err)
		}
	}
	// Materialized secret files are plaintext: they go with the
	// stack, data kept or not. The values stay in the secret
	// store, so a reinstall re-materializes them. RemoveAll on a
	// symlinked secrets directory removes the link, not its target.
	if err := os.RemoveAll(filepath.Join(dir, SecretsDirName)); err != nil {
		return fmt.Errorf("catalog: remove secret files: %w", err)
	}
	// The now-empty stack directory goes too (S288). Remove, not RemoveAll:
	// a directory still holding anything (data folders included) stays.
	if err := removeIfEmpty(dir); err != nil {
		return fmt.Errorf("catalog: remove stack directory: %w", err)
	}
	return nil
}
