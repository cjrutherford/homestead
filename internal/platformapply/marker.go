package platformapply

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"homestead/internal/store"
)

// ErrPending marks an apply already in flight: the single marker row
// exists. It doubles as the cross-process apply lock.
var ErrPending = errors.New("platformapply: a platform update is already pending")

// Marker describes an apply in flight. The daemon writes it after
// staging and before starting the apply unit (ADR-027); the apply unit
// reads it, re-verifies, swaps, health-checks, finalizes the operation,
// and clears it. One row at most, by schema, which makes it the apply
// lock.
type Marker struct {
	OpID         int64     `json:"op_id"`
	Component    string    `json:"component"`
	FromVersion  string    `json:"from_version"`
	FromCounter  int64     `json:"from_counter"`
	ToVersion    string    `json:"to_version"`
	ToCounter    int64     `json:"to_counter"`
	SHA256       string    `json:"sha256"`
	BinaryPath   string    `json:"binary_path"`
	PreviousPath string    `json:"previous_path"`
	PreviousSHA  string    `json:"previous_sha256"`
	Override     bool      `json:"override"`
	OverrideWhy  string    `json:"override_reason"`
	Actor        string    `json:"actor"`
	CreatedAt    time.Time `json:"created_at"`
	// Staged files the apply unit re-verifies, whether the owner allowed an
	// older release, and the listeners it health-checks (ADR-027).
	StagedArtifact  string `json:"staged_artifact"`
	StagedChecksums string `json:"staged_checksums"`
	StagedSignature string `json:"staged_signature"`
	AllowOlder      bool   `json:"allow_older"`
	Listen          string `json:"listen"`
	SocketPath      string `json:"socket_path"`
}

// Previous names the binary kept before the last replacement: what a
// manual restore (and S273 rollback) puts back.
type Previous struct {
	Component  string    `json:"component"`
	Version    string    `json:"version"`
	Counter    int64     `json:"counter"`
	SHA256     string    `json:"sha256"`
	Path       string    `json:"path"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Store persists the marker and previous-binary records in SQLite.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over an open, migrated store.
func NewStore(s *store.Store) *Store { return &Store{db: s.DB()} }

// Begin writes the marker. A marker that already exists is ErrPending.
func (s *Store) Begin(m Marker) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO platform_apply_marker
		(id, op_id, component, from_version, from_counter, to_version, to_counter, sha256, binary_path,
		 previous_path, previous_sha256, override, override_reason, actor, created_at,
		 staged_artifact, staged_checksums, staged_signature, allow_older, listen, socket_path)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.OpID, m.Component, m.FromVersion, m.FromCounter, m.ToVersion, m.ToCounter, m.SHA256, m.BinaryPath,
		m.PreviousPath, m.PreviousSHA, m.Override, m.OverrideWhy, m.Actor, m.CreatedAt.Format(time.RFC3339Nano),
		m.StagedArtifact, m.StagedChecksums, m.StagedSignature, m.AllowOlder, m.Listen, m.SocketPath)
	if err != nil {
		if strings.Contains(err.Error(), "constraint") {
			return ErrPending
		}
		return fmt.Errorf("platformapply: write marker: %w", err)
	}
	return nil
}

// Pending returns the marker, if one exists.
func (s *Store) Pending() (Marker, bool, error) {
	var m Marker
	var created string
	err := s.db.QueryRow(`SELECT op_id, component, from_version, from_counter, to_version, to_counter, sha256,
		binary_path, previous_path, previous_sha256, override, override_reason, actor, created_at,
		staged_artifact, staged_checksums, staged_signature, allow_older, listen, socket_path
		FROM platform_apply_marker WHERE id = 1`).Scan(
		&m.OpID, &m.Component, &m.FromVersion, &m.FromCounter, &m.ToVersion, &m.ToCounter, &m.SHA256,
		&m.BinaryPath, &m.PreviousPath, &m.PreviousSHA, &m.Override, &m.OverrideWhy, &m.Actor, &created,
		&m.StagedArtifact, &m.StagedChecksums, &m.StagedSignature, &m.AllowOlder, &m.Listen, &m.SocketPath)
	if errors.Is(err, sql.ErrNoRows) {
		return Marker{}, false, nil
	}
	if err != nil {
		return Marker{}, false, fmt.Errorf("platformapply: read marker: %w", err)
	}
	m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return m, true, nil
}

// Clear removes the marker belonging to opID; other markers stay.
func (s *Store) Clear(opID int64) error {
	if _, err := s.db.Exec(`DELETE FROM platform_apply_marker WHERE id = 1 AND op_id = ?`, opID); err != nil {
		return fmt.Errorf("platformapply: clear marker: %w", err)
	}
	return nil
}

// SetPrevious records the preserved previous binary on the marker, so a
// failure report (and S273) can name it.
func (s *Store) SetPrevious(opID int64, path, sha string) error {
	if _, err := s.db.Exec(`UPDATE platform_apply_marker SET previous_path = ?, previous_sha256 = ? WHERE id = 1 AND op_id = ?`,
		path, sha, opID); err != nil {
		return fmt.Errorf("platformapply: set previous: %w", err)
	}
	return nil
}

// SavePrevious records the preserved previous binary for a component.
func (s *Store) SavePrevious(p Previous) error {
	if p.RecordedAt.IsZero() {
		p.RecordedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO platform_previous (component, version, counter, sha256, path, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(component) DO UPDATE SET version = excluded.version, counter = excluded.counter,
			sha256 = excluded.sha256, path = excluded.path, recorded_at = excluded.recorded_at`,
		p.Component, p.Version, p.Counter, p.SHA256, p.Path, p.RecordedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("platformapply: save previous: %w", err)
	}
	return nil
}

// Previous returns the preserved previous binary for a component.
func (s *Store) Previous(component string) (Previous, bool, error) {
	var p Previous
	var at string
	err := s.db.QueryRow(`SELECT component, version, counter, sha256, path, recorded_at FROM platform_previous WHERE component = ?`,
		component).Scan(&p.Component, &p.Version, &p.Counter, &p.SHA256, &p.Path, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Previous{}, false, nil
	}
	if err != nil {
		return Previous{}, false, fmt.Errorf("platformapply: read previous: %w", err)
	}
	p.RecordedAt, _ = time.Parse(time.RFC3339Nano, at)
	return p, true, nil
}

// AllPrevious lists preserved previous binaries keyed by component.
func (s *Store) AllPrevious() (map[string]Previous, error) {
	rows, err := s.db.Query(`SELECT component, version, counter, sha256, path, recorded_at FROM platform_previous`)
	if err != nil {
		return nil, fmt.Errorf("platformapply: list previous: %w", err)
	}
	defer rows.Close()
	out := map[string]Previous{}
	for rows.Next() {
		var p Previous
		var at string
		if err := rows.Scan(&p.Component, &p.Version, &p.Counter, &p.SHA256, &p.Path, &at); err != nil {
			return nil, fmt.Errorf("platformapply: list previous: %w", err)
		}
		p.RecordedAt, _ = time.Parse(time.RFC3339Nano, at)
		out[p.Component] = p
	}
	return out, rows.Err()
}
