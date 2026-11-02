//go:build !everythingdev

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"homestead/internal/everything"
)

func TestProductionRefusesIncompleteConfigAndIgnoresDevIdentity(t *testing.T) {
	t.Setenv("HOMESTEAD_EVERYTHING_INSECURE_DEV", "i-understand-this-is-not-authentication")
	dir := t.TempDir()
	store, err := everything.OpenStore(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	static := filepath.Join(dir, "static")
	_ = os.Mkdir(static, 0o700)
	_ = os.WriteFile(filepath.Join(static, "index.html"), []byte("<html><head></head></html>"), 0o600)
	secret := filepath.Join(dir, "secret")
	_ = os.WriteFile(secret, []byte("s3cret\n"), 0o600)
	good := config{app: "tasks", listen: "127.0.0.1:0", base: "/apps/tasks", static: static, origins: []string{"https://home.example"},
		issuer: "https://auth.example/application/o/tasks/", clientID: "tasks", secretFile: secret,
		redirectURL: "https://home.example/apps/tasks/_homestead/callback"}
	for _, dev := range []bool{false, true} {
		c := config{dev: dev, app: "tasks", listen: "127.0.0.1:0"}
		if id, _, err := newIdentity(c, store); id != nil || err == nil {
			t.Fatalf("dev=%v: empty config started: %v %v", dev, id, err)
		}
	}
	for name, mutate := range map[string]func(*config){
		"no issuer":       func(c *config) { c.issuer = "" },
		"no client id":    func(c *config) { c.clientID = "" },
		"no secret file":  func(c *config) { c.secretFile = "" },
		"missing secret":  func(c *config) { c.secretFile = filepath.Join(dir, "absent") },
		"no redirect":     func(c *config) { c.redirectURL = "" },
		"no static":       func(c *config) { c.static = "" },
		"no origin":       func(c *config) { c.origins = nil },
		"http issuer":     func(c *config) { c.issuer = "http://auth.example/" },
		"wrong redirect":  func(c *config) { c.redirectURL = "https://home.example/cb" },
		"missing CA file": func(c *config) { c.caFile = filepath.Join(dir, "no-ca") },
	} {
		c := good
		mutate(&c)
		if id, _, err := newIdentity(c, store); id != nil || err == nil {
			t.Errorf("%s: started with incomplete config", name)
		}
	}
	id, routes, err := newIdentity(good, store)
	if err != nil || id == nil || routes == nil {
		t.Fatalf("complete config rejected: %v", err)
	}
}

// TestAwaitSignInServesNothingUntilConfigured: the package starts the bridge
// before Homestead has applied sign-in (ADR-057). It must answer 503, refuse
// data and sessions, and still refuse a half-configured bridge.
func TestAwaitSignInServesNothingUntilConfigured(t *testing.T) {
	dir := t.TempDir()
	store, err := everything.OpenStore(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := config{app: "tasks", base: "/apps/tasks", await: true}
	id, extra, err := newIdentity(c, store)
	if err != nil || id == nil {
		t.Fatalf("awaiting bridge refused to start: %v", err)
	}
	h, err := everything.NewServerAt(c.base, store, id, []string{"https://sign-in-pending.invalid"}, extra)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/apps/tasks/", "/apps/tasks/index.html", "/apps/tasks/_homestead/login"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s = %d %q", path, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/apps/tasks/_homestead/data", nil)
	req.Header.Set("Origin", "https://sign-in-pending.invalid")
	h.ServeHTTP(rec, req)
	if rec.Code < 400 {
		t.Errorf("data route answered %d with no sign-in", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/apps/tasks/_homestead/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, the container must stay healthy while waiting", rec.Code)
	}
	// Half configured is still an error even with the flag.
	half := c
	half.issuer = "https://auth.example/"
	if id, _, err := newIdentity(half, store); id != nil || err == nil {
		t.Fatal("half-configured bridge started")
	}
}
