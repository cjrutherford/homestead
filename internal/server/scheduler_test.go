package server

import (
	"context"
	"encoding/json"
	"homestead/internal/catalog"
	"homestead/internal/exposure"
	"homestead/internal/operation"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/backup"
	"homestead/internal/scheduler"
	"homestead/internal/store"
)

func TestSchedulerFiresBackupSchedulesAndServesStatus(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	saved, err := backup.NewSchedules(st).Save(backup.Schedule{
		Package: "ghost", IntervalSeconds: 3600, Retain: 3, Enabled: true, Actor: "owner",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	now := saved.UpdatedAt
	clock := func() time.Time { return now }
	sched := NewScheduler(st, filepath.Join(t.TempDir(), "no-catalog"), nil, scheduler.WithClock(clock))
	srv := New("127.0.0.1:0")
	srv.MountScheduler(sched)

	var jobs []scheduler.Status
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/scheduler/jobs", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(jobs) != 3 || jobs[1].ID != "cert:renewal-check" || jobs[2].ID != "drift:scan" || jobs[0].ID != "backup:ghost" || !jobs[0].NextDue.Equal(saved.UpdatedAt.Add(time.Hour)) {
		t.Fatalf("jobs = %+v", jobs)
	}

	// Due: the unknown package cannot assemble, which records the failure
	// instead of silently dropping the window.
	now = now.Add(time.Hour)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/scheduler/jobs", nil)
	if err := json.Unmarshal(recorder.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(jobs[0].LastError, "catalog unavailable") || !jobs[0].LastRunAt.Equal(now) {
		t.Fatalf("jobs = %+v", jobs)
	}
}

// A due scheduled backup runs end to end through the real runner: a real
// file copy lands under the destination in a directory stamped by the
// scheduler's injected clock, and the operation succeeds.
func TestSchedulerRunsBackupEndToEnd(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mount, dest := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(mount, "app", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "app", "data", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	since := now
	sched := scheduler.New(st, scheduler.WithClock(func() time.Time { return now }))
	manifest := catalog.Manifest{
		Name: "app", Version: "1.0.0", Title: "App", Blurb: "App.", Description: "App.",
		Category: "media", Icon: "mdi:image",
		Services:     []catalog.Service{{Name: "web", Image: "example/web:1.0.0"}},
		Requirements: catalog.Requirements{MemoryMB: 64},
		Exposure: exposure.PackageExposure{Package: "app", Endpoints: []exposure.EndpointExposure{
			{Endpoint: "web", Recommended: "local"},
		}},
	}
	sched.Register("test", scheduler.SourceFunc(func() ([]scheduler.Job, error) {
		return []scheduler.Job{{
			ID: "backup:app", Interval: time.Hour, Enabled: true, Since: since,
			Build: func() (operation.Definition, error) {
				return backup.JobDefinition(backup.Job{
					Manifest: manifest, Package: "app", DestRoot: dest, Retain: 2,
					Sources: []backup.Source{{Mountpoint: mount, Path: "app/data"}},
				}, sched.Now)
			},
		}}, nil
	}))
	now = now.Add(time.Hour)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	stamp := now.Format("20060102T150405Z")
	var op operation.Operation
	deadline := time.Now().Add(10 * time.Second)
	for {
		ops, _ := operation.New(st).List(0)
		if len(ops) == 1 && operation.Terminal(ops[0].Status) {
			op = ops[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation did not finish: %+v", ops)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if op.Status != operation.StatusSucceeded || op.Type != "backup.job" {
		t.Fatalf("op = %+v", op)
	}
	if _, err := os.Stat(filepath.Join(dest, "app", stamp, "backup.json")); err != nil {
		t.Fatalf("manifest missing under injected-clock stamp %s: %v", stamp, err)
	}
	jobs, _ := sched.Status()
	if len(jobs) != 1 || jobs[0].LastOperationID != op.ID || jobs[0].LastError != "" {
		t.Fatalf("status = %+v", jobs)
	}
}

// A retention edit must not push the next run out.
func TestBackupSourceAnchorSurvivesRetentionEdit(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	schedules := backup.NewSchedules(st)
	first, _ := schedules.Save(backup.Schedule{Package: "ghost", IntervalSeconds: 3600, Retain: 3, Enabled: true, Actor: "owner"})
	time.Sleep(2 * time.Millisecond)
	if _, err := schedules.Save(backup.Schedule{Package: "ghost", IntervalSeconds: 3600, Retain: 5, Enabled: true, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	sched := NewScheduler(st, t.TempDir(), nil, scheduler.WithClock(func() time.Time { return first.UpdatedAt }))
	jobs, err := sched.Status()
	if err != nil || len(jobs) != 3 || !jobs[0].NextDue.Equal(first.UpdatedAt.Add(time.Hour)) {
		t.Fatalf("jobs = %+v, %v", jobs, err)
	}
}
