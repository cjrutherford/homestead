package server

import (
	"context"
	"encoding/json"
	"net/http"
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
	sched := NewScheduler(st, filepath.Join(t.TempDir(), "no-catalog"), scheduler.WithClock(clock))
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
	if len(jobs) != 1 || jobs[0].ID != "backup:ghost" || !jobs[0].NextDue.Equal(saved.UpdatedAt.Add(time.Hour)) {
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
