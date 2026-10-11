package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/server"
	"homestead/internal/store"
)

func storageDaemon(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := server.New("127.0.0.1:0")
	srv.MountStorage(s)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestStorageMounts(t *testing.T) {
	code, stdout, _ := invoke(t, []string{"--server", storageDaemon(t), "storage", "mounts"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "/") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestStorageRegisterAssign(t *testing.T) {
	serverArgs := []string{"--server", storageDaemon(t)}
	code, stdout, stderr := invoke(t, append(serverArgs, "storage", "register",
		"--mountpoint", "/", "--label", "root", "--actor", "owner"))
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "/ root") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, stdout, _ = invoke(t, append(serverArgs, "storage", "registrations"))
	if code != exitOK || !strings.Contains(stdout, "/ root by owner") {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
	code, stdout, stderr = invoke(t, append(serverArgs, "storage", "assign",
		"--class", "application", "--mountpoint", "/", "--actor", "owner"))
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "application /") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, stdout, _ = invoke(t, append(serverArgs, "storage", "assignments"))
	if code != exitOK || !strings.Contains(stdout, "application / by owner") {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
	// Phantom mounts refuse with the server's reason.
	code, _, stderr = invoke(t, append(serverArgs, "storage", "register",
		"--mountpoint", "/no/such/mount", "--label", "ghost", "--actor", "owner"))
	if code != exitError || !strings.Contains(stderr, "discovered mount") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	// Missing flags refuse usage-first.
	code, _, _ = invoke(t, append(serverArgs, "storage", "register", "--mountpoint", "/"))
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
	if code, _, _ := invoke(t, append(serverArgs, "storage", "bogus")); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}
