package exposure

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

func openExposureWizard(t *testing.T) *Wizard {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewWizard(wizard.New(s))
}

func wizardRoute() docker.Route {
	return docker.Route{
		Host:     "app.example.com",
		Target:   docker.Target{Host: "app", Port: 8080},
		Exposure: docker.ExposureLocal,
		TLS:      docker.TLSInternal,
	}
}

func wizardPayload(t *testing.T, route docker.Route, extra map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	payload := map[string]any{
		"route":     decoded,
		"to":        "public",
		"zone":      "example.com",
		"public_ip": "203.0.113.7",
	}
	for key, value := range extra {
		payload[key] = value
	}
	return payload
}

func TestExposureWizardFlow(t *testing.T) {
	w := openExposureWizard(t)
	payload := wizardPayload(t, wizardRoute(), nil)
	for _, step := range ExposureSteps {
		if err := w.Save(step, payload); err != nil {
			t.Fatalf("save %s: %v", step, err)
		}
	}
	plan, err := w.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.From != docker.ExposureLocal || plan.To != docker.ExposurePublic ||
		len(plan.DNS) != 1 || plan.TLS.Action != TLSIssue ||
		len(plan.Firewall) != 2 || plan.Edge.Exposure != docker.ExposurePublic {
		t.Fatalf("plan = %+v", plan)
	}
	// The plan recomputes: saved inputs drive, never stored output.
	again, err := w.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(again.DNS) != 1 || again.TLS.Name != plan.TLS.Name {
		t.Fatalf("replan = %+v", again)
	}
}

func TestExposureWizardRejects(t *testing.T) {
	w := openExposureWizard(t)
	good := wizardPayload(t, wizardRoute(), nil)
	cases := []struct {
		name    string
		step    string
		payload map[string]any
		want    string
	}{
		{"unknown step", "nope", good, "unknown step"},
		{"no route", "route", map[string]any{}, "needs a route"},
		{"bad route", "route", wizardPayload(t, docker.Route{}, nil), "route.host"},
		{"no target", "target", wizardPayload(t, wizardRoute(),
			map[string]any{"to": "", "zone": "", "public_ip": ""}), "target must be"},
		{"already there", "target", wizardPayload(t, func() docker.Route {
			r := wizardRoute()
			r.Exposure = docker.ExposurePublic
			return r
		}(), nil), "already at public"},
		{"bad ip", "target", wizardPayload(t, wizardRoute(),
			map[string]any{"public_ip": "nope"}), "public address"},
		{"private public", "review", wizardPayload(t, func() docker.Route {
			r := wizardRoute()
			r.Host = "app.lan"
			return r
		}(), nil), "cannot go public"},
		{"no public ip", "review", wizardPayload(t, wizardRoute(),
			map[string]any{"public_ip": ""}), "public address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := w.Save(tc.step, tc.payload)
			if err == nil {
				t.Fatal("saved invalid payload")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
	empty := openExposureWizard(t)
	if _, err := empty.Plan(); err == nil {
		t.Fatal("planned without selection")
	}
}

func TestWizardMarksDashboardFromRecordedRoutes(t *testing.T) {
	w := openExposureWizard(t)
	w.SetRoutes(func() (docker.RouteSet, error) {
		return docker.RouteSet{Routes: []docker.Route{{
			Host: "homestead.lan", Aliases: []docker.Hostname{"homestead.local"},
			Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Dashboard: true,
		}}}, nil
	})
	// The payload omits the dashboard flag.
	route := wizardRoute()
	route.Host = "homestead.local"
	payload := wizardPayload(t, route, nil)
	var err error
	for _, step := range ExposureSteps {
		if err = w.Save(step, payload); err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "tunnel") {
		t.Fatalf("public dashboard via omitted flag: err = %v", err)
	}
	// Other hosts still plan.
	if err := w.Save("target", wizardPayload(t, wizardRoute(), nil)); err != nil {
		t.Fatalf("non-dashboard: %v", err)
	}
}
