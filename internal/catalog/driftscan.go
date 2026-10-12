package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"homestead/internal/store"
)

// Periodic drift scan (S261). Scanning only detects: it never accepts,
// restores, or rewrites anything. Resolution stays an owner choice
// (S213).
const (
	// DefaultScanInterval is the cadence before the owner sets one.
	DefaultScanInterval int64 = 24 * 3600
	// MinScanInterval floors the cadence: scans read local files only,
	// but tighter loops fill operations and audit with noise.
	MinScanInterval int64 = 300
)

// ErrInvalidScanSchedule marks scan schedule validation failures (HTTP 422).
var ErrInvalidScanSchedule = errors.New("catalog: invalid drift scan schedule")

// ScanSchedule is the owner-set scan cadence. With no saved row it is
// the default: enabled, daily.
type ScanSchedule struct {
	IntervalSeconds int64 `json:"interval_seconds"`
	Enabled         bool  `json:"enabled"`
	// UpdatedAt anchors the cadence: it moves when the interval changes
	// or the scan is re-enabled, so unrelated saves never delay a scan.
	// Zero means the default was never saved.
	UpdatedAt time.Time `json:"updated_at"`
}

// GetScanSchedule returns the saved schedule, or the default.
func GetScanSchedule(st *store.Store) (ScanSchedule, error) {
	var out ScanSchedule
	var updated string
	err := st.DB().QueryRow(
		`SELECT interval_seconds, enabled, updated_at FROM drift_scan_schedule WHERE id = 1`,
	).Scan(&out.IntervalSeconds, &out.Enabled, &updated)
	if err == sql.ErrNoRows {
		return ScanSchedule{IntervalSeconds: DefaultScanInterval, Enabled: true}, nil
	}
	if err != nil {
		return ScanSchedule{}, fmt.Errorf("catalog: read drift scan schedule: %w", err)
	}
	if out.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return ScanSchedule{}, fmt.Errorf("catalog: read drift scan schedule: %w", err)
	}
	return out, nil
}

// SaveScanSchedule validates and stores the schedule at now.
func SaveScanSchedule(st *store.Store, interval int64, enabled bool, actor string, now time.Time) (ScanSchedule, error) {
	if interval < MinScanInterval {
		return ScanSchedule{}, fmt.Errorf("%w: interval must be at least %d seconds", ErrInvalidScanSchedule, MinScanInterval)
	}
	if _, err := st.DB().Exec(
		`INSERT INTO drift_scan_schedule (id, interval_seconds, enabled, actor, updated_at)
		 VALUES (1, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		 updated_at = CASE WHEN interval_seconds != excluded.interval_seconds
		   OR (enabled = 0 AND excluded.enabled = 1) THEN excluded.updated_at ELSE updated_at END,
		 interval_seconds = excluded.interval_seconds,
		 enabled = excluded.enabled, actor = excluded.actor`,
		interval, enabled, actor, now.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return ScanSchedule{}, fmt.Errorf("catalog: save drift scan schedule: %w", err)
	}
	return GetScanSchedule(st)
}

// ScanFile is one drifted file in a scan result.
type ScanFile struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// StackDrift is one installed package whose generated files drifted.
type StackDrift struct {
	Package string     `json:"package"`
	Files   []ScanFile `json:"files"`
}

// ScanResult is one completed scan across installed packages. Errors
// names packages that could not be read; they are neither clean nor
// drifted.
type ScanResult struct {
	ScannedAt time.Time    `json:"scanned_at"`
	Scanned   int          `json:"scanned"`
	Drifted   []StackDrift `json:"drifted"`
	Errors    []string     `json:"errors"`
}

// Clean reports a scan with nothing drifted and nothing unreadable.
func (r ScanResult) Clean() bool { return len(r.Drifted) == 0 && len(r.Errors) == 0 }

// InstalledStacks lists packages with recorded generated files, sorted:
// the set drift can watch.
func InstalledStacks(st *store.Store) ([]string, error) {
	rows, err := st.DB().Query(`SELECT DISTINCT stack FROM generated_fingerprints ORDER BY stack`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list stacks: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var stack string
		if err := rows.Scan(&stack); err != nil {
			return nil, fmt.Errorf("catalog: scan stack: %w", err)
		}
		out = append(out, stack)
	}
	return out, rows.Err()
}

// Scan checks every installed package for file drift. dirFor resolves
// stack directories. A package that cannot be read lands in Errors and
// the scan continues. Nothing is resolved.
func Scan(st *store.Store, dirFor func(string) string, now time.Time) (ScanResult, error) {
	stacks, err := InstalledStacks(st)
	if err != nil {
		return ScanResult{}, err
	}
	result := ScanResult{ScannedAt: now.UTC(), Drifted: []StackDrift{}, Errors: []string{}}
	for _, stack := range stacks {
		recorded, err := StoredFingerprints(st, stack)
		if err == nil {
			var drifts []Drift
			if drifts, err = Detect(dirFor(stack), recorded); err == nil {
				result.Scanned++
				if len(drifts) > 0 {
					entry := StackDrift{Package: stack}
					for _, d := range drifts {
						entry.Files = append(entry.Files, ScanFile{Path: d.Path, Kind: d.Kind})
					}
					result.Drifted = append(result.Drifted, entry)
				}
				continue
			}
		}
		result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", stack, err))
	}
	return result, nil
}

// SaveScan replaces the latest scan result.
func SaveScan(st *store.Store, result ScanResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("catalog: encode drift scan: %w", err)
	}
	if _, err := st.DB().Exec(
		`INSERT INTO drift_scan_latest (id, scanned_at, result) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET scanned_at = excluded.scanned_at, result = excluded.result`,
		result.ScannedAt.UTC().Format(time.RFC3339Nano), string(raw),
	); err != nil {
		return fmt.Errorf("catalog: save drift scan: %w", err)
	}
	return nil
}

// LatestScan returns the most recent scan, or ok=false when none ran.
func LatestScan(st *store.Store) (ScanResult, bool, error) {
	var raw string
	err := st.DB().QueryRow(`SELECT result FROM drift_scan_latest WHERE id = 1`).Scan(&raw)
	if err == sql.ErrNoRows {
		return ScanResult{}, false, nil
	}
	if err != nil {
		return ScanResult{}, false, fmt.Errorf("catalog: read drift scan: %w", err)
	}
	var out ScanResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return ScanResult{}, false, fmt.Errorf("catalog: decode drift scan: %w", err)
	}
	return out, true, nil
}
