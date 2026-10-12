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
	"syscall"
	"time"

	"homestead/internal/discover"
	"homestead/internal/operation"
	"homestead/internal/storage"
)

// Source is one data tree to capture: a registered mount plus the
// package-relative path beneath it, mirroring provisioning so backup
// and provision always name the same roots.
//
// Class optionally names the storage class of the mount. A package can
// hold data on several mounts (S276: application and bulk), and every
// mount's tree shares the package-relative Path, so classed sources
// land at <run>/<class>/<path> and run as step "backup:<class>:<path>".
// Unclassed sources keep the original single-source layout
// (<run>/<path>), which restore still reads from older runs.
type Source struct {
	Mountpoint string
	Path       string
	Class      storage.Class
}

// Entry names the source inside a run: the manifest entry key and the
// run-relative destination.
func (s Source) Entry() string {
	if s.Class == "" {
		return s.Path
	}
	return string(s.Class) + "/" + s.Path
}

// StepName names the copy step, unique per entry.
func (s Source) StepName() string {
	if s.Class == "" {
		return "backup:" + s.Path
	}
	return "backup:" + string(s.Class) + ":" + s.Path
}

// validateSources checks every source and refuses entries that would
// share a destination: two trees written to one subpath would merge
// silently, so the run fails before copying anything.
func validateSources(sources []Source) error {
	seen := map[string]bool{}
	for i := range sources {
		if err := sources[i].Validate(); err != nil {
			return fmt.Errorf("backup: source #%d: %w", i, err)
		}
		entry := sources[i].Entry()
		if seen[entry] {
			return fmt.Errorf("backup: sources share the destination %q; give each mount a distinct class", entry)
		}
		seen[entry] = true
	}
	return nil
}

// Validate checks source shape: absolute mounts, relative contained
// paths, never roots.
func (s Source) Validate() error {
	if s.Mountpoint == "" || s.Mountpoint[0] != '/' {
		return fmt.Errorf("backup: source mountpoint must be absolute")
	}
	if s.Class != "" && (strings.Contains(string(s.Class), "/") || s.Class == "." || s.Class == "..") {
		return fmt.Errorf("backup: source class %q must be a plain name", s.Class)
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
// Sources maps each entry to its origin mountpoint, so restores can
// default targets to original locations without guessing.
type Manifest struct {
	Package string            `json:"package"`
	Created string            `json:"created_at"`
	Entries []string          `json:"entries"`
	Files   int               `json:"files"`
	Sources map[string]string `json:"sources"`
	// Paths maps each classed entry to its package-relative path on
	// the origin mount (restore target = mountpoint + path). Absent
	// for older runs, where the entry is the path itself.
	Paths map[string]string `json:"paths,omitempty"`
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
// created, files copied, and symlinks recreated as links only when
// they are relative and stay inside the tree (applications ship such
// links in their code trees, e.g. Nextcloud's .license files); any
// other symlink is refused because it escapes on restore. Links are
// never followed. Missing sources fail loudly; typos must not read as
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
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if err := copySymlink(src, path, target); err != nil {
				return err
			}
			files++
			return chownLike(target, info)
		}
		if entry.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			return chownLike(target, info)
		}
		if err := copyFile(path, target, entry); err != nil {
			return err
		}
		files++
		return chownLike(target, info)
	}
	if err := filepath.WalkDir(src, walk); err != nil {
		return 0, err
	}
	return files, nil
}

// chownLike gives dst the owner and group of the copied entry, so a
// restored database directory keeps the uid its container needs
// (postgres refuses a data directory owned by anyone else). Only root
// can chown; other callers keep their own identity.
func chownLike(dst string, info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 {
		return nil
	}
	if err := os.Lchown(dst, int(stat.Uid), int(stat.Gid)); err != nil {
		return fmt.Errorf("backup: chown %q: %w", dst, err)
	}
	return nil
}

// copySymlink recreates a link that is relative and resolves inside
// root, lexically, without following it.
func copySymlink(root, path, dst string) error {
	link, err := os.Readlink(path)
	if err != nil {
		return err
	}
	resolved := filepath.Join(filepath.Dir(path), link)
	inside, err := filepath.Rel(root, resolved)
	if filepath.IsAbs(link) || err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return fmt.Errorf("backup: symlink at %q refused", path)
	}
	return os.Symlink(link, dst)
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
	return validateSources(r.Sources)
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
			Name: source.StepName(),
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
	dst := filepath.Join(root, source.Entry())
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
	manifest := Manifest{Package: request.Package, Created: stamp, Sources: map[string]string{}}
	for _, source := range request.Sources {
		manifest.recordSource(source)
		count := 0
		_ = filepath.WalkDir(filepath.Join(root, source.Entry()), func(path string, entry fs.DirEntry, err error) error {
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

// recordSource adds one file source to the manifest: entry, origin
// mount, and (for classed entries) the original relative path.
func (m *Manifest) recordSource(source Source) {
	entry := source.Entry()
	m.Entries = append(m.Entries, entry)
	m.Sources[entry] = source.Mountpoint
	if source.Class != "" {
		if m.Paths == nil {
			m.Paths = map[string]string{}
		}
		m.Paths[entry] = source.Path
	}
}
