package server

import (
	"net/http"

	"homestead/internal/backup"
	"homestead/internal/discover"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// MountBackups serves backup configuration (S238): destination
// resolution status plus schedule records. Destinations resolve
// from the backup-class assignment (S235 surfaces own that half);
// schedules persist here. Backup runs themselves fire through the
// operation engine in later slices; these routes configure.
func (s *Server) MountBackups(st *store.Store) {
	s.mux.HandleFunc("GET /api/v1/backups/destination", handleBackupDestination(st))
	s.mux.HandleFunc("GET /api/v1/backups/schedules", handleBackupSchedules(st))
	s.mux.HandleFunc("POST /api/v1/backups/schedules", handleBackupScheduleSave(st))
	s.mux.HandleFunc("GET /api/v1/backups/schedules/{package}", handleBackupScheduleGet(st))
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
		writeJSON(w, map[string]any{"destination": destination})
	}
}

func handleBackupSchedules(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		schedules, err := backup.NewSchedules(st).List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if schedules == nil {
			schedules = []backup.Schedule{}
		}
		writeJSON(w, schedules)
	}
}

func handleBackupScheduleSave(st *store.Store) http.HandlerFunc {
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
		schedule, err := backup.NewSchedules(st).Save(backup.Schedule{
			Package: body.Package, IntervalSeconds: body.IntervalSeconds,
			Retain: body.Retain, Enabled: body.Enabled, Actor: body.Actor,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, schedule)
	}
}

func handleBackupScheduleGet(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		schedule, err := backup.NewSchedules(st).Get(r.PathValue("package"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
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
