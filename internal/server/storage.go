package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/discover"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// MountStorage serves storage setup (S235): discovered mounts,
// mount registration, and class assignment — the surfaces installs
// need before planning. Reads are live discovery; writes validate
// against it, so phantom paths refuse instead of recording.
func (s *Server) MountStorage(st *store.Store) {
	s.mux.HandleFunc("GET /api/v1/storage/mounts", handleStorageMounts())
	s.mux.HandleFunc("GET /api/v1/storage/registrations", handleStorageRegistrations(st))
	s.mux.HandleFunc("POST /api/v1/storage/registrations", handleStorageRegister(st))
	s.mux.HandleFunc("GET /api/v1/storage/assignments", handleStorageAssignments(st))
	s.mux.HandleFunc("POST /api/v1/storage/assignments", handleStorageAssign(st, audit.New(st)))
}

// storageMountView adds registrability to a discovered mount. The
// embedded fields keep their PascalCase names, so existing clients are
// unaffected; the new fields are snake_case.
type storageMountView struct {
	discover.Mount
	Registrable       bool   `json:"registrable"`
	RegistrableReason string `json:"registrable_reason"`
}

func storageMountViews(mounts []discover.Mount) []storageMountView {
	out := make([]storageMountView, 0, len(mounts))
	for _, mount := range mounts {
		view := storageMountView{Mount: mount, Registrable: true}
		if err := storage.Registrable(mount); err != nil {
			view.Registrable = false
			view.RegistrableReason = err.Error()
		}
		out = append(out, view)
	}
	return out
}

// storageImpact is the 409 body: installed packages whose data sits on
// the mount a class would leave.
type storageImpact struct {
	Error            string   `json:"error"`
	Class            string   `json:"class"`
	From             string   `json:"from"`
	To               string   `json:"to"`
	AffectedPackages []string `json:"affected_packages"`
}

// affectedByReassign lists installed packages holding `<from>/<pkg>`
// data when `from` would no longer be bound to any class. Backups
// follow the current class bindings, so those packages would stop
// finding their data. The backup class is exempt: its package
// directories hold backup runs, not application data.
func affectedByReassign(st *store.Store, class storage.Class, from string, assignments []storage.Assignment) ([]string, error) {
	if class == storage.ClassBackup {
		return nil, nil
	}
	for _, other := range assignments {
		if other.Class != class && other.Mountpoint == from {
			return nil, nil // another class keeps the mount in play
		}
	}
	stacks, err := catalog.InstalledStacks(st)
	if err != nil {
		return nil, err
	}
	var affected []string
	for _, name := range stacks {
		if info, err := os.Stat(filepath.Join(from, name)); err == nil && info.IsDir() {
			affected = append(affected, name)
		}
	}
	return affected, nil
}

func handleStorageMounts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		discovered, err := discover.DiscoverStorage()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, storageMountViews(discovered.Mounts))
	}
}

func handleStorageRegistrations(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		registrations, err := storage.NewRegistry(st).List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if registrations == nil {
			registrations = []storage.Registration{}
		}
		writeJSON(w, registrations)
	}
}

func handleStorageRegister(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mountpoint string `json:"mountpoint"`
			Label      string `json:"label"`
			Actor      string `json:"actor"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		discovered, err := discover.DiscoverStorage()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		registration, err := storage.NewRegistry(st).Register(body.Mountpoint, body.Label, body.Actor, discovered)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, registration)
	}
}

func handleStorageAssignments(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assignments, err := storage.NewAssignments(st).List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if assignments == nil {
			assignments = []storage.Assignment{}
		}
		writeJSON(w, assignments)
	}
}

func handleStorageAssign(st *store.Store, auditor auditRecorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Class      storage.Class `json:"class"`
			Mountpoint string        `json:"mountpoint"`
			Actor      string        `json:"actor"`
			// AcknowledgeImpact is the owner's explicit consent to move a
			// class off a mount that installed packages still use.
			AcknowledgeImpact bool `json:"acknowledge_impact"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !body.Class.Valid() {
			http.Error(w, "unknown storage class", http.StatusBadRequest)
			return
		}
		assignments := storage.NewAssignments(st)
		if current, err := assignments.Get(body.Class); err == nil && current.Mountpoint != body.Mountpoint {
			all, err := assignments.List()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			affected, err := affectedByReassign(st, body.Class, current.Mountpoint, all)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if len(affected) > 0 {
				if !body.AcknowledgeImpact {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(storageImpact{
						Error: fmt.Sprintf("moving %s off %s affects installed packages: their backups stop finding data there; re-send with acknowledge_impact to proceed",
							body.Class, current.Mountpoint),
						Class: string(body.Class), From: current.Mountpoint, To: body.Mountpoint, AffectedPackages: affected,
					})
					return
				}
				if _, err := auditor.Record(audit.Entry{
					Actor: body.Actor, Action: "storage.assign.impact_acknowledged", Outcome: audit.OutcomeOK,
					Detail: fmt.Sprintf("%s moved from %s to %s; affected packages: %v", body.Class, current.Mountpoint, body.Mountpoint, affected),
				}); err != nil {
					http.Error(w, "audit: "+err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
		discovered, err := discover.DiscoverStorage()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		assignment, err := storage.NewAssignments(st).Assign(body.Class, body.Mountpoint, body.Actor, storage.NewRegistry(st), discovered, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, assignment)
	}
}
