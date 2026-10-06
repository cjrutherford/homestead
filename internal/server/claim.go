package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"homestead/internal/claim"
	"homestead/internal/version"
)

// MountClaim registers the appliance identity and claim endpoints
// (S019; ADR-015). Identity is always available; claim redeems the
// single-use console token and flips UNCLAIMED to CLAIMED:
//
//	GET  /api/v1/identity  {"state", "version"}
//	POST /api/v1/claim     {"token"} -> 200 {"state": "CLAIMED"}
//
// Failures: 400 malformed or empty token, 401 unknown/expired token,
// 409 already claimed.
func (s *Server) MountClaim(svc *claim.Service) {
	s.mux.HandleFunc("GET /api/v1/identity", s.handleIdentity(svc))
	s.mux.HandleFunc("POST /api/v1/claim", s.handleClaim(svc))
}

func (s *Server) handleIdentity(svc *claim.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		state, err := svc.CurrentState()
		if err != nil {
			http.Error(w, "failed to read appliance state", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"state": state.String(), "version": version.Version})
	}
}

func (s *Server) handleClaim(svc *claim.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Token == "" {
			http.Error(w, "invalid request body (want {\"token\": ...})", http.StatusBadRequest)
			return
		}
		if err := svc.Claim(body.Token); err != nil {
			switch {
			case errors.Is(err, claim.ErrAlreadyClaimed):
				http.Error(w, "appliance already claimed", http.StatusConflict)
			case errors.Is(err, claim.ErrTokenInvalid), errors.Is(err, claim.ErrTokenExpired):
				http.Error(w, "invalid or expired claim token", http.StatusUnauthorized)
			default:
				http.Error(w, "failed to claim appliance", http.StatusInternalServerError)
			}
			return
		}
		writeJSON(w, map[string]any{"state": claim.StateClaimed.String()})
	}
}
