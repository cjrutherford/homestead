package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDrift(t *testing.T) {
	manifest := map[string]string{"web": "example/web:2.0.0", "worker": "example/worker:1.0.0"}
	running := map[string]string{"web": "example/web:1.0.0", "worker": "example/worker:1.0.0"}
	got, err := Drift(manifest, running)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(got) != 1 || got[0].Service != "web" || got[0].Target != "example/web:2.0.0" ||
		got[0].Source != SourceManifest {
		t.Fatalf("drift = %+v", got)
	}
	same, err := Drift(manifest, manifest)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(same) != 0 {
		t.Fatalf("drift = %+v", same)
	}
	// Unknown running services stay out: drift covers pins, not strays.
	extra := map[string]string{"web": "example/web:2.0.0", "ghost": "example/ghost:1.0.0"}
	got, err = Drift(map[string]string{"web": "example/web:2.0.0"}, extra)
	if err != nil || len(got) != 0 {
		t.Fatalf("drift = %+v %v", got, err)
	}
	if _, err := Drift(manifest, nil); err == nil {
		t.Fatal("drifted empty running set")
	}
}

func TestNewerTags(t *testing.T) {
	pinned := "35.0.1-apache"
	tags := []string{"latest", "35.0.1-apache", "35.0.2-apache", "35.0.2-fpm", "34.9.9-apache", "9", "35.0.10-apache", "35.0.2"}
	got := NewerTags(pinned, tags)
	want := []string{"35.0.10-apache", "35.0.2-apache"}
	if len(got) != len(want) {
		t.Fatalf("newer = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("newer = %v", got)
		}
	}
	if newer := NewerTags("9", []string{"10", "9", "8"}); len(newer) != 1 || newer[0] != "10" {
		t.Fatalf("numeric = %v", newer)
	}
	if len(NewerTags("1.0.0", []string{"latest", "stable"})) != 0 {
		t.Fatal("non-numeric compared")
	}
}

func hubDouble(t *testing.T, pages map[string]string) (string, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path+"?"+r.URL.RawQuery)
		body, ok := pages[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": func() []any {
				var out []any
				for _, name := range strings.Split(body, ",") {
					out = append(out, map[string]string{"name": name})
				}
				return out
			}(),
			"next": "",
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &calls
}

func TestRegistryTags(t *testing.T) {
	base, calls := hubDouble(t, map[string]string{
		"/library/nextcloud/tags": "latest,35.0.2-apache,35.0.1-apache",
		"/jellyfin/jellyfin/tags": "12.2,12.1",
	})
	client := &http.Client{Timeout: 10 * time.Second}
	tags, err := RegistryTags(client, base, "nextcloud:35.0.1-apache")
	if err != nil {
		t.Fatalf("RegistryTags: %v", err)
	}
	if len(tags) != 3 || tags[0] != "latest" {
		t.Fatalf("tags = %v", tags)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], "page_size=100") {
		t.Fatalf("calls = %v", *calls)
	}
	tags, err = RegistryTags(client, base, "jellyfin/jellyfin:12.1")
	if err != nil || len(tags) != 2 {
		t.Fatalf("tags = %v %v", tags, err)
	}
	if _, err := RegistryTags(client, base, "ghcr.io/immich-app/immich-server:v3.3.0"); err == nil {
		t.Fatal("queried ghcr on Docker Hub")
	}
	if _, err := RegistryTags(client, base, "missing:1.0.0"); err == nil {
		t.Fatal("listed missing repo")
	}
	updates, err := RegistryUpdates(client, base, "nextcloud", "35.0.1-apache")
	if err != nil {
		t.Fatalf("RegistryUpdates: %v", err)
	}
	if len(updates) != 1 || updates[0] != "35.0.2-apache" {
		t.Fatalf("updates = %v", updates)
	}
}
