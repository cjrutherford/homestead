package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/scheduler"
	"homestead/internal/store"
)

// Stateless packages are not backup targets (S249, ADR-028).

const statelessEntry = `name: statelessapp
version: 1.0.0
title: Stateless
blurb: Keeps nothing.
description: A static app that keeps no state on the appliance.
category: testing
icon: mdi:test
services:
- name: web
  image: example/web:2.0.0
  exposes: web
  data:
  - target: /tmp
    ephemeral: true
    reason: temporary files
requirements:
  memorymb: 64
  ports: [8080]
exposure:
  package: statelessapp
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: false
`

const statefulEntry = `name: statefulapp
version: 1.0.0
title: Stateful
blurb: Keeps data.
description: An app with persistent data.
category: testing
icon: mdi:test
services:
- name: web
  image: example/web:2.0.0
  exposes: web
  data:
  - target: /config
    class: application
    subpath: config
requirements:
  memorymb: 64
  storage:
  - class: application
    minbytes: 1048576
  ports: [8080]
exposure:
  package: statefulapp
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
backuppaths: [config]
`

func statelessCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"statelessapp.yaml": statelessEntry, "statefulapp.yaml": statefulEntry} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func statelessStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func statelessManifest(t *testing.T, name string) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load(statelessCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := entries.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestScheduleSaveRefusesStatelessPackage(t *testing.T) {
	st := statelessStore(t)
	srv := New("127.0.0.1:0")
	srv.MountBackups(st, statelessCatalog(t))
	body := func(pkg string) map[string]any {
		return map[string]any{"package": pkg, "interval_seconds": 86400, "retain": 7, "enabled": true, "actor": "owner"}
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/backups/schedules", body("statelessapp"))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "statelessapp") ||
		!strings.Contains(recorder.Body.String(), "stateless") {
		t.Fatalf("stateless save: code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if schedules, _ := backup.NewSchedules(st).List(); len(schedules) != 0 {
		t.Fatalf("a refused schedule was stored: %+v", schedules)
	}
	// A stateful package, and one the catalog does not know, schedule as before.
	for _, pkg := range []string{"statefulapp", "unknown"} {
		if recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/backups/schedules", body(pkg)); recorder.Code != http.StatusOK {
			t.Fatalf("%s save: code = %d (%s)", pkg, recorder.Code, recorder.Body.String())
		}
	}
}

func TestScheduleSurfacesReportStateless(t *testing.T) {
	st := statelessStore(t)
	srv := New("127.0.0.1:0")
	srv.MountBackups(st, statelessCatalog(t))
	// Per-package read: an explicit no-op, not a 404.
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/backups/schedules/statelessapp", nil)
	var got backup.Schedule
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &got) != nil || !got.Stateless || got.Package != "statelessapp" {
		t.Fatalf("stateless read: code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	// A stateful package with no schedule is still a 404.
	if recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/backups/schedules/statefulapp", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("stateful read: code = %d", recorder.Code)
	}
	// A schedule stored before the package became stateless lists flagged.
	if _, err := backup.NewSchedules(st).Save(backup.Schedule{Package: "statelessapp", IntervalSeconds: 3600, Retain: 1, Enabled: true, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.NewSchedules(st).Save(backup.Schedule{Package: "statefulapp", IntervalSeconds: 3600, Retain: 1, Enabled: true, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	var list []backup.Schedule
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/backups/schedules", nil)
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("list: %v (%s)", err, recorder.Body.String())
	}
	for _, schedule := range list {
		if schedule.Stateless != (schedule.Package == "statelessapp") {
			t.Fatalf("schedule %+v flagged wrongly", schedule)
		}
	}
}

func TestSchedulerBuildsNoJobForStatelessPackages(t *testing.T) {
	st := statelessStore(t)
	for _, pkg := range []string{"statelessapp", "statefulapp"} {
		if _, err := backup.NewSchedules(st).Save(backup.Schedule{Package: pkg, IntervalSeconds: 3600, Retain: 1, Enabled: true, Actor: "owner"}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	sched := NewScheduler(st, statelessCatalog(t), nil, scheduler.WithClock(func() time.Time { return now }))
	statuses, err := sched.Status()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, status := range statuses {
		ids = append(ids, status.ID)
	}
	joined := strings.Join(ids, ",")
	if strings.Contains(joined, "statelessapp") || !strings.Contains(joined, "backup:statefulapp") {
		t.Fatalf("jobs = %v", ids)
	}
	// Due: ticking records no run for the stateless package either.
	now = now.Add(2 * time.Hour)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	statuses, _ = sched.Status()
	for _, status := range statuses {
		if strings.Contains(status.ID, "statelessapp") {
			t.Fatalf("a job exists for the stateless package: %+v", status)
		}
	}
}

func TestBackupJobAndGuardReturnTheStatelessResult(t *testing.T) {
	manifest := statelessManifest(t, "statelessapp")
	_, err := backupJob(statelessStore(t), manifest, nil, "")
	if !errors.Is(err, backup.ErrStateless) || errors.Is(err, scheduler.ErrUnavailable) {
		t.Fatalf("backupJob err = %v, want ErrStateless and not ErrUnavailable", err)
	}
	_, err = guardBackupSources(manifest, nil, nil, nil)
	if !errors.Is(err, backup.ErrStateless) {
		t.Fatalf("guard err = %v, want ErrStateless", err)
	}
	// A stateful package with no sources yet stays retryable, unchanged.
	_, err = backupJob(statelessStore(t), statelessManifest(t, "statefulapp"), nil, "")
	if !errors.Is(err, scheduler.ErrUnavailable) || errors.Is(err, backup.ErrStateless) {
		t.Fatalf("stateful backupJob err = %v, want ErrUnavailable", err)
	}
}

func TestPreUpdateBackupIsSkippedForStatelessPackage(t *testing.T) {
	st := statelessStore(t)
	catalogDir := statelessCatalog(t)
	srv := New("127.0.0.1:0")
	srv.MountUpdates(st, catalogDir, dockerFakeFor(t, "statelessapp", "example/web:1.0.0"), nil)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/updates/statelessapp/apply", strings.NewReader(`{"backup":true}`))
	srv.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var reply struct {
		ID     int64  `json:"id"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil || reply.ID == 0 {
		t.Fatalf("reply = %s (%v)", recorder.Body.String(), err)
	}
	if !strings.Contains(reply.Detail, "skipped") || !strings.Contains(reply.Detail, "statelessapp") {
		t.Fatalf("no recorded skip note: %q", reply.Detail)
	}
	entries, err := audit.New(st).List(100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		found = found || (entry.Action == "update.backup.skipped" && strings.Contains(entry.Detail, "statelessapp"))
	}
	if !found {
		t.Fatalf("no audit record of the skipped backup: %+v", entries)
	}
}
