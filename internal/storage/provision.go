package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"homestead/internal/operation"
)

// UnsetID leaves ownership untouched: provisioning sets only what the
// caller names, never inventing users for app data.
const UnsetID = -1

// Directory is one application directory under a registered mount:
// relative path, directory mode, and optional ownership. Modes stay
// within 0700-0775 without world-write — app data never goes
// world-writable by provisioning — and ownership defaults to
// untouched.
type Directory struct {
	Path string
	Mode os.FileMode
	Uid  int
	Gid  int
}

// Validate checks directory shape: relative contained paths plus sane
// permissions and ownership. Symlink components refuse at provision
// time against live state, not here.
func (d Directory) Validate() error {
	if d.Path == "" || filepath.IsAbs(d.Path) {
		return fmt.Errorf("storage: directory path must be relative")
	}
	cleaned := filepath.Clean(d.Path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return fmt.Errorf("storage: directory path must stay inside the mount")
	}
	perm := d.Mode.Perm()
	if perm < 0o700 || perm > 0o775 || perm&0o002 != 0 {
		return fmt.Errorf("storage: directory mode must be 0700-0775 without world-write")
	}
	if d.Uid < UnsetID || d.Uid > 65535 || d.Gid < UnsetID || d.Gid > 65535 {
		return fmt.Errorf("storage: owner IDs must be -1 or 0-65535")
	}
	return nil
}

// joinConfined resolves a validated relative path under base,
// refusing symlink components and escapes against live state: every
// ancestor must either miss (to be created) or be a real directory.
func joinConfined(base, relative string) (string, error) {
	cleaned := filepath.Clean(relative)
	full := filepath.Join(base, cleaned)
	if full != base && !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage: %q escapes its mount", relative)
	}
	current := base
	for _, part := range strings.Split(cleaned, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return full, nil
			}
			return "", fmt.Errorf("storage: inspect %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("storage: %q crosses a symlink", current)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("storage: %q is not a directory", current)
		}
	}
	return full, nil
}

// provisionOne converges one directory: create, then enforce mode and
// named ownership. Existing files abort — provisioning never converts
// files into directories — while existing directories converge.
func provisionOne(base string, dir Directory) error {
	if err := dir.Validate(); err != nil {
		return err
	}
	full, err := joinConfined(base, dir.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(full, dir.Mode.Perm()); err != nil {
		return fmt.Errorf("storage: create %q: %w", dir.Path, err)
	}
	if err := os.Chmod(full, dir.Mode.Perm()); err != nil {
		return fmt.Errorf("storage: permissions %q: %w", dir.Path, err)
	}
	if dir.Uid != UnsetID || dir.Gid != UnsetID {
		if err := os.Chown(full, dir.Uid, dir.Gid); err != nil {
			return fmt.Errorf("storage: ownership %q: %w", dir.Path, err)
		}
	}
	return nil
}

// Provision converges directories on a registered mount: approval is
// re-checked at run time, so unregistration between planning and
// running refuses instead of writing on revoked storage.
func Provision(registry *Registry, mountpoint string, dirs []Directory) error {
	if len(dirs) == 0 {
		return fmt.Errorf("storage: nothing to provision")
	}
	if _, err := registry.Get(mountpoint); err != nil {
		return err
	}
	for i := range dirs {
		if err := provisionOne(mountpoint, dirs[i]); err != nil {
			return fmt.Errorf("directory #%d: %w", i, err)
		}
	}
	return nil
}

// ProvisionDefinition plans provisioning as a runner-backed operation:
// one step per directory for progress granularity, failing fast with
// the offending path named.
func ProvisionDefinition(registry *Registry, mountpoint string, dirs []Directory) (operation.Definition, error) {
	if len(dirs) == 0 {
		return operation.Definition{}, fmt.Errorf("storage: nothing to provision")
	}
	if _, err := registry.Get(mountpoint); err != nil {
		return operation.Definition{}, err
	}
	for i := range dirs {
		if err := dirs[i].Validate(); err != nil {
			return operation.Definition{}, fmt.Errorf("directory #%d: %w", i, err)
		}
	}
	def := operation.Definition{Type: "storage.provision"}
	for i := range dirs {
		dir := dirs[i]
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "provision:" + dir.Path,
			Run: func(ctx context.Context) error {
				return Provision(registry, mountpoint, []Directory{dir})
			},
		})
	}
	return def, nil
}
