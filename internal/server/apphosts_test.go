package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/store"
)

type trustCall struct {
	app, address string
	add          []string
}

// hostsRig has the real catalog, Nextcloud installed on a name of its own
// (the VM 2 shape) and trustAppHosts recording its calls.
func hostsRig(t *testing.T) (*store.Store, string, *[]trustCall) {
	t.Helper()
	_, st, _ := tunnelTestServer(t)
	dir := t.TempDir()
	old := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(dir, name) }
	t.Cleanup(func() { stackDirFor = old })
	calls := &[]trustCall{}
	prev := trustAppHosts
	trustAppHosts = func(_ context.Context, m catalog.Manifest, address string, add, _ []string) error {
		*calls = append(*calls, trustCall{m.Name, address, add})
		return nil
	}
	t.Cleanup(func() { trustAppHosts = prev })
	return st, filepath.Join("..", "..", "catalog"), calls
}

// S396: Nextcloud on nextcloud.lan trusts that name, whatever its
// install-time list held.
func TestReconcileAppHostsAddsTheMissingLocalRouteName(t *testing.T) {
	st, catalogDir, calls := hostsRig(t)
	installApp(t, st, "nextcloud", docker.Route{Host: "nextcloud.lan", Target: docker.Target{Host: "nextcloud", Port: 80},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Tunnel: []docker.Hostname{"cloud.example.com"}})
	if err := reconcileAppHosts(context.Background(), st, catalogDir, nil); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].app != "nextcloud" || (*calls)[0].address != "https://nextcloud.lan" ||
		!strings.Contains(strings.Join((*calls)[0].add, " "), "nextcloud.lan") ||
		!strings.Contains(strings.Join((*calls)[0].add, " "), "cloud.example.com") {
		t.Fatalf("calls = %+v", *calls)
	}
}

// An edge change that leaves an app's names alone does not touch the
// app; one that changes them does, and a failure is returned.
func TestReconcileAppHostsOnlyForChangedNamesAndSurfacesErrors(t *testing.T) {
	st, catalogDir, calls := hostsRig(t)
	installApp(t, st, "nextcloud", docker.Route{Host: "nextcloud.lan", Target: docker.Target{Host: "nextcloud", Port: 80},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
	before := appHostsSnapshot(st, catalogDir)
	if err := reconcileAppHosts(context.Background(), st, catalogDir, before); err != nil || len(*calls) != 0 {
		t.Fatalf("unchanged names: calls = %+v, err = %v", *calls, err)
	}
	set, _ := loadEdgeRoutes(st)
	set.Routes[len(set.Routes)-1].Aliases = []docker.Hostname{"files.lan"}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	if err := reconcileAppHosts(context.Background(), st, catalogDir, before); err != nil || len(*calls) != 1 ||
		!strings.Contains(strings.Join((*calls)[0].add, " "), "files.lan") {
		t.Fatalf("changed names: calls = %+v, err = %v", *calls, err)
	}
	trustAppHosts = func(context.Context, catalog.Manifest, string, []string, []string) error {
		return errors.New("occ: container is not running")
	}
	err := reconcileAppHosts(context.Background(), st, catalogDir, nil)
	if err == nil || !strings.Contains(err.Error(), "Nextcloud did not take its names") || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("err = %v", err)
	}
}

// The craft-link step brings Nextcloud's trusted domains up to date
// before linking, so a setup stopped on an untrusted name resumes; when
// the reconcile fails the step stops with the reason and the audit.
func TestCraftLinkReconcilesTrustedDomainsFirst(t *testing.T) {
	r := newLinkRig(t)
	installApp(t, r.st, "nextcloud", docker.Route{Host: "nextcloud.lan", Target: docker.Target{Host: "nextcloud", Port: 80},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
	trusted := false
	trustAppHosts = func(_ context.Context, _ catalog.Manifest, _ string, add, _ []string) error {
		trusted = strings.Contains(strings.Join(add, " "), "nextcloud.lan")
		return nil
	}
	r.link = func(int) error {
		if !trusted {
			return errors.New("400 untrusted domain")
		}
		return nil
	}
	plan := r.a.filesLinkPreview(r.manifest(t, "cadcraft"))
	rec := r.install(t, "cadcraft", pathRoute("cadcraft", "cadcraft", 8080, true), setupApproval{FilesLink: &setupDigest{Digest: plan.Digest}})
	if states(rec) != "address:done nextcloud-link:done" || !trusted {
		t.Fatalf("setup = %s, trusted = %v", states(rec), trusted)
	}

	r2 := newLinkRig(t)
	installApp(t, r2.st, "nextcloud", docker.Route{Host: "nextcloud.lan", Target: docker.Target{Host: "nextcloud", Port: 80},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
	trustAppHosts = func(context.Context, catalog.Manifest, string, []string, []string) error {
		return errors.New("occ failed")
	}
	plan = r2.a.filesLinkPreview(r2.manifest(t, "cadcraft"))
	rec = r2.install(t, "cadcraft", pathRoute("cadcraft", "cadcraft", 8080, true), setupApproval{FilesLink: &setupDigest{Digest: plan.Digest}})
	if states(rec) != "address:done nextcloud-link:failed" || r2.links != 0 ||
		!strings.Contains(rec.step(setupFilesLink).Reason, "does not trust the names") {
		t.Fatalf("setup = %s links = %d (%s)", states(rec), r2.links, rec.step(setupFilesLink).Reason)
	}
}
