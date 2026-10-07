package server

import (
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

func auditServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	claimSvc := claim.New(st)
	authSvc := auth.New(st, claimSvc)
	srv := New("127.0.0.1:0")
	srv.MountClaim(claimSvc)
	srv.MountAuth(authSvc, audit.New(st))
	srv.MountAudit(audit.New(st))
	token, err := claimSvc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := claimSvc.Claim(token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := authSvc.SetupAdmin("owner", "long-enough-password"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	return srv
}

func doAudit(t *testing.T, srv *Server, method, target, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(payload))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAuditListEmpty(t *testing.T) {
	srv := auditServer(t)
	rec := doAudit(t, srv, http.MethodGet, "/api/v1/audit", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("body = %s, want []", rec.Body.String())
	}
}

func TestLoginAttemptsAudited(t *testing.T) {
	srv := auditServer(t)
	rec := doAudit(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"username":"owner","password":"long-enough-password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", rec.Code)
	}
	rec = doAudit(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"username":"owner","password":"wrong-password-here"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want 401", rec.Code)
	}
	rec = doAudit(t, srv, http.MethodGet, "/api/v1/audit", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("audit status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"action":"admin.login"`) {
		t.Fatalf("body missing login entries: %s", body)
	}
	if !strings.Contains(body, `"outcome":"ok"`) || !strings.Contains(body, `"outcome":"error"`) {
		t.Fatalf("body missing both outcomes: %s", body)
	}
	if strings.Contains(body, "long-enough-password") || strings.Contains(body, "wrong-password") {
		t.Fatalf("body leaks passwords: %s", body)
	}
}

func TestAuditLimitValidation(t *testing.T) {
	srv := auditServer(t)
	rec := doAudit(t, srv, http.MethodGet, "/api/v1/audit?limit=bogus", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET status = %d, want 400", rec.Code)
	}
	rec = doAudit(t, srv, http.MethodGet, "/api/v1/audit?limit=10", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
}
