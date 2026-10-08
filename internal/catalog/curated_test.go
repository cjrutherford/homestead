package catalog

import (
	"path/filepath"
	"runtime"
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

func TestCuratedJellyfin(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("jellyfin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
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
	if names := catalog.Names(); len(names) != 3 || names[0] != "immich" || names[1] != "jellyfin" || names[2] != "nextcloud" {
		t.Fatalf("names = %v", names)
	}
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
	if names := catalog.Names(); len(names) != 3 {
		t.Fatalf("names = %v", names)
	}
}
