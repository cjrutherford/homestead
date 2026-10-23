package catalog

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homestead/internal/storage"
)

// repoCatalog locates the curated repository catalog from the package
// directory: packages ship with the code they were reviewed against.
func repoCatalog(t *testing.T) Catalog {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller unavailable")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "catalog")
	catalog, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return catalog
}

// curatedPackages is the one place the expected shipped catalog is listed
// (sorted, as Names returns it). Adding a package to catalog/ edits this
// list and nothing else here: each craft slice appends its app.
var curatedPackages = []string{"authentik", "cadcraft", "deckcraft", "designcraft", "effectcraft", "figcraft", "filmcraft", "gridcraft", "immich", "jellyfin", "lightcraft", "nextcloud", "paperless-ngx", "pdfcraft", "photocraft", "soundcraft", "vaultwarden", "vectorcraft", "wordcraft"}

// assertCuratedNames fails when the shipped catalog and curatedPackages
// differ, in either direction.
func assertCuratedNames(t *testing.T, catalog Catalog) {
	t.Helper()
	names := catalog.Names()
	if len(names) != len(curatedPackages) {
		t.Fatalf("names = %v, want %v", names, curatedPackages)
	}
	for i := range names {
		if names[i] != curatedPackages[i] {
			t.Fatalf("names = %v, want %v", names, curatedPackages)
		}
	}
}

func TestCuratedJellyfin(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("jellyfin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// S319: Homestead finishes the startup wizard, with three libraries
	// on provisioned bulk folders.
	if f := manifest.FirstRun; f == nil || f.Routine != FirstRunJellyfin || f.Account != "admin" || len(f.Libraries) != 3 ||
		!manifest.HeldSecret("admin_password") {
		t.Fatalf("first run = %+v", manifest.FirstRun)
	}
	if manifest.Version != "12.2" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 || manifest.Services[0].Image != "jellyfin/jellyfin:12.2" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	if manifest.Requirements.MemoryMB < 1024 || len(manifest.Requirements.Ports) != 1 {
		t.Fatalf("requirements = %+v", manifest.Requirements)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || !endpoint.AllowWider {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	// A sufficient host installs cleanly from the curated entry.
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 8192, CPUCount: 4,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 10 << 30,
			storage.ClassBulk:        500 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

func TestCuratedNextcloud(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("nextcloud")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "35.0.1" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 || manifest.Services[0].Image != "nextcloud:35.0.1-apache" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || !endpoint.AllowWider {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	assertCuratedNames(t, catalog)
}

func TestCuratedImmich(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("immich")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "3.3.0" {
		t.Fatalf("version = %q", manifest.Version)
	}
	// S318: Homestead creates Immich's administrator, with a held,
	// revealable password no container reads.
	if f := manifest.FirstRun; f == nil || f.Routine != FirstRunImmich || f.Service != "server" || f.Secret != "admin_password" ||
		!manifest.HeldSecret("admin_password") || len(SecretFilesFor(manifest)) != 2 {
		t.Fatalf("first run = %+v, files %+v", manifest.FirstRun, SecretFilesFor(manifest))
	}
	if len(manifest.Services) != 4 {
		t.Fatalf("services = %+v", manifest.Services)
	}
	internal := 0
	for _, service := range manifest.Services {
		if service.Exposes == "" {
			internal++
		}
	}
	if internal != 3 {
		t.Fatalf("internal services = %d", internal)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || !endpoint.AllowWider {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 8192, CPUCount: 4,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 10 << 30,
			storage.ClassBulk:        500 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
	assertCuratedNames(t, catalog)
}

func TestCuratedAuthentik(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("authentik")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "2026.8.3" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 3 {
		t.Fatalf("services = %+v", manifest.Services)
	}
	internal := 0
	for _, service := range manifest.Services {
		if service.Exposes == "" {
			internal++
		}
	}
	if internal != 2 {
		t.Fatalf("internal services = %d", internal)
	}
	if manifest.Database == nil || manifest.Database.Service != "postgresql" ||
		manifest.Database.Kind != DatabasePostgres || manifest.Database.Database != "authentik" ||
		manifest.Database.Username != "authentik" {
		t.Fatalf("database = %+v", manifest.Database)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || !endpoint.AllowWider {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 8192, CPUCount: 4,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 10 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedVaultwarden pins the first Wave 1 package (S345): a single
// container with owner signup, no administrative surface by default, and
// local exposure.
func TestCuratedVaultwarden(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("vaultwarden")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "1.34.3" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 || manifest.Services[0].Image != "vaultwarden/server:1.34.3" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	if service.Environment["SIGNUPS_ALLOWED"] != "true" {
		t.Fatalf("signups = %q, want owner signup open", service.Environment["SIGNUPS_ALLOWED"])
	}
	if _, enabled := service.Environment["ADMIN_TOKEN"]; enabled {
		t.Fatal("the admin page must stay off by default")
	}
	if !strings.HasPrefix(service.Environment["DOMAIN"], "https://") ||
		!strings.Contains(service.Environment["DOMAIN"], RouteHostToken) {
		t.Fatalf("domain = %q, want the routed HTTPS host", service.Environment["DOMAIN"])
	}
	if len(service.Data) != 1 || service.Data[0].Target != "/data" ||
		service.Data[0].Class != storage.ClassApplication || service.Data[0].Subpath != "data" {
		t.Fatalf("data = %+v", service.Data)
	}
	if len(manifest.BackupPaths) != 1 || manifest.BackupPaths[0] != "data" {
		t.Fatalf("backuppaths = %v", manifest.BackupPaths)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if len(manifest.Integrations) != 1 || manifest.Integrations[0].Kind != IntegrationIdentity ||
		manifest.Integrations[0].Target != "authentik" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	if manifest.Database != nil {
		t.Fatalf("database = %+v, want SQLite in the volume", manifest.Database)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 2048, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{storage.ClassApplication: 2 << 30},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedPaperless pins the three-service document archive (S346):
// Postgres metadata, a Valkey queue, bulk documents, and supervised
// secret export for the image that reads only plaintext variables.
func TestCuratedPaperless(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("paperless-ngx")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "3.3.4" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 3 {
		t.Fatalf("services = %+v", manifest.Services)
	}
	internals := 0
	for _, service := range manifest.Services {
		if service.Exposes == "" {
			internals++
		}
	}
	if internals != 2 {
		t.Fatalf("internal services = %d, want 2", internals)
	}
	web := manifest.Services[0]
	if web.Image != "ghcr.io/paperless-ngx/paperless-ngx:3.3.4" {
		t.Fatalf("web image = %q", web.Image)
	}
	if len(web.Entrypoint) != 1 || web.Entrypoint[0] != "/init" {
		t.Fatalf("entrypoint = %v, want the s6 init the supervisor execs", web.Entrypoint)
	}
	for key, want := range map[string]string{
		"PAPERLESS_DBHOST":       "db",
		"PAPERLESS_REDIS":        "redis://broker:6379",
		"PAPERLESS_OCR_LANGUAGE": "eng",
		"PAPERLESS_ADMIN_USER":   "admin",
	} {
		if got := web.Environment[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if domain := web.Environment["PAPERLESS_URL"]; !strings.HasPrefix(domain, "https://") ||
		!strings.Contains(domain, RouteHostToken) {
		t.Fatalf("PAPERLESS_URL = %q, want the routed HTTPS host", domain)
	}
	paths := map[string]bool{}
	for _, data := range web.Data {
		paths[data.Target] = true
		if data.Class != storage.ClassApplication && data.Class != storage.ClassBulk {
			t.Fatalf("data %s class = %q", data.Target, data.Class)
		}
	}
	for _, volume := range []string{
		"/usr/src/paperless/data", "/usr/src/paperless/media",
		"/usr/src/paperless/consume", "/usr/src/paperless/export",
	} {
		if !paths[volume] {
			t.Fatalf("image VOLUME %s has no declaration", volume)
		}
	}
	var exportVars []string
	for _, decl := range manifest.Secrets {
		for _, consumer := range decl.Consumers {
			if consumer.Export {
				exportVars = append(exportVars, consumer.Env)
			}
		}
	}
	for _, want := range []string{
		"PAPERLESS_DBPASS_FILE", "PAPERLESS_SECRET_KEY_FILE", "PAPERLESS_ADMIN_PASSWORD_FILE",
	} {
		found := false
		for _, env := range exportVars {
			if env == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s is not exported through the supervisor", want)
		}
	}
	if manifest.Database == nil || manifest.Database.Kind != "postgres" ||
		manifest.Database.Database != "paperless" || manifest.Database.PasswordSecret != "db_password" {
		t.Fatalf("database = %+v", manifest.Database)
	}
	if len(manifest.BackupPaths) != 2 {
		t.Fatalf("backuppaths = %v, want the index and the library", manifest.BackupPaths)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if len(manifest.Integrations) != 2 ||
		manifest.Integrations[0].Kind != IntegrationIdentity || manifest.Integrations[0].Target != "authentik" ||
		manifest.Integrations[1].Kind != IntegrationLibrary || manifest.Integrations[1].Target != "nextcloud" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	if scripts := EntrypointScriptsFor(manifest); len(scripts) != 1 || scripts[0].Service != "webserver" {
		t.Fatalf("scripts = %+v, want one webserver supervisor", scripts)
	}
	// The supervisor replaces the image entrypoint and receives the image
	// initialization as its command: exporting must not change what runs.
	plan, err := PlanInstall(InstallRequest{
		Manifest: manifest, Host: installHost(), Domain: "lan",
		ContainerPorts: map[string]int{"webserver": 8000},
		Storage:        curatedStorage(),
	})
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	var composed struct {
		Services map[string]struct {
			Entrypoint []string `yaml:"entrypoint"`
			Command    []string `yaml:"command"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(plan.Compose.ComposeYAML), &composed); err != nil {
		t.Fatalf("compose: %v", err)
	}
	composedWeb := composed.Services["webserver"]
	if len(composedWeb.Entrypoint) != 1 || composedWeb.Entrypoint[0] != EntrypointName {
		t.Fatalf("entrypoint = %v, want the supervisor", composedWeb.Entrypoint)
	}
	if len(composedWeb.Command) != 1 || composedWeb.Command[0] != "/init" {
		t.Fatalf("command = %v, want the image init", composedWeb.Command)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 4096, CPUCount: 4,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 4 << 30,
			storage.ClassBulk:        20 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}
