package server

import (
	"net/http"

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
	s.mux.HandleFunc("POST /api/v1/storage/assignments", handleStorageAssign(st))
}

func handleStorageMounts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		discovered, err := discover.DiscoverStorage()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mounts := discovered.Mounts
		if mounts == nil {
			mounts = []discover.Mount{}
		}
		writeJSON(w, mounts)
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

func handleStorageAssign(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Class      storage.Class `json:"class"`
			Mountpoint string        `json:"mountpoint"`
			Actor      string        `json:"actor"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !body.Class.Valid() {
			http.Error(w, "unknown storage class", http.StatusBadRequest)
			return
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
