package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/exposure"
	"homestead/internal/storage"
)

func TestParseImage(t *testing.T) {
	cases := []struct {
		image    string
		registry string
		repo     string
		tag      string
		err      bool
	}{
		{"jellyfin", RegistryHub, "library/jellyfin", "", false},
		{"jellyfin:10.10.3", RegistryHub, "library/jellyfin", "10.10.3", false},
		{"jellyfin/jellyfin:12.1", RegistryHub, "jellyfin/jellyfin", "12.1", false},
		{"docker.io/library/redis:7", RegistryHub, "library/redis", "7", false},
		{"ghcr.io/immich-app/immich-server:v1.119.0", RegistryGHCR, "immich-app/immich-server", "v1.119.0", false},
		{"ghcr.io/paperless-ngx/paperless-ngx:2.15.1", RegistryGHCR, "paperless-ngx/paperless-ngx", "2.15.1", false},
		{"ghcr.io/owner/app@sha256:abc", RegistryGHCR, "owner/app", "", false},
		{"localhost:5000/app:1.0", RegistryUnknown, "", "", true},
		{"lscr.io/linuxserver/sonarr:4.0", RegistryUnknown, "", "", true},
		{"", RegistryUnknown, "", "", true},
	}
	for _, c := range cases {
		ref, err := ParseImage(c.image)
		if c.err {
			if err == nil {
				t.Fatalf("%q: want an error, got %+v", c.image, ref)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", c.image, err)
		}
		if ref.Registry != c.registry || ref.Repository != c.repo || ref.Tag != c.tag {
			t.Fatalf("%q: ref = %+v", c.image, ref)
		}
	}
}

// ghcrDouble serves the token and tag endpoints, recording the token
// request and the authorization the listing used.
func ghcrDouble(t *testing.T, tags []string, status int) (*httptest.Server, *[]string, *string) {
	t.Helper()
	var calls []string
	var auth string
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "pull-token"})
	})
	mux.HandleFunc("/v2/immich-app/immich-server/tags/list", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		auth = r.Header.Get("Authorization")
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "immich-app/immich-server", "tags": tags})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls, &auth
}

func TestGHCRDiscoveryUsesTheTokenFlow(t *testing.T) {
	srv, calls, auth := ghcrDouble(t, []string{"v1.118.0", "v1.119.0", "latest"}, 0)
	rc := RegistryClient{HTTP: &http.Client{Timeout: 5 * time.Second}, GHCRBase: srv.URL}

	discovery := rc.Discover("immich", "ghcr.io/immich-app/immich-server:v1.119.0")
	if discovery.Err != nil {
		t.Fatalf("Discover: %v", discovery.Err)
	}
	if discovery.Registry != RegistryGHCR || discovery.Repository != "immich-app/immich-server" {
		t.Fatalf("discovery = %+v", discovery)
	}
	if len(discovery.Tags) != 3 {
		t.Fatalf("tags = %v", discovery.Tags)
	}
	if len(*calls) != 2 || !strings.Contains((*calls)[0], "scope=repository:immich-app/immich-server:pull") {
		t.Fatalf("calls = %v", *calls)
	}
	if *auth != "Bearer pull-token" {
		t.Fatalf("authorization = %q", *auth)
	}
	if newer := NewerTags("v1.119.0", discovery.Tags); len(newer) != 0 {
		t.Fatalf("newer = %v, want none", newer)
	}
	if newer := NewerTags("v1.118.0", discovery.Tags); len(newer) != 1 || newer[0] != "v1.119.0" {
		t.Fatalf("newer = %v", newer)
	}
}

func TestGHCRDiscoveryFollowsOneContinuationPage(t *testing.T) {
	var seen []string
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "t"})
	})
	mux.HandleFunc("/v2/owner/app/tags/list", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RawQuery)
		if r.URL.Query().Get("last") == "" {
			w.Header().Set("Link", `</v2/owner/app/tags/list?n=1000&last=1.1.0>; rel="next"`)
			_ = json.NewEncoder(w).Encode(map[string]any{"tags": []string{"1.0.0", "1.1.0"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tags": []string{"1.2.0"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := RegistryClient{HTTP: srv.Client(), GHCRBase: srv.URL}
	discovery := rc.Discover("app", "ghcr.io/owner/app:1.0.0")
	if discovery.Err != nil {
		t.Fatalf("Discover: %v", discovery.Err)
	}
	if len(discovery.Tags) != 3 || len(seen) != 2 {
		t.Fatalf("tags = %v, pages = %v", discovery.Tags, seen)
	}
}

func TestGHCRDiscoveryReportsPrivateOrMissing(t *testing.T) {
	srv, _, _ := ghcrDouble(t, nil, http.StatusUnauthorized)
	rc := RegistryClient{HTTP: srv.Client(), GHCRBase: srv.URL}
	discovery := rc.Discover("app", "ghcr.io/immich-app/immich-server:v1.0.0")
	if discovery.Err == nil || !strings.Contains(discovery.Err.Error(), "private") {
		t.Fatalf("err = %v, want a private-or-missing error", discovery.Err)
	}
}

func TestDiscoverRefusesUnsupportedRegistries(t *testing.T) {
	rc := RegistryClient{}
	discovery := rc.Discover("sonarr", "lscr.io/linuxserver/sonarr:4.0")
	if discovery.Err == nil || !strings.Contains(discovery.Err.Error(), "lscr.io") {
		t.Fatalf("err = %v, want the registry named", discovery.Err)
	}
	if discovery.Registry != RegistryUnknown {
		t.Fatalf("registry = %q", discovery.Registry)
	}
}

func TestDiscoverKeepsHubOnTheHubPath(t *testing.T) {
	base, calls := hubDouble(t, map[string]string{"/library/nextcloud/tags": "35.0.1-apache"})
	rc := RegistryClient{HTTP: &http.Client{Timeout: 5 * time.Second}, HubBase: base, GHCRBase: "http://127.0.0.1:1"}
	discovery := rc.Discover("web", "nextcloud:35.0.1-apache")
	if discovery.Err != nil {
		t.Fatalf("Discover: %v", discovery.Err)
	}
	if discovery.Registry != RegistryHub || len(*calls) != 1 {
		t.Fatalf("discovery = %+v calls = %v", discovery, *calls)
	}
}

func provenanceManifest() catalog.Manifest {
	return catalog.Manifest{
		Name: "demo", Version: "1.0.0", Title: "Demo", Blurb: "Demo package.",
		Description: "Demo package for update planning.", Category: "media", Icon: "mdi:test",
		Services: []catalog.Service{
			{Name: "web", Image: "nextcloud:35.0.1-apache", Exposes: "web"},
			{Name: "media", Image: "ghcr.io/owner/media:1.0.0", Exposes: "web"},
		},
		Requirements: catalog.Requirements{
			MemoryMB: 512,
			Storage:  []catalog.StorageNeed{{Class: storage.ClassApplication, MinBytes: 1 << 20}},
			Ports:    []int{8090},
		},
		Exposure: exposure.PackageExposure{Package: "demo", Endpoints: []exposure.EndpointExposure{
			{Endpoint: "web", Recommended: "local"},
		}},
	}
}

func TestPlanRecordsDiscoveryProvenance(t *testing.T) {
	running := map[string]string{
		"web":   "nextcloud:35.0.1-apache",
		"media": "ghcr.io/owner/media:1.0.0",
	}
	plan, err := PlanUpdateFromDiscoveries(provenanceManifest(), running, map[string]Discovery{
		"web": {Service: "web", Registry: RegistryHub, Repository: "library/nextcloud",
			Tags: []string{"35.0.2-apache"}},
		"media": {Service: "media", Registry: RegistryGHCR, Repository: "owner/media",
			Err: errTestRegistry},
	})
	if err != nil {
		t.Fatalf("PlanUpdateFromDiscoveries: %v", err)
	}
	if len(plan.Provenance) != 2 {
		t.Fatalf("provenance = %+v", plan.Provenance)
	}
	if p := plan.Provenance[0]; p.Service != "media" || p.Discovered || p.Reason != "update: registry down" {
		t.Fatalf("media provenance = %+v", p)
	}
	if p := plan.Provenance[1]; p.Service != "web" || !p.Discovered || p.Registry != RegistryHub {
		t.Fatalf("web provenance = %+v", p)
	}
	// A failed listing never becomes a target: the pin still governs.
	for _, move := range plan.Services {
		if move.Service == "media" {
			t.Fatalf("failed registry produced a move: %+v", move)
		}
	}
}

func TestPlanRecordsLocalBuilds(t *testing.T) {
	m := provenanceManifest()
	m.Services[1].Image = "craft/media:1.0.0"
	plan, err := PlanUpdateFromDiscoveries(m, map[string]string{
		"web": "nextcloud:35.0.1-apache", "media": "craft/media:1.0.0",
	}, map[string]Discovery{
		"media": {Service: "media", Registry: RegistryLocal, Repository: "craft/media:1.0.0", Local: true},
	})
	if err != nil {
		t.Fatalf("PlanUpdateFromDiscoveries: %v", err)
	}
	if len(plan.Provenance) != 1 || !plan.Provenance[0].Local || plan.Provenance[0].Discovered {
		t.Fatalf("provenance = %+v", plan.Provenance)
	}
}

func TestVersionComparisonToleratesVPrefix(t *testing.T) {
	newer := NewerTags("v1.118.0", []string{"v1.119.0", "1.119.1", "latest", "v1.118.0"})
	if len(newer) != 2 {
		t.Fatalf("newer = %v, want both v-prefixed and bare versions", newer)
	}
	if !majorBump("app:v1.9.0", "app:v2.0.0") {
		t.Fatal("v-prefixed major bump not detected")
	}
	if majorBump("app:v2.0.0", "app:v1.9.0") {
		t.Fatal("downgrade reported as a major bump")
	}
}

var errTestRegistry = &registryError{"update: registry down"}

type registryError struct{ message string }

func (e *registryError) Error() string { return e.message }
