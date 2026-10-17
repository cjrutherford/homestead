package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/discover"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// MountBackups serves backup configuration (S238): destination
// resolution status plus schedule records. Destinations resolve
// from the backup-class assignment (S235 surfaces own that half);
// schedules persist here. The daemon scheduler fires them through the
// operation engine (S260); these routes configure.
func (s *Server) MountBackups(st *store.Store, catalogDir string) {
	s.mux.HandleFunc("GET /api/v1/backups/destination", handleBackupDestination(st))
	s.mux.HandleFunc("GET /api/v1/backups/schedules", handleBackupSchedules(st, catalogDir))
	s.mux.HandleFunc("POST /api/v1/backups/schedules", handleBackupScheduleSave(st, catalogDir))
	s.mux.HandleFunc("GET /api/v1/backups/schedules/{package}", handleBackupScheduleGet(st, catalogDir))
	s.mux.HandleFunc("DELETE /api/v1/backups/schedules/{package}", handleBackupScheduleDelete(st))
}

func handleBackupDestination(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		discovered, err := discover.DiscoverStorage()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		destination, err := backup.ResolveDestination(storage.NewAssignments(st), discovered)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		// Backups are always age-encrypted (S259, ADR-022); the format
		// version tells readers which layout new runs use.
		writeJSON(w, map[string]any{
			"destination": destination, "encrypted": true,
			"format_version": backup.CurrentFormat,
		})
	}
}

// statelessPackage reports whether the named package is in the catalog and
// stateless (ADR-028). An unreadable catalog or an unknown package reads as
// stateful: schedules for packages the catalog cannot vouch for keep their
// historic behavior.
func statelessPackage(catalogDir, name string) bool {
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return false
	}
	manifest, err := entries.Get(name)
	return err == nil && manifest.Stateless()
}

func handleBackupSchedules(st *store.Store, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		schedules, err := backup.NewSchedules(st).List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if schedules == nil {
			schedules = []backup.Schedule{}
		}
		for i := range schedules {
			schedules[i].Stateless = statelessPackage(catalogDir, schedules[i].Package)
		}
		writeJSON(w, schedules)
	}
}

func handleBackupScheduleSave(st *store.Store, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Package         string `json:"package"`
			IntervalSeconds int64  `json:"interval_seconds"`
			Retain          int    `json:"retain"`
			Enabled         bool   `json:"enabled"`
			Actor           string `json:"actor"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// A stateless package keeps nothing to back up: refuse to schedule
		// it, naming it (ADR-028).
		if statelessPackage(catalogDir, strings.TrimSpace(body.Package)) {
			http.Error(w, fmt.Sprintf("%v: %q keeps nothing on the appliance, so it has no backup schedule",
				backup.ErrStateless, strings.TrimSpace(body.Package)), http.StatusUnprocessableEntity)
			return
		}
		schedule, err := backup.NewSchedules(st).Save(backup.Schedule{
			Package: body.Package, IntervalSeconds: body.IntervalSeconds,
			Retain: body.Retain, Enabled: body.Enabled, Actor: body.Actor,
		})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, backup.ErrInvalidSchedule) {
				status = http.StatusUnprocessableEntity
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, schedule)
	}
}

func handleBackupScheduleGet(st *store.Store, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("package")
		schedule, err := backup.NewSchedules(st).Get(name)
		if statelessPackage(catalogDir, name) {
			// An explicit no-op, not a 404: nothing is scheduled and nothing
			// ever will be for a package that keeps no data.
			if err != nil {
				schedule = backup.Schedule{Package: name}
			}
			schedule.Stateless = true
			writeJSON(w, schedule)
			return
		}
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, backup.ErrScheduleNotFound) {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, schedule)
	}
}

func handleBackupScheduleDelete(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := backup.NewSchedules(st).Delete(r.PathValue("package")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
