package server

import (
	"net/http"

	"homestead/internal/doctor"
)

// MountDoctor serves explainable diagnostics (S044). The doctor is a
// stateless computation over live discovery, so unlike other mounts it
// takes no service: GET /api/v1/doctor -> {"overall", "checks"}.
//
// Extra checks (S261: latest drift scan) append store-backed facts the
// host-only doctor package cannot see; the overall verdict covers them.
func (s *Server) MountDoctor(extra ...func() doctor.Check) {
	s.mux.HandleFunc("GET /api/v1/doctor", func(w http.ResponseWriter, _ *http.Request) {
		report := doctor.Run()
		for _, check := range extra {
			report.Add(check())
		}
		writeJSON(w, report)
	})
}
