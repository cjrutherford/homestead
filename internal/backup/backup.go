package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"homestead/internal/discover"
	"homestead/internal/operation"
	"homestead/internal/storage"
)

// Source is one data tree to capture: a registered mount plus the
// package-relative path beneath it, mirroring provisioning so backup
// and provision always name the same roots.
type Source struct {
	Mountpoint string
	Path       string
}

// Validate checks source shape: absolute mounts, relative contained
// paths, never roots.
func (s Source) Validate() error {
	if s.Mountpoint == "" || s.Mountpoint[0] != '/' {
		return fmt.Errorf("backup: source mountpoint must be absolute")
	}
	if s.Path == "" || s.Path[0] == '/' || s.Path == "." {
		return fmt.Errorf("backup: source path must be a relative subdirectory")
	}
	cleaned := filepath.Clean(s.Path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return fmt.Errorf("backup: source path %q must stay inside its mount", s.Path)
	}
	return nil
}

// timestampLayout names backup roots sortable and unambiguous in UTC.
const timestampLayout = "20060102T150405Z"

// Manifest records one backup run for restore planners (S185): what,
// from where, and when. Written last, so its presence marks completion.
type Manifest struct {
	Package string   `json:"package"`
	Created string   `json:"created_at"`
	Entries []string `json:"entries"`
	Files   int      `json:"files"`
}

// ResolveDestination finds the backup root: the backup-class
// assignment's mountpoint, re-checked against live facts (present,
// serving usage, writable) because assignments age and mounts
// vanish. Unassigned backup classes fail instead of guessing.
func ResolveDestination(assignments *storage.Assignments, discovered discover.Storage) (string, error) {
	assignment, err := assignments.Get(storage.ClassBackup)
	if err != nil {
		return "", fmt.Errorf("backup: no backup class assignment: %w", err)
	}
	for _, mount := range discovered.Mounts {
		if mount.Mountpoint != assignment.Mountpoint {
			continue
		}
		if !mount.Accessible {
			return "", fmt.Errorf("backup: destination %q serves no usage data", mount.Mountpoint)
		}
		if mount.ReadOnly {
			return "", fmt.Errorf("backup: destination %q is read-only", mount.Mountpoint)
		}
		return mount.Mountpoint, nil
	}
	return "", fmt.Errorf("backup: destination %q absent from discovery", assignment.Mountpoint)
}

// copyTree replicates src into dst preserving modes: directories
// created, files copied, symlinks refused (they escape on restore,
// and data trees should not contain them — owners investigate
// instead). Missing sources fail loudly; typos must not read as
// empty backups.
func copyTree(src, dst string) (int, error) {
	info, err := os.Stat(src)
	if err != nil {
		return 0, fmt.Errorf("backup: source %q: %w", src, err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("backup: source %q is not a directory", src)
	}
	files := 0
	walk := func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup: symlink at %q refused", path)
		}
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if err := copyFile(path, target, entry); err != nil {
			return err
		}
		files++
		return nil
	}
	if err := filepath.WalkDir(src, walk); err != nil {
		return 0, err
	}
	return files, nil
}

// copyFile replicates one regular file with its mode.
func copyFile(src, dst string, entry fs.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup: %q is not a regular file", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Request scopes one backup run: the package name, source trees, and
// the resolved destination root. Timestamps name runs at execution,
// never at planning, so previews never promise times.
type Request struct {
	Package     string
	Sources     []Source
	Destination string
}

// Validate checks the request shape.
func (r Request) Validate() error {
	if r.Package == "" {
		return fmt.Errorf("backup: package must not be empty")
	}
	if r.Destination == "" || r.Destination[0] != '/' {
		return fmt.Errorf("backup: destination must be absolute")
	}
	if len(r.Sources) == 0 {
		return fmt.Errorf("backup: at least one source is required")
	}
	for i := range r.Sources {
		if err := r.Sources[i].Validate(); err != nil {
			return fmt.Errorf("backup: source #%d: %w", i, err)
		}
	}
	return nil
}

// Definition plans a backup run: one copy step per source plus a
// closing manifest write, failing fast with the offending source
// named. Runs sort under destination/package/timestamp; the manifest
// landing last marks completion for restore planners.
func Definition(request Request, now func() time.Time) (operation.Definition, error) {
	if err := request.Validate(); err != nil {
		return operation.Definition{}, err
	}
	if now == nil {
		now = time.Now
	}
	def := operation.Definition{Type: "backup.run"}
	var stamp string
	stampFor := func() string {
		if stamp == "" {
			stamp = now().UTC().Format(timestampLayout)
		}
		return stamp
	}
	for i := range request.Sources {
		source := request.Sources[i]
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "backup:" + source.Path,
			Run: func(ctx context.Context) error {
				root := filepath.Join(request.Destination, request.Package, stampFor())
				return copySource(root, source)
			},
		})
	}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "write-manifest",
		Run: func(ctx context.Context) error {
			return writeManifest(request, stampFor())
		},
	})
	return def, nil
}

// copySource replicates one source tree into its run root, refusing
// symlinked roots before walking: a linked root would back up the
// target tree under a false name.
func copySource(root string, source Source) error {
	full := filepath.Join(source.Mountpoint, source.Path)
	if info, err := os.Lstat(full); err != nil {
		return fmt.Errorf("backup: source %q: %w", source.Path, err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup: source %q is a symlink: refused", source.Path)
	}
	dst := filepath.Join(root, source.Path)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if _, err := copyTree(full, dst); err != nil {
		return err
	}
	return nil
}

// writeManifest records the completed run: entries plus file counts
// re-walked from disk, so the manifest describes what landed, not
// what was planned.
func writeManifest(request Request, stamp string) error {
	root := filepath.Join(request.Destination, request.Package, stamp)
	manifest := Manifest{Package: request.Package, Created: stamp}
	for _, source := range request.Sources {
		manifest.Entries = append(manifest.Entries, source.Path)
		count := 0
		_ = filepath.WalkDir(filepath.Join(root, source.Path), func(path string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() {
				count++
			}
			return nil
		})
		manifest.Files += count
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "backup.json"), raw, 0o644); err != nil {
		return fmt.Errorf("backup: manifest: %w", err)
	}
	return nil
}
