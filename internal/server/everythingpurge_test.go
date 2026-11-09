package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"homestead/internal/purge"
	"homestead/internal/store"
)

func TestEverythingTargetsAreInstalledBridgeApps(t *testing.T) {
	var dir string
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir = "../../catalog"
	// Not installed: nothing.
	got, err := everythingTargets(st, dir)(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := st.DB().Exec(`INSERT INTO generated_fingerprints(stack, path, sha256, updated_at) VALUES('tasks','compose.yaml','x','now')`); err != nil {
		t.Fatal(err)
	}
	got, err = everythingTargets(st, dir)(context.Background())
	if err != nil || len(got) != 1 || got[0] != (purge.Target{App: "tasks", Container: "tasks-tasks"}) {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestEverythingPurgeJobOnlyWhilePending(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := NewEverythingPurger(st, t.TempDir(), time.Now)
	jobs, _ := everythingPurgeSource(p, time.Now).ScheduledJobs()
	if len(jobs) != 1 || jobs[0].Enabled {
		t.Fatalf("idle: %+v", jobs)
	}
	if _, err := st.DB().Exec(`INSERT INTO everything_purges(app, username, subject, requested_at) VALUES('tasks','ada','','2026-11-03T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	jobs, _ = everythingPurgeSource(p, time.Now).ScheduledJobs()
	if !jobs[0].Enabled {
		t.Fatal("must be enabled while a purge is pending")
	}
}
