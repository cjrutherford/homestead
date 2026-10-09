package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

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
)

// Drift is one diverged generated file: its stack-relative path and
// how it diverged. Resolution belongs to S213; detection only names.
type Drift struct {
	Path string
	Kind string
}

// Fingerprints hashes the managed files present under dir: path to
// hex SHA-256. Absent files are skipped, not failed — overrides only
// exist after configuration, and recording happens right after
// writes, so presence is the writer's business. Read failures fail
// loudly instead of hashing partial bytes.
func Fingerprints(dir string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range ManagedFiles() {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("catalog: fingerprint %s: %w", name, err)
		}
		sum := sha256.Sum256(raw)
		out[name] = hex.EncodeToString(sum[:])
	}
	return out, nil
}

// RecordFingerprints stores the current hashes of managed files for
// stack, replacing prior rows: writes re-baseline, so only edits
// after the last Homestead write read as drift. Stacks with no
// managed files present record nothing and clear stale rows.
func RecordFingerprints(st *store.Store, stack, dir string) error {
	hashes, err := Fingerprints(dir)
	if err != nil {
		return err
	}
	db := st.DB()
	if _, err := db.Exec(`DELETE FROM generated_fingerprints WHERE stack = ?`, stack); err != nil {
		return fmt.Errorf("catalog: clear fingerprints: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, name := range ManagedFiles() {
		hash, ok := hashes[name]
		if !ok {
			continue
		}
		if _, err := db.Exec(
			`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES (?, ?, ?, ?)`,
			stack, name, hash, now,
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
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
