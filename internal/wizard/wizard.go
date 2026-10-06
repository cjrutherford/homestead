// Package wizard persists resumable multi-step flow state (S036).
//
// Each wizard (claim, firewall, domain setup, …) keeps one record: the
// current step plus an opaque JSON payload of already-collected,
// non-sensitive input. The dashboard shell renders steps and drives
// transitions; this package only stores and returns them. Sessions,
// credentials, and claim tokens never belong here (S019/S020).
package wizard

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"homestead/internal/store"
)

var validID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// ErrNotFound reports no saved state for a wizard.
var ErrNotFound = errors.New("wizard state not found")

// ErrBadID reports a malformed wizard identifier.
var ErrBadID = errors.New("wizard id must match [a-z][a-z0-9_-]{0,63}")

// ErrBadStep reports an empty step name.
var ErrBadStep = errors.New("wizard step must not be empty")

// State is one wizard's saved progress.
type State struct {
	Wizard    string
	Step      string
	Payload   map[string]any
	UpdatedAt time.Time
}

// Service stores wizard state in the operational database.
type Service struct {
	db *sql.DB
}

// New returns a Service over an open store (S013 migrations provide v4+).
func New(s *store.Store) *Service {
	return &Service{db: s.DB()}
}

// Get returns the saved state for id, or ErrNotFound when never saved.
func (s *Service) Get(id string) (State, error) {
	if !validID.MatchString(id) {
		return State{}, ErrBadID
	}
	var step, payload, updated string
	err := s.db.QueryRow(
		`SELECT step, payload, updated_at FROM wizard_state WHERE wizard = ?`, id,
	).Scan(&step, &payload, &updated)
	if err == sql.ErrNoRows {
		return State{}, ErrNotFound
	}
	if err != nil {
		return State{}, fmt.Errorf("wizard: read state: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return State{}, fmt.Errorf("wizard: decode payload: %w", err)
	}
	if data == nil {
		data = map[string]any{}
	}
	stamp, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return State{}, fmt.Errorf("wizard: parse timestamp: %w", err)
	}
	return State{Wizard: id, Step: step, Payload: data, UpdatedAt: stamp}, nil
}

// Put upserts the state for id, returning what was stored.
func (s *Service) Put(id, step string, payload map[string]any) (State, error) {
	if !validID.MatchString(id) {
		return State{}, ErrBadID
	}
	if step == "" {
		return State{}, ErrBadStep
	}
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return State{}, fmt.Errorf("wizard: encode payload: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO wizard_state (wizard, step, payload, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (wizard) DO UPDATE SET step = ?, payload = ?, updated_at = ?`,
		id, step, string(encoded), now, step, string(encoded), now,
	); err != nil {
		return State{}, fmt.Errorf("wizard: save state: %w", err)
	}
	return s.Get(id)
}

// Delete forgets the state for id. Unknown ids succeed (idempotent).
func (s *Service) Delete(id string) error {
	if !validID.MatchString(id) {
		return ErrBadID
	}
	if _, err := s.db.Exec(`DELETE FROM wizard_state WHERE wizard = ?`, id); err != nil {
		return fmt.Errorf("wizard: delete state: %w", err)
	}
	return nil
}
