package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/doctor"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/store"
)

func waitOps(t *testing.T, st *store.Store, want int) []operation.Operation {
	t.Helper()
	ops := operation.New(st)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list, err := ops.List(50)
		if err != nil {
			t.Fatal(err)
		}
		done := 0
		for _, op := range list {
			if op.Type == "catalog.drift-scan" && operation.Terminal(op.Status) {
				done++
			}
		}
		if done >= want {
			return list
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d drift scan operation(s)", want)
	return nil
}

func scanAudit(t *testing.T, st *store.Store) []audit.Entry {
	t.Helper()
	all, err := audit.New(st).List(100)
	if err != nil {
		t.Fatal(err)
	}
	var out []audit.Entry
	for _, e := range all {
		if e.Action == "drift.scan" {
			out = append(out, e)
		}
	}
	return out
}

// The scan fires on the owner interval by the injected clock, records
// an operation and an audit entry, saves the latest result, and never
// resolves drift.
func TestDriftScanSchedulerRecordsWithoutResolving(t *testing.T) {
	srv, st, stackDir := driftTestServer(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	sched := NewScheduler(st, t.TempDir(), scheduler.WithClock(func() time.Time { return now }))
	srv.MountDriftScan(st, sched.Now)

	// Owner sets a 1h cadence at now; nothing is due before it elapses.
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/drift/schedule",
		map[string]any{"interval_seconds": 3600, "enabled": true}); rec.Code != http.StatusOK {
		t.Fatalf("save = %d (%s)", rec.Code, rec.Body.String())
	}
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := scanAudit(t, st); len(got) != 0 {
		t.Fatalf("scan ran before its interval: %+v", got)
	}

	now = now.Add(time.Hour)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitOps(t, st, 1)

	entries := scanAudit(t, st)
	if len(entries) != 1 || entries[0].Actor != scheduler.Actor || entries[0].Outcome != audit.OutcomeOK ||
		!strings.Contains(entries[0].Detail, "demo (compose.yaml modified)") {
		t.Fatalf("audit = %+v", entries)
	}
	latest, ok, err := catalog.LatestScan(st)
	if err != nil || !ok || !latest.ScannedAt.Equal(now) || len(latest.Drifted) != 1 || latest.Scanned != 1 {
		t.Fatalf("latest = %+v ok=%v err=%v", latest, ok, err)
	}
	raw, err := os.ReadFile(filepath.Join(stackDir, "compose.yaml"))
	if err != nil || !strings.Contains(string(raw), "expert") {
		t.Fatalf("scan changed the drifted file: %q %v", raw, err)
	}
	review, err := catalog.Review(st, "demo", stackDir)
	if err != nil || len(review) != 1 {
		t.Fatalf("drift was resolved by scan: %+v %v", review, err)
	}

	// Route shows the last scheduled scan; doctor warns with the time.
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/drift/schedule", nil)
	var view struct {
		IntervalSeconds int64 `json:"interval_seconds"`
		LastScan        *struct {
			Drifted []struct {
				Package string `json:"package"`
			} `json:"drifted"`
		} `json:"last_scan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.IntervalSeconds != 3600 || view.LastScan == nil || len(view.LastScan.Drifted) != 1 || view.LastScan.Drifted[0].Package != "demo" {
		t.Fatalf("view = %s", rec.Body.String())
	}
	check := driftDoctorCheck(st)()
	if check.Status != doctor.StatusWarn || !strings.Contains(check.Detail, "2026-10-11T13:00:00Z") {
		t.Fatalf("doctor = %+v", check)
	}

	// Not due again until another interval passes.
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(scanAudit(t, st)) != 1 {
		t.Fatal("scan fired twice in one window")
	}
}

// A clean scan records its operation and one terse audit line, with no
// package detail, and doctor reports ok.
func TestDriftScanCleanStaysQuiet(t *testing.T) {
	_, st, stackDir := driftTestServer(t)
	if _, err := catalog.Accept(st, "demo", stackDir); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	sched := NewScheduler(st, t.TempDir(), scheduler.WithClock(func() time.Time { return now }))
	// Default schedule is enabled and daily, anchored at first read: not
	// due on a fresh install, due one day later.
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(scanAudit(t, st)) != 0 {
		t.Fatal("default schedule scanned on first tick")
	}
	now = now.Add(24 * time.Hour)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitOps(t, st, 1)
	entries := scanAudit(t, st)
	if len(entries) != 1 || entries[0].Detail != "clean: 1 package(s) scanned" || entries[0].Outcome != audit.OutcomeOK {
		t.Fatalf("audit = %+v", entries)
	}
	if check := driftDoctorCheck(st)(); check.Status != doctor.StatusOK || !strings.Contains(check.Detail, "clean at") {
		t.Fatalf("doctor = %+v", check)
	}
}

func TestDriftScanDisabledAndValidation(t *testing.T) {
	srv, st, _ := driftTestServer(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	sched := NewScheduler(st, t.TempDir(), scheduler.WithClock(clock))
	srv.MountDriftScan(st, clock)

	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/drift/schedule",
		map[string]any{"interval_seconds": 10, "enabled": true}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("short interval = %d", rec.Code)
	}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/drift/schedule",
		map[string]any{"interval_seconds": 3600, "enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("save = %d", rec.Code)
	}
	now = now.Add(48 * time.Hour)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(scanAudit(t, st)) != 0 {
		t.Fatal("disabled scan ran")
	}
	if check := driftDoctorCheck(st)(); check.Status != doctor.StatusOK || !strings.Contains(check.Detail, "off") {
		t.Fatalf("doctor = %+v", check)
	}
}
