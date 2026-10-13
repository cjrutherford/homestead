package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"homestead/internal/auth"
)

// MountRecovery registers administrator recovery (S021) on the Unix-socket
// mux only: the 0600 socket file restricts callers to root, so this route
// never appears on TCP. Break-glass semantics — no claim-state gate, no
// session — with the existing administrator required to exist:
//
//	POST /api/v1/admins/recover  {"username", "password"} -> 200 {"id", "username"}
//	404 unknown administrator, 422 weak input, 400 malformed.
func (s *Server) MountRecovery(svc *auth.Service) {
	s.socketMux.HandleFunc("POST /api/v1/admins/recover", s.handleRecover(svc))
}

func (s *Server) handleRecover(svc *auth.Service) http.HandlerFunc {
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
		admin, err := svc.Recover(body.Username, body.Password)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrAdminNotFound):
				http.Error(w, "administrator not found", http.StatusNotFound)
			case errors.Is(err, auth.ErrBadUsername), errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrPasswordTooLong):
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			default:
				http.Error(w, "failed to recover administrator", http.StatusInternalServerError)
			}
			return
		}
		writeJSON(w, map[string]any{"id": admin.ID, "username": admin.Username})
	}
}
