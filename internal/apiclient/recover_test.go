package apiclient

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/server"
	"homestead/internal/store"
)

func unixRecoverClient(t *testing.T) (*Client, *Client) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	claimSvc := claim.New(st)
	authSvc := auth.New(st, claimSvc)
	tcpSrv := server.New("127.0.0.1:0")
	tcpSrv.MountClaim(claimSvc)
	tcpSrv.MountAuth(authSvc, audit.New(st))
	ts := httptest.NewServer(tcpSrv.Handler())
	t.Cleanup(ts.Close)
	unixSrv := server.New("127.0.0.1:0")
	unixSrv.MountRecovery(authSvc)
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
	socketPath := filepath.Join(t.TempDir(), "homestead.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- unixSrv.RunUnix(ctx, socketPath) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	unix := New("http://localhost", WithHTTPClient(UnixClient(socketPath)))
	return unix, New(ts.URL)
}

func TestRecoverOverUnixSocket(t *testing.T) {
	unix, tcp := unixRecoverClient(t)
	ctx := context.Background()
	admin, err := unix.Recover(ctx, "owner", "recovered-password")
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if admin.Username != "owner" {
		t.Fatalf("Admin = %+v, want owner", admin)
	}
	session, err := tcp.Login(ctx, "owner", "recovered-password")
	if err != nil {
		t.Fatalf("Login with recovered password: %v", err)
	}
	if session.Token == "" {
		t.Fatal("empty session token")
	}
	if _, err := unix.Recover(ctx, "ghost", "recovered-password"); err == nil {
		t.Fatal("unknown Recover succeeded, want error")
	} else if statusErr, ok := err.(*StatusError); !ok || statusErr.Status != 404 {
		t.Fatalf("err = %v, want 404 StatusError", err)
	}
}
