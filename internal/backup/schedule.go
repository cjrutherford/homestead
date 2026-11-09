package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"filippo.io/age"

	"homestead/internal/catalog"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// minScheduleInterval floors backup cadence at one hour: tighter loops
// churn I/O and storage for data that changes by the day, and owners
// asking faster want continuous replication, not scheduled copies.
const minScheduleInterval = 3600

var (
	// ErrInvalidSchedule marks schedule validation failures (HTTP 422).
	ErrInvalidSchedule = errors.New("backup: invalid schedule")
	// ErrScheduleNotFound marks a package with no schedule (HTTP 404).
	ErrScheduleNotFound = errors.New("backup: schedule not found")
	// ErrStateless marks a package that keeps no persistent data on the
	// appliance (ADR-028): it has no backup schedule and no backup job, and
	// asking for one is a defined no-op, never "no sources" (which stays
	// retryable for stateful packages) and never a failure.
	ErrStateless = errors.New("backup: stateless package, nothing to back up")
)

// Schedule is one retained backup job: the package, cadence in
// seconds, runs to keep (0 keeps everything), enablement, and who set
// it. Wall-clock triggering belongs to timers; this table declares
// intent, and job runs execute it through the operation engine where
// results already record.
//
// The JSON form is snake_case, matching the save request body.
type Schedule struct {
	Package         string `json:"package"`
	IntervalSeconds int64  `json:"interval_seconds"`
	Retain          int    `json:"retain"`
	Enabled         bool   `json:"enabled"`
	Actor           string `json:"actor"`
	// UpdatedAt anchors the cadence: it moves when the schedule is
	// created, its interval changes, or it is re-enabled, and stays put
	// on edits that keep the cadence (retention, actor), so those never
	// delay the next run.
	UpdatedAt time.Time `json:"updated_at"`
	// Stateless is set by the API (never stored) when the package keeps no
	// persistent data: the schedule is a no-op and no job runs (ADR-028).
	Stateless bool `json:"stateless,omitempty"`
}

// Validate checks schedule shape plus actor presence.
func (s Schedule) Validate() error {
	if err := s.validateShape(); err != nil {
		return err
	}
	if strings.TrimSpace(s.Actor) == "" {
		return fmt.Errorf("%w: actor must not be empty", ErrInvalidSchedule)
	}
	return nil
}

// validateShape checks everything but the actor, for flows (wizard
// review) where ownership attaches at apply time.
func (s Schedule) validateShape() error {
	if strings.TrimSpace(s.Package) == "" {
		return fmt.Errorf("%w: package must not be empty", ErrInvalidSchedule)
	}
	if s.IntervalSeconds < minScheduleInterval {
		return fmt.Errorf("%w: interval must be at least one hour", ErrInvalidSchedule)
	}
	if s.Retain < 0 {
		return fmt.Errorf("%w: retention must not be negative", ErrInvalidSchedule)
	}
	return nil
}

// Schedules keeps backup schedules over the operational database
// (migration v11): one row per package, replaced on re-save.
type Schedules struct {
	db *sql.DB
}

// NewSchedules returns Schedules over an open store.
func NewSchedules(s *store.Store) *Schedules {
	return &Schedules{db: s.DB()}
}

// Save creates or replaces a package schedule.
func (s *Schedules) Save(schedule Schedule) (Schedule, error) {
	schedule.Package = strings.TrimSpace(schedule.Package)
	schedule.Actor = strings.TrimSpace(schedule.Actor)
	if err := schedule.Validate(); err != nil {
		return Schedule{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO backup_schedules (package, interval_seconds, retain, enabled, actor, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(package) DO UPDATE SET
		 updated_at = CASE WHEN interval_seconds != excluded.interval_seconds
		   OR (enabled = 0 AND excluded.enabled = 1) THEN excluded.updated_at ELSE updated_at END,
		 interval_seconds = excluded.interval_seconds,
		 retain = excluded.retain, enabled = excluded.enabled,
		 actor = excluded.actor`,
		schedule.Package, schedule.IntervalSeconds, schedule.Retain, schedule.Enabled, schedule.Actor, now,
	); err != nil {
		return Schedule{}, fmt.Errorf("backup: save schedule %q: %w", schedule.Package, err)
	}
	return s.Get(schedule.Package)
}

// Get returns one package schedule.
func (s *Schedules) Get(packageName string) (Schedule, error) {
	var schedule Schedule
	var updated string
	var enabled bool
	err := s.db.QueryRow(
		`SELECT package, interval_seconds, retain, enabled, actor, updated_at
		 FROM backup_schedules WHERE package = ?`, packageName,
	).Scan(&schedule.Package, &schedule.IntervalSeconds, &schedule.Retain, &enabled, &schedule.Actor, &updated)
	if err == sql.ErrNoRows {
		return Schedule{}, fmt.Errorf("%w: %q", ErrScheduleNotFound, packageName)
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("backup: get schedule %q: %w", packageName, err)
	}
	schedule.Enabled = enabled
	schedule.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return Schedule{}, fmt.Errorf("backup: get schedule %q: %w", packageName, err)
	}
	return schedule, nil
}

// List returns every schedule ordered by package.
func (s *Schedules) List() ([]Schedule, error) {
	rows, err := s.db.Query(
		`SELECT package, interval_seconds, retain, enabled, actor, updated_at
		 FROM backup_schedules ORDER BY package`)
	if err != nil {
		return nil, fmt.Errorf("backup: list schedules: %w", err)
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var schedule Schedule
		var updated string
		var enabled bool
		if err := rows.Scan(&schedule.Package, &schedule.IntervalSeconds, &schedule.Retain, &enabled, &schedule.Actor, &updated); err != nil {
			return nil, fmt.Errorf("backup: list schedules: %w", err)
		}
		schedule.Enabled = enabled
		schedule.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, fmt.Errorf("backup: list schedules: %w", err)
		}
		out = append(out, schedule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("backup: list schedules: %w", err)
	}
	return out, nil
}

// Delete drops one schedule, idempotent: absent packages converge.
func (s *Schedules) Delete(packageName string) error {
	if _, err := s.db.Exec(`DELETE FROM backup_schedules WHERE package = ?`, packageName); err != nil {
		return fmt.Errorf("backup: delete schedule %q: %w", packageName, err)
	}
	return nil
}

// runName matches timestamped run directories and nothing else:
// pruning only ever removes what the runner names.
var runName = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

// Job is one retained run: the manifest (database declaration and
// hooks ride along), file sources, database export parameters,
// destination root, and retention count. Container and password serve
// database engines and hooks; file-only jobs leave them empty.
type Job struct {
	Manifest  catalog.Manifest
	Package   string
	Sources   []Source
	Container string
	Password  string
	SrcRoot   string
	DestRoot  string
	Retain    int
	// Warnings are recorded in the run manifest.
	Warnings []string
	// Recipients encrypt the run (S259). Empty writes the legacy
	// plaintext layout, which sensitive sources refuse.
	Recipients []age.Recipient
}

// Validate checks job shape: manifest, package, sources, destination,
// retention, and the engine/container/password coherence the export
// path demands.
func (j Job) Validate() error {
	if err := j.Manifest.Validate(); err != nil {
		return err
	}
	if j.Package == "" {
		return fmt.Errorf("backup: job package must not be empty")
	}
	if len(j.Sources) == 0 && j.Manifest.Database == nil {
		return fmt.Errorf("backup: job captures nothing")
	}
	if err := validateSources(j.Sources, len(j.Recipients) > 0); err != nil {
		return err
	}
	if j.DestRoot == "" || j.DestDirAbsent() {
		return fmt.Errorf("backup: destination root must be absolute")
	}
	if j.Retain < 0 {
		return fmt.Errorf("backup: retention must not be negative")
	}
	if j.Manifest.Database != nil {
		if j.Container == "" {
			return fmt.Errorf("backup: database export needs a container")
		}
		if j.Manifest.Database.Kind.Server() && j.Password == "" {
			return fmt.Errorf("backup: %s export needs a password", j.Manifest.Database.Kind)
		}
		if j.SrcRoot == "" {
			return fmt.Errorf("backup: database export needs a source root")
		}
	}
	return nil
}

// DestDirAbsent reports an empty or relative destination root.
func (j Job) DestDirAbsent() bool {
	return j.DestRoot == "" || j.DestRoot[0] != '/'
}

// JobDefinition plans one retained run: file copies, database export
// with hooks, pruning to retention, and the closing manifest —
// failing fast with offenders named. The manifest lands last, after
// pruning, so it always describes the retained set.
func JobDefinition(job Job, now func() time.Time) (operation.Definition, error) {
	if err := job.Validate(); err != nil {
		return operation.Definition{}, err
	}
	if now == nil {
		now = time.Now
	}
	def := operation.Definition{Type: "backup.job"}
	state := &runState{}
	var stamp string
	stampFor := func() string {
		if stamp == "" {
			stamp = now().UTC().Format(timestampLayout)
		}
		return stamp
	}
	rootFor := func() string {
		return filepath.Join(job.DestRoot, job.Package, stampFor())
	}
	for i := range job.Sources {
		source := job.Sources[i]
		def.Steps = append(def.Steps, operation.StepDef{
			Name: source.StepName(),
			Run: func(ctx context.Context) error {
				n, err := copySource(rootFor(), source, job.Recipients, state)
				state.record(source.Entry(), n)
				return err
			},
		})
	}
	if job.Manifest.Database != nil {
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "export:db",
			Run: func(ctx context.Context) error {
				_, err := ExportDatabase(ctx, ExportRequest{
					Manifest: job.Manifest, Package: job.Package,
					Database: *job.Manifest.Database, SrcRoot: job.SrcRoot,
					DestDir: rootFor(), Container: job.Container,
					Password: job.Password, Hooks: job.Manifest.Hooks,
					Recipients: job.Recipients,
				})
				return err
			},
		})
	}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "write-manifest",
		Run: func(ctx context.Context) error {
			return writeJobManifest(job, stampFor(), state)
		},
	})
	// Prune only after the new run is complete: retention must never
	// delete the last good run for one that did not finish.
	if job.Retain > 0 {
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "prune",
			Run: func(ctx context.Context) error {
				return prune(job, stampFor())
			},
		})
	}
	def.Cleanup = func() {
		if stamp != "" {
			cleanupPartial(rootFor())
		}
	}
	return def, nil
}

// prune applies retention to complete runs only: runs with a readable
// manifest, newest kept, the current run always kept. Runner-named
// directories without a manifest are garbage from interrupted runs; they
// never count toward retention and are removed once an older-than-newest
// complete run exists (an unfinished newer run might still be writing).
// Stray files and foreign directories survive unconditionally.
func prune(job Job, current string) error {
	base := filepath.Join(job.DestRoot, job.Package)
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("backup: list runs: %w", err)
	}
	var good, partial []string
	for _, entry := range entries {
		if !entry.IsDir() || !runName.MatchString(entry.Name()) {
			continue
		}
		if runComplete(filepath.Join(base, entry.Name())) {
			good = append(good, entry.Name())
		} else {
			partial = append(partial, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(good)))
	keep := map[string]bool{current: true}
	for i, run := range good {
		if i < job.Retain {
			keep[run] = true
		}
	}
	for _, run := range good {
		if keep[run] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(base, run)); err != nil {
			return fmt.Errorf("backup: prune %q: %w", run, err)
		}
	}
	if len(good) == 0 {
		return nil
	}
	for _, run := range partial {
		if run < good[0] && run != current {
			if err := os.RemoveAll(filepath.Join(base, run)); err != nil {
				return fmt.Errorf("backup: prune partial %q: %w", run, err)
			}
		}
	}
	return nil
}

// writeJobManifest records the retained run: file sources plus the
// database export when present, with counts re-walked from disk.
func writeJobManifest(job Job, stamp string, state *runState) error {
	root := filepath.Join(job.DestRoot, job.Package, stamp)
	manifest := Manifest{Package: job.Package, Created: stamp, Sources: map[string]string{}, Warnings: append(append([]string(nil), job.Warnings...), state.warned()...)}
	for _, source := range job.Sources {
		manifest.recordSource(source)
	}
	dbEntry := ""
	if job.Manifest.Database != nil {
		name := job.Manifest.Database.Database + ".sql"
		if job.Manifest.Database.Kind == catalog.DatabaseSQLite {
			name = baseName(job.Manifest.Database.Path)
		}
		manifest.Entries = append(manifest.Entries, name)
		dbEntry = name
	}
	for _, entry := range manifest.Entries {
		if len(job.Recipients) > 0 && entry == dbEntry {
			manifest.Files++
			continue
		}
		manifest.Files += countEntry(root, entry, len(job.Recipients) > 0, state)
	}
	manifest.markFormat(job.Recipients, dbEntry)
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: manifest: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(root, "backup.json"), raw, 0o644); err != nil {
		return fmt.Errorf("backup: manifest: %w", err)
	}
	return nil
}

// baseName returns the final path element without directories.
func baseName(path string) string {
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}
	return path
}
