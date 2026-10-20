package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/localdns"
	"homestead/internal/store"
)

func namesFixture(t *testing.T, startErr error) (*Server, *LocalNames, *int) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	set := docker.RouteSet{Routes: []docker.Route{
		{Host: "homestead.lan", Dashboard: true, Aliases: []docker.Hostname{"homestead.local"}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal},
		{Host: "immich.lan", Target: docker.Target{Host: "immich", Port: 2283}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal},
		{Host: "photos.example.com", Target: docker.Target{Host: "immich", Port: 2283}, Exposure: docker.ExposurePublic, TLS: docker.TLSAuto},
	}}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	names := NewLocalNames(st)
	starts := 0
	names.links = func() ([]localdns.Link, error) {
		return []localdns.Link{{Addr: netip.MustParseAddr("192.168.1.10"), Prefix: netip.MustParsePrefix("192.168.1.0/24")}}, nil
	}
	names.start = func(cfg localdns.Config) (*localdns.Server, error) {
		starts++
		if startErr != nil {
			return nil, startErr
		}
		return &localdns.Server{Listening: []string{"192.168.1.10:53"}}, nil
	}
	srv := New("127.0.0.1:0")
	srv.MountNames(names)
	return srv, names, &starts
}

func namesCall(t *testing.T, srv *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	return rec
}

func TestNamesStatusListsLocalNames(t *testing.T) {
	srv, _, _ := namesFixture(t, nil)
	rec := namesCall(t, srv, http.MethodGet, "/api/v1/names", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	var got namesStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Resolver.Enabled || got.Resolver.Running {
		t.Fatalf("resolver on by default: %+v", got.Resolver)
	}
	if strings.Join(got.Resolver.Names, ",") != "homestead.lan,immich.lan" {
		t.Fatalf("resolver names %v", got.Resolver.Names)
	}
	if len(got.Apps) != 2 || got.Apps[1].Host != "immich.lan" || strings.Join(got.Apps[1].Names, ",") != "immich.lan,immich.local" {
		t.Fatalf("apps %+v", got.Apps)
	}
	if !strings.Contains(got.Resolver.RouterHint, "conditional forward") {
		t.Fatalf("hint %q", got.Resolver.RouterHint)
	}
}

func TestResolverPlanThenApply(t *testing.T) {
	srv, names, starts := namesFixture(t, nil)
	rec := namesCall(t, srv, http.MethodPost, "/api/v1/names/resolver/plan", map[string]any{"enabled": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("plan %d %s", rec.Code, rec.Body)
	}
	var plan resolverPlan
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if on, _ := names.Enabled(); on {
		t.Fatal("plan changed state")
	}
	if strings.Join(plan.Listen, ",") != "192.168.1.10:53" || len(plan.Firewall) < 2 || plan.Digest == "" {
		t.Fatalf("plan %+v", plan)
	}
	drops := 0
	for _, f := range plan.Firewall {
		if strings.HasSuffix(f.Name, "public-drop") {
			drops++
		}
	}
	if drops != 2 {
		t.Fatalf("firewall %+v", plan.Firewall)
	}

	rec = namesCall(t, srv, http.MethodPost, "/api/v1/names/resolver/apply", map[string]any{"enabled": true, "digest": "stale"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale digest %d", rec.Code)
	}
	rec = namesCall(t, srv, http.MethodPost, "/api/v1/names/resolver/apply", map[string]any{"enabled": true, "digest": plan.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply %d %s", rec.Code, rec.Body)
	}
	var status namesStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &status)
	if !status.Resolver.Enabled || !status.Resolver.Running {
		t.Fatalf("after apply %+v", status.Resolver)
	}
	if on, _ := names.Enabled(); !on {
		t.Fatal("not stored")
	}
	var actions string
	_ = names.st.DB().QueryRow(`SELECT group_concat(action) FROM audit_entries`).Scan(&actions)
	if !strings.Contains(actions, "names.resolver.enable") {
		t.Fatalf("audit %q", actions)
	}
	// Three port probes (plan, stale apply, apply) and one start.
	if *starts != 4 {
		t.Fatalf("starts %d", *starts)
	}

	rec = namesCall(t, srv, http.MethodPost, "/api/v1/names/resolver/plan", map[string]any{"enabled": false})
	_ = json.Unmarshal(rec.Body.Bytes(), &plan)
	rec = namesCall(t, srv, http.MethodPost, "/api/v1/names/resolver/apply", map[string]any{"enabled": false, "digest": plan.Digest})
	_ = json.Unmarshal(rec.Body.Bytes(), &status)
	if rec.Code != http.StatusOK || status.Resolver.Enabled || status.Resolver.Running {
		t.Fatalf("disable %d %+v", rec.Code, status.Resolver)
	}
}

func TestResolverPlanWarnsWhenPortTaken(t *testing.T) {
	srv, names, _ := namesFixture(t, errors.New("address already in use"))
	rec := namesCall(t, srv, http.MethodPost, "/api/v1/names/resolver/plan", map[string]any{"enabled": true})
	var plan resolverPlan
	_ = json.Unmarshal(rec.Body.Bytes(), &plan)
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "Port 53 is not free") {
		t.Fatalf("warnings %v", plan.Warnings)
	}
	// Approving anyway stores the switch; the doctor reports the failure.
	rec = namesCall(t, srv, http.MethodPost, "/api/v1/names/resolver/apply", map[string]any{"enabled": true, "digest": plan.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply %d", rec.Code)
	}
	checks := resolverDoctorCheck(names)()
	if len(checks) != 1 || checks[0].Name != "local-resolver" || !strings.Contains(checks[0].Detail, "already in use") {
		t.Fatalf("doctor %+v", checks)
	}
}

func TestResolverRequestValidation(t *testing.T) {
	srv, _, _ := namesFixture(t, nil)
	if rec := namesCall(t, srv, http.MethodPost, "/api/v1/names/resolver/plan", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled %d", rec.Code)
	}
	if rec := namesCall(t, srv, http.MethodPost, "/api/v1/names/resolver/plan", map[string]any{"enabled": true, "upstream": "1.1.1.1"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field %d", rec.Code)
	}
}
