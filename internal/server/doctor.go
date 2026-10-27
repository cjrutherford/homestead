package server

import (
	"net/http"
	"time"

	"homestead/internal/doctor"
)

// MountDoctor serves explainable diagnostics (S044, S316):
// GET /api/v1/doctor[?live=1] -> {"overall", "checks", "live", "checked_at"}.
// Host discovery is stateless; extra sources (S263 certificate renewal)
// append their own checks and the overall verdict is recomputed over all
// of them. Without ?live=1 the report is cheap and local. With it, the
// probes registered by MountDoctorLive also run, each bounded, within one
// budget. Doctor only reads: it never acts.
func (s *Server) MountDoctor(extra ...func() []doctor.Check) {
	s.mux.HandleFunc("GET /api/v1/doctor", func(w http.ResponseWriter, r *http.Request) {
		report := doctor.Run()
		for _, source := range extra {
			report.Add(source()...)
		}
		if r.URL.Query().Get("live") == "1" && s.doctorProbes != nil {
			report.Add(doctor.RunLive(r.Context(), s.doctorBudget, s.doctorProbes())...)
			report.Live = true
			report.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, report)
	})
}

// MountDoctorLive registers the live probes ?live=1 runs, built per
// request so they see current state, and the budget for the whole run (zero
// is doctor.DefaultBudget).
func (s *Server) MountDoctorLive(probes func() []doctor.Probe, budget time.Duration) {
	s.doctorProbes, s.doctorBudget = probes, budget
}
