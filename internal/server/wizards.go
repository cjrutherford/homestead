package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"homestead/internal/wizard"
)

// MountWizards serves per-wizard progress state (S036) for resumable flows:
//
//	GET    /api/v1/wizards/{id}  saved state or 404
//	PUT    /api/v1/wizards/{id}  {"step", "payload"} -> saved state
//	DELETE /api/v1/wizards/{id}  forget (always 204)
//
// Steps and payloads are opaque here; each wizard validates its own.
// Payloads hold UI progress only — never secrets (S019/S020).
func (s *Server) MountWizards(svc *wizard.Service) {
	s.mux.HandleFunc("GET /api/v1/wizards/{id}", s.handleWizardGet(svc))
	s.mux.HandleFunc("PUT /api/v1/wizards/{id}", s.handleWizardPut(svc))
	s.mux.HandleFunc("DELETE /api/v1/wizards/{id}", s.handleWizardDelete(svc))
}

func wizardStateResponse(state wizard.State) map[string]any {
	return map[string]any{
		"wizard":     state.Wizard,
		"step":       state.Step,
		"payload":    state.Payload,
		"updated_at": state.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

func (s *Server) handleWizardGet(svc *wizard.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := svc.Get(r.PathValue("id"))
		if err != nil {
			if errors.Is(err, wizard.ErrNotFound) {
				http.Error(w, "no saved wizard state", http.StatusNotFound)
				return
			}
			http.Error(w, "invalid wizard id", http.StatusBadRequest)
			return
		}
		writeJSON(w, wizardStateResponse(state))
	}
}

func (s *Server) handleWizardPut(svc *wizard.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Step    string         `json:"step"`
			Payload map[string]any `json:"payload"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Step == "" {
			http.Error(w, "invalid request body (want {\"step\", \"payload\"})", http.StatusBadRequest)
			return
		}
		if body.Payload == nil {
			body.Payload = map[string]any{}
		}
		state, err := svc.Put(r.PathValue("id"), body.Step, body.Payload)
		if err != nil {
			if errors.Is(err, wizard.ErrBadID) || errors.Is(err, wizard.ErrBadStep) {
				http.Error(w, "invalid wizard state", http.StatusBadRequest)
				return
			}
			http.Error(w, "failed to save wizard state", http.StatusInternalServerError)
			return
		}
		writeJSON(w, wizardStateResponse(state))
	}
}

func (s *Server) handleWizardDelete(svc *wizard.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Delete(r.PathValue("id")); err != nil {
			if errors.Is(err, wizard.ErrBadID) {
				http.Error(w, "invalid wizard id", http.StatusBadRequest)
				return
			}
			http.Error(w, "failed to forget wizard state", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
