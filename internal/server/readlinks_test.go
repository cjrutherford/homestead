package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/store"
)

type readLinksRig struct {
	srv   *Server
	st    *store.Store
	dir   string
	ups   *int
	count *int
}

// readLinksTestRig is the dashboard published on example.com, Tasks
// installed at /apps/tasks/, a fake edge and a fake stack.
func readLinksTestRig(t *testing.T, published bool) *readLinksRig {
	t.Helper()
	srv, st, _ := tunnelTestServer(t)
	srv.MountReadLinks(st, filepath.Join("..", "..", "catalog"))
	fakeEdge(t)
	seedTunnelEdge(t, srv, st, false, false)
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		if set.Routes[i].Dashboard {
			set.Routes[i].Tunnel = []docker.Hostname{"homestead.example.com"}
		}
	}
	set.Routes = append(set.Routes, docker.Route{Host: "tasks.lan", Target: docker.Target{Host: "tasks", Port: 8081},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Path: "/apps/tasks/", Published: published})
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	old := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(dir, name) }
	t.Cleanup(func() { stackDirFor = old })
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks", "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rig := &readLinksRig{srv: srv, st: st, dir: dir, ups: new(int), count: new(int)}
	*rig.count = 3
	oldUp, oldFP, oldCount := readLinksUp, readLinksFingerprint, bridgeLinkCount
	readLinksUp = func(context.Context, string, string) error { *rig.ups++; return nil }
	readLinksFingerprint = func(*store.Store, string, string) error { return nil }
	bridgeLinkCount = func(context.Context, string, string) int { return *rig.count }
	t.Cleanup(func() { readLinksUp, readLinksFingerprint, bridgeLinkCount = oldUp, oldFP, oldCount })
	return rig
}

func (r *readLinksRig) plan(t *testing.T, enabled bool) (int, readLinksPlan, string) {
	t.Helper()
	rec := serveRequest(t, r.srv, http.MethodPost, "/api/v1/apps/tasks/read-links/plan", map[string]any{"enabled": enabled})
	var plan readLinksPlan
	_ = json.Unmarshal(rec.Body.Bytes(), &plan)
	return rec.Code, plan, rec.Body.String()
}

func (r *readLinksRig) approve(t *testing.T, enabled bool, digest string) (int, string) {
	t.Helper()
	rec := serveRequest(t, r.srv, http.MethodPost, "/api/v1/apps/tasks/read-links/approve", map[string]any{"enabled": enabled, "digest": digest})
	if rec.Code == http.StatusOK {
		if op := waitOperation(t, r.st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
			t.Fatalf("operation %s: %s", op.Status, op.Error)
		}
	}
	return rec.Code, rec.Body.String()
}

func (r *readLinksRig) route(t *testing.T) docker.Route {
	t.Helper()
	set, _ := loadEdgeRoutes(r.st)
	for _, rt := range set.Routes {
		if rt.Host == "tasks.lan" {
			return rt
		}
	}
	t.Fatal("no tasks route")
	return docker.Route{}
}

// publicSite renders the dashboard route's tunnel entry from the stored table.
func (r *readLinksRig) publicSite(t *testing.T) string {
	t.Helper()
	set, _ := loadEdgeRoutes(r.st)
	dir := t.TempDir()
	if _, err := docker.ReconcileRoutes(dir, set, docker.RouteSet{}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "routes", "*"+docker.RouteSnippetExt))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if i := strings.Index(string(b), "# Tunnel entry"); i >= 0 {
			return string(b)[i:]
		}
	}
	return ""
}

func (r *readLinksRig) override(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(r.dir, "tasks", catalog.OverrideFileName))
	return string(b)
}

func TestReadLinksPlanChangesNothing(t *testing.T) {
	rig := readLinksTestRig(t, false)
	code, plan, body := rig.plan(t, true)
	if code != http.StatusOK {
		t.Fatalf("plan: %d %s", code, body)
	}
	if plan.Action != "expose" || plan.Origin != "https://homestead.example.com" || plan.Links != 3 || plan.ExpiryCapDays != 365 || plan.Digest == "" ||
		strings.Join(plan.Patterns, "|") != "https://homestead.example.com/apps/tasks/_homestead/read/*|https://homestead.example.com/apps/tasks/_homestead/view/*" {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Warnings) == 0 || len(plan.Refused) == 0 || !strings.Contains(strings.Join(plan.Warnings, " "), "bearer secrets") {
		t.Fatalf("plan lacks warnings: %+v", plan)
	}
	if rig.route(t).ReadLinks || rig.override(t) != "" || *rig.ups != 0 || strings.Contains(rig.publicSite(t), "tasks") {
		t.Fatal("a plan changed something")
	}
	// The live count is not part of the digest.
	*rig.count = 9
	if _, again, _ := rig.plan(t, true); again.Digest != plan.Digest || again.Links != 9 {
		t.Fatalf("digest moved with the link count: %s vs %s", again.Digest, plan.Digest)
	}
}

func TestReadLinksUnapprovedExposesNothing(t *testing.T) {
	rig := readLinksTestRig(t, false)
	_, plan, _ := rig.plan(t, true)
	for _, c := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"no digest", map[string]any{"enabled": true}, http.StatusBadRequest},
		{"wrong digest", map[string]any{"enabled": true, "digest": "nope"}, http.StatusConflict},
		{"digest of the other direction", map[string]any{"enabled": false, "digest": plan.Digest}, http.StatusUnprocessableEntity},
		{"no enabled", map[string]any{"digest": plan.Digest}, http.StatusBadRequest},
		{"extra field", map[string]any{"enabled": true, "digest": plan.Digest, "path": "/apps/tasks/"}, http.StatusBadRequest},
	} {
		rec := serveRequest(t, rig.srv, http.MethodPost, "/api/v1/apps/tasks/read-links/approve", c.body)
		if rec.Code != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, rec.Code, rec.Body.String(), c.want)
		}
	}
	if rig.route(t).ReadLinks || rig.override(t) != "" || *rig.ups != 0 || strings.Contains(rig.publicSite(t), "tasks") {
		t.Fatal("an unapproved plan exposed something")
	}
	if entries, _ := audit.New(rig.st).List(50); len(entries) != 0 {
		t.Fatalf("audit = %+v", entries)
	}
}

func TestReadLinksExposeAndRemove(t *testing.T) {
	rig := readLinksTestRig(t, false)
	_, plan, _ := rig.plan(t, true)
	if code, body := rig.approve(t, true, plan.Digest); code != http.StatusOK {
		t.Fatalf("approve: %d %s", code, body)
	}
	if !rig.route(t).ReadLinks || rig.route(t).Published {
		t.Fatalf("route = %+v", rig.route(t))
	}
	if !strings.Contains(rig.override(t), ReadLinksOriginEnv+": https://homestead.example.com") || *rig.ups != 1 {
		t.Fatalf("override %q, ups %d", rig.override(t), *rig.ups)
	}
	site := rig.publicSite(t)
	if !strings.Contains(site, "handle /apps/tasks/_homestead/* {") || strings.Contains(site, "handle /apps/tasks/* {") ||
		strings.Count(site, "reverse_proxy tasks:8081") != 1 {
		t.Fatalf("public site:\n%s", site)
	}
	// Planning it again is refused, and the removal needs its own plan.
	if code, _, _ := rig.plan(t, true); code != http.StatusUnprocessableEntity {
		t.Fatalf("second expose plan = %d", code)
	}
	if code, body := rig.approve(t, false, plan.Digest); code != http.StatusConflict {
		t.Fatalf("expose digest on removal: %d %s", code, body)
	}
	_, off, _ := rig.plan(t, false)
	if off.Action != "unexpose" {
		t.Fatalf("plan = %+v", off)
	}
	if code, body := rig.approve(t, false, off.Digest); code != http.StatusOK {
		t.Fatalf("remove: %d %s", code, body)
	}
	if rig.route(t).ReadLinks || strings.Contains(rig.publicSite(t), "tasks") || strings.Contains(rig.override(t), ReadLinksOriginEnv) {
		t.Fatalf("not removed: route %+v site\n%s\noverride %q", rig.route(t), rig.publicSite(t), rig.override(t))
	}
	rig.srv.waitAudits()
	var actions []string
	entries, _ := audit.New(rig.st).List(50)
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	joined := strings.Join(actions, " ")
	for _, want := range []string{"readlinks.expose.start", "readlinks.expose", "readlinks.unexpose.start", "readlinks.unexpose"} {
		if !strings.Contains(" "+joined+" ", " "+want+" ") {
			t.Errorf("audit lacks %s: %s", want, joined)
		}
	}
	if code, _, _ := rig.plan(t, false); code != http.StatusUnprocessableEntity {
		t.Fatalf("removal plan when not exposed = %d", code)
	}
}

// A posted route table cannot add or drop the exposure (only the approved
// plan does), and the public name stays closed without a tunnel.
func TestReadLinksSurvivePostedTables(t *testing.T) {
	rig := readLinksTestRig(t, false)
	_, plan, _ := rig.plan(t, true)
	rig.approve(t, true, plan.Digest)
	set, _ := loadEdgeRoutes(rig.st)
	posted := make([]docker.Route, 0, len(set.Routes))
	for _, r := range set.Routes {
		r.ReadLinks = false
		posted = append(posted, r)
	}
	for _, r := range preserveTunnelNames(rig.st, posted) {
		if r.Host == "tasks.lan" && !r.ReadLinks {
			t.Fatal("a posted table dropped the exposure")
		}
	}
	rig2 := readLinksTestRig(t, false)
	set, _ = loadEdgeRoutes(rig2.st)
	posted = nil
	for _, r := range set.Routes {
		r.ReadLinks = r.IsPath()
		posted = append(posted, r)
	}
	for _, r := range preserveTunnelNames(rig2.st, posted) {
		if r.ReadLinks {
			t.Fatal("a posted table added an exposure")
		}
	}
}

func TestReadLinksRefusals(t *testing.T) {
	rig := readLinksTestRig(t, false)
	for _, c := range []struct {
		path string
		code int
		want string
	}{
		{"/api/v1/apps/ghost/read-links/plan", http.StatusNotFound, "unknown"},
		{"/api/v1/apps/immich/read-links/plan", http.StatusNotFound, "unknown"}, // not an Everything app
		{"/api/v1/apps/notes/read-links/plan", http.StatusUnprocessableEntity, "Install"},
	} {
		rec := serveRequest(t, rig.srv, http.MethodPost, c.path, map[string]any{"enabled": true})
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: %d %s", c.path, rec.Code, rec.Body.String())
		}
	}
	// No public name: nothing to expose.
	set, _ := loadEdgeRoutes(rig.st)
	for i := range set.Routes {
		set.Routes[i].Tunnel = nil
	}
	if err := saveEdgeRoutes(rig.st, set); err != nil {
		t.Fatal(err)
	}
	if code, _, body := rig.plan(t, true); code != http.StatusUnprocessableEntity || !strings.Contains(body, "no public name") {
		t.Fatalf("no tunnel: %d %s", code, body)
	}
	var st readLinksStatus
	rec := serveRequest(t, rig.srv, http.MethodGet, "/api/v1/apps/tasks/read-links", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st.Available || st.Exposed || st.Reason == "" {
		t.Fatalf("status = %+v %s", st, rec.Body.String())
	}
}

// Exposed read links can still be removed when the public name is gone.
func TestReadLinksRemovableWithoutPublicName(t *testing.T) {
	rig := readLinksTestRig(t, false)
	_, plan, _ := rig.plan(t, true)
	rig.approve(t, true, plan.Digest)
	set, _ := loadEdgeRoutes(rig.st)
	for i := range set.Routes {
		set.Routes[i].Tunnel = nil
	}
	_ = saveEdgeRoutes(rig.st, set)
	code, off, body := rig.plan(t, false)
	if code != http.StatusOK {
		t.Fatalf("plan: %d %s", code, body)
	}
	if c, b := rig.approve(t, false, off.Digest); c != http.StatusOK {
		t.Fatalf("approve: %d %s", c, b)
	}
	if rig.route(t).ReadLinks || strings.Contains(rig.override(t), ReadLinksOriginEnv) {
		t.Fatal("still exposed")
	}
}

func TestReadLinksStatusReportsExposure(t *testing.T) {
	rig := readLinksTestRig(t, true) // the app itself is published
	_, plan, _ := rig.plan(t, true)
	if !strings.Contains(strings.Join(plan.Warnings, " "), "already published") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
	rig.approve(t, true, plan.Digest)
	var st readLinksStatus
	rec := serveRequest(t, rig.srv, http.MethodGet, "/api/v1/apps/tasks/read-links", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || !st.Available || !st.Exposed || st.Links != 3 || len(st.Patterns) != 2 {
		t.Fatalf("status = %+v %s", st, rec.Body.String())
	}
}
