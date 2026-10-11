package apiclient

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/server"
	"homestead/internal/store"
)

func auditClient(t *testing.T) *Client {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	claimSvc := claim.New(st)
	authSvc := auth.New(st, claimSvc)
	srv := server.New("127.0.0.1:0")
	srv.MountClaim(claimSvc)
	srv.MountAuth(authSvc, audit.New(st))
	srv.MountAudit(audit.New(st))
	srv.MountSessionBoundary(authSvc, claimSvc, audit.New(st))
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
	session, err := authSvc.Login("owner", "long-enough-password") // service-level: no audit entry
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return New(ts.URL, WithToken(session.Token))
}

func TestAuditListRoundTrip(t *testing.T) {
	c := auditClient(t)
	ctx := context.Background()
	if _, err := c.Login(ctx, "owner", "long-enough-password"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := c.Login(ctx, "owner", "wrong-password-here"); err == nil {
		t.Fatal("bad login succeeded, want error")
	}
	entries, err := c.AuditList(ctx, 10)
	if err != nil {
		t.Fatalf("AuditList: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2 logins", entries)
	}
	if entries[0].Outcome != "error" || entries[1].Outcome != "ok" {
		t.Fatalf("order/outcomes = %+v, want newest-first", entries)
	}
	if entries[0].Actor != "owner" || entries[0].Action != "admin.login" {
		t.Fatalf("entry = %+v", entries[0])
	}
}

func TestAuditListDefaultLimit(t *testing.T) {
	c := auditClient(t)
	entries, err := c.AuditList(context.Background(), 0)
	if err != nil {
		t.Fatalf("AuditList: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want empty", entries)
	}
}
