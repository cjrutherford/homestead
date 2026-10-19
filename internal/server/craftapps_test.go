package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// craftAppsCatalog copies shipped packages into a temp catalog: two craft
// apps and one non-craft package.
func craftAppsCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"gridcraft", "pdfcraft", "nextcloud"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "catalog", name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fakeCraftSource(t *testing.T, installed map[string]bool, routes []docker.Route) craftAppsSource {
	dir := craftAppsCatalog(t)
	return craftAppsSource{
		Catalog:   func() (catalog.Catalog, error) { return catalog.Load(dir) },
		Installed: func(name string) bool { return installed[name] },
		Routes:    func() ([]docker.Route, error) { return routes, nil },
	}
}

func getCraftApps(t *testing.T, src craftAppsSource) []CraftApp {
	t.Helper()
	rec := httptest.NewRecorder()
	src.handler()(rec, httptest.NewRequest(http.MethodGet, "/api/v1/craft/apps", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Apps []CraftApp `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Apps
}

func TestCraftAppsEmptyList(t *testing.T) {
	src := fakeCraftSource(t, nil, nil)
	rec := httptest.NewRecorder()
	src.handler()(rec, httptest.NewRequest(http.MethodGet, "/api/v1/craft/apps", nil))
	if !strings.Contains(rec.Body.String(), `"apps":[]`) {
		t.Fatalf("want an empty array, got %s", rec.Body)
	}
}

func TestCraftAppsListsOnlyInstalledCraftPackages(t *testing.T) {
	routes := []docker.Route{
		{Host: "gridcraft.lan", Target: docker.Target{Host: "gridcraft", Port: 8080}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, CraftApp: "gridcraft"},
		{Host: "pdfcraft.lan", Target: docker.Target{Host: "pdfcraft", Port: 8080}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal},
	}
	// nextcloud is installed too but is not a craft package.
	apps := getCraftApps(t, fakeCraftSource(t, map[string]bool{"gridcraft": true, "pdfcraft": true, "nextcloud": true}, routes))
	if len(apps) != 2 {
		t.Fatalf("apps = %+v", apps)
	}
	if apps[0].Name != "gridcraft" || apps[1].Name != "pdfcraft" {
		t.Fatalf("order = %s, %s", apps[0].Name, apps[1].Name)
	}
	g := apps[0]
	if g.Title != "GridCraft" || g.Version == "" || g.Status != "installed" || g.URL != "https://gridcraft.lan/" || g.URLReason != "" {
		t.Fatalf("gridcraft = %+v", g)
	}
	// A table recorded before CraftApp existed still matches by service name.
	if apps[1].URL != "https://pdfcraft.lan/" {
		t.Fatalf("pdfcraft = %+v", apps[1])
	}
}

func TestCraftAppWithoutRouteHasNoURLAndSaysWhy(t *testing.T) {
	apps := getCraftApps(t, fakeCraftSource(t, map[string]bool{"gridcraft": true}, nil))
	if len(apps) != 1 || apps[0].URL != "" || !strings.Contains(apps[0].URLReason, "No edge route") {
		t.Fatalf("apps = %+v", apps)
	}
}

func TestCraftAppsRemovedAppDisappears(t *testing.T) {
	installed := map[string]bool{"gridcraft": true, "pdfcraft": true}
	src := fakeCraftSource(t, installed, nil)
	if got := getCraftApps(t, src); len(got) != 2 {
		t.Fatalf("before = %+v", got)
	}
	delete(installed, "pdfcraft")
	got := getCraftApps(t, src)
	if len(got) != 1 || got[0].Name != "gridcraft" {
		t.Fatalf("after = %+v", got)
	}
}

func TestCraftAppRouteClassification(t *testing.T) {
	cases := []struct {
		name   string
		routes []docker.Route
		url    string
	}{
		{"tls off is http", []docker.Route{{Host: "g.lan", Target: docker.Target{Host: "gridcraft", Port: 8080}, TLS: docker.TLSOff, CraftApp: "gridcraft"}}, "http://g.lan/"},
		{"internal is https", []docker.Route{{Host: "g.lan", Target: docker.Target{Host: "gridcraft", Port: 8080}, TLS: docker.TLSInternal, CraftApp: "gridcraft"}}, "https://g.lan/"},
		{"auto is https", []docker.Route{{Host: "g.example.org", Target: docker.Target{Host: "gridcraft", Port: 8080}, TLS: docker.TLSAuto, CraftApp: "gridcraft"}}, "https://g.example.org/"},
		{"https route beats http route", []docker.Route{
			{Host: "plain.lan", Target: docker.Target{Host: "gridcraft", Port: 8080}, TLS: docker.TLSOff, CraftApp: "gridcraft"},
			{Host: "secure.lan", Target: docker.Target{Host: "gridcraft", Port: 8080}, TLS: docker.TLSInternal, CraftApp: "gridcraft"},
		}, "https://secure.lan/"},
		{"other app's route ignored", []docker.Route{{Host: "p.lan", Target: docker.Target{Host: "pdfcraft", Port: 8080}, TLS: docker.TLSInternal, CraftApp: "pdfcraft"}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			apps := getCraftApps(t, fakeCraftSource(t, map[string]bool{"gridcraft": true}, c.routes))
			if len(apps) != 1 || apps[0].URL != c.url {
				t.Fatalf("apps = %+v, want url %q", apps, c.url)
			}
		})
	}
}

func TestCraftAppsRouteIsSessionClassifiedAndReadOnly(t *testing.T) {
	if c, ok := Classify("GET /api/v1/craft/apps"); !ok || c != ClassSession {
		t.Fatalf("class = %v ok=%v", c, ok)
	}
	if _, ok := Classify("POST /api/v1/craft/apps"); ok {
		t.Fatal("the route must stay read-only")
	}
}

func TestCraftAppsCatalogFailureIs500(t *testing.T) {
	src := craftAppsSource{
		Catalog:   func() (catalog.Catalog, error) { return catalog.Catalog{}, errors.New("boom") },
		Installed: func(string) bool { return false },
		Routes:    func() ([]docker.Route, error) { return nil, nil },
	}
	rec := httptest.NewRecorder()
	src.handler()(rec, httptest.NewRequest(http.MethodGet, "/api/v1/craft/apps", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

// The real store-backed source reflects install and uninstall on the next
// load with no regeneration, and makes no outbound network call: the
// default transport and resolver are replaced by ones that fail the test.
func TestCraftAppsStoreSourceMakesNoNetworkCalls(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	catDir := craftAppsCatalog(t)
	oldStack := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(dir, "stacks", name) }
	t.Cleanup(func() { stackDirFor = oldStack })

	oldTransport, oldResolver := http.DefaultTransport, net.DefaultResolver
	http.DefaultTransport = failingTransport{t}
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		t.Error("handler attempted a DNS lookup")
		return nil, errors.New("network forbidden")
	}}
	t.Cleanup(func() { http.DefaultTransport, net.DefaultResolver = oldTransport, oldResolver })

	src := storeCraftAppsSource(st, catDir)
	if got := getCraftApps(t, src); len(got) != 0 {
		t.Fatalf("nothing installed, got %+v", got)
	}
	stack := filepath.Join(dir, "stacks", "gridcraft")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stack, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := getCraftApps(t, src); len(got) != 1 || got[0].URL != "" {
		t.Fatalf("installed without route: %+v", got)
	}
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: []docker.Route{
		{Host: "gridcraft.lan", Target: docker.Target{Host: "gridcraft", Port: 8080}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, CraftApp: "gridcraft"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := getCraftApps(t, src); len(got) != 1 || got[0].URL != "https://gridcraft.lan/" {
		t.Fatalf("with route: %+v", got)
	}
	if err := os.Remove(filepath.Join(stack, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	if got := getCraftApps(t, src); len(got) != 0 {
		t.Fatalf("after uninstall: %+v", got)
	}
}

type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("handler made an outbound HTTP request to %s", r.URL)
	return nil, errors.New("network forbidden")
}

// The handler's source file may not import or call anything that dials
// out: structure check beside the behavioral one above.
func TestCraftAppsSourceHasNoNetworkClient(t *testing.T) {
	raw, err := os.ReadFile("craftapps.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"http.Get", "http.Post", "http.Client", "http.DefaultClient", "net.Dial", "net.Lookup", "os/exec", "NewRequest"} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("craftapps.go must not use %s", banned)
		}
	}
}

func TestCraftAppCarriesManifestBlurb(t *testing.T) {
	apps := getCraftApps(t, fakeCraftSource(t, map[string]bool{"gridcraft": true}, nil))
	if len(apps) != 1 || apps[0].Blurb != "Spreadsheet that runs in your browser." {
		t.Fatalf("apps = %+v", apps)
	}
}
