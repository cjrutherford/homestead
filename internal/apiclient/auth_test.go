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

func authClient(t *testing.T) (*Client, *claim.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	claimSvc := claim.New(st)
	srv := server.New("127.0.0.1:0")
	srv.MountClaim(claimSvc)
	srv.MountAuth(auth.New(st, claimSvc), audit.New(st))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return New(ts.URL), claimSvc
}

func claimedAuthClient(t *testing.T) *Client {
	t.Helper()
	c, claimSvc := authClient(t)
	token, err := claimSvc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := claimSvc.Claim(token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return c
}

func TestSetupLoginLogoutRoundTrip(t *testing.T) {
	c := claimedAuthClient(t)
	ctx := context.Background()
	admin, err := c.SetupAdmin(ctx, "owner", "long-enough-password")
	if err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	if admin.Username != "owner" || admin.ID == 0 {
		t.Fatalf("Admin = %+v, want id and username", admin)
	}
	session, err := c.Login(ctx, "owner", "long-enough-password")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if session.Token == "" || session.ExpiresAt.IsZero() {
		t.Fatalf("Session = %+v, want token and expiry", session)
	}
	if err := c.Logout(ctx, session.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if err := c.Logout(ctx, session.Token); err == nil {
		t.Fatal("second Logout succeeded, want error")
	} else if statusErr, ok := err.(*StatusError); !ok || statusErr.Status != 401 {
		t.Fatalf("err = %v, want 401 StatusError", err)
	}
}

func TestSetupAndLoginFailures(t *testing.T) {
	c := claimedAuthClient(t)
	ctx := context.Background()
	if _, err := c.SetupAdmin(ctx, "owner", "short"); err == nil {
		t.Fatal("weak SetupAdmin succeeded, want error")
	} else if statusErr, ok := err.(*StatusError); !ok || statusErr.Status != 422 {
		t.Fatalf("err = %v, want 422 StatusError", err)
	}
	if _, err := c.Login(ctx, "owner", "wrong-password-here"); err == nil {
		t.Fatal("bad Login succeeded, want error")
	} else if statusErr, ok := err.(*StatusError); !ok || statusErr.Status != 401 {
		t.Fatalf("err = %v, want 401 StatusError", err)
	}
}
