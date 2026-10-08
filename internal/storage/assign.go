package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"homestead/internal/discover"
	"homestead/internal/store"
)

// ErrNoAssignment reports classes with no bound mount.
var ErrNoAssignment = errors.New("storage: class has no assignment")

// networkFilesystems cannot take databases: locking hazards over the
// wire corrupt structured data. Every other class accepts them —
// homelab media and backup targets commonly live on NAS — so only the
// database profile gates here, exactly as S140 contracts.
func isNetworkFSType(fstype string) bool {
	switch fstype {
	case "nfs", "nfs4", "smbfs", "cifs", "sshfs":
		return true
	}
	return strings.HasPrefix(fstype, "fuse.")
}

// SuitableMounts ranks discovered mounts for a class, most free space
// first with mountpoint tiebreaks for determinism. Filters reject
// inaccessible, read-only, pseudo, undersized, and (for databases)
// network mounts. Pure against facts: registration and approval live
// one layer up in Assign.
func SuitableMounts(class Class, mounts []discover.Mount, minFreeBytes uint64) ([]discover.Mount, error) {
	profile, err := ProfileFor(class)
	if err != nil {
		return nil, err
	}
	var suitable []discover.Mount
	for _, mount := range mounts {
		if !mount.Accessible || mount.ReadOnly {
			continue
		}
		if pseudoFilesystems[mount.FSType] {
			continue
		}
		if profile.LocalOnly && isNetworkFSType(mount.FSType) {
			continue
		}
		if mount.FreeBytes < minFreeBytes {
			continue
		}
		suitable = append(suitable, mount)
	}
	sort.Slice(suitable, func(i, j int) bool {
		if suitable[i].FreeBytes != suitable[j].FreeBytes {
			return suitable[i].FreeBytes > suitable[j].FreeBytes
		}
		return suitable[i].Mountpoint < suitable[j].Mountpoint
	})
	return suitable, nil
}

// Assignment binds one class to one registered mount. Classes share
// mounts freely (per-class subdirectories, provisioned in S143);
// mountpoints never validate here beyond approval, since space and
// suitability re-check at every assignment.
type Assignment struct {
	Class      Class
	Mountpoint string
	Actor      string
	CreatedAt  time.Time
}

// Assignments keeps class bindings over the operational database
// (migration v10).
type Assignments struct {
	db *sql.DB
}

// NewAssignments returns Assignments over an open store.
func NewAssignments(s *store.Store) *Assignments {
	return &Assignments{db: s.DB()}
}

// Assign binds a class to a registered, currently suitable mount:
// approval first (the mount must be registered), then suitability
// against live facts (profile fit plus minimum free space).
// Re-assignment replaces, so owners migrate classes between mounts
// without unassign churn.
func (a *Assignments) Assign(class Class, mountpoint, actor string, registry *Registry, discovered discover.Storage, minFreeBytes uint64) (Assignment, error) {
	if actor == "" {
		return Assignment{}, fmt.Errorf("storage: actor must not be empty")
	}
	if _, err := registry.Get(mountpoint); err != nil {
		return Assignment{}, fmt.Errorf("storage: cannot assign unregistered mount: %w", err)
	}
	suitable, err := SuitableMounts(class, discovered.Mounts, minFreeBytes)
	if err != nil {
		return Assignment{}, err
	}
	approved := false
	for _, mount := range suitable {
		if mount.Mountpoint == mountpoint {
			approved = true
			break
		}
	}
	if !approved {
		return Assignment{}, fmt.Errorf("storage: %q is not suitable for %s", mountpoint, class)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := a.db.Exec(
		`INSERT INTO storage_assignments (class, mountpoint, actor, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(class) DO UPDATE SET mountpoint = excluded.mountpoint,
		 actor = excluded.actor, created_at = excluded.created_at`,
		string(class), mountpoint, actor, now,
	); err != nil {
		return Assignment{}, fmt.Errorf("storage: assign %s: %w", class, err)
	}
	return a.Get(class)
}

// Unassign drops a class binding, idempotent: absent classes converge
// to nil so cleanup never trips on ordering.
func (a *Assignments) Unassign(class Class) error {
	if _, err := a.db.Exec(`DELETE FROM storage_assignments WHERE class = ?`, string(class)); err != nil {
		return fmt.Errorf("storage: unassign %s: %w", class, err)
	}
	return nil
}

// Get returns the binding for a class.
func (a *Assignments) Get(class Class) (Assignment, error) {
	var assignment Assignment
	var rawClass, created string
	err := a.db.QueryRow(
		`SELECT class, mountpoint, actor, created_at FROM storage_assignments WHERE class = ?`,
		string(class),
	).Scan(&rawClass, &assignment.Mountpoint, &assignment.Actor, &created)
	if err == sql.ErrNoRows {
		return Assignment{}, ErrNoAssignment
	}
	if err != nil {
		return Assignment{}, fmt.Errorf("storage: get %s: %w", class, err)
	}
	assignment.Class = Class(rawClass)
	assignment.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return Assignment{}, fmt.Errorf("storage: get %s: %w", class, err)
	}
	return assignment, nil
}

// List returns every binding ordered by class.
func (a *Assignments) List() ([]Assignment, error) {
	rows, err := a.db.Query(
		`SELECT class, mountpoint, actor, created_at FROM storage_assignments ORDER BY class`)
	if err != nil {
		return nil, fmt.Errorf("storage: list: %w", err)
	}
	defer rows.Close()
	var out []Assignment
	for rows.Next() {
		var assignment Assignment
		var rawClass, created string
		if err := rows.Scan(&rawClass, &assignment.Mountpoint, &assignment.Actor, &created); err != nil {
			return nil, fmt.Errorf("storage: list: %w", err)
		}
		assignment.Class = Class(rawClass)
		assignment.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("storage: list: %w", err)
		}
		out = append(out, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list: %w", err)
	}
	return out, nil
}
