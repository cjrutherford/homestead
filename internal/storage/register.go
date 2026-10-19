package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"homestead/internal/discover"
	"homestead/internal/store"
)

// ErrNotRegistered reports reads of unregistered mountpoints.
var ErrNotRegistered = errors.New("storage: mountpoint not registered")

var validLabel = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// pseudoFilesystems can never hold application data: kernel, device,
// and container-overlay mounts that would corrupt or vanish.
// Read-only media fails earlier at writability; this list fails
// nonsense even when writable overlays pretend otherwise. tmpfs stays
// out too: RAM-backed storage fills into OOM kills, and no quota
// machinery exists yet to bound it.
var pseudoFilesystems = map[string]bool{
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
	"devpts": true, "devtmpfs": true, "tmpfs": true, "overlay": true,
	"squashfs": true, "iso9660": true,
	// Kernel interface filesystems report zero capacity yet are rw.
	"hugetlbfs": true, "mqueue": true, "bpf": true, "tracefs": true,
	"debugfs": true, "configfs": true, "securityfs": true, "pstore": true,
	"fusectl": true, "binfmt_misc": true, "autofs": true, "nsfs": true,
	"ramfs": true, "rpc_pipefs": true, "efivarfs": true, "selinuxfs": true,
}

// Registrable reports why a discovered mount can never be registered
// (no usage data, read-only, pseudo filesystem), or nil. Discovery
// responses and Register share it so clients never duplicate the rules.
func Registrable(m discover.Mount) error {
	if !m.Accessible {
		return fmt.Errorf("storage: %q serves no usage data", m.Mountpoint)
	}
	if m.ReadOnly {
		return fmt.Errorf("storage: %q is read-only", m.Mountpoint)
	}
	if pseudoFilesystems[m.FSType] {
		return fmt.Errorf("storage: %q is a %s pseudo-filesystem", m.Mountpoint, m.FSType)
	}
	return nil
}

// Registration is one owner-approved storage location: the mountpoint
// approved, the owner's label for it, who approved, and when. Labels
// stay unique so UIs and assignments name locations unambiguously.
type Registration struct {
	Mountpoint string
	Label      string
	Actor      string
	AddedAt    time.Time
}

// Registry keeps owner-approved mounts over the operational database
// (migration v9). Registration validates against live discovery — the
// mount must exist, serve usage, accept writes, and escape the pseudo
// list — so approvals name real places, never aspirations.
type Registry struct {
	db *sql.DB
}

// NewRegistry returns a Registry over an open store.
func NewRegistry(s *store.Store) *Registry {
	return &Registry{db: s.DB()}
}

// Register approves a discovered mount under an owner label, updating
// label and actor on re-registration so owners rename without
// unregister churn. discovered carries S043 facts; approval without
// facts is refused.
func (r *Registry) Register(mountpoint, label, actor string, discovered discover.Storage) (Registration, error) {
	if !validLabel.MatchString(label) || len(label) > 32 {
		return Registration{}, fmt.Errorf("storage: label must match [a-z][a-z0-9-]* (max 32)")
	}
	if actor == "" {
		return Registration{}, fmt.Errorf("storage: actor must not be empty")
	}
	if !filepath.IsAbs(mountpoint) || len(mountpoint) > 1024 {
		return Registration{}, fmt.Errorf("storage: mountpoint must be an absolute path")
	}
	var found *discover.Mount
	for i := range discovered.Mounts {
		if discovered.Mounts[i].Mountpoint == mountpoint {
			found = &discovered.Mounts[i]
			break
		}
	}
	if found == nil {
		return Registration{}, fmt.Errorf("storage: %q is not a discovered mount", mountpoint)
	}
	if err := Registrable(*found); err != nil {
		return Registration{}, err
	}
	var clash string
	err := r.db.QueryRow(`SELECT mountpoint FROM storage_registrations WHERE label = ? AND mountpoint != ?`,
		label, mountpoint).Scan(&clash)
	if err != nil && err != sql.ErrNoRows {
		return Registration{}, fmt.Errorf("storage: label check: %w", err)
	}
	if clash != "" {
		return Registration{}, fmt.Errorf("storage: label %q already names %q", label, clash)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := r.db.Exec(
		`INSERT INTO storage_registrations (mountpoint, label, actor, added_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(mountpoint) DO UPDATE SET label = excluded.label,
		 actor = excluded.actor, added_at = excluded.added_at`,
		mountpoint, label, actor, now,
	); err != nil {
		return Registration{}, fmt.Errorf("storage: register %q: %w", mountpoint, err)
	}
	return r.Get(mountpoint)
}

// Unregister drops an approval, idempotent: absent mountpoints converge
// to nil so cleanup never trips on ordering.
func (r *Registry) Unregister(mountpoint string) error {
	if _, err := r.db.Exec(`DELETE FROM storage_registrations WHERE mountpoint = ?`, mountpoint); err != nil {
		return fmt.Errorf("storage: unregister %q: %w", mountpoint, err)
	}
	return nil
}

// Get returns the approval for a mountpoint.
func (r *Registry) Get(mountpoint string) (Registration, error) {
	var registration Registration
	var added string
	err := r.db.QueryRow(
		`SELECT mountpoint, label, actor, added_at FROM storage_registrations WHERE mountpoint = ?`,
		mountpoint,
	).Scan(&registration.Mountpoint, &registration.Label, &registration.Actor, &added)
	if err == sql.ErrNoRows {
		return Registration{}, ErrNotRegistered
	}
	if err != nil {
		return Registration{}, fmt.Errorf("storage: get %q: %w", mountpoint, err)
	}
	registration.AddedAt, err = time.Parse(time.RFC3339Nano, added)
	if err != nil {
		return Registration{}, fmt.Errorf("storage: get %q: %w", mountpoint, err)
	}
	return registration, nil
}

// List returns every approval ordered by mountpoint.
func (r *Registry) List() ([]Registration, error) {
	rows, err := r.db.Query(
		`SELECT mountpoint, label, actor, added_at FROM storage_registrations ORDER BY mountpoint`)
	if err != nil {
		return nil, fmt.Errorf("storage: list: %w", err)
	}
	defer rows.Close()
	var out []Registration
	for rows.Next() {
		var registration Registration
		var added string
		if err := rows.Scan(&registration.Mountpoint, &registration.Label, &registration.Actor, &added); err != nil {
			return nil, fmt.Errorf("storage: list: %w", err)
		}
		registration.AddedAt, err = time.Parse(time.RFC3339Nano, added)
		if err != nil {
			return nil, fmt.Errorf("storage: list: %w", err)
		}
		out = append(out, registration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list: %w", err)
	}
	return out, nil
}

// UnregisterTx is Unregister inside a caller's transaction.
func (r *Registry) UnregisterTx(tx *sql.Tx, mountpoint string) error {
	if _, err := tx.Exec(`DELETE FROM storage_registrations WHERE mountpoint = ?`, mountpoint); err != nil {
		return fmt.Errorf("storage: unregister %q: %w", mountpoint, err)
	}
	return nil
}
