package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/discover"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// freeBytesPattern matches live free-space quantities in plan output.
var freeBytesPattern = regexp.MustCompile(`offers \d+ bytes`)

// normalizePlan masks volatile live facts so equivalence assertions
// compare decisions, not background disk activity.
func normalizePlan(raw []byte) string {
	return freeBytesPattern.ReplaceAllString(string(raw), "offers N bytes")
}

func catalogTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	catalogDir := filepath.Join(dir, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	entry := `name: demo
version: 1.0.0
title: Demo
blurb: Demo package.
description: Demonstrates catalog serving.
category: testing
icon: mdi:test
services:
- name: web
  image: example/web:1.0.0
  exposes: web
requirements:
  memorymb: 64
  storage:
  - class: application
    minbytes: 1048576
  ports: [18080]
exposure:
  package: demo
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
backuppaths: [data]
`
	if err := os.WriteFile(filepath.Join(catalogDir, "demo.yaml"), []byte(entry), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	srv := New("127.0.0.1:0")
	srv.MountCatalog(s, catalogDir, nil)
	// Planning verifies class mounts against live discovery; stub it
	// so tests do not depend on the host mount table.
	oldDiscover := discoverStorage
	discoverStorage = func() (discover.Storage, error) {
		return discover.Storage{Mounts: []discover.Mount{
			{Mountpoint: "/", Accessible: true},
			{Mountpoint: "/tmp", Accessible: true},
			{Mountpoint: "/ro", Accessible: true, ReadOnly: true},
		}}, nil
	}
	t.Cleanup(func() { discoverStorage = oldDiscover })
	oldStack := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(dir, "stacks", name) }
	t.Cleanup(func() { stackDirFor = oldStack })
	// The planner needs an assigned application mount with live space:
	// approve / against fixture discovery (real discovery always
	// carries / with usage), then bind the class.
	discovered := discover.Storage{Mounts: []discover.Mount{
		{Mountpoint: "/", FSType: "ext4", Accessible: true},
	}}
	registry := storage.NewRegistry(s)
	if _, err := registry.Register("/", "root", "test", discovered); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := storage.NewAssignments(s).Assign("application", "/", "test", registry, discovered, 0); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	return srv, catalogDir
}

func serveRequest(t *testing.T, srv *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	return recorder
}

func TestCatalogListGet(t *testing.T) {
	srv, _ := catalogTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var list []packageSummary
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].Name != "demo" || list[0].Version != "1.0.0" {
		t.Fatalf("list = %+v", list)
	}
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/demo", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d", recorder.Code)
	}
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/ghost", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func installTestBody() map[string]any {
	return map[string]any{
		"domain":          "lan",
		"container_ports": map[string]int{"web": 18080},
		"storage":         map[string]string{"application": "/tmp"},
		"volumes": []map[string]any{
			{"Service": "web", "Class": "application", "Subpath": "data", "Target": "/data"},
		},
		"networks": []map[string]any{},
	}
}

func TestCatalogParityAcceptance(t *testing.T) {
	srv, _ := catalogTestServer(t)
	// Both surfaces post the same document shape: file-driven CLI
	// bodies and dashboard form bodies share keys, so one acceptance
	// body proves both paths.
	bodies := []map[string]any{
		installTestBody(),
		{
			// Dashboard-shaped: every key present, empties explicit.
			// CLI files may omit empties; both must plan identically.
			"domain":          "lan",
			"hostnames":       map[string]string{},
			"container_ports": map[string]int{"web": 18080},
			"storage":         map[string]string{"application": "/tmp"},
			"volumes": []map[string]any{
				{"Service": "web", "Class": "application", "Subpath": "data", "Target": "/data"},
			},
			"networks":  []map[string]any{},
			"zone":      "",
			"public_ip": "",
		},
	}
	var first []byte
	for i, body := range bodies {
		recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/plan", body)
		if recorder.Code != http.StatusOK {
			t.Fatalf("body %d: code = %d (%s)", i, recorder.Code, recorder.Body.String())
		}
		if first == nil {
			first = recorder.Body.Bytes()
			continue
		}
		// Free-space bytes are live facts: background disk activity
		// between the two plans must not fail body equivalence.
		// Normalize them away on both sides before comparing.
		if normalizePlan(recorder.Body.Bytes()) != normalizePlan(first) {
			t.Fatalf("plan %d differs:\n%s\n---\n%s", i, recorder.Body.String(), first)
		}
	}
}

func TestCatalogPlan(t *testing.T) {
	srv, _ := catalogTestServer(t)
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/plan", installTestBody())
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var plan struct {
		Package string `json:"Package"`
		Routes  []struct {
			Host string `json:"Host"`
		} `json:"Routes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if plan.Package != "demo" || len(plan.Routes) != 1 || plan.Routes[0].Host != "demo.lan" {
		t.Fatalf("plan = %+v", plan)
	}
	// Unplannable selections fail with 422, not 500.
	bad := installTestBody()
	bad["domain"] = ""
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/plan", bad)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	// Unknown packages 404 on plan too.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/ghost/plan", installTestBody())
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Malformed bodies 400.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/demo/plan", strings.NewReader("{bad"))
	recorder = httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func TestCatalogConfigure(t *testing.T) {
	srv, _ := catalogTestServer(t)
	body := map[string]any{
		"changes": []map[string]any{
			{"Service": "web", "Set": map[string]string{"TZ": "UTC"}},
		},
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/configure", body)
	// No docker daemon here: the operation starts, then fails at up —
	// but the route accepted a valid plan and returned an operation.
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var started struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if started.ID == 0 {
		t.Fatal("no operation id")
	}
	// Invalid changes refuse before running anything.
	bad := map[string]any{"changes": []map[string]any{{"Service": "ghost"}}}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/configure", bad)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/ghost/configure", body)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func uninstallTestServer(t *testing.T, withStack bool) (*Server, string) {
	t.Helper()
	srv, _ := catalogTestServer(t)
	stackDir := filepath.Join(t.TempDir(), "demo")
	if withStack {
		if err := os.MkdirAll(stackDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("name: demo\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	old := stackDirFor
	stackDirFor = func(name string) string {
		if name == "demo" {
			return stackDir
		}
		return old(name)
	}
	t.Cleanup(func() { stackDirFor = old })
	return srv, stackDir
}

func TestCatalogUninstall(t *testing.T) {
	srv, _ := uninstallTestServer(t, true)
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/uninstall", map[string]any{})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var started struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if started.ID == 0 {
		t.Fatal("no operation id")
	}
	// Unknown packages 404; missing stacks refuse before running.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/ghost/uninstall", map[string]any{})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("code = %d", recorder.Code)
	}
	bare, _ := uninstallTestServer(t, false)
	recorder = serveRequest(t, bare, http.MethodPost, "/api/v1/catalog/demo/uninstall", map[string]any{})
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "not installed") {
		t.Fatalf("code = %d (%s), want 404 not installed", recorder.Code, recorder.Body.String())
	}
	// Data removal without explicit roots refuses.
	fresh, _ := uninstallTestServer(t, true)
	recorder = serveRequest(t, fresh, http.MethodPost, "/api/v1/catalog/demo/uninstall", map[string]any{"remove_data": true})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func TestCatalogInstallRefusesInstalled(t *testing.T) {
	srv, _ := catalogTestServer(t)
	stack := stackDirFor("demo")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-S276 stack: no bind mounts for the declared paths.
	if err := os.WriteFile(filepath.Join(stack, "compose.yaml"), []byte("name: demo\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, route := range []string{"plan", "install"} {
		recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/"+route, installTestBody())
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "already installed") {
			t.Fatalf("%s: code = %d (%s)", route, recorder.Code, recorder.Body.String())
		}
	}
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog", nil)
	var list []packageSummary
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil || len(list) != 1 || !list[0].Installed {
		t.Fatalf("list = %+v (%v)", list, err)
	}
}

func TestCatalogPlanRefusesUnmountedStorage(t *testing.T) {
	srv, _ := catalogTestServer(t)
	for _, mount := range []string{"/not-mounted", "/ro"} {
		body := installTestBody()
		body["storage"] = map[string]string{"application": mount}
		recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/plan", body)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: code = %d (%s)", mount, recorder.Code, recorder.Body.String())
		}
	}
}

func TestApplyStorageOverridesReadsTheChosenMountsFreeSpace(t *testing.T) {
	facts := catalog.HostFacts{FreeSpace: map[storage.Class]uint64{storage.ClassBulk: 1}}
	mounts := []discover.Mount{
		{Mountpoint: "/mnt/app", Accessible: true, FreeBytes: 20},
		{Mountpoint: "/mnt/dead", Accessible: false, FreeBytes: 99},
	}
	applyStorageOverrides(&facts, map[storage.Class]string{
		storage.ClassApplication: "/mnt/app",
		storage.ClassBulk:        "/mnt/dead",
		storage.ClassBackup:      "/mnt/missing",
	}, mounts)
	if facts.FreeSpace[storage.ClassApplication] != 20 {
		t.Fatalf("application free = %d", facts.FreeSpace[storage.ClassApplication])
	}
	if _, ok := facts.FreeSpace[storage.ClassBulk]; ok {
		t.Fatal("an inaccessible mount must not carry free space")
	}
	if _, ok := facts.FreeSpace[storage.ClassBackup]; ok {
		t.Fatal("an unknown mount must not carry free space")
	}
}
