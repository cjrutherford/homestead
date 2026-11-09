package server

import (
	"encoding/json"
	"errors"
	"fmt"
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
// Session transport (ADR-020, S256): login sets an HttpOnly,
// SameSite=Strict cookie (Secure when the request arrived over TLS)
// for the browser and returns the same token in the body as a bearer
// for the CLI; both resolve to one session. A second, script-readable
// CSRF cookie carries the per-session token the dashboard echoes in
// X-CSRF-Token on cookie-authenticated mutations. Sessions live 12h
// idle / 7d absolute and are revoked by logout and password change.
//
// Setup needs CLAIMED state with no administrator yet: 403 unclaimed,
// 409 already set up, 422 weak credentials, 201 created and logged in
// (same cookies and token as login). Login answers 200 with the
// reusable token or 401 without saying why. Logout, password change and
// lockout status are session routes: the boundary (ADR-020, S257)
// rejects them without a valid session and hands the verified session to
// the handler through the request context.
func (s *Server) MountAuth(svc *auth.Service, auditSvc *audit.Service) {
	s.hasAdmin = svc.HasAdmin
	s.mux.HandleFunc("POST /api/v1/admins/setup", s.handleAdminSetup(svc, auditSvc))
	s.mux.HandleFunc("POST /api/v1/sessions", s.handleLogin(svc, auditSvc))
	s.mux.HandleFunc("DELETE /api/v1/sessions", s.handleLogout(svc, auditSvc))
	s.mux.HandleFunc("GET /api/v1/me", s.handleMe())
	s.mux.HandleFunc("POST /api/v1/admins/password", s.handleChangePassword(svc))
	s.mux.HandleFunc("GET /api/v1/admins/lockout", s.handleLockoutStatus(svc))
}

// ErrorCodeHeader carries a stable machine-readable error code on
// responses whose status alone is ambiguous.
const (
	ErrorCodeHeader                 = "X-Homestead-Error"
	ErrorCodeInvalidCurrentPassword = "invalid-current-password"
	// ErrorCodeOlderRelease marks a platform apply or rollback refused
	// only because it moves to an older counter without the owner override.
	ErrorCodeOlderRelease = "older-release-needs-override"
)

func (s *Server) handleAdminSetup(svc *auth.Service, auditSvc *audit.Service) http.HandlerFunc {
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
			case errors.Is(err, auth.ErrBadUsername), errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrPasswordTooLong):
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			default:
				http.Error(w, "failed to create administrator", http.StatusInternalServerError)
			}
			return
		}
		resp := map[string]any{"id": admin.ID, "username": admin.Username}
		// The owner is logged in by setup (ADR-020, S257): the claim
		// wizard keeps saving after the first administrator exists and
		// every non-public route now needs a session. Same transport
		// as login: cookies for the browser, token in the body for the
		// CLI. A failure here leaves the account created; the owner
		// can still log in.
		if session, err := svc.Login(body.Username, body.Password); err != nil {
			s.log.Warn("first-admin setup: automatic login failed", "error", err)
		} else {
			if _, recordErr := auditSvc.Record(audit.Entry{
				Actor: body.Username, Action: "admin.login", Outcome: audit.OutcomeOK,
				Detail: "first-admin setup",
			}); recordErr != nil {
				s.log.Warn("audit record failed", "action", "admin.login", "error", recordErr)
			}
			setSessionCookies(w, r, session)
			resp["token"] = session.Token
			resp["csrf_token"] = session.CSRFToken
			resp["expires_at"] = session.ExpiresAt.UTC().Format(time.RFC3339)
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, resp)
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
				Outcome: audit.OutcomeError, Detail: viaEdge(r, "invalid username or password"),
			}); recordErr != nil {
				s.log.Warn("audit record failed", "action", "admin.login", "error", recordErr)
			}
			if errors.Is(err, auth.ErrInvalidCredentials) {
				http.Error(w, "invalid username or password", http.StatusUnauthorized)
				return
			}
			if errors.Is(err, auth.ErrAccountLocked) {
				http.Error(w, "account temporarily locked after repeated failures", http.StatusTooManyRequests)
				return
			}
			http.Error(w, "failed to log in", http.StatusInternalServerError)
			return
		}
		if _, recordErr := auditSvc.Record(audit.Entry{
			Actor: body.Username, Action: "admin.login", Outcome: audit.OutcomeOK, Detail: viaEdge(r, ""),
		}); recordErr != nil {
			s.log.Warn("audit record failed", "action", "admin.login", "error", recordErr)
		}
		setSessionCookies(w, r, session)
		writeJSON(w, map[string]any{
			"token":      session.Token,
			"csrf_token": session.CSRFToken,
			"expires_at": session.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
}

// requireOperator returns the session the boundary verified. The
// middleware has already rejected unauthenticated requests; a missing
// value means the handler is mounted without the boundary, which fails
// closed.
func requireOperator(w http.ResponseWriter, r *http.Request) (operatorSession, bool) {
	op, ok := requireSession(w, r)
	if ok && op.Session.Role == auth.RolePerson {
		// The boundary already refuses person sessions; this keeps a
		// handler mounted behind a mistake from serving one (ADR-050 §2).
		w.Header().Set(ErrorCodeHeader, ErrorCodeOwnerOnly)
		http.Error(w, "this is for the owner of this Homestead", http.StatusForbidden)
		return operatorSession{}, false
	}
	return op, ok
}

// requireSession is requireOperator for the few routes a person session
// may also reach (personRoutes).
func requireSession(w http.ResponseWriter, r *http.Request) (operatorSession, bool) {
	op, ok := operatorFromContext(r.Context())
	if !ok {
		http.Error(w, "missing session", http.StatusUnauthorized)
	}
	return op, ok
}

// handleMe says who the session is: the role and display name (ADR-050
// §2). Both owner and person sessions may ask.
func (s *Server) handleMe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		op, ok := requireSession(w, r)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if op.Session.Role == auth.RolePerson {
			writeJSON(w, map[string]any{
				"role": auth.RolePerson, "display_name": op.Session.DisplayName,
				"username": op.Session.PersonUsername, "person_id": op.Session.PersonID,
			})
			return
		}
		writeJSON(w, map[string]any{
			"role": auth.RoleOwner, "display_name": op.Session.Username,
			"username": op.Session.Username, "method": op.Session.Method,
		})
	}
}

func (s *Server) handleLogout(svc *auth.Service, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		op, ok := requireSession(w, r)
		if !ok {
			return
		}
		if err := svc.Logout(op.Token); err != nil {
			if errors.Is(err, auth.ErrInvalidSession) {
				http.Error(w, "invalid or expired session", http.StatusUnauthorized)
				return
			}
			http.Error(w, "failed to log out", http.StatusInternalServerError)
			return
		}
		if op.Session.Role == auth.RolePerson {
			if _, err := auditSvc.Record(audit.Entry{
				Actor: op.Session.PersonUsername, Action: "session.logout", Outcome: audit.OutcomeOK,
				Detail: viaEdge(r, fmt.Sprintf("role=person person=%d name=%s", op.Session.PersonID, op.Session.DisplayName)),
			}); err != nil {
				s.log.Warn("audit record failed", "action", "session.logout", "error", err)
			}
		}
		clearSessionCookies(w, r)
		w.WriteHeader(http.StatusNoContent)
	}
}

// Cookie names (ADR-020). The session cookie is HttpOnly; the CSRF
// cookie is deliberately readable by the dashboard script.
const (
	SessionCookieName = "homestead_session"
	CSRFCookieName    = "homestead_csrf"
	// HostSessionCookieName and HostCSRFCookieName are the names over TLS
	// (ADR-061 §4, S470): the __Host- prefix makes a browser refuse to
	// store them unless they are Secure, Path=/ and without a Domain, so
	// another subdomain or a plain-HTTP page cannot overwrite them. A
	// plain-HTTP request cannot carry a Secure cookie, so it keeps the
	// unprefixed names; the daemon reads only the names that fit the scheme.
	HostSessionCookieName = "__Host-" + SessionCookieName
	HostCSRFCookieName    = "__Host-" + CSRFCookieName
	// CSRFHeader carries the CSRF token on cookie-authenticated
	// mutating requests.
	CSRFHeader = "X-CSRF-Token"
)

// requestIsTLS reports whether the request reached us over TLS, directly
// or via a TLS-terminating proxy (the tunnel) that says so. A spoofed
// header can only make the cookie Secure, never weaker.
func requestIsTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setSessionCookies(w http.ResponseWriter, r *http.Request, session auth.Session) {
	setSessionCookiesAs(w, requestIsTLS(r), session)
}

// setSessionCookiesAs sets the ADR-020 cookies with an explicit Secure
// flag: the SSO callback knows its scheme from the planned origin, not
// from request headers.
func setSessionCookiesAs(w http.ResponseWriter, secure bool, session auth.Session) {
	sessName, csrfName := cookieNames(secure)
	http.SetCookie(w, &http.Cookie{
		Name: sessName, Value: session.Token, Path: "/",
		Expires: session.ExpiresAt, HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfName, Value: session.CSRFToken, Path: "/",
		Expires: session.ExpiresAt, HttpOnly: false, Secure: secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// cookieNames are the session and CSRF cookie names for a scheme.
func cookieNames(secure bool) (session, csrf string) {
	if secure {
		return HostSessionCookieName, HostCSRFCookieName
	}
	return SessionCookieName, CSRFCookieName
}

// sessionCookie is the request's session cookie under the name that fits
// its scheme.
func sessionCookie(r *http.Request) (*http.Cookie, error) {
	name, _ := cookieNames(requestIsTLS(r))
	return r.Cookie(name)
}

// clearSessionCookies expires both names, so a cookie from before the
// rename goes away at the next sign-out.
func clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	secure := requestIsTLS(r)
	for _, name := range []string{HostSessionCookieName, HostCSRFCookieName, SessionCookieName, CSRFCookieName} {
		if !secure && strings.HasPrefix(name, "__Host-") {
			continue
		}
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(0, 0),
			HttpOnly: name == SessionCookieName || name == HostSessionCookieName, Secure: secure,
			SameSite: http.SameSiteStrictMode,
		})
	}
}

// sessionCredential extracts the session token from the Authorization
// bearer header, else from the session cookie. viaCookie tells callers
// whether the credential was ambient (browser) and so needs CSRF.
func sessionCredential(r *http.Request) (token string, viaCookie, ok bool) {
	if t, found := bearerToken(r.Header.Get("Authorization")); found {
		return t, false, true
	}
	if c, err := sessionCookie(r); err == nil && c.Value != "" {
		return c.Value, true, true
	}
	return "", false, false
}

func bearerToken(header string) (string, bool) {
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found || token == "" {
		return "", false
	}
	return token, true
}

func (s *Server) handleChangePassword(svc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		op, ok := requireOperator(w, r)
		if !ok {
			return
		}
		var body struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.CurrentPassword == "" || body.NewPassword == "" {
			http.Error(w, "invalid request body (want {\"current_password\", \"new_password\"})", http.StatusBadRequest)
			return
		}
		// Self-service only: the session owner is the only identity
		// a caller may rotate, so sessions never escalate.
		admin, err := svc.ChangePasswordKeepingSession(op.Session.Username, body.CurrentPassword, body.NewPassword, op.Token)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrInvalidCredentials):
				// The code tells this 401 (wrong current password) apart
				// from the boundary's 401 (bad session).
				w.Header().Set(ErrorCodeHeader, ErrorCodeInvalidCurrentPassword)
				http.Error(w, "invalid username or password", http.StatusUnauthorized)
			case errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrPasswordTooLong):
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			case errors.Is(err, auth.ErrAccountLocked):
				http.Error(w, "account temporarily locked after repeated failures", http.StatusTooManyRequests)
			default:
				http.Error(w, "failed to change password", http.StatusInternalServerError)
			}
			return
		}
		s.RevokeCraftSessions(admin.Username)
		writeJSON(w, map[string]any{"id": admin.ID, "username": admin.Username})
	}
}

func (s *Server) handleLockoutStatus(svc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		op, ok := requireOperator(w, r)
		if !ok {
			return
		}
		info, err := svc.LockoutDetail(op.Session.Username)
		if err != nil {
			http.Error(w, "failed to read lockout", http.StatusInternalServerError)
			return
		}
		out := map[string]any{"username": op.Session.Username, "locked": info.Locked, "failures": info.Failures}
		if info.Locked {
			out["locked_until"] = info.LockedUntil.UTC().Format(time.RFC3339)
		}
		writeJSON(w, out)
	}
}
