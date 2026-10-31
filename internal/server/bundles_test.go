package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/storage"
	"homestead/internal/store"
)

var errTestFacts = errors.New("facts unavailable")

// TestCatalogBundlesGradesAgainstHostFacts proves the bundle route grades
// every curated profile against the injected facts rather than the test
// machine's own capacity.
func TestCatalogBundlesGradesAgainstHostFacts(t *testing.T) {
	srv, _ := catalogTestServer(t)
	old := hostFactsFor
	hostFactsFor = func(*store.Store) (catalog.HostFacts, error) {
		return catalog.HostFacts{
			Arch: "amd64", MemoryMB: 16384, CPUCount: 8,
			FreeSpace: map[storage.Class]uint64{
				storage.ClassApplication: 1 << 40,
				storage.ClassBulk:        1 << 40,
			},
			UsedPorts: map[int]bool{},
		}, nil
	}
	t.Cleanup(func() { hostFactsFor = old })

	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/bundles", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var reports []catalog.BundleReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &reports); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(reports) != len(catalog.Bundles) {
		t.Fatalf("reports = %d, want %d", len(reports), len(catalog.Bundles))
	}
	if reports[0].Bundle != "starter" || reports[0].Title != "Starter" {
		t.Fatalf("first report = %+v", reports[0])
	}
	// The fixture catalog carries one unrelated package, so every curated
	// member is honestly reported missing instead of silently dropped.
	if reports[0].Complete || len(reports[0].Missing) != len(catalog.Bundles[0].Packages) {
		t.Fatalf("starter = %+v", reports[0])
	}
	if reports[0].Passed {
		t.Fatalf("a bundle with no available members passed: %+v", reports[0])
	}
	for _, member := range reports[0].Members {
		if member.Available || member.Name == "" {
			t.Fatalf("member = %+v", member)
		}
	}
}

func TestCatalogBundlesReportsHostDiscoveryFailure(t *testing.T) {
	srv, _ := catalogTestServer(t)
	old := hostFactsFor
	hostFactsFor = func(*store.Store) (catalog.HostFacts, error) {
		return catalog.HostFacts{}, errTestFacts
	}
	t.Cleanup(func() { hostFactsFor = old })

	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/bundles", nil)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502", recorder.Code)
	}
}

// TestCatalogBundlesJSONListsAreArrays guards the dashboard contract: it
// reads .length on Missing and iterates Members/Storage, so a nil slice
// serialised as null broke the whole bundle render.
func TestCatalogBundlesJSONListsAreArrays(t *testing.T) {
	srv, _ := catalogTestServer(t)
	old := hostFactsFor
	hostFactsFor = func(*store.Store) (catalog.HostFacts, error) {
		return catalog.HostFacts{Arch: "amd64", MemoryMB: 16384, FreeSpace: map[storage.Class]uint64{}}, nil
	}
	t.Cleanup(func() { hostFactsFor = old })

	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/bundles", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil || len(raw) == 0 {
		t.Fatalf("decode: %v (%d bundles)", err, len(raw))
	}
	for _, bundle := range raw {
		for _, key := range []string{"Members", "Missing", "Storage"} {
			if string(bundle[key]) == "null" || len(bundle[key]) == 0 || bundle[key][0] != '[' {
				t.Errorf("%s %s = %s, want an array", bundle["Bundle"], key, bundle[key])
			}
		}
	}
}
