package catalog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// Old volume states.
const (
	// VolumePendingRemoval: migrated away from; kept until the owner
	// removes it.
	VolumePendingRemoval = "pending-removal"
	// VolumeRemoved: deleted by an explicit owner action.
	VolumeRemoved = "removed"
)

// OldVolumeRecord is a tracked volume left behind by a migration.
type OldVolumeRecord struct {
	Volume     string `json:"volume"`
	Package    string `json:"package"`
	Service    string `json:"service"`
	Target     string `json:"target"`
	SizeBytes  int64  `json:"size_bytes"`
	State      string `json:"state"`
	Actor      string `json:"actor"`
	MigratedAt string `json:"migrated_at"`
	RemovedAt  string `json:"removed_at,omitempty"`
}

// RecordOldVolumes marks migrated-away volumes pending removal. It is
// the last migration step: only a migration that passed its health
// check leaves volumes eligible for removal.
func RecordOldVolumes(st *store.Store, pkg string, volumes []OldVolume, actor string, now time.Time) error {
	db := st.DB()
	for _, volume := range volumes {
		if _, err := db.Exec(`INSERT INTO app_data_old_volumes
			(volume, package, service, target, size_bytes, state, actor, migrated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(volume) DO UPDATE SET package=excluded.package, service=excluded.service,
				target=excluded.target, size_bytes=excluded.size_bytes, state=excluded.state,
				actor=excluded.actor, migrated_at=excluded.migrated_at, removed_at=''`,
			volume.Name, pkg, volume.Service, volume.Target, volume.SizeBytes, VolumePendingRemoval,
			actor, now.UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("catalog: record old volume %s: %w", volume.Name, err)
		}
	}
	return nil
}

// ListOldVolumes returns tracked volumes, optionally for one package.
func ListOldVolumes(st *store.Store, pkg string) ([]OldVolumeRecord, error) {
	query := `SELECT volume, package, service, target, size_bytes, state, actor, migrated_at, removed_at
		FROM app_data_old_volumes`
	var args []any
	if pkg != "" {
		query += " WHERE package = ?"
		args = append(args, pkg)
	}
	rows, err := st.DB().Query(query+" ORDER BY package, volume", args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: list old volumes: %w", err)
	}
	defer rows.Close()
	out := []OldVolumeRecord{}
	for rows.Next() {
		var record OldVolumeRecord
		if err := rows.Scan(&record.Volume, &record.Package, &record.Service, &record.Target,
			&record.SizeBytes, &record.State, &record.Actor, &record.MigratedAt, &record.RemovedAt); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// ReferencedVolumes returns which of the named volumes any container
// (running or stopped) still mounts, with the referencing container.
func ReferencedVolumes(containers []docker.Container, names []string) map[string]string {
	want := map[string]bool{}
	for _, name := range names {
		want[name] = true
	}
	out := map[string]string{}
	for _, container := range containers {
		for _, mount := range container.Mounts {
			if mount.Type == "volume" && want[mount.Name] {
				out[mount.Name] = strings.Join(container.Names, ",")
			}
		}
	}
	return out
}

// RemoveOldVolumesRequest names the volumes an owner explicitly
// removes.
type RemoveOldVolumesRequest struct {
	Package string
	Volumes []string
	Actor   string
	// Audit records execution outcomes (the request itself is audited
	// by the caller); nil skips.
	Audit func(outcome, detail string)
}

// CheckOldVolumeRemoval validates a removal request against tracked
// records and live container references: every volume must be tracked
// as pending removal for the package, and none may still be mounted.
func CheckOldVolumeRemoval(st *store.Store, req RemoveOldVolumesRequest, containers []docker.Container) error {
	if req.Package == "" || len(req.Volumes) == 0 {
		return fmt.Errorf("catalog: removal needs a package and explicit volumes")
	}
	records, err := ListOldVolumes(st, req.Package)
	if err != nil {
		return err
	}
	state := map[string]string{}
	for _, record := range records {
		state[record.Volume] = record.State
	}
	for _, name := range req.Volumes {
		switch state[name] {
		case VolumePendingRemoval:
		case "":
			return fmt.Errorf("catalog: %s is not an old volume of %s recorded by a migration", name, req.Package)
		default:
			return fmt.Errorf("catalog: volume %s is already %s", name, state[name])
		}
	}
	if referenced := ReferencedVolumes(containers, req.Volumes); len(referenced) > 0 {
		var parts []string
		for name, container := range referenced {
			parts = append(parts, name+" (mounted by "+container+")")
		}
		return fmt.Errorf("catalog: refusing to remove volumes still in use: %s", strings.Join(parts, ", "))
	}
	return nil
}

// RemoveOldVolumesDefinition deletes the volumes, one step each,
// re-checking references at execution time.
func RemoveOldVolumesDefinition(st *store.Store, req RemoveOldVolumesRequest, now func() time.Time) (operation.Definition, error) {
	if now == nil {
		now = time.Now
	}
	def := operation.Definition{Type: "catalog.migrate.remove-volumes"}
	for _, name := range req.Volumes {
		volume := name
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "remove:" + volume,
			Run: func(ctx context.Context) (runErr error) {
				defer func() {
					if req.Audit == nil {
						return
					}
					if runErr != nil {
						req.Audit("error", fmt.Sprintf("removing volume %s of %s failed: %v", volume, req.Package, runErr))
					} else if volume == req.Volumes[len(req.Volumes)-1] {
						req.Audit("ok", fmt.Sprintf("removed old volume(s) of %s: %v", req.Package, req.Volumes))
					}
				}()
				all, err := allContainers(ctx)
				if err != nil {
					return err
				}
				if err := CheckOldVolumeRemoval(st, RemoveOldVolumesRequest{Package: req.Package, Volumes: []string{volume}, Actor: req.Actor}, all); err != nil {
					return err
				}
				if err := volumeRemove(ctx, volume); err != nil {
					return err
				}
				_, err = st.DB().Exec(`UPDATE app_data_old_volumes SET state=?, removed_at=?, actor=? WHERE volume=?`,
					VolumeRemoved, now().UTC().Format(time.RFC3339), req.Actor, volume)
				return err
			},
		})
	}
	if len(def.Steps) == 0 {
		return operation.Definition{}, fmt.Errorf("catalog: removal needs explicit volumes")
	}
	return def, nil
}

// allContainers reads every container (volumes may be referenced from
// outside the package's own project).
var allContainers = func(ctx context.Context) ([]docker.Container, error) {
	return docker.NewClient("").Containers(ctx)
}
