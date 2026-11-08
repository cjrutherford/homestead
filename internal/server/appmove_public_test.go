package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	installHostApp(t, st, "wordcraft", "wordcraft", 8080)
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		if set.Routes[i].Host == "wordcraft.lan" {
			set.Routes[i].Tunnel = []docker.Hostname{"wordcraft.example.com"}
		}
	}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	if s := moveStatus(t, srv, "wordcraft"); s.Offered || !strings.Contains(s.Reason, "never published") {
		t.Fatalf("wordcraft = %+v", s)
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
