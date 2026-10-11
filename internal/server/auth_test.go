package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/store"
)

func authServer(t *testing.T) (*Server, *claim.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	claimSvc := claim.New(st)
	srv := New("127.0.0.1:0")
	srv.MountClaim(claimSvc)
	authSvc := auth.New(st, claimSvc)
	srv.MountAuth(authSvc, audit.New(st))
	srv.MountSessionBoundary(authSvc, claimSvc, audit.New(st))
	return srv, claimSvc
}

func claimedAuthServer(t *testing.T) *Server {
	t.Helper()
	srv, claimSvc := authServer(t)
	token, err := claimSvc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := claimSvc.Claim(token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return srv
}

func doJSON(t *testing.T, srv *Server, method, target, payload, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(payload))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAdminSetupFlow(t *testing.T) {
	srv := claimedAuthServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/admins/setup", `{"username":"owner","password":"long-enough-password"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"owner"`) {
		t.Fatalf("body = %s, want username", rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/admins/setup", `{"username":"second","password":"another-long-password"}`, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("second setup status = %d, want 409", rec.Code)
	}
}

func TestAdminSetupRequiresClaim(t *testing.T) {
	srv, _ := authServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/admins/setup", `{"username":"owner","password":"long-enough-password"}`, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("setup status = %d, want 403", rec.Code)
	}
}

func TestAdminSetupValidatesInput(t *testing.T) {
	srv := claimedAuthServer(t)
	for _, tc := range []struct {
		name    string
		payload string
		status  int
	}{
		{"malformed", "{oops", http.StatusBadRequest},
		{"missing password", `{"username":"owner"}`, http.StatusBadRequest},
		{"bad username", `{"username":"Bad!","password":"long-enough-password"}`, http.StatusUnprocessableEntity},
		{"weak password", `{"username":"owner","password":"short"}`, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := doJSON(t, srv, http.MethodPost, "/api/v1/admins/setup", tc.payload, ""); rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}

func TestLoginLogoutFlow(t *testing.T) {
	srv := claimedAuthServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/admins/setup", `{"username":"owner","password":"long-enough-password"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, want 201", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/sessions", `{"username":"owner","password":"long-enough-password"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&login); err != nil || login.Token == "" {
		t.Fatalf("login body = %q, err = %v; want token", rec.Body.String(), err)
	}
	token := login.Token
	rec = doJSON(t, srv, http.MethodDelete, "/api/v1/sessions", "", token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodDelete, "/api/v1/sessions", "", token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("second logout status = %d, want 401", rec.Code)
	}
}

func TestLoginFailures(t *testing.T) {
	srv := claimedAuthServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/admins/setup", `{"username":"owner","password":"long-enough-password"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, want 201", rec.Code)
	}
	for _, payload := range []string{
		`{"username":"nobody","password":"long-enough-password"}`,
		`{"username":"owner","password":"wrong-password-here"}`,
	} {
		if rec := doJSON(t, srv, http.MethodPost, "/api/v1/sessions", payload, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("login status = %d, want 401", rec.Code)
		}
	}
}

// TestLoginLockoutStatus proves repeated failures trip 429 instead
// of the 500 the unmapped sentinel once produced: throttling is a
// client signal, never a server error.
func TestLoginLockoutStatus(t *testing.T) {
	srv := claimedAuthServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/admins/setup", `{"username":"owner","password":"long-enough-password"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, want 201", rec.Code)
	}
	for range 4 {
		rec = doJSON(t, srv, http.MethodPost, "/api/v1/sessions", `{"username":"owner","password":"wrong-password-here"}`, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("login status = %d, want 401", rec.Code)
		}
	}
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/sessions", `{"username":"owner","password":"wrong-password-here"}`, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("login status = %d, want 429", rec.Code)
	}
}

func TestLogoutRequiresBearer(t *testing.T) {
	srv := claimedAuthServer(t)
	rec := doJSON(t, srv, http.MethodDelete, "/api/v1/sessions", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("logout status = %d, want 401", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodDelete, "/api/v1/sessions", "", "bogus")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("logout status = %d, want 401", rec.Code)
	}
}

func loginToken(t *testing.T, srv *Server, password string) string {
	t.Helper()
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"username":"owner","password":"`+password+`"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", rec.Code, rec.Body.String())
	}
	var session struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&session); err != nil || session.Token == "" {
		t.Fatalf("login body = %q", rec.Body.String())
	}
	return session.Token
}

func TestChangePasswordFlow(t *testing.T) {
	srv := claimedAuthServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/admins/setup", `{"username":"owner","password":"long-enough-password"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, want 201", rec.Code)
	}
	// Missing sessions refuse before touching anything.
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/admins/password",
		`{"current_password":"long-enough-password","new_password":"replacement-password-long"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	token := loginToken(t, srv, "long-enough-password")
	other := loginToken(t, srv, "long-enough-password")
	// Wrong current answers invalid without saying more.
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/admins/password",
		`{"current_password":"wrong-password-here","new_password":"replacement-password-long"}`, token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	// Weak replacements refuse under the setup policy.
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/admins/password",
		`{"current_password":"long-enough-password","new_password":"short"}`, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/admins/password",
		`{"current_password":"long-enough-password","new_password":"replacement-password-long"}`, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	// The old credential dies and every OTHER session with it; the
	// session that rotated the password survives (ADR-020).
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"username":"owner","password":"long-enough-password"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old login status = %d, want 401", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodDelete, "/api/v1/sessions", "", other)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("other session status = %d, want 401 (revoked)", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodGet, "/api/v1/admins/lockout", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotating session status = %d, want 200 (kept)", rec.Code)
	}
	fresh := loginToken(t, srv, "replacement-password-long")
	rec = doJSON(t, srv, http.MethodGet, "/api/v1/admins/lockout", "", fresh)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"locked":false`) {
		t.Fatalf("lockout = %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestLockoutStatusFlow(t *testing.T) {
	srv := claimedAuthServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/admins/setup", `{"username":"owner","password":"long-enough-password"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, want 201", rec.Code)
	}
	token := loginToken(t, srv, "long-enough-password")
	for range 5 {
		doJSON(t, srv, http.MethodPost, "/api/v1/sessions",
			`{"username":"owner","password":"wrong-password-here"}`, "")
	}
	rec = doJSON(t, srv, http.MethodGet, "/api/v1/admins/lockout", "", token)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"locked":true`) {
		t.Fatalf("lockout = %d (%s)", rec.Code, rec.Body.String())
	}
	// Rotation while locked refuses with throttling, not credentials.
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/admins/password",
		`{"current_password":"long-enough-password","new_password":"replacement-password-long"}`, token)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	// Unknown sessions never reach status.
	rec = doJSON(t, srv, http.MethodGet, "/api/v1/admins/lockout", "", "bogus")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
