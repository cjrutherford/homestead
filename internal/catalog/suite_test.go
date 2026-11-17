package catalog

import (
	"strings"
	"testing"

	"homestead/internal/storage"
)

func suiteHost() HostFacts {
	return HostFacts{
		Arch: "amd64", MemoryMB: 16384, CPUCount: 8,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 50 << 30,
			storage.ClassBulk:        2000 << 30,
		},
	}
}

func TestLoadSuite(t *testing.T) {
	cat := repoCatalogForSuite(t)
	manifests, err := LoadSuite(cat)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	if len(manifests) != 3 || manifests[0].Name != "jellyfin" ||
		manifests[2].Name != "immich" {
		t.Fatalf("suite = %v", namesOf(manifests))
	}
	empty := Catalog{entries: map[string]Manifest{}}
	if _, err := LoadSuite(empty); err == nil ||
		!strings.Contains(err.Error(), "suite missing") {
		t.Fatalf("err = %v", err)
	}
}

func repoCatalogForSuite(t *testing.T) Catalog {
	t.Helper()
	cat, err := Load("../../catalog")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cat
}

func namesOf(manifests []Manifest) []string {
	var out []string
	for _, manifest := range manifests {
		out = append(out, manifest.Name)
	}
	return out
}

func TestCheckSuite(t *testing.T) {
	cat := repoCatalogForSuite(t)
	manifests, err := LoadSuite(cat)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	report, err := CheckSuite(manifests, suiteHost())
	if err != nil {
		t.Fatalf("CheckSuite: %v", err)
	}
	if !report.Passed || len(report.Packages) != 3 {
		t.Fatalf("report = %+v", report)
	}
	if report.TotalMemory == 0 || report.HostMemory != 16384 {
		t.Fatalf("memory = %+v", report)
	}
	// Starved hosts fail with the combined footprint named.
	starved := suiteHost()
	starved.MemoryMB = 1024
	report, err = CheckSuite(manifests, starved)
	if err != nil {
		t.Fatalf("CheckSuite: %v", err)
	}
	if report.Passed || !strings.Contains(report.Detail, "combined memory") {
		t.Fatalf("report = %+v", report)
	}
	// Port collisions across members fail with both owners named.
	clash := append([]Manifest(nil), manifests...)
	clash[0].Requirements.Ports = []int{8096}
	dupe := clash[0]
	dupe.Name = "jellyfin-twin"
	dupe.Exposure.Package = "jellyfin-twin"
	dupe.Requirements.Ports = []int{8096}
	clash = append(clash, dupe)
	if _, err := CheckSuite(clash, suiteHost()); err == nil ||
		!strings.Contains(err.Error(), "claimed by both") {
		t.Fatalf("err = %v", err)
	}
	if _, err := CheckSuite([]Manifest{{}}, suiteHost()); err == nil {
		t.Fatal("checked invalid manifest")
	}
}
