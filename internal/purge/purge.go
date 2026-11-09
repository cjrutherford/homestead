// Package purge removes a person's Everything data sharing (grants, read
// links, sessions) when the owner removes their access in People (S427,
// ADR-058 section 1). It runs the bridge's own offline tool inside the
// app's container; the bridge gains no listener or credential. A container
// that is stopped leaves the purge pending in SQLite, retried by a timer and
// when the app starts. Plain Compose keeps the manual command.
package purge

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Target is one installed Everything app: its package name (the bridge's
// application name) and its container.
type Target struct {
	App       string
	Container string
}

// Pending is a purge that has not succeeded yet.
type Pending struct {
	App       string    `json:"app"`
	Username  string    `json:"username"`
	Since     time.Time `json:"since"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error,omitempty"`
}

// Outcome is the state of one app's purge.
type Outcome struct {
	App   string
	State string // StateDone or StatePending
}

const (
	StateDone    = "done"
	StatePending = "pending"
)

// Paths inside the bridge image (images/everything/Dockerfile).
const (
	bridgeBinary = "/everything-bridge"
	bridgeDB     = "/data/bridge.db"
)

// Service holds what the purge needs. Now and Audit are optional.
type Service struct {
	DB *sql.DB
	// Apps lists the installed Everything apps.
	Apps func(ctx context.Context) ([]Target, error)
	// Exec runs a command in a container and returns its output. The
	// arguments carry no secret: a person's name and subject only.
	Exec  func(ctx context.Context, container string, argv ...string) (string, error)
	Audit func(actor, action, outcome, detail string)
	Now   func() time.Time

	mu sync.Mutex
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) audit(actor, outcome, detail string) {
	if s.Audit != nil {
		s.Audit(actor, "people.purge", outcome, detail)
	}
}

// Request purges a person in the named app, or in every installed Everything
// app when app is empty. An app that is not an Everything app is ignored.
// Removal never waits on it: a failed attempt is stored as pending.
func (s *Service) Request(ctx context.Context, actor, username, subject, app string) []Outcome {
	if s == nil || s.DB == nil || s.Apps == nil || (username == "" && subject == "") {
		return nil
	}
	targets, err := s.Apps(ctx)
	if err != nil {
		s.audit(actor, "error", fmt.Sprintf("%s: could not list Everything apps: %v", username, err))
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Outcome
	for _, t := range targets {
		if app != "" && t.App != app {
			continue
		}
		_, err := s.DB.Exec(`INSERT OR IGNORE INTO everything_purges(app, username, subject, requested_at, attempts, last_error)
			VALUES(?, ?, ?, ?, 0, '')`, t.App, username, subject, s.now().Format(time.RFC3339))
		if err != nil {
			s.audit(actor, "error", fmt.Sprintf("%s in %s: could not store the purge: %v", username, t.App, err))
			continue
		}
		out = append(out, Outcome{App: t.App, State: s.attempt(ctx, actor, t, username, subject)})
	}
	return out
}

// attempt runs one stored purge. Callers hold s.mu.
func (s *Service) attempt(ctx context.Context, actor string, t Target, username, subject string) string {
	err := s.run(ctx, t, username, subject)
	if err != nil {
		msg := trim(err.Error())
		_, _ = s.DB.Exec(`UPDATE everything_purges SET attempts = attempts + 1, last_error = ?
			WHERE app = ? AND username = ? AND subject = ?`, msg, t.App, username, subject)
		s.audit(actor, "pending", fmt.Sprintf("%s in %s: pending, will retry (%s)", label(username, subject), t.App, msg))
		return StatePending
	}
	_, _ = s.DB.Exec(`DELETE FROM everything_purges WHERE app = ? AND username = ? AND subject = ?`, t.App, username, subject)
	s.audit(actor, "ok", fmt.Sprintf("%s in %s: sharing and sessions purged", label(username, subject), t.App))
	return StateDone
}

// run executes the bridge tool. A subject that matches nobody falls back to
// the username (the app may know the person by a different subject).
func (s *Service) run(ctx context.Context, t Target, username, subject string) error {
	base := []string{bridgeBinary, "people", "purge", "-db", bridgeDB, "-app", t.App}
	// The "=" form keeps a value from ever being read as a flag.
	if subject != "" {
		out, err := s.Exec(ctx, t.Container, append(base, "-subject="+subject)...)
		if err != nil {
			return err
		}
		if username == "" || !strings.Contains(out, "purged 0 people") {
			return nil
		}
	}
	if username == "" {
		return nil
	}
	_, err := s.Exec(ctx, t.Container, append(base, "-username="+username)...)
	return err
}

// Retry attempts every pending purge (all apps when app is empty). A
// purge for an app that is no longer installed is dropped.
func (s *Service) Retry(ctx context.Context, actor, app string) {
	if s == nil || s.DB == nil || s.Apps == nil {
		return
	}
	targets, err := s.Apps(ctx)
	if err != nil {
		return
	}
	byApp := map[string]Target{}
	for _, t := range targets {
		byApp[t.App] = t
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.queryPending()
	if err != nil {
		return
	}
	for _, r := range rows {
		if app != "" && r.App != app {
			continue
		}
		t, ok := byApp[r.App]
		if !ok {
			_, _ = s.DB.Exec(`DELETE FROM everything_purges WHERE app = ? AND username = ? AND subject = ?`, r.App, r.Username, r.subject)
			s.audit(actor, "skipped", fmt.Sprintf("%s in %s: app is no longer installed", label(r.Username, r.subject), r.App))
			continue
		}
		s.attempt(ctx, actor, t, r.Username, r.subject)
	}
}

// Cancel drops pending purges for a username (all apps when app is empty):
// the person was given access again, so the stored purge must not remove it.
func (s *Service) Cancel(username, app string) {
	if s == nil || s.DB == nil || username == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if app == "" {
		_, _ = s.DB.Exec(`DELETE FROM everything_purges WHERE username = ?`, username)
		return
	}
	_, _ = s.DB.Exec(`DELETE FROM everything_purges WHERE username = ? AND app = ?`, username, app)
}

// HasPending reports whether any purge waits.
func (s *Service) HasPending() bool {
	if s == nil || s.DB == nil {
		return false
	}
	var n int
	return s.DB.QueryRow(`SELECT COUNT(*) FROM everything_purges`).Scan(&n) == nil && n > 0
}

// List returns the pending purges, oldest first.
func (s *Service) List() ([]Pending, error) {
	out := []Pending{}
	if s == nil || s.DB == nil {
		return out, nil
	}
	rows, err := s.queryPending()
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out = append(out, r.Pending)
	}
	return out, nil
}

type pendingRow struct {
	Pending
	subject string
}

func (s *Service) queryPending() ([]pendingRow, error) {
	rows, err := s.DB.Query(`SELECT app, username, subject, requested_at, attempts, last_error
		FROM everything_purges ORDER BY requested_at, app, username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pendingRow
	for rows.Next() {
		var r pendingRow
		var at string
		if err := rows.Scan(&r.App, &r.Username, &r.subject, &at, &r.Attempts, &r.LastError); err != nil {
			return nil, err
		}
		r.Since, _ = time.Parse(time.RFC3339, at)
		out = append(out, r)
	}
	return out, rows.Err()
}

func label(username, subject string) string {
	if username != "" {
		return username
	}
	return "a person"
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
