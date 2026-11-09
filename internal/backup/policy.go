package backup

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"homestead/internal/store"
)

// Policy is the one nightly backup policy (S297, ADR-039 §2): every
// stateful installed app without its own schedule is backed up at
// TimeOfDay (appliance local time), keeping Retain runs. It is stored
// only once the owner approves a plan.
type Policy struct {
	TimeOfDay string    `json:"time_of_day"`
	Retain    int       `json:"retain"`
	Enabled   bool      `json:"enabled"`
	Actor     string    `json:"actor,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Policy defaults: 02:00 local, seven runs, on.
const (
	DefaultPolicyTime   = "02:00"
	DefaultPolicyRetain = 7
	// MaxPolicyRetain bounds retention: a nightly run kept for a year.
	MaxPolicyRetain = 366
)

// ProposedPolicy is what setup proposes before anything is stored.
func ProposedPolicy() Policy {
	return Policy{TimeOfDay: DefaultPolicyTime, Retain: DefaultPolicyRetain, Enabled: true}
}

// ErrInvalidPolicy marks policy validation failures (HTTP 422).
var ErrInvalidPolicy = errors.New("backup: invalid policy")

// Clock parses TimeOfDay as hour and minute.
func (p Policy) Clock() (hour, minute int, err error) {
	t, err := time.Parse("15:04", strings.TrimSpace(p.TimeOfDay))
	if err != nil {
		return 0, 0, fmt.Errorf("%w: time of day %q is not HH:MM (24-hour)", ErrInvalidPolicy, p.TimeOfDay)
	}
	return t.Hour(), t.Minute(), nil
}

// Validate checks the time of day and retention.
func (p Policy) Validate() error {
	if _, _, err := p.Clock(); err != nil {
		return err
	}
	if p.Retain < 1 || p.Retain > MaxPolicyRetain {
		return fmt.Errorf("%w: keep between 1 and %d runs", ErrInvalidPolicy, MaxPolicyRetain)
	}
	return nil
}

// Normalize returns the policy with TimeOfDay in canonical HH:MM form.
func (p Policy) Normalize() Policy {
	if h, m, err := p.Clock(); err == nil {
		p.TimeOfDay = fmt.Sprintf("%02d:%02d", h, m)
	}
	return p
}

// Policies keeps the policy row (migration 49).
type Policies struct {
	db *sql.DB
}

// NewPolicies returns Policies over an open store.
func NewPolicies(s *store.Store) *Policies {
	return &Policies{db: s.DB()}
}

// Get returns the approved policy; ok is false before the first approval.
func (s *Policies) Get() (p Policy, ok bool, err error) {
	var updated string
	err = s.db.QueryRow(`SELECT time_of_day, retain, enabled, actor, updated_at FROM backup_policy WHERE id = 1`).
		Scan(&p.TimeOfDay, &p.Retain, &p.Enabled, &p.Actor, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{}, false, nil
	}
	if err != nil {
		return Policy{}, false, fmt.Errorf("backup: read policy: %w", err)
	}
	p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return p, true, nil
}

// Save stores the policy. UpdatedAt moves only when the time of day
// changes or the policy is switched on, as a schedule's anchor does, so
// a retention edit never delays the next run.
func (s *Policies) Save(p Policy, actor string) (Policy, error) {
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`INSERT INTO backup_policy (id, time_of_day, retain, enabled, actor, updated_at)
		VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		updated_at = CASE WHEN time_of_day != excluded.time_of_day
		  OR (enabled = 0 AND excluded.enabled = 1) THEN excluded.updated_at ELSE updated_at END,
		time_of_day = excluded.time_of_day, retain = excluded.retain,
		enabled = excluded.enabled, actor = excluded.actor`,
		p.TimeOfDay, p.Retain, p.Enabled, strings.TrimSpace(actor), now); err != nil {
		return Policy{}, fmt.Errorf("backup: save policy: %w", err)
	}
	saved, _, err := s.Get()
	return saved, err
}
