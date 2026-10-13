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

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// fakeLive is the fake Docker client for server tests.
type fakeLive struct {
	inv docker.Inventory
	err error
}

func (f fakeLive) Inventory(context.Context) (docker.Inventory, error) { return f.inv, f.err }

// Tests never touch the real Docker socket: default to an unreachable
// engine; tests wanting a live comparison call useLive.
func init() {
	driftLiveSource = func() catalog.LiveSource { return fakeLive{} }
}

func healthy(service, image string, ports ...docker.Port) docker.Container {
	return docker.Container{
		ID: service, Names: []string{"demo-" + service}, Image: image, State: "running",
		Labels:   map[string]string{docker.LabelProject: "demo", docker.LabelService: service},
		Ports:    ports,
		Networks: []string{"demo_default"},
	}
}

func useLive(t *testing.T, inv docker.Inventory, err error) {
	t.Helper()
	old := driftLiveSource
	driftLiveSource = func() catalog.LiveSource { return fakeLive{inv: inv, err: err} }
	t.Cleanup(func() { driftLiveSource = old })
}

const liveCompose = "name: demo\nservices:\n  web:\n    image: nginx:1.27\n    ports: ['8080:80']\n  db:\n    image: postgres:16\n"

func liveStack(t *testing.T) (string, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(liveCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordFingerprints(st, "demo", dir); err != nil {
		t.Fatal(err)
	}
	old := driftDirFor
	driftDirFor = func(string) string { return dir }
	t.Cleanup(func() { driftDirFor = old })
	return dir, st
}

func TestScanRecordsLiveFindingsAndSkips(t *testing.T) {
	_, st := liveStack(t)
	now := time.Now()
	web := healthy("web", "nginx:1.27", docker.Port{IP: "0.0.0.0", Private: 80, Public: 8080, Type: "tcp"})
	// web runs, db is missing: a partial stack is drift.
	useLive(t, docker.Inventory{Reachable: true, Containers: []docker.Container{web}}, nil)
	res, err := catalog.ScanWithLive(context.Background(), st, driftDirFor, now, driftLiveSource())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drifted) != 1 || len(res.Drifted[0].Live) != 1 || res.Drifted[0].Live[0].Kind != catalog.LiveMissing {
		t.Fatalf("scan = %+v", res)
	}
	if !strings.Contains(scanSummary(res), "live missing db") {
		t.Fatalf("summary = %q", scanSummary(res))
	}
	// Nothing running at all is a run state, reported but not drift.
	useLive(t, docker.Inventory{Reachable: true}, nil)
	res, _ = catalog.ScanWithLive(context.Background(), st, driftDirFor, now, driftLiveSource())
	if !res.Clean() || len(res.Stopped) != 1 || !strings.Contains(scanSummary(res), "stopped (not drift): demo") {
		t.Fatalf("stopped scan = %+v", res)
	}
	check := driftDoctorCheck(st, func() time.Time { return now })
	if err := catalog.SaveScan(st, res); err != nil {
		t.Fatal(err)
	}
	if c := check(); c.Status != "ok" || !strings.Contains(c.Detail, "stopped (not drift)") {
		t.Fatalf("doctor = %+v", c)
	}
	// Unreachable engine: files still scan, comparison is skipped, not drift.
	useLive(t, docker.Inventory{}, nil)
	res, _ = catalog.ScanWithLive(context.Background(), st, driftDirFor, now, driftLiveSource())
	if !res.Clean() || res.LiveSkipped == "" || !strings.Contains(scanSummary(res), "skipped") {
		t.Fatalf("skipped scan = %+v", res)
	}
}

func TestLiveRouteReportsFindings(t *testing.T) {
	srv, _, stackDir := driftTestServer(t)
	if err := os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte(liveCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	useLive(t, docker.Inventory{Reachable: true, Containers: []docker.Container{
		healthy("web", "nginx:1.26", docker.Port{IP: "0.0.0.0", Private: 80, Public: 8080, Type: "tcp"}),
		healthy("db", "postgres:16"),
	}}, nil)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/demo/drift/live", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var live catalog.LiveReview
	if err := json.Unmarshal(recorder.Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if !live.Checked || len(live.Findings) != 1 || live.Findings[0].Kind != catalog.LiveImage {
		t.Fatalf("live = %+v", live)
	}
	// Engine unreachable: checked=false, no error.
	useLive(t, docker.Inventory{}, nil)
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/demo/drift/live", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"checked":false`) {
		t.Fatalf("unreachable = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRestoreRunsOnlyForRestoreFindings(t *testing.T) {
	_, _, stackDir := driftTestServer(t)
	if err := os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte(liveCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	port := docker.Port{IP: "0.0.0.0", Private: 80, Public: 8080, Type: "tcp"}
	ghost := healthy("ghost", "alpine")
	// Only an extra container: manual resolution.
	useLive(t, docker.Inventory{Reachable: true, Containers: []docker.Container{
		healthy("web", "nginx:1.27", port), healthy("db", "postgres:16"), ghost,
	}}, nil)
	if liveConverges(context.Background(), "demo") {
		t.Fatal("manual-only drift must not trigger restore")
	}
	// Recreate-only (port missing): restore alone would be a no-op.
	useLive(t, docker.Inventory{Reachable: true, Containers: []docker.Container{
		healthy("web", "nginx:1.27"), healthy("db", "postgres:16"),
	}}, nil)
	if liveConverges(context.Background(), "demo") {
		t.Fatal("recreate-only drift must not offer restore")
	}
	// Owner-stopped stack: nothing to restore.
	useLive(t, docker.Inventory{Reachable: true}, nil)
	if liveConverges(context.Background(), "demo") {
		t.Fatal("a stopped stack is not drift")
	}
	// Partial stack: db missing, web up: restore converges it.
	useLive(t, docker.Inventory{Reachable: true, Containers: []docker.Container{healthy("web", "nginx:1.27", port)}}, nil)
	if !liveConverges(context.Background(), "demo") {
		t.Fatal("missing container should be convergable")
	}
}
