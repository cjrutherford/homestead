package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/catalog"
	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/storage"
	"homestead/internal/store"
)

const migrationManifest = `name: demo
version: 1.0.0
title: Demo
blurb: Demo package.
description: Demonstrates data migration.
category: testing
icon: mdi:test
services:
- name: web
  image: example/web:1.0.0
  exposes: web
  data:
  - target: /data
    class: application
    subpath: data
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

type migrationEnv struct {
	srv   *Server
	st    *store.Store
	mount string
	vol   string
	free  uint64
}

func newMigrationEnv(t *testing.T) *migrationEnv {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	catalogDir := filepath.Join(root, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catalogDir, "demo.yaml"), []byte(migrationManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &migrationEnv{st: st, mount: filepath.Join(root, "mnt"), vol: filepath.Join(root, "vol", "_data"), free: 1 << 30}
	for _, dir := range []string{env.mount, env.vol} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(env.vol, "file"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	stacks := filepath.Join(root, "stacks", "demo")
	if err := os.MkdirAll(stacks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stacks, "compose.yaml"), []byte("name: demo\nservices:\n  web:\n    image: example/web:1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	discoverStorage = func() (discover.Storage, error) {
		return discover.Storage{Mounts: []discover.Mount{
			{Mountpoint: env.mount, FSType: "ext4", Accessible: true, FreeBytes: env.free},
		}}, nil
	}
	stackDirFor = func(string) string { return stacks }
	migrationContainers = func(context.Context) ([]docker.Container, error) {
		return []docker.Container{{
			Names: []string{"demo-web"}, State: "running",
			Labels: map[string]string{docker.LabelProject: "demo", docker.LabelService: "web"},
			Mounts: []docker.Mount{{Type: "volume", Name: "vol1", Source: env.vol, Destination: "/data"}},
		}}, nil
	}
	t.Cleanup(func() {
		discoverStorage = discover.DiscoverStorage
		stackDirFor = docker.DirFor
		migrationContainers = func(ctx context.Context) ([]docker.Container, error) {
			return docker.NewClient("").Containers(ctx)
		}
	})
	discovered := discover.Storage{Mounts: []discover.Mount{{Mountpoint: env.mount, FSType: "ext4", Accessible: true, FreeBytes: env.free}}}
	registry := storage.NewRegistry(st)
	if _, err := registry.Register(env.mount, "m", "test", discovered); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.NewAssignments(st).Assign("application", env.mount, "test", registry, discovered, 0); err != nil {
		t.Fatal(err)
	}
	env.srv = New("127.0.0.1:0")
	env.srv.MountMigrations(st, catalogDir, nil)
	return env
}

func (e *migrationEnv) do(t *testing.T, method, path string, body any, operator bool) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if operator {
		req = req.WithContext(context.WithValue(req.Context(), operatorKey{},
			operatorSession{Token: "t", Session: auth.VerifiedSession{Username: "owner"}}))
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestMigrationDetectionRoute(t *testing.T) {
	env := newMigrationEnv(t)
	rec := env.do(t, http.MethodGet, "/api/v1/data-migrations", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body)
	}
	var list migrationList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Apps) != 1 || list.Apps[0].State != catalog.DataNeedsMigration {
		t.Fatalf("list = %+v", list)
	}
	checks := migrationDoctorChecks(env.st, filepath.Join(filepath.Dir(env.mount), "catalog"))()
	if len(checks) != 1 || checks[0].Status != "warn" || !strings.Contains(checks[0].Detail, "plan the migration") {
		t.Fatalf("doctor = %+v", checks)
	}
}

func TestMigrationPlanAndApprovalGates(t *testing.T) {
	env := newMigrationEnv(t)
	rec := env.do(t, http.MethodPost, "/api/v1/data-migrations/demo/plan", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan code = %d: %s", rec.Code, rec.Body)
	}
	var plan migrationPlanView
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Digest == "" || len(plan.Items) != 1 || plan.Items[0].Dest != env.mount+"/demo/data" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Backup.Possible || plan.Backup.Reason == "" {
		t.Fatalf("backup = %+v (no backup class is assigned)", plan.Backup)
	}
	approve := "/api/v1/data-migrations/demo/approve"
	if code := env.do(t, http.MethodPost, approve, map[string]any{"digest": plan.Digest}, false).Code; code != http.StatusUnauthorized {
		t.Fatalf("no session code = %d", code)
	}
	if code := env.do(t, http.MethodPost, approve, map[string]any{"digest": "wrong"}, true).Code; code != http.StatusConflict {
		t.Fatalf("stale digest code = %d", code)
	}
	rec = env.do(t, http.MethodPost, approve, map[string]any{"digest": plan.Digest}, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "acknowledge_no_backup") {
		t.Fatalf("no-backup code = %d: %s", rec.Code, rec.Body)
	}
	entries, _ := audit.New(env.st).List(10)
	for _, entry := range entries {
		if entry.Action == auditMigrateApprove {
			t.Fatalf("a refused approval must not be audited as approved: %+v", entry)
		}
	}
}

func TestMigrationPlanRefusesLowSpace(t *testing.T) {
	env := newMigrationEnv(t)
	env.free = 3
	rec := env.do(t, http.MethodPost, "/api/v1/data-migrations/demo/plan", nil, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "free space") {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body)
	}
}

func TestRemoveVolumesRefusals(t *testing.T) {
	env := newMigrationEnv(t)
	path := "/api/v1/data-migrations/demo/remove-volumes"
	if code := env.do(t, http.MethodPost, path, map[string]any{"volumes": []string{"vol1"}}, true).Code; code != http.StatusConflict {
		t.Fatalf("untracked code = %d", code)
	}
	if err := catalog.RecordOldVolumes(env.st, "demo", []catalog.OldVolume{{Name: "vol1", Service: "web", Target: "/data"}}, "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	// vol1 is still mounted by the running container.
	rec := env.do(t, http.MethodPost, path, map[string]any{"volumes": []string{"vol1"}}, true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "still in use") {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body)
	}
}

func guardFixture(t *testing.T) (catalog.Manifest, map[storage.Class]string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.yaml"), []byte(migrationManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := entries.Get("demo")
	mount := t.TempDir()
	return manifest, map[storage.Class]string{storage.ClassApplication: mount}, mount
}
