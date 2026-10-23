package catalog

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
var curatedPackages = []string{"authentik", "cadcraft", "deckcraft", "designcraft", "effectcraft", "figcraft", "filmcraft", "gridcraft", "immich", "jellyfin", "lightcraft", "nextcloud", "pdfcraft", "photocraft", "soundcraft", "vaultwarden", "vectorcraft", "wordcraft"}

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
