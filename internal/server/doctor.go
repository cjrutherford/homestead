package server

import (
	"net/http"

	"homestead/internal/doctor"
)

// MountDoctor serves explainable diagnostics (S044). The doctor is a
// stateless computation over live discovery, so unlike other mounts it
// takes no service: GET /api/v1/doctor -> {"overall", "checks"}.
func (s *Server) MountDoctor() {
	s.mux.HandleFunc("GET /api/v1/doctor", handleDoctor)
}

func handleDoctor(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, doctor.Run())
}
