package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"homestead/internal/catalog"
)

// Run is one restorable backup: its package, timestamp, directory,
// and parsed manifest. Runs come from ListRuns, never hand-built,
// so planning always starts from on-disk truth.
type Run struct {
	Package  string
	Stamp    string
	Dir      string
	Manifest Manifest
}

// ListRuns inventories restorable runs for a package under a
// destination root, newest first: timestamp-named directories with
// parseable manifests only. Anything else — foreign files, partial
// runs without manifests — stays out without error, since destinations
// legitimately hold more than Homestead runs.
func ListRuns(destination, packageName string) ([]Run, error) {
	if packageName == "" {
		return nil, fmt.Errorf("backup: package must not be empty")
	}
	base := filepath.Join(destination, packageName)
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: list runs: %w", err)
	}
	var out []Run
	for _, entry := range entries {
		if !entry.IsDir() || !runName.MatchString(entry.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(base, entry.Name(), "backup.json"))
		if err != nil {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			continue
		}
		if manifest.Package != packageName {
			continue
		}
		out = append(out, Run{
			Package: packageName, Stamp: entry.Name(),
			Dir: filepath.Join(base, entry.Name()), Manifest: manifest,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Stamp > out[j].Stamp })
	return out, nil
}

// PlannedEntry is one restore line: the backup path, its live target,
// whether the target exists (overwrites destroy), and the evidence.
type PlannedEntry struct {
	BackupPath string
	Target     string
	Exists     bool
	// Encrypted marks an age artifact (BackupPath names the .age file);
	// Sensitive additionally forces restored files to 0600.
	Encrypted bool
	Sensitive bool
	// Preserve names top-level children of the target that restore must
	// neither remove nor replace: a stack's secrets/ belongs to its own
	// entry, so a run without that entry must not delete live secrets.
	Preserve []string
}

// RestorePlan previews one run's restoration: per-entry targets with
// existence, overall destructiveness, and warnings naming every
// consequence — overwrites plus application quiescence for database
// artifacts. Nothing here writes; S186 executes approved plans.
type RestorePlan struct {
	Package     string
	Stamp       string
	Entries     []PlannedEntry
	Destructive bool
	Warnings    []string
}

// databaseSuffixes mark entries needing application quiescence: live
// writers corrupt restores, so these warn loudly even when their
// targets are absent.
func databaseSuffix(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".sqlite") ||
		strings.HasSuffix(lower, ".sqlite3") || strings.HasSuffix(lower, ".sql")
}

// PlanRestore previews restoring a run: each manifest entry maps to
// its provenance mountpoint by default, or an explicit override target
// per entry. Missing provenance without an override fails — guessing
// restore targets would scatter data — and every existing target
// marks the plan destructive with a named warning.
func PlanRestore(run Run, overrides map[string]string) (RestorePlan, error) {
	if run.Dir == "" {
		return RestorePlan{}, fmt.Errorf("backup: run has no directory")
	}
	if len(run.Manifest.Entries) == 0 {
		return RestorePlan{}, fmt.Errorf("backup: run %q captures nothing", run.Stamp)
	}
	if run.Manifest.Format() > CurrentFormat {
		return RestorePlan{}, fmt.Errorf("backup: run %q uses format %d, newer than this version understands (%d): upgrade Homestead or decrypt it with age directly",
			run.Stamp, run.Manifest.Format(), CurrentFormat)
	}
	plan := RestorePlan{Package: run.Package, Stamp: run.Stamp}
	for _, entry := range run.Manifest.Entries {
		target, ok := overrides[entry]
		if !ok {
			mountpoint, ok := run.Manifest.Sources[entry]
			if !ok || mountpoint == "" {
				return RestorePlan{}, fmt.Errorf("backup: entry %q needs an explicit target", entry)
			}
			// Classed entries (<class>/<path>) restore to the recorded
			// relative path; older runs stored the path as the entry.
			relative := entry
			if recorded, ok := run.Manifest.Paths[entry]; ok {
				relative = recorded
			}
			target = filepath.Join(mountpoint, relative)
		}
		if target == "" || target[0] != '/' {
			return RestorePlan{}, fmt.Errorf("backup: target for %q must be absolute", entry)
		}
		planned := PlannedEntry{BackupPath: filepath.Join(run.Dir, entry), Target: target}
		if strings.HasPrefix(entry, StackClass+"/") {
			planned.Preserve = []string{catalog.SecretsDirName}
		}
		if archive, ok := run.Manifest.Archives[entry]; ok && run.Manifest.Encrypted {
			if archive == "" || filepath.IsAbs(archive) || strings.Contains(archive, "..") {
				return RestorePlan{}, fmt.Errorf("backup: entry %q names an invalid archive", entry)
			}
			planned.BackupPath = filepath.Join(run.Dir, archive)
			planned.Encrypted = true
			planned.Sensitive = strings.HasPrefix(entry, SecretsClass+"/")
		}
		if _, err := os.Stat(target); err == nil {
			planned.Exists = true
			plan.Destructive = true
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("overwrites %s", target))
		} else if !os.IsNotExist(err) {
			return RestorePlan{}, fmt.Errorf("backup: inspect %q: %w", target, err)
		}
		if databaseSuffix(entry) {
			plan.Warnings = append(plan.Warnings,
				fmt.Sprintf("stop %s before restoring database entry %q", run.Package, entry))
		}
		plan.Entries = append(plan.Entries, planned)
	}
	return plan, nil
}
