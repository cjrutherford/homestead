package catalog

import (
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/storage"
)

func TestNextcloudTrustsItsRouteHostname(t *testing.T) {
	manifest, err := repoCatalog(t).Get("nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	req := InstallRequest{
		Manifest: manifest, Host: installHost(), Domain: "lan",
		ContainerPorts: map[string]int{"nextcloud": 80},
		Storage:        map[storage.Class]string{storage.ClassApplication: "/mnt/data", storage.ClassBulk: "/mnt/bulk"},
	}
	plan, err := PlanInstall(req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	// S315 (ADR-044): Nextcloud answers under the appliance's names at
	// /apps/nextcloud/, and builds its links there.
	for _, want := range []string{"NEXTCLOUD_TRUSTED_DOMAINS: homestead.lan homestead.local", "OVERWRITEPROTOCOL: https",
		"OVERWRITECLIURL: https://homestead.lan/apps/nextcloud", "OVERWRITEWEBROOT: /apps/nextcloud"} {
		if !strings.Contains(plan.Compose.ComposeYAML, want) {
			t.Fatalf("missing %q:\n%s", want, plan.Compose.ComposeYAML)
		}
	}
	if strings.Contains(plan.Compose.ComposeYAML, "OVERWRITEHOST") {
		t.Fatalf("overwritehost pins every link to one name:\n%s", plan.Compose.ComposeYAML)
	}
	// An owner who named the dashboard elsewhere gets those names.
	req.ApplianceNames = []string{"home.example.home", "homestead.local"}
	plan, err = PlanInstall(req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if !strings.Contains(plan.Compose.ComposeYAML, "NEXTCLOUD_TRUSTED_DOMAINS: home.example.home homestead.local") ||
		!strings.Contains(plan.Compose.ComposeYAML, "OVERWRITECLIURL: https://home.example.home/apps/nextcloud") ||
		strings.Contains(plan.Compose.ComposeYAML, "{{") {
		t.Fatalf("appliance names not trusted:\n%s", plan.Compose.ComposeYAML)
	}
}

// TestPackagesRenderTheirPath covers each package's serving (ADR-044):
// the route's path and strip, the .well-known redirects, and the setting
// that hands the path to the app.
func TestPackagesRenderTheirPath(t *testing.T) {
	cat := repoCatalog(t)
	cases := []struct {
		name, service string
		port          int
		path          string
		strip         bool
		redirects     int
		compose       []string
		seed          string
	}{
		{"nextcloud", "nextcloud", 80, "/apps/nextcloud/", true, 2, []string{"OVERWRITEWEBROOT: /apps/nextcloud"}, ""},
		{"authentik", "server", 9000, "/apps/auth/", false, 0, []string{"AUTHENTIK_WEB__PATH: /apps/auth/"}, ""},
		{"jellyfin", "jellyfin", 8096, "/apps/jellyfin/", false, 0, nil, "<BaseUrl>/apps/jellyfin</BaseUrl>"},
		{"immich", "server", 2283, "", false, 0, nil, ""},
	}
	for _, c := range cases {
		manifest, err := cat.Get(c.name)
		if err != nil {
			t.Fatal(err)
		}
		storageMap := map[storage.Class]string{storage.ClassApplication: "/mnt/data", storage.ClassBulk: "/mnt/bulk"}
		ports := map[string]int{}
		for _, svc := range manifest.Services {
			if svc.Exposes != "" {
				ports[svc.Name] = c.port
			}
		}
		plan, err := PlanInstall(InstallRequest{Manifest: manifest, Host: installHost(), Domain: "lan",
			ContainerPorts: ports, Storage: storageMap})
		if err != nil {
			t.Fatalf("%s: PlanInstall: %v", c.name, err)
		}
		route := plan.Routes[0]
		if route.Path != c.path || route.StripPrefix != c.strip || len(route.Redirects) != c.redirects {
			t.Fatalf("%s: route = %+v", c.name, route)
		}
		if c.path != "" && len(plan.TLS) != 0 {
			t.Fatalf("%s: a path app needs no certificate of its own: %+v", c.name, plan.TLS)
		}
		if c.path == "" && route.Host != "immich.lan" {
			t.Fatalf("%s: host app route = %+v", c.name, route)
		}
		for _, want := range c.compose {
			if strings.Count(plan.Compose.ComposeYAML, want) == 0 {
				t.Fatalf("%s: compose lacks %q:\n%s", c.name, want, plan.Compose.ComposeYAML)
			}
		}
		if c.seed != "" {
			if len(plan.Seeds) != 1 || !strings.Contains(plan.Seeds[0].Content, c.seed) ||
				plan.Seeds[0].Source != "/mnt/data/jellyfin/config/config/network.xml" {
				t.Fatalf("%s: seeds = %+v", c.name, plan.Seeds)
			}
		}
	}
	if m, _ := cat.Get("authentik"); strings.Count(mustGenerate(t, m), "AUTHENTIK_WEB__PATH: /apps/auth/") != 2 {
		t.Fatal("Authentik's server and worker must both carry the path")
	}
}

func mustGenerate(t *testing.T, m Manifest) string {
	t.Helper()
	files, err := Generate(m, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	return files.ComposeYAML
}

func TestServeDeclarationValidation(t *testing.T) {
	m, err := repoCatalog(t).Get("nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Manifest){
		"unknown serve":  func(m *Manifest) { m.Serve = "both" },
		"path on host":   func(m *Manifest) { m.Serve = ServeHost },
		"outside /apps/": func(m *Manifest) { m.Path = "/api/" },
		"seed outside":   func(m *Manifest) { m.Seeds = []Seed{{Service: "nextcloud", Target: "/etc/passwd", Content: "x"}} },
		"seed traversal": func(m *Manifest) {
			m.Seeds = []Seed{{Service: "nextcloud", Target: "/var/www/html/../x", Content: "x"}}
		},
		"bad redirect to": func(m *Manifest) { m.Redirects = []docker.PathRedirect{{From: "/.well-known/caldav", To: "/x"}} },
	} {
		copyM := m
		mutate(&copyM)
		if err := copyM.Validate(); err == nil {
			t.Fatalf("%s: validated", name)
		}
	}
}

func TestRouteHostTokenWithoutRouteIsLeftOut(t *testing.T) {
	manifest, err := repoCatalog(t).Get("nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	files, err := Generate(manifest, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(files.ComposeYAML, "NEXTCLOUD_TRUSTED_DOMAINS") || strings.Contains(files.ComposeYAML, "OVERWRITECLIURL") ||
		strings.Contains(files.ComposeYAML, "{{") {
		t.Fatalf("unrouted render carries the variable:\n%s", files.ComposeYAML)
	}
}

func TestRouteHostTokenNeedsAnEndpoint(t *testing.T) {
	svc := Service{Name: "db", Image: "postgres:16", Environment: map[string]string{"X": RouteHostToken}}
	if err := svc.Validate(map[string]bool{}); err == nil || !strings.Contains(err.Error(), "exposes no endpoint") {
		t.Fatalf("err = %v", err)
	}
}

// The edge proxy holds ports 80 and 443 on the host; Nextcloud publishes
// no host port, so it must still pass its requirements.
func TestNextcloudRequirementsPassWithTheEdgeOnPorts80And443(t *testing.T) {
	manifest, err := repoCatalog(t).Get("nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Requirements.Ports) != 0 {
		t.Fatalf("nextcloud declares host ports: %v", manifest.Requirements.Ports)
	}
	host := installHost()
	host.UsedPorts = map[int]bool{80: true, 443: true}
	report, err := Evaluate(manifest, host)
	if err != nil || !report.Passed {
		t.Fatalf("requirements with 80 and 443 bound = %+v, %v", report, err)
	}
}
