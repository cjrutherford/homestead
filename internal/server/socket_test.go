package server

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/store"
)

func socketServer(t *testing.T) (*Server, *auth.Service, string) {
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
	srv.MountRecovery(authSvc)
	if _, err := authSvc.SetupAdmin("owner", "long-enough-password"); err == nil {
		t.Fatal("setup succeeded unclaimed, want failure")
	}
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
	return srv, authSvc, filepath.Join(t.TempDir(), "homestead.sock")
}

func unixRoundTrip(t *testing.T, socketPath, method, target, payload string) (int, string) {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer conn.Close()
	req, err := http.NewRequest(method, "http://localhost"+target, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := req.Write(conn); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer res.Body.Close()
	buf := make([]byte, 4096)
	n, _ := res.Body.Read(buf)
	return res.StatusCode, string(buf[:n])
}

func TestUnixSocketRecoverRoundTrip(t *testing.T) {
	srv, _, socketPath := socketServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.RunUnix(ctx, socketPath) }()
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
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o, want 600", info.Mode().Perm())
	}
	status, body := unixRoundTrip(t, socketPath, http.MethodPost, "/api/v1/admins/recover",
		`{"username":"owner","password":"recovered-password"}`)
	if status != http.StatusOK {
		t.Fatalf("recover status = %d, want 200: %s", status, body)
	}
	if !strings.Contains(body, `"owner"`) {
		t.Fatalf("body = %s, want username", body)
	}
	status, _ = unixRoundTrip(t, socketPath, http.MethodPost, "/api/v1/admins/recover",
		`{"username":"ghost","password":"recovered-password"}`)
	if status != http.StatusNotFound {
		t.Fatalf("unknown status = %d, want 404", status)
	}
	// The socket mux serves recovery only: config API is absent here.
	status, _ = unixRoundTrip(t, socketPath, http.MethodGet, "/api/v1/config", "")
	if status != http.StatusNotFound {
		t.Fatalf("config on socket status = %d, want 404", status)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunUnix: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunUnix did not shut down")
	}
}

func TestRecoverAbsentOnTCP(t *testing.T) {
	srv, _, _ := socketServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admins/recover", strings.NewReader(`{"username":"owner","password":"x"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("TCP recover status = %d, want 404", rec.Code)
	}
}

func TestRunUnixMissingDirFails(t *testing.T) {
	srv, _, _ := socketServer(t)
	err := srv.RunUnix(context.Background(), filepath.Join(t.TempDir(), "no-dir", "x.sock"))
	if err == nil {
		t.Fatal("RunUnix with missing dir succeeded, want error")
	}
}
