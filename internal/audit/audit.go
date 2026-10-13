// Package audit persists the security audit trail (S063).
//
// Every security-relevant action records who (actor), what (action), the
// reviewed input (plan), and how it ended (outcome plus detail), including
// explicit owner overrides of safety recommendations (S125 pattern).
// Records are append-only: no update or delete path exists by design.
// Actor vocabulary: administrator usernames, "root" (socket/local root
// flows), "system" (daemon-initiated), "anonymous" (unauthenticated).
// Callers pass the actor explicitly — there is no ambient identity yet;
// enforcement slices will supply it from sessions.
package audit

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"homestead/internal/store"
)

// Outcomes.
const (
	// OutcomeOK records success.
	OutcomeOK = "ok"
	// OutcomeError records failure.
	OutcomeError = "error"
	// OutcomeSkipped records work deliberately not performed (for
	// example a scheduled run skipped because the previous one is live).
	OutcomeSkipped = "skipped"
)

// ErrNotFound reports unknown audit ids.
var ErrNotFound = errors.New("audit entry not found")

// Entry is one immutable audit record. JSON tags serve the read API.
type Entry struct {
	ID             int64     `json:"id"`
	Actor          string    `json:"actor"`
	Action         string    `json:"action"`
	Plan           string    `json:"plan"`
	Outcome        string    `json:"outcome"`
	Detail         string    `json:"detail"`
	Override       bool      `json:"override"`
	OverrideReason string    `json:"override_reason"`
	CreatedAt      time.Time `json:"created_at"`
}

// Service appends and lists audit entries.
type Service struct {
	db *sql.DB
}

// New returns a Store over an open store (migrations provide v6+).
func New(s *store.Store) *Service {
	return &Service{db: s.DB()}
}

// Record validates and appends one entry. Overrides require a reason;
// plans must be empty or JSON.
func (s *Service) Record(entry Entry) (Entry, error) {
	if entry.Actor == "" {
		return Entry{}, fmt.Errorf("audit: actor must not be empty")
	}
	if entry.Action == "" {
		return Entry{}, fmt.Errorf("audit: action must not be empty")
	}
	if entry.Outcome != OutcomeOK && entry.Outcome != OutcomeError && entry.Outcome != OutcomeSkipped {
		return Entry{}, fmt.Errorf("audit: outcome must be ok, error, or skipped")
	}
	if entry.Plan != "" && !json.Valid([]byte(entry.Plan)) {
		return Entry{}, fmt.Errorf("audit: plan must be empty or JSON")
	}
	if entry.Override && entry.OverrideReason == "" {
		return Entry{}, fmt.Errorf("audit: override requires a reason")
	}
	// Safety net (redact.go): callers record names, never values.
	// The actor of a failed login is the attempted username, which a
	// mistyped form can fill with a password or token.
	entry.Actor = RedactText(entry.Actor)
	entry.Detail = RedactText(entry.Detail)
	entry.OverrideReason = RedactText(entry.OverrideReason)
	entry.Plan = RedactPlan(entry.Plan)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(
		`INSERT INTO audit_entries (actor, action, plan, outcome, detail, override, override_reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Actor, entry.Action, entry.Plan, entry.Outcome, entry.Detail,
		entry.Override, entry.OverrideReason, now,
	)
	if err != nil {
		return Entry{}, fmt.Errorf("audit: record: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Entry{}, fmt.Errorf("audit: record: %w", err)
	}
	return s.Get(id)
}

// Get returns one entry by id.
func (s *Service) Get(id int64) (Entry, error) {
	var entry Entry
	var created string
	var override int
	err := s.db.QueryRow(
		`SELECT id, actor, action, plan, outcome, detail, override, override_reason, created_at
		 FROM audit_entries WHERE id = ?`, id,
	).Scan(&entry.ID, &entry.Actor, &entry.Action, &entry.Plan, &entry.Outcome,
		&entry.Detail, &override, &entry.OverrideReason, &created)
	if err == sql.ErrNoRows {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("audit: get: %w", err)
	}
	entry.Override = override != 0
	entry.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return Entry{}, fmt.Errorf("audit: get: %w", err)
	}
	return entry, nil
}

// List returns the most recent entries, newest first. Non-positive limits
// default to 100; larger than 1000 clamp to 1000.
func (s *Service) List(limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(
		`SELECT id FROM audit_entries ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("audit: list: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entry, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
