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
	"homestead/internal/doctor"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/store"
)

func scheduleAs(t *testing.T, srv *Server, user string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/drift/schedule", bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), operatorKey{},
		operatorSession{Token: "t", Session: auth.VerifiedSession{Username: user}}))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

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
	if rec := scheduleAs(t, srv, "owner",
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
	check := driftDoctorCheck(st, func() time.Time { return now })()
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
	if check := driftDoctorCheck(st, func() time.Time { return now })(); check.Status != doctor.StatusOK || !strings.Contains(check.Detail, "clean at") {
		t.Fatalf("doctor = %+v", check)
	}
}

func TestDriftScanDisabledAndValidation(t *testing.T) {
	srv, st, _ := driftTestServer(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	sched := NewScheduler(st, t.TempDir(), scheduler.WithClock(clock))
	srv.MountDriftScan(st, clock)

	if rec := scheduleAs(t, srv, "owner",
		map[string]any{"interval_seconds": 10, "enabled": true}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("short interval = %d", rec.Code)
	}
	if rec := scheduleAs(t, srv, "owner",
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
	if check := driftDoctorCheck(st, func() time.Time { return now })(); check.Status != doctor.StatusOK || !strings.Contains(check.Detail, "off") {
		t.Fatalf("doctor = %+v", check)
	}
}

// Boundary: a 300s interval is accepted and fires exactly at 300s, not
// before; the first tick of an unsaved default anchors rather than fires;
// changing the interval re-anchors at the change.
func TestDriftScanBoundaryAnchorAndReanchor(t *testing.T) {
	srv, st, _ := driftTestServer(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	sched := NewScheduler(st, t.TempDir(), scheduler.WithClock(func() time.Time { return now }))
	srv.MountDriftScan(st, sched.Now)
	tick := func() {
		t.Helper()
		if err := sched.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	// First tick with no saved schedule: anchors at now, no scan.
	tick()
	if len(scanAudit(t, st)) != 0 {
		t.Fatal("first tick scanned")
	}
	if got, _ := catalog.GetScanSchedule(st); !got.UpdatedAt.Equal(now) {
		t.Fatalf("anchor = %v, want %v", got.UpdatedAt, now)
	}

	// 299s accepted? No: 300 is the minimum, 299 is refused.
	if rec := scheduleAs(t, srv, "owner", map[string]any{"interval_seconds": 299, "enabled": true}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("299 = %d", rec.Code)
	}
	if rec := scheduleAs(t, srv, "owner", map[string]any{"interval_seconds": 300, "enabled": true}); rec.Code != http.StatusOK {
		t.Fatalf("300 = %d %s", rec.Code, rec.Body.String())
	}
	now = now.Add(299 * time.Second)
	tick()
	if len(scanAudit(t, st)) != 0 {
		t.Fatal("scanned at 299s")
	}
	now = now.Add(time.Second)
	tick()
	waitOps(t, st, 1)

	// Re-anchor: change to 1h at +10m after the last run; due 1h after the
	// change, not 1h after the last run.
	now = now.Add(10 * time.Minute)
	if rec := scheduleAs(t, srv, "owner", map[string]any{"interval_seconds": 3600, "enabled": true}); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	now = now.Add(59 * time.Minute)
	tick()
	if len(scanAudit(t, st)) != 1 {
		t.Fatal("scanned before re-anchored interval elapsed")
	}
	now = now.Add(time.Minute)
	tick()
	waitOps(t, st, 2)
}

// Omitting enabled is a client error, not a silent disable; the audit
// actor is the authenticated operator.
func TestDriftScheduleRequiresEnabledAndAuditsOperator(t *testing.T) {
	srv, st, _ := driftTestServer(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	srv.MountDriftScan(st, func() time.Time { return now })
	if rec := scheduleAs(t, srv, "owner", map[string]any{"interval_seconds": 3600, "enabled": true}); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if rec := scheduleAs(t, srv, "owner", map[string]any{"interval_seconds": 7200}); rec.Code != http.StatusBadRequest {
		t.Fatalf("omitted enabled = %d", rec.Code)
	}
	if rec := scheduleAs(t, srv, "owner", map[string]any{"enabled": false}); rec.Code != http.StatusBadRequest {
		t.Fatalf("omitted interval = %d", rec.Code)
	}
	if got, _ := catalog.GetScanSchedule(st); !got.Enabled || got.IntervalSeconds != 3600 {
		t.Fatalf("rejected save changed schedule: %+v", got)
	}
	if rec := scheduleAs(t, srv, "alice", map[string]any{"interval_seconds": 600, "enabled": true}); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	all, _ := audit.New(st).List(20)
	found := false
	for _, e := range all {
		if e.Action == "drift.schedule" && e.Actor == "alice" {
			found = true
		}
		if e.Action == "drift.schedule" && e.Actor == "owner" && strings.Contains(e.Detail, "interval 600s") {
			t.Fatalf("wrong actor: %+v", e)
		}
	}
	if !found {
		t.Fatalf("no audit entry for alice: %+v", all)
	}
	// Unauthenticated (no operator in context) is refused.
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/drift/schedule", map[string]any{"interval_seconds": 600, "enabled": true}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d", rec.Code)
	}
}

// Accept and restore drop the package from the stored latest scan, and
// doctor shows the scan age.
func TestDriftScanInvalidatedByAcceptAndRestore(t *testing.T) {
	_, st, stackDir := driftTestServer(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	res, err := catalog.Scan(st, driftDirFor, now)
	if err != nil || len(res.Drifted) != 1 {
		t.Fatalf("scan = %+v %v", res, err)
	}
	if err := catalog.SaveScan(st, res); err != nil {
		t.Fatal(err)
	}
	check := driftDoctorCheck(st, func() time.Time { return now.Add(90 * time.Minute) })()
	if check.Status != doctor.StatusWarn || !strings.Contains(check.Detail, "age 1h30m0s") {
		t.Fatalf("doctor = %+v", check)
	}
	if _, err := catalog.Accept(st, "demo", stackDir); err != nil {
		t.Fatal(err)
	}
	latest, ok, _ := catalog.LatestScan(st)
	if !ok || len(latest.Drifted) != 0 || !latest.ScannedAt.Equal(now) {
		t.Fatalf("latest after accept = %+v", latest)
	}
	if check := driftDoctorCheck(st, func() time.Time { return now })(); check.Status != doctor.StatusOK {
		t.Fatalf("doctor after accept = %+v", check)
	}

	// Restore path: drift again, scan, restore.
	if err := os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services: {again: true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ = catalog.Scan(st, driftDirFor, now)
	_ = catalog.SaveScan(st, res)
	if len(res.Drifted) != 1 {
		t.Fatalf("scan = %+v", res)
	}
	if _, err := catalog.Restore(st, "demo", stackDir); err != nil {
		t.Fatal(err)
	}
	latest, _, _ = catalog.LatestScan(st)
	if len(latest.Drifted) != 0 {
		t.Fatalf("latest after restore = %+v", latest)
	}
}
