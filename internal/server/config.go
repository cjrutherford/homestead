package server

import (
	"encoding/json"
	"net/http"

	"homestead/internal/config"
	"homestead/internal/desired"
	"homestead/internal/schema"
)

// MountConfig registers the validated configuration API (S016; ADR-004):
//
//	GET  /api/v1/config       read current desired state
//	POST /api/v1/config/plan  validate a candidate without persisting
//	PUT  /api/v1/config       validate and atomically apply a candidate
//
// Validation failures answer 422 with structured errors; malformed bodies
// answer 400. Apply persists nothing when validation fails.
func (s *Server) MountConfig(svc *config.Service) {
	s.mux.HandleFunc("GET /api/v1/config", s.handleConfigGet(svc))
	s.mux.HandleFunc("GET /api/v1/config/schema", s.handleConfigSchema(svc))
	s.mux.HandleFunc("POST /api/v1/config/plan", s.handleConfigPlan(svc))
	s.mux.HandleFunc("PUT /api/v1/config", s.handleConfigApply(svc))
}

type configRequest struct {
	Settings map[string]any `json:"settings"`
}

type planResponse struct {
	Valid     bool                `json:"valid"`
	Effective map[string]any      `json:"effective,omitempty"`
	Errors    []schema.FieldError `json:"errors,omitempty"`
}

func decodeSettings(r *http.Request) (map[string]any, error) {
	var body configRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	if body.Settings == nil {
		body.Settings = map[string]any{}
	}
	return body.Settings, nil
}

func writePlan(w http.ResponseWriter, plan config.Plan) {
	if !plan.Valid {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	writeJSON(w, planResponse{Valid: plan.Valid, Effective: plan.Effective, Errors: plan.Errors})
}

func (s *Server) handleConfigGet(svc *config.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		state, err := svc.Get()
		if err != nil {
			http.Error(w, "failed to read configuration", http.StatusInternalServerError)
			return
		}
		writeJSON(w, stateResponse(state))
	}
}

// handleConfigSchema serves field metadata for schema-driven UIs (S034).
func (s *Server) handleConfigSchema(svc *config.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		fields := svc.Schema().Fields
		if fields == nil {
			fields = []schema.Field{}
		}
		writeJSON(w, map[string]any{"fields": fields})
	}
}

func (s *Server) handleConfigPlan(svc *config.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := decodeSettings(r)
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		writePlan(w, svc.Plan(settings))
	}
}

func (s *Server) handleConfigApply(svc *config.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := decodeSettings(r)
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		state, fieldErrs, err := svc.Apply(settings)
		if err != nil {
			http.Error(w, "failed to persist configuration", http.StatusInternalServerError)
			return
		}
		if len(fieldErrs) > 0 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			writeJSON(w, planResponse{Valid: false, Errors: fieldErrs})
			return
		}
		writeJSON(w, stateResponse(state))
	}
}

func stateResponse(state desired.State) map[string]any {
	settings := state.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	return map[string]any{"version": state.Version, "settings": settings}
}
