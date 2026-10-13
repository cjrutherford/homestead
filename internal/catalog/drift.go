package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"homestead/internal/operation"
	"homestead/internal/store"
)

// ManagedFiles lists generated files drift watches: the base Compose
// document plus layered configuration overrides. Experts may add
// files beside these (volumes, READMEs, Dockge state) — only recorded
// generated files are compared, so foreign files never read as drift.
func ManagedFiles() []string {
	return []string{"compose.yaml", OverrideFileName}
}

// Drift kinds naming how a recorded file changed out from under us.
const (
	// DriftModified marks recorded content that no longer matches.
	DriftModified = "modified"
	// DriftRemoved marks a recorded file that no longer exists.
	DriftRemoved = "removed"
	// DriftMode marks a secret file whose mode is not 0600 (S266).
	// Secret files are opaque to drift: presence and mode only.
	DriftMode = "mode"
)

// Drift is one diverged generated file: its stack-relative path and
// how it diverged. Resolution belongs to S213; detection only names.
type Drift struct {
	Path string
	Kind string
}

// readManaged returns the managed files present under dir: path to
// content. Absent files are skipped, not failed — overrides only
// exist after configuration, and recording happens right after
// writes, so presence is the writer's business. Read failures fail
// loudly instead of hashing partial bytes.
func readManaged(dir string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range ManagedFiles() {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("catalog: read %s: %w", name, err)
		}
		out[name] = string(raw)
	}
	return out, nil
}

// Fingerprints hashes the managed files present under dir: path to
// hex SHA-256.
func Fingerprints(dir string) (map[string]string, error) {
	contents, err := readManaged(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, content := range contents {
		sum := sha256.Sum256([]byte(content))
		out[name] = hex.EncodeToString(sum[:])
	}
	return out, nil
}

// RecordFingerprints stores the current hashes of managed files for
// stack, replacing prior rows: writes re-baseline, so only edits
// after the last Homestead write read as drift. Stacks with no
// managed files present record nothing and clear stale rows.
// Content records alongside hashes (S213) so viewing and restoring
// never need regeneration inputs Homestead does not keep.
func RecordFingerprints(st *store.Store, stack, dir string) error {
	contents, err := readManaged(dir)
	if err != nil {
		return err
	}
	db := st.DB()
	if _, err := db.Exec(`DELETE FROM generated_fingerprints WHERE stack = ?`, stack); err != nil {
		return fmt.Errorf("catalog: clear fingerprints: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, name := range ManagedFiles() {
		content, ok := contents[name]
		if !ok {
			continue
		}
		sum := sha256.Sum256([]byte(content))
		if _, err := db.Exec(
			`INSERT INTO generated_fingerprints (stack, path, sha256, content, updated_at) VALUES (?, ?, ?, ?, ?)`,
			stack, name, hex.EncodeToString(sum[:]), content, now,
		); err != nil {
			return fmt.Errorf("catalog: record fingerprint: %w", err)
		}
	}
	return nil
}

// StoredFingerprints returns the recorded hashes for stack: path to
// hex SHA-256. Unknown stacks return empty, never an error — nothing
// recorded means nothing watched.
func StoredFingerprints(st *store.Store, stack string) (map[string]string, error) {
	rows, err := st.DB().Query(`SELECT path, sha256 FROM generated_fingerprints WHERE stack = ?`, stack)
	if err != nil {
		return nil, fmt.Errorf("catalog: read fingerprints: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, fmt.Errorf("catalog: scan fingerprint: %w", err)
		}
		out[path] = hash
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: read fingerprints: %w", err)
	}
	return out, nil
}

// ClearFingerprints drops every recorded hash for stack. Removals
// call it so deleted stacks stop reporting; unknown stacks succeed.
func ClearFingerprints(st *store.Store, stack string) error {
	if _, err := st.DB().Exec(`DELETE FROM generated_fingerprints WHERE stack = ?`, stack); err != nil {
		return fmt.Errorf("catalog: clear fingerprints: %w", err)
	}
	return nil
}

// Detect compares recorded hashes against the files on disk: missing
// files report removed, differing content reports modified, matching
// content stays silent. Output sorts by path for stable reports.
// Unreadable files fail instead of guessing clean or drifted.
func Detect(dir string, recorded map[string]string) ([]Drift, error) {
	current, err := Fingerprints(dir)
	if err != nil {
		return nil, err
	}
	var out []Drift
	for _, name := range ManagedFiles() {
		want, watched := recorded[name]
		if !watched {
			continue
		}
		got, present := current[name]
		switch {
		case !present:
			out = append(out, Drift{Path: name, Kind: DriftRemoved})
		case got != want:
			out = append(out, Drift{Path: name, Kind: DriftModified})
		}
	}
	if _, watched := recorded["compose.yaml"]; watched {
		out = append(out, detectSecretFiles(dir)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// StoredContents returns the recorded file contents for stack: path
// to managed bytes. Rows recorded before content tracking (schema
// v14) read empty until the next Homestead write re-baselines them.
func StoredContents(st *store.Store, stack string) (map[string]string, error) {
	rows, err := st.DB().Query(`SELECT path, content FROM generated_fingerprints WHERE stack = ?`, stack)
	if err != nil {
		return nil, fmt.Errorf("catalog: read contents: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			return nil, fmt.Errorf("catalog: scan content: %w", err)
		}
		out[path] = content
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: read contents: %w", err)
	}
	return out, nil
}

// FileReview is one drifted file with both sides attached: the
// recorded managed bytes and the current disk bytes (empty when the
// file was removed). Surfaces render the diff; this package only
// pairs the sides.
type FileReview struct {
	Path     string
	Kind     string
	Recorded string
	Current  string
}

// Review pairs detection with contents: every drifted file with its
// recorded and current bytes, sorted by path. Clean stacks review
// empty — nothing to choose. Unreadable files fail instead of
// reviewing half a picture.
func Review(st *store.Store, stack, dir string) ([]FileReview, error) {
	recordedHashes, err := StoredFingerprints(st, stack)
	if err != nil {
		return nil, err
	}
	recordedContents, err := StoredContents(st, stack)
	if err != nil {
		return nil, err
	}
	drifts, err := Detect(dir, recordedHashes)
	if err != nil {
		return nil, err
	}
	current, err := readManaged(dir)
	if err != nil {
		return nil, err
	}
	out := make([]FileReview, 0, len(drifts))
	for _, drift := range drifts {
		out = append(out, FileReview{
			Path:     drift.Path,
			Kind:     drift.Kind,
			Recorded: recordedContents[drift.Path],
			Current:  current[drift.Path],
		})
	}
	return out, nil
}

// Accept adopts the current files as managed: re-baselines records
// to disk and returns the adopted paths sorted. Experts keep their
// edits; Homestead stops calling them drift.
func Accept(st *store.Store, stack, dir string) ([]string, error) {
	defer lockProject(stack)()
	if err := RecordFingerprints(st, stack, dir); err != nil {
		return nil, err
	}
	if err := ResolveInScan(st, stack); err != nil {
		return nil, err
	}
	stored, err := StoredFingerprints(st, stack)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range ManagedFiles() {
		if _, ok := stored[name]; ok {
			out = append(out, name)
		}
	}
	return out, nil
}

// restorePerms keeps managed files on their generated modes:
// world-readable base documents, owner-only overrides (S157).
func restorePerms(name string) os.FileMode {
	if name == OverrideFileName {
		return 0o600
	}
	return 0o644
}

// Restore rewrites drifted files from recorded bytes and returns the
// restored paths sorted. Files nobody drifted stay untouched;
// stacks with nothing recorded restore nothing. Containers keep
// running the old bytes until something ups — pair with the restore
// operation to converge.
func Restore(st *store.Store, stack, dir string) ([]string, error) {
	defer lockProject(stack)()
	recordedHashes, err := StoredFingerprints(st, stack)
	if err != nil {
		return nil, err
	}
	recordedContents, err := StoredContents(st, stack)
	if err != nil {
		return nil, err
	}
	drifts, err := Detect(dir, recordedHashes)
	if err != nil {
		return nil, err
	}
	var out, missing []string
	for _, drift := range drifts {
		if isSecretPath(drift.Path) {
			// Opaque secret files: restore can fix the mode but never
			// holds their contents (configure re-materializes them).
			if drift.Kind == DriftMode {
				if err := os.Chmod(filepath.Join(dir, drift.Path), SecretFileMode); err != nil {
					return nil, fmt.Errorf("catalog: restore %s mode: %w", drift.Path, err)
				}
				out = append(out, drift.Path)
			} else if drift.Kind == DriftRemoved {
				missing = append(missing, drift.Path)
			}
			continue
		}
		content, ok := recordedContents[drift.Path]
		if !ok {
			return nil, fmt.Errorf("catalog: no recorded content for %s", drift.Path)
		}
		if err := os.WriteFile(filepath.Join(dir, drift.Path), []byte(content), restorePerms(drift.Path)); err != nil {
			return nil, fmt.Errorf("catalog: restore %s: %w", drift.Path, err)
		}
		out = append(out, drift.Path)
	}
	if len(missing) > 0 {
		// Restore holds no secret contents: report loudly instead of
		// resolving drift that still exists.
		return out, fmt.Errorf("catalog: secret files missing and not restorable from drift records (%s); run configure to re-materialize them from the secret store", strings.Join(missing, ", "))
	}
	if err := ResolveInScan(st, stack); err != nil {
		return nil, err
	}
	return out, nil
}

// RestoreDefinition plans drift restoration: rewrite recorded files,
// then bring the stack up so containers converge on managed state —
// one restore plus up, failing fast with the offender named. Empty
// stack, directory, or project refuse before anything runs; a stack
// with no drift restores nothing and still converges.
func RestoreDefinition(st *store.Store, stack, dir, project string) (operation.Definition, error) {
	if st == nil {
		return operation.Definition{}, fmt.Errorf("catalog: restore needs a store")
	}
	if stack == "" {
		return operation.Definition{}, fmt.Errorf("catalog: restore needs a stack")
	}
	if err := RefuseDuringMigration(stack); err != nil {
		return operation.Definition{}, err
	}
	if dir == "" {
		return operation.Definition{}, fmt.Errorf("catalog: restore needs a directory")
	}
	if project == "" {
		return operation.Definition{}, fmt.Errorf("catalog: restore needs a project")
	}
	def := operation.Definition{Type: "catalog.restore"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "restore-files",
		Run: func(ctx context.Context) error {
			_, err := Restore(st, stack, dir)
			return err
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "up",
		Run: func(ctx context.Context) error {
			return composeUp(ctx, dir, project)
		},
	})
	return def, nil
}
