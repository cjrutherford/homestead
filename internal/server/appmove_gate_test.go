package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/operation"
)

// S472 (ADR-061, ADR-055): Navidrome's move to its path takes its
// forward-auth gate inside the dashboard site: the bypass regexes gain the
// prefix, the base URL is set, and the old public name goes.
func TestAppMoveNavidromeCarriesTheGateToItsPath(t *testing.T) {
	srv, st, steps, _, _ := moveRigCF(t)
	installApp(t, st, "navidrome", docker.Route{Host: "navidrome.lan", Target: docker.Target{Host: "navidrome", Port: 4533},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal,
		Gate: &docker.Gate{Canonical: "navidrome.lan", CopyHeaders: []string{"X-Authentik-Username"}, Bypass: []string{`^/rest/.*`, `^/share/.*`}}})
	status := moveStatus(t, srv, "navidrome")
	if !status.Offered {
		t.Fatalf("status = %+v", status)
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/navidrome/move/plan", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body.String())
	}
	var plan appMoveView
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.AppSettings, "\n"), "ND_BASEURL=/apps/navidrome") {
		t.Fatalf("settings = %v", plan.AppSettings)
	}
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/navidrome/move/apply", map[string]any{"digest": plan.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("operation = %+v", op)
	}
	r := movedRoute(t, st, "navidrome.lan")
	if r.Path != "/apps/navidrome/" || r.Gate == nil || r.StripPrefix {
		t.Fatalf("route = %+v", r)
	}
	if got := strings.Join(r.Gate.Bypass, " "); got != `^/apps/navidrome/rest/.* ^/apps/navidrome/share/.*` {
		t.Fatalf("bypass = %q", got)
	}
	if !strings.Contains(strings.Join(*steps, "\n"), "env navidrome ND_BASEURL=/apps/navidrome") {
		t.Fatalf("steps = %v", *steps)
	}
}
