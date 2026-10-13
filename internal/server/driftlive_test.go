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

func useLive(t *testing.T, inv docker.Inventory, err error) {
	t.Helper()
	old := driftLiveSource
	driftLiveSource = func() catalog.LiveSource { return fakeLive{inv: inv, err: err} }
	t.Cleanup(func() { driftLiveSource = old })
}

const liveCompose = "name: demo\nservices:\n  web:\n    image: nginx:1.27\n    ports: ['8080:80']\n"

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
	useLive(t, docker.Inventory{Reachable: true}, nil)
	res, err := catalog.ScanWithLive(context.Background(), st, driftDirFor, now, driftLiveSource())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drifted) != 1 || len(res.Drifted[0].Live) != 1 || res.Drifted[0].Live[0].Kind != catalog.LiveMissing {
		t.Fatalf("scan = %+v", res)
	}
	if !strings.Contains(scanSummary(res), "live missing web") {
		t.Fatalf("summary = %q", scanSummary(res))
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
	labels := map[string]string{docker.LabelProject: "demo", docker.LabelService: "web"}
	useLive(t, docker.Inventory{Reachable: true, Containers: []docker.Container{{
		ID: "c1", Names: []string{"demo-web"}, Image: "nginx:1.26", State: "running", Labels: labels,
		Ports:    []docker.Port{{IP: "0.0.0.0", Private: 80, Public: 8080, Type: "tcp"}},
		Networks: []string{"demo_default"},
	}}}, nil)
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

func TestRestoreRunsOnlyForConvergableLiveDrift(t *testing.T) {
	_, _, stackDir := driftTestServer(t)
	if err := os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte(liveCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{docker.LabelProject: "demo", docker.LabelService: "ghost"}
	// Only an extra container: manual resolution, restore has nothing to do.
	web := map[string]string{docker.LabelProject: "demo", docker.LabelService: "web"}
	useLive(t, docker.Inventory{Reachable: true, Containers: []docker.Container{
		{ID: "x", Names: []string{"ghost"}, Image: "alpine", State: "running", Labels: labels},
		{ID: "w", Names: []string{"demo-web"}, Image: "nginx:1.27", State: "running", Labels: web,
			Ports:    []docker.Port{{IP: "0.0.0.0", Private: 80, Public: 8080, Type: "tcp"}},
			Networks: []string{"demo_default"}},
	}}, nil)
	if liveConverges(context.Background(), "demo") {
		t.Fatal("manual-only drift must not trigger restore")
	}
	// web missing as well: up converges it.
	useLive(t, docker.Inventory{Reachable: true}, nil)
	if !liveConverges(context.Background(), "demo") {
		t.Fatal("missing container should be convergable")
	}
}
