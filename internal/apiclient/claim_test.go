package apiclient

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"homestead/internal/claim"
	"homestead/internal/server"
	"homestead/internal/store"
)

func claimClient(t *testing.T) (*Client, *claim.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := claim.New(st)
	srv := server.New("127.0.0.1:0")
	srv.MountClaim(svc)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return New(ts.URL), svc
}

func TestIdentityAndClaimRoundTrip(t *testing.T) {
	c, svc := claimClient(t)
	ctx := context.Background()
	id, err := c.Identity(ctx)
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if id.State != "UNCLAIMED" || id.Version == "" {
		t.Fatalf("Identity = %+v, want UNCLAIMED with version", id)
	}
	token, err := svc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	state, err := c.Claim(ctx, token)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if state != "CLAIMED" {
		t.Fatalf("state = %q, want CLAIMED", state)
	}
}

func TestClaimFailuresAreStatusErrors(t *testing.T) {
	c, svc := claimClient(t)
	ctx := context.Background()
	if _, err := svc.MintToken(claim.TokenTTL); err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if _, err := c.Claim(ctx, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err == nil {
		t.Fatal("bad token Claim succeeded, want error")
	} else if statusErr, ok := err.(*StatusError); !ok || statusErr.Status != 401 {
		t.Fatalf("err = %v, want 401 StatusError", err)
	}
	token, err := svc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if _, err := c.Claim(ctx, token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := c.Claim(ctx, token); err == nil {
		t.Fatal("second Claim succeeded, want error")
	} else if statusErr, ok := err.(*StatusError); !ok || statusErr.Status != 409 {
		t.Fatalf("err = %v, want 409 StatusError", err)
	}
}
