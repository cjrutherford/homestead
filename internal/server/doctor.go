package server

import (
	"net/http"

	"homestead/internal/doctor"
)

// MountDoctor serves explainable diagnostics (S044):
// GET /api/v1/doctor -> {"overall", "checks"}. Host discovery is
// stateless; extra sources (S263 certificate renewal) append their own
// checks and the overall verdict is recomputed over all of them.
func (s *Server) MountDoctor(extra ...func() []doctor.Check) {
	s.mux.HandleFunc("GET /api/v1/doctor", func(w http.ResponseWriter, _ *http.Request) {
		report := doctor.Run()
		for _, source := range extra {
			report.Add(source()...)
		}
		writeJSON(w, report)
	})
}
