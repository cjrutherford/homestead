package catalog

import (
	"strings"
	"testing"

	"homestead/internal/storage"
)

func bundleHost() HostFacts {
	return HostFacts{
		Arch: "amd64", MemoryMB: 16384, CPUCount: 8,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 100 << 30,
			storage.ClassDatabase:    50 << 30,
			storage.ClassBulk:        500 << 30,
		},
		UsedPorts: map[int]bool{},
	}
}

func TestBundlesAreValidAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, bundle := range Bundles {
		if err := bundle.Validate(); err != nil {
			t.Fatalf("bundle %q: %v", bundle.Name, err)
		}
		if seen[bundle.Name] {
			t.Fatalf("duplicate bundle %q", bundle.Name)
		}
		seen[bundle.Name] = true
	}
	for _, name := range []string{"starter", "family", "media", "full"} {
		if !seen[name] {
			t.Fatalf("missing curated bundle %q", name)
		}
	}
}

func TestBundlesIncludeTheStarterSet(t *testing.T) {
	starter := Bundles[0]
	if starter.Name != "starter" {
		t.Fatalf("first bundle = %q", starter.Name)
	}
	for _, bundle := range Bundles[1:] {
		for _, member := range starter.Packages {
			if !containsMember(bundle, member) {
				t.Fatalf("bundle %q omits starter member %q", bundle.Name, member)
			}
		}
	}
}

func containsMember(bundle Bundle, name string) bool {
	for _, member := range bundle.Packages {
		if member == name {
			return true
		}
	}
	return false
}

func TestCheckBundleGradesAvailableMembers(t *testing.T) {
	cat := repoCatalog(t)
	var starter Bundle
	for _, bundle := range Bundles {
		if bundle.Name == "starter" {
			starter = bundle
		}
	}
	report, err := CheckBundle(cat, starter, bundleHost())
	if err != nil {
		t.Fatalf("CheckBundle: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
	if report.Complete {
		t.Fatal("starter reports complete before its Wave 1 packages ship")
	}
	if len(report.Missing) == 0 {
		t.Fatal("missing members must be named")
	}
	if report.TotalMemory == 0 {
		t.Fatal("memory total must include available members")
	}
	found := false
	for _, member := range report.Members {
		if member.Name == "nextcloud" && member.Available && member.MemoryMB > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("members = %+v", report.Members)
	}
	if !strings.Contains(report.Detail, "bundle fits") {
		t.Fatalf("detail = %q", report.Detail)
	}
}

func TestCheckBundleBlocksOnCombinedMemory(t *testing.T) {
	host := bundleHost()
	host.MemoryMB = 512
	report, err := CheckBundle(repoCatalog(t), Bundles[0], host)
	if err != nil {
		t.Fatalf("CheckBundle: %v", err)
	}
	if report.Passed || !strings.Contains(report.Detail, "combined memory") {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckBundleBlocksOnCombinedStorage(t *testing.T) {
	host := bundleHost()
	host.FreeSpace[storage.ClassApplication] = 1
	report, err := CheckBundle(repoCatalog(t), Bundles[0], host)
	if err != nil {
		t.Fatalf("CheckBundle: %v", err)
	}
	if report.Passed {
		t.Fatalf("report = %+v", report)
	}
	var application *BundleStorage
	for i := range report.Storage {
		if report.Storage[i].Class == storage.ClassApplication {
			application = &report.Storage[i]
		}
	}
	if application == nil || application.OK || application.Needed == 0 {
		t.Fatalf("storage = %+v", report.Storage)
	}
}

func TestCheckBundleFailsOnMissingStorageData(t *testing.T) {
	host := bundleHost()
	host.FreeSpace = map[storage.Class]uint64{}
	report, err := CheckBundle(repoCatalog(t), Bundles[0], host)
	if err != nil {
		t.Fatalf("CheckBundle: %v", err)
	}
	if report.Passed || !strings.Contains(report.Detail, "no space data") {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckBundleFailsOnMissingMemoryData(t *testing.T) {
	host := bundleHost()
	host.MemoryMB = 0
	report, err := CheckBundle(repoCatalog(t), Bundles[0], host)
	if err != nil {
		t.Fatalf("CheckBundle: %v", err)
	}
	if report.Passed || !strings.Contains(report.Detail, "no memory data") {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckBundleRejectsInvalidBundles(t *testing.T) {
	cases := []Bundle{
		{Title: "No name", Packages: []string{"jellyfin"}},
		{Name: "bad/name", Title: "Bad", Packages: []string{"jellyfin"}},
		{Name: "empty", Title: "Empty"},
		{Name: "dup", Title: "Dup", Packages: []string{"jellyfin", "jellyfin"}},
		{Name: "blank", Title: "Blank", Packages: []string{""}},
	}
	for _, bundle := range cases {
		if _, err := CheckBundle(repoCatalog(t), bundle, bundleHost()); err == nil {
			t.Fatalf("bundle %+v accepted", bundle)
		}
	}
}

func TestBundleWithNoAvailableMembersIsBlocked(t *testing.T) {
	bundle := Bundle{Name: "future", Title: "Future", Packages: []string{"not-shipped-yet"}}
	report, err := CheckBundle(repoCatalog(t), bundle, bundleHost())
	if err != nil {
		t.Fatalf("CheckBundle: %v", err)
	}
	if report.Passed || len(report.Missing) != 1 {
		t.Fatalf("report = %+v", report)
	}
}
