package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
)

// MountAuth registers administrator authentication (S020):
//
//	POST   /api/v1/admins/setup  create the first administrator (once)
//	POST   /api/v1/sessions      log in, mint a session token
//	DELETE /api/v1/sessions      log out (Authorization: Bearer <token>)
//
// Setup needs CLAIMED state with no administrator yet: 403 unclaimed,
// 409 already set up, 422 weak credentials, 201 created. Login answers
// 200 with the single-use token or 401 without saying why. Logout
// answers 204 or 401. Requiring sessions on other endpoints belongs to
// later slices.
func (s *Server) MountAuth(svc *auth.Service, auditSvc *audit.Service) {
	s.mux.HandleFunc("POST /api/v1/admins/setup", s.handleAdminSetup(svc))
	s.mux.HandleFunc("POST /api/v1/sessions", s.handleLogin(svc, auditSvc))
	s.mux.HandleFunc("DELETE /api/v1/sessions", s.handleLogout(svc))
}

func (s *Server) handleAdminSetup(svc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Username == "" || body.Password == "" {
			http.Error(w, "invalid request body (want {\"username\", \"password\"})", http.StatusBadRequest)
			return
		}
		admin, err := svc.SetupAdmin(body.Username, body.Password)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrNotClaimed):
				http.Error(w, "appliance must be claimed first", http.StatusForbidden)
			case errors.Is(err, auth.ErrAdminExists):
				http.Error(w, "administrator already exists", http.StatusConflict)
			case errors.Is(err, auth.ErrBadUsername), errors.Is(err, auth.ErrWeakPassword):
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			default:
				http.Error(w, "failed to create administrator", http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"id": admin.ID, "username": admin.Username})
	}
}

func (s *Server) handleLogin(svc *auth.Service, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Username == "" || body.Password == "" {
			http.Error(w, "invalid request body (want {\"username\", \"password\"})", http.StatusBadRequest)
			return
		}
		session, err := svc.Login(body.Username, body.Password)
		if err != nil {
			// Failed logins are audited with the attempted username; the
			// response stays oracle-free either way. Audit failures never
			// fail the login itself — they log server-side.
			if _, recordErr := auditSvc.Record(audit.Entry{
				Actor: body.Username, Action: "admin.login",
				Outcome: audit.OutcomeError, Detail: "invalid username or password",
			}); recordErr != nil {
				s.log.Warn("audit record failed", "action", "admin.login", "error", recordErr)
			}
			if errors.Is(err, auth.ErrInvalidCredentials) {
				http.Error(w, "invalid username or password", http.StatusUnauthorized)
				return
			}
			http.Error(w, "failed to log in", http.StatusInternalServerError)
			return
		}
		if _, recordErr := auditSvc.Record(audit.Entry{
			Actor: body.Username, Action: "admin.login", Outcome: audit.OutcomeOK,
		}); recordErr != nil {
			s.log.Warn("audit record failed", "action", "admin.login", "error", recordErr)
		}
		writeJSON(w, map[string]any{
			"token":      session.Token,
			"expires_at": session.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
}

func (s *Server) handleLogout(svc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			http.Error(w, "missing bearer session", http.StatusUnauthorized)
			return
		}
		if err := svc.Logout(token); err != nil {
			if errors.Is(err, auth.ErrInvalidSession) {
				http.Error(w, "invalid or expired session", http.StatusUnauthorized)
				return
			}
			http.Error(w, "failed to log out", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func bearerToken(header string) (string, bool) {
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found || token == "" {
		return "", false
	}
	return token, true
}
