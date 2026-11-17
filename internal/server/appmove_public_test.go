package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/store"
)

// S471 (ADR-061): an installed path-package app that still holds a public
// name of its own is moved to its path on the dashboard's public name, and
// the old name is removed with no redirect.

const moveOldName = "jellyfin.example.com"

// publicMoveRig installs Jellyfin on jellyfin.lan published at
// jellyfin.example.com, with the dashboard published at
// homestead.example.com and Homestead's own CNAME for the old name.
func publicMoveRig(t *testing.T) (*Server, *store.Store, *[]string, *[][]string, *fakeCloudflare, string) {
	t.Helper()
	srv, st, steps, trusted, cf := moveRigCF(t)
	installHostApp(t, st, "jellyfin", "jellyfin", 8096)
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		switch set.Routes[i].Host {
		case "jellyfin.lan":
			set.Routes[i].Tunnel = []docker.Hostname{moveOldName}
		case dashLAN:
			set.Routes[i].Tunnel = []docker.Hostname{"homestead.example.com"}
		}
	}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	record := cf.addRecord("CNAME", moveOldName, router.TunnelTarget("tid"))
	if err := router.RecordOwned(st, router.OwnedRecord{RecordID: record, ZoneID: "zone1", Hostname: moveOldName, TunnelID: "tid"}); err != nil {
		t.Fatal(err)
	}
	if err := router.RecordRoute(st, router.TunnelRoute{Hostname: moveOldName, TunnelID: "tid", Service: tunnelEntryService}, "homestead"); err != nil {
		t.Fatal(err)
	}
	return srv, st, steps, trusted, cf, record
}

type movePublicView struct {
	appMoveView
	PublicFrom string   `json:"public_from"`
	PublicTo   string   `json:"public_to"`
	Removes    []string `json:"removes"`
	Stops      []string `json:"stops"`
}

func movePublicPlan(t *testing.T, srv *Server) movePublicView {
	t.Helper()
	var v movePublicView
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/plan", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAppMovePublicNamePreviewAndApply(t *testing.T) {
	srv, st, _, trusted, cf, record := publicMoveRig(t)
	status := moveStatus(t, srv, "jellyfin")
	if !status.Offered {
		t.Fatalf("status = %+v", status)
	}
	plan := movePublicPlan(t, srv)
	if plan.PublicFrom != "https://"+moveOldName+"/" || plan.PublicTo != "https://homestead.example.com/apps/jellyfin/" {
		t.Fatalf("public = %q -> %q", plan.PublicFrom, plan.PublicTo)
	}
	warnings := strings.Join(plan.Warnings, "\n")
	for _, want := range []string{
		"Old bookmarks and links on https://" + moveOldName + "/ stop working",
		"nothing redirects",
		"must be changed by hand to https://homestead.example.com/apps/jellyfin/",
		"No DNS record changes are needed for https://homestead.example.com/apps/jellyfin/",
		"shares one web origin",
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings miss %q:\n%s", want, warnings)
		}
	}
	removes := strings.Join(plan.Removes, "\n")
	if !strings.Contains(removes, "CNAME "+moveOldName) || !strings.Contains(removes, "ingress entry for "+moveOldName) ||
		len(plan.Stops) != 1 || plan.Stops[0] != "https://"+moveOldName+"/" {
		t.Fatalf("removes = %v stops = %v", plan.Removes, plan.Stops)
	}
	// Reading the plan removed nothing.
	if r := movedRoute(t, st, "jellyfin.lan"); r.IsPath() || len(r.Tunnel) != 1 || cf.records[record] == nil {
		t.Fatalf("plan changed state: %+v", r)
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/apply", map[string]any{"digest": plan.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("operation = %+v", op)
	}
	r := movedRoute(t, st, "jellyfin.lan")
	if r.Path != "/apps/jellyfin/" || !r.Published || len(r.Tunnel) != 0 {
		t.Fatalf("route = %+v", r)
	}
	if cf.records[record] != nil {
		t.Error("the old CNAME is still in Cloudflare")
	}
	if ingress, err := recordedIngress(st, "tid"); err != nil {
		t.Fatal(err)
	} else {
		for _, in := range ingress {
			if in.Hostname == moveOldName {
				t.Errorf("the old ingress entry is still recorded: %+v", ingress)
			}
		}
	}
	// The app's host list gains the dashboard's public name and loses the old one.
	if len(*trusted) != 1 || !strings.Contains(strings.Join((*trusted)[0], " "), "homestead.example.com") {
		t.Fatalf("trusted = %v", *trusted)
	}
	if after := moveStatus(t, srv, "jellyfin"); after.Offered {
		t.Fatalf("still offered: %+v", after)
	}
}

// A failure before the old name's removal stops the run with the old name
// intact: publish first, remove last.
func TestAppMovePublicNameFailureKeepsOldName(t *testing.T) {
	srv, st, _, _, cf, record := publicMoveRig(t)
	trustAppHosts = func(context.Context, catalog.Manifest, string, []string, []string) error {
		return errors.New("the app refused")
	}
	plan := movePublicPlan(t, srv)
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/apply", map[string]any{"digest": plan.Digest})
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusFailed {
		t.Fatalf("operation = %+v", op)
	}
	if cf.records[record] == nil {
		t.Error("the CNAME was deleted although the move failed")
	}
	ingress, _ := recordedIngress(st, "tid")
	kept := false
	for _, in := range ingress {
		kept = kept || in.Hostname == moveOldName
	}
	if !kept {
		t.Error("the ingress entry was removed although the move failed")
	}
}

// Refusals: a host package with a reason, a never app, and a subpath move
// with no public name for the dashboard.
func TestAppMovePublicRefusals(t *testing.T) {
	srv, st, _, _, _, _ := publicMoveRig(t)
	if s := moveStatus(t, srv, "immich"); s.Offered || !strings.Contains(s.Reason, "name of its own") || !strings.Contains(s.Reason, "cannot run under a path") {
		t.Fatalf("immich = %+v", s)
	}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/vaultwarden/move/plan", nil); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "you chose to keep it on its own origin") {
		t.Fatalf("vaultwarden plan: %d %s", rec.Code, rec.Body.String())
	}
	if s := moveStatus(t, srv, "uptime-kuma"); s.Offered || !strings.Contains(s.Reason, "never published") {
		t.Fatalf("kuma = %+v", s)
	}
	// A never package that holds a public name is not moved with it.
	installHostApp(t, st, "uptime-kuma", "uptime-kuma", 3001)
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		if set.Routes[i].Host == "uptime-kuma.lan" {
			set.Routes[i].Tunnel = []docker.Hostname{"uptime-kuma.example.com"}
		}
	}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	if s := moveStatus(t, srv, "uptime-kuma"); s.Offered || !strings.Contains(s.Reason, "never published") {
		t.Fatalf("kuma with a name = %+v", s)
	}
	// No public name on the dashboard: nowhere to move to.
	set, _ = loadEdgeRoutes(st)
	for i := range set.Routes {
		if set.Routes[i].Host == dashLAN {
			set.Routes[i].Tunnel = nil
		}
	}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	if s := moveStatus(t, srv, "jellyfin"); s.Offered || !strings.Contains(s.Reason, "dashboard has none") {
		t.Fatalf("jellyfin = %+v", s)
	}
}

// A path app known to set Path=/ cookies gets a warning in the preview.
func TestCookiePathWarning(t *testing.T) {
	m := catalog.Manifest{Title: "Demo", Serve: catalog.ServePath, CookiePathRoot: true}
	if w := cookiePathWarning(m); !strings.Contains(w, "Path=/") || !strings.Contains(w, "Demo") {
		t.Errorf("warning = %q", w)
	}
	m.CookiePathRoot = false
	if w := cookiePathWarning(m); w != "" {
		t.Errorf("unflagged warning = %q", w)
	}
}

// Doctor offers the move for an app still on a name its package no longer uses.
func TestSubpathPublishDoctor(t *testing.T) {
	_, st, _, _, _, _ := publicMoveRig(t)
	checks := subpathPublishChecks(DoctorLiveConfig{Store: st, CatalogDir: "../../catalog"})
	if len(checks) != 1 || checks[0].Status != "warn" || !strings.Contains(checks[0].Detail, "move available") ||
		!strings.Contains(checks[0].Detail, moveOldName) || checks[0].Fix == nil || !strings.Contains(checks[0].Fix.Command, "apps move plan jellyfin") {
		t.Fatalf("checks = %+v", checks)
	}
}

// S479: the new address must answer before the old name goes. A failed
// probe stops the run with the connector entry and the CNAME intact, and
// the half-done move shows as a finishable leftover.
func TestAppMoveProbeFailureKeepsOldName(t *testing.T) {
	srv, st, steps, _, cf, record := publicMoveRig(t)
	tries := 0
	movePathProbe = func(_ context.Context, url string) error {
		tries++
		if url != "https://homestead.example.com/apps/jellyfin/" {
			t.Errorf("probed %q", url)
		}
		return errors.New("answered 404 through the public edge")
	}
	plan := movePublicPlan(t, srv)
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/apply", map[string]any{"digest": plan.Digest})
	op := waitOperation(t, st, rec.Body.Bytes())
	if op.Status != operation.StatusFailed || !strings.Contains(op.Error, "was not removed") {
		t.Fatalf("operation = %+v", op)
	}
	if tries != 2 {
		t.Errorf("probe tries = %d", tries)
	}
	if cf.records[record] == nil {
		t.Error("the CNAME was deleted although the new address did not answer")
	}
	if ingress, _ := recordedIngress(st, "tid"); len(ingress) == 0 {
		t.Error("the ingress entry was removed although the new address did not answer")
	}
	_ = steps

	// The state shows: a finish is offered, naming the old and new address.
	status := moveStatus(t, srv, "jellyfin")
	if !status.Offered {
		t.Fatalf("finish not offered: %+v", status)
	}
	var raw map[string]any
	r := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/jellyfin/move", nil)
	_ = json.Unmarshal(r.Body.Bytes(), &raw)
	if raw["finish"] != true || raw["public_from"] != "https://"+moveOldName+"/" {
		t.Fatalf("status = %v", raw)
	}
	checks := publicNamePlanChecks(DoctorLiveConfig{Store: st})
	if len(checks) != 1 || checks[0].Status != "warn" || !strings.Contains(checks[0].Detail, "a move of jellyfin stopped") ||
		checks[0].Fix == nil || !strings.Contains(checks[0].Fix.Command, "apps move plan jellyfin") {
		t.Fatalf("doctor = %+v", checks)
	}

	// Finish: reviewed plan, digest, then the same removal steps.
	finish := movePublicPlan(t, srv)
	var fv struct {
		Finish bool `json:"finish"`
	}
	fr := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/plan", nil)
	_ = json.Unmarshal(fr.Body.Bytes(), &fv)
	if !fv.Finish || len(finish.AppSettings) != 0 || len(finish.Stops) != 1 {
		t.Fatalf("finish plan = %+v finish=%v", finish, fv.Finish)
	}
	if stale := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/apply", map[string]any{"digest": plan.Digest}); stale.Code != http.StatusConflict {
		t.Fatalf("a digest from another plan was accepted: %d", stale.Code)
	}
	// Still failing: still nothing removed.
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/apply", map[string]any{"digest": finish.Digest})
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusFailed || cf.records[record] == nil {
		t.Fatalf("second failure: %+v", op)
	}
	movePathProbe = func(context.Context, string) error { return nil }
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/apply", map[string]any{"digest": finish.Digest})
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("finish: %+v", op)
	}
	if cf.records[record] != nil {
		t.Error("the CNAME is still in Cloudflare after the finish")
	}
	if ingress, _ := recordedIngress(st, "tid"); len(ingress) != 1 {
		t.Errorf("ingress after finish = %+v", ingress)
	}
	if got := moveStatus(t, srv, "jellyfin"); got.Offered {
		t.Errorf("finish still offered: %+v", got)
	}
	if checks := publicNamePlanChecks(DoctorLiveConfig{Store: st}); len(checks) != 1 || checks[0].Status != "ok" {
		t.Fatalf("doctor after finish = %+v", checks)
	}
	srv.waitAudits()
}

// A probe that answers lets the move remove the old name, and the probe
// treats 404 and 5xx as not answering but a sign-in redirect as answering.
func TestProbePublicPathStatuses(t *testing.T) {
	for code, ok := range map[int]bool{200: true, 302: true, 401: true, 403: true, 404: false, 502: false, 503: false} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if code == 302 {
				w.Header().Set("Location", "/elsewhere")
			}
			w.WriteHeader(code)
		}))
		prev := http.DefaultTransport
		http.DefaultTransport = server.Client().Transport
		err := probePublicPath(context.Background(), server.URL+"/apps/x/")
		http.DefaultTransport = prev
		server.Close()
		if (err == nil) != ok {
			t.Errorf("status %d: err = %v", code, err)
		}
	}
}

// A public name no route carries is flagged; carried names are green.
func TestPublicNamePlanDoctor(t *testing.T) {
	_, st, _, _, _, _ := publicMoveRig(t)
	checks := publicNamePlanChecks(DoctorLiveConfig{Store: st})
	if len(checks) != 1 || checks[0].Status != "ok" {
		t.Fatalf("carried names: %+v", checks)
	}
	if err := router.RecordRoute(st, router.TunnelRoute{Hostname: "stray.example.com", TunnelID: "tid", Service: tunnelEntryService}, "homestead"); err != nil {
		t.Fatal(err)
	}
	checks = publicNamePlanChecks(DoctorLiveConfig{Store: st})
	if len(checks) != 1 || checks[0].Status != "warn" || !strings.Contains(checks[0].Detail, "stray.example.com") ||
		!strings.Contains(checks[0].Detail, "no approved plan") || !strings.Contains(checks[0].Fix.Command, "tunnel plan") {
		t.Fatalf("stray: %+v", checks)
	}
}
