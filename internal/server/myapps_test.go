package server

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/people"
	"homestead/internal/store"
)

func myAppsRoutes() []docker.Route {
	return []docker.Route{
		{Host: "homestead.lan", Dashboard: true, Tunnel: []docker.Hostname{"home.example.com"}},
		{Host: "nextcloud.lan", Path: "/apps/nextcloud/", Published: true, Target: docker.Target{Host: "nextcloud"}},
		{Host: "jellyfin", Path: "/apps/jellyfin/", Target: docker.Target{Host: "jellyfin"}},
	}
}

func mySource(t *testing.T, installed map[string]bool, routes []docker.Route) myAppsSource {
	dir := craftAppsCatalog(t)
	return myAppsSource{
		Catalog:   func() (catalog.Catalog, error) { return catalog.Load(dir) },
		Installed: func(n string) bool { return installed[n] },
		Routes:    func() ([]docker.Route, error) { return routes, nil },
	}
}

func TestMyAppsOwnerListsInstalledAppsWithAddresses(t *testing.T) {
	src := mySource(t, map[string]bool{"nextcloud": true, "gridcraft": true, "pdfcraft": false}, myAppsRoutes())
	apps, err := src.list(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// gridcraft is installed but has no route: no web address.
	if len(apps) != 1 || apps[0].Name != "nextcloud" || apps[0].Role != "owner" || apps[0].Icon == "" || apps[0].Title == "" {
		t.Fatalf("apps: %+v", apps)
	}
	if apps[0].URL != "https://homestead.lan/apps/nextcloud/" {
		t.Fatalf("url: %q", apps[0].URL)
	}
}

func TestMyAppsPublicVersusLocalURL(t *testing.T) {
	src := mySource(t, map[string]bool{"nextcloud": true}, myAppsRoutes())
	pub, _ := src.list(true, nil)
	if len(pub) != 1 || pub[0].URL != "https://home.example.com/apps/nextcloud/" {
		t.Fatalf("public: %+v", pub)
	}
	// Not published: the public name still gets the local address.
	routes := myAppsRoutes()
	routes[1].Published = false
	src = mySource(t, map[string]bool{"nextcloud": true}, routes)
	pub, _ = src.list(true, nil)
	if pub[0].URL != "https://homestead.lan/apps/nextcloud/" {
		t.Fatalf("unpublished: %+v", pub)
	}
}

func TestMyAppsGrantFiltering(t *testing.T) {
	src := mySource(t, map[string]bool{"nextcloud": true, "gridcraft": true}, append(myAppsRoutes(),
		docker.Route{Host: "gridcraft.lan", CraftApp: "gridcraft", Target: docker.Target{Host: "gridcraft"}}))
	cases := []struct {
		name   string
		grants map[string]string
		want   []string
		role   string
	}{
		{"user", map[string]string{"nextcloud": "user"}, []string{"nextcloud"}, "user"},
		{"admin", map[string]string{"nextcloud": "admin"}, []string{"nextcloud"}, "admin"},
		{"no grant", map[string]string{}, nil, ""},
		{"app not installed", map[string]string{"pdfcraft": "user"}, nil, ""},
	}
	for _, c := range cases {
		apps, err := src.list(false, c.grants)
		if err != nil {
			t.Fatal(err)
		}
		if len(apps) != len(c.want) {
			t.Fatalf("%s: %+v", c.name, apps)
		}
		for i, a := range apps {
			if a.Name != c.want[i] || a.Role != c.role {
				t.Errorf("%s: %+v", c.name, a)
			}
		}
	}
}

func TestMyAppsRoutesAreSessionClass(t *testing.T) {
	for _, p := range []string{"GET /api/v1/me/apps", "GET /api/v1/people/{id}/apps"} {
		if c, ok := Classify(p); !ok || c != ClassSession {
			t.Errorf("%s: %v %v", p, c, ok)
		}
	}
}

func TestMyAppsHandlerShape(t *testing.T) {
	src := mySource(t, map[string]bool{"nextcloud": true}, myAppsRoutes())
	s := New("")
	s.mountMyApps(src)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/me/apps", nil))
	var out struct{ Apps []MyApp }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Apps) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/people/x/apps", nil))
	if rec.Code != 404 {
		t.Fatalf("bad id: %d", rec.Code)
	}

	// A real record: granted user, then disabled, then unknown.
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s.people = &people.Service{DB: st.DB()}
	if _, err := st.DB().Exec(`INSERT INTO people (id, display_name, username, email, authentik_pk, state, origin, created_by, created_at)
		VALUES (7, 'Sam', 'sam', '', 1, 'active', 'created', 'o', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO people_grants (person_id, app, role) VALUES (7, 'nextcloud', 'admin')`); err != nil {
		t.Fatal(err)
	}
	get := func(id string) (int, []MyApp) {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/people/"+id+"/apps", nil))
		var o struct{ Apps []MyApp }
		_ = json.Unmarshal(rec.Body.Bytes(), &o)
		return rec.Code, o.Apps
	}
	if code, apps := get("7"); code != 200 || len(apps) != 1 || apps[0].Role != "admin" {
		t.Fatalf("granted: %d %+v", code, apps)
	}
	if code, _ := get("99"); code != 404 {
		t.Fatalf("unknown: %d", code)
	}
	if _, err := st.DB().Exec(`UPDATE people SET state = 'disabled' WHERE id = 7`); err != nil {
		t.Fatal(err)
	}
	if code, apps := get("7"); code != 200 || len(apps) != 0 {
		t.Fatalf("disabled: %d %+v", code, apps)
	}
}
