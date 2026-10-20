package server

import (
	"errors"
	"net/http"

	"homestead/internal/docker"
	"homestead/internal/exposure"
	"homestead/internal/operation"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// MountExposure serves exposure review, approval, and application
// (S245): the reviewed plan from saved wizard inputs, recorded
// ownership decisions, and firewall-plus-edge application through
// the operation engine. Review recomputes from inputs every call,
// so it never goes stale; application returns operation IDs
// instead of blocking.
func (s *Server) MountExposure(st *store.Store, daemonPort int) {
	wiz := exposure.NewWizard(wizard.New(st))
	s.mux.HandleFunc("GET /api/v1/exposure/review", handleExposureReview(wiz))
	s.mux.HandleFunc("GET /api/v1/exposure/approvals", handleExposureApprovals(st))
	s.mux.HandleFunc("POST /api/v1/exposure/approvals", handleExposureApprove(st, wiz))
	s.mux.HandleFunc("POST /api/v1/exposure/apply", handleExposureApply(st, daemonPort))
}

func handleExposureReview(wiz *exposure.Wizard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		plan, err := wiz.Plan()
		if err != nil {
			if errors.Is(err, wizard.ErrNotFound) {
				http.Error(w, "no exposure review saved", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, plan)
	}
}

func handleExposureApprovals(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		approvals, err := exposure.Approvals(st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		type summary struct {
			Host  string `json:"host"`
			Actor string `json:"actor"`
		}
		out := []summary{}
		for _, approval := range approvals {
			out = append(out, summary{Host: string(approval.Host), Actor: approval.Actor})
		}
		writeJSON(w, out)
	}
}

func handleExposureApprove(st *store.Store, wiz *exposure.Wizard) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Actor    string   `json:"actor"`
			Networks []string `json:"networks"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		plan, err := wiz.Plan()
		if err != nil {
			if errors.Is(err, wizard.ErrNotFound) {
				http.Error(w, "no exposure review saved", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if plan.To == docker.ExposurePublic {
			if set, lerr := loadEdgeRoutes(st); lerr == nil {
				if dash, ok := set.DashboardRoute(); ok {
					for _, n := range dash.Names() {
						if n == plan.Host {
							http.Error(w, "exposure: "+docker.ErrDashboardPublic, http.StatusUnprocessableEntity)
							return
						}
					}
				}
			}
		}
		approval, err := exposure.Approve(plan, body.Actor, body.Networks)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if err := exposure.RecordApproval(st, approval); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"host": string(approval.Host), "actor": approval.Actor})
	}
}

func handleExposureApply(st *store.Store, daemonPort int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Host docker.Hostname `json:"host"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Host == "" {
			http.Error(w, "host must not be empty", http.StatusBadRequest)
			return
		}
		def, err := exposure.ApplyDefinition(st, body.Host, daemonPort, docker.DirFor("caddy"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		runner := operation.NewRunner(st)
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
}
