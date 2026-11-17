package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/craftfiles"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// linkRig is an appliance with the real catalog, craft files and app
// setup. Addresses land in the edge table directly; linking Nextcloud is
// stubbed by link, which records the link the way the manager does.
type linkRig struct {
	srv   *Server
	st    *store.Store
	a     *appSetup
	mu    sync.Mutex
	links int
	// link answers each link attempt; nil records the link.
	link func(n int) error
}

func newLinkRig(t *testing.T) *linkRig {
	t.Helper()
	srv, st, secrets := tunnelTestServer(t)
	seedTunnelEdge(t, srv, st, false, false)
	dir := t.TempDir()
	old := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(dir, name) }
	t.Cleanup(func() { stackDirFor = old })
	catalogDir := filepath.Join("..", "..", "catalog")
	srv.MountCraftFiles(CraftFilesConfig{Store: st, Secrets: secrets, YAMLPath: filepath.Join(dir, "craft-files.yaml"), CatalogDir: catalogDir})
	srv.MountAppSetup(st, catalogDir, nil)
	r := &linkRig{srv: srv, st: st, a: srv.appSetup}
	prevTrust := trustAppHosts
	trustAppHosts = func(context.Context, catalog.Manifest, string, []string, []string) error { return nil }
	t.Cleanup(func() { trustAppHosts = prevTrust })
	r.a.linkRetry = time.Millisecond
	r.a.linkFn = func(_ context.Context, root, _ string) error {
		r.mu.Lock()
		r.links++
		n := r.links
		r.mu.Unlock()
		if r.link != nil {
			if err := r.link(n); err != nil {
				return err
			}
		}
		base, account, ok := applianceNextcloud(st, catalogDir, true)
		if !ok {
			return fmt.Errorf("%w: Nextcloud is not installed", craftfiles.ErrInput)
		}
		if err := secrets.SetSystem(craftfiles.SecretName, "generated-app-password"); err != nil {
			return err
		}
		_, err := st.DB().Exec(`INSERT INTO craft_file_link (id, base_url, account, root, daemon_created, created_at, updated_at)
			VALUES (1, ?, ?, ?, 1, 'now', 'now')`, base, account, root)
		return err
	}
	// The address step records the routes; the others run for real.
	r.a.startFn = func(ctx context.Context, rec setupRecord, name, approved, token string) (int64, error) {
		if name != setupAddress {
			return r.a.startStep(ctx, rec, name, approved, token)
		}
		set, err := loadEdgeRoutes(st)
		if err != nil {
			return 0, err
		}
		set.Routes = append(set.Routes, rec.Routes...)
		return 0, saveEdgeRoutes(st, set)
	}
	return r
}

func (r *linkRig) manifest(t *testing.T, name string) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load(r.a.catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func pathRoute(app, service string, port int, craft bool) docker.Route {
	r := docker.Route{Host: docker.Hostname(app + ".lan"), Target: docker.Target{Host: service, Port: port},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Path: docker.AppPathFor(app), StripPrefix: true}
	if craft {
		r.CraftApp = app
	}
	return r
}

// install runs app's install with setup approval and waits for the setup.
func (r *linkRig) install(t *testing.T, app string, route docker.Route, approval setupApproval) setupRecord {
	t.Helper()
	m := r.manifest(t, app)
	rec, err := r.a.prepare(m, catalog.InstallPlan{Routes: []docker.Route{route}}, approval, "owner")
	if err != nil {
		t.Fatal(err)
	}
	def := operation.Definition{Type: "app.install", Steps: []operation.StepDef{{Name: "install", Run: func(context.Context) error {
		_, err := r.st.DB().Exec(`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES (?, 'compose.yaml', 'x', 'now')`, app)
		return err
	}}}}
	if err := r.a.attach(rec, &def); err != nil {
		t.Fatal(err)
	}
	op, err := r.a.runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatal(err)
	}
	r.a.installed(app, op.ID)
	if done, err := r.a.runner.Wait(context.Background(), op.ID); err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("install = %+v, %v", done, err)
	}
	got, ok, err := r.a.load(app)
	if err != nil || !ok || got.Operation == 0 {
		t.Fatalf("setup record = %+v, %v, %v", got, ok, err)
	}
	if _, err := r.a.runner.Wait(context.Background(), got.Operation); err != nil {
		t.Fatal(err)
	}
	got, _, err = r.a.current(app)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (r *linkRig) audits(t *testing.T) string {
	t.Helper()
	rows, err := r.st.DB().Query(`SELECT outcome, detail FROM audit_entries WHERE action = 'craftfiles.link' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var outcome, detail string
		_ = rows.Scan(&outcome, &detail)
		out = append(out, outcome+" "+detail)
	}
	return strings.Join(out, "\n")
}

func (r *linkRig) installNextcloud(t *testing.T) {
	t.Helper()
	installApp(t, r.st, "nextcloud", pathRoute("nextcloud", "nextcloud", 80, false))
}

// A craft app installed with Nextcloud on the appliance is linked as
// part of its setup, after its address; the review lists the link and
// the install binds it by digest.
func TestCraftInstallLinksNextcloudAfterItsAddress(t *testing.T) {
	r := newLinkRig(t)
	if p := r.a.filesLinkPreview(r.manifest(t, "cadcraft")); p != nil {
		t.Fatalf("no Nextcloud, but a link is planned: %+v", p)
	}
	if p := r.a.filesLinkPreview(r.manifest(t, "lightcraft")); p != nil {
		t.Fatalf("lightcraft has no file hook, but a link is planned: %+v", p)
	}
	r.installNextcloud(t)
	plan := r.a.filesLinkPreview(r.manifest(t, "cadcraft"))
	if plan == nil || strings.Join(plan.Lines, "|") != "Link CADCraft to Nextcloud for files" || !plan.NewLink ||
		plan.BaseURL != "https://homestead.lan/apps/nextcloud" || plan.Account != "admin" || plan.Root != craftfiles.DefaultRoot {
		t.Fatalf("preview = %+v", plan)
	}
	if _, err := r.a.prepare(r.manifest(t, "cadcraft"), catalog.InstallPlan{}, setupApproval{FilesLink: &setupDigest{Digest: "stale"}}, "owner"); !errors.Is(err, errPlanChanged) {
		t.Fatalf("stale link digest: %v", err)
	}
	rec := r.install(t, "cadcraft", pathRoute("cadcraft", "cadcraft", 8080, true), setupApproval{FilesLink: &setupDigest{Digest: plan.Digest}})
	if states(rec) != "address:done nextcloud-link:done" || rec.Status != setupFinished || strings.Join(rec.FilesApps, ",") != "cadcraft" {
		t.Fatalf("setup = %s %s %v", states(rec), rec.Status, rec.FilesApps)
	}
	st, _ := r.srv.files.Manager.Status()
	for _, a := range st.Apps {
		if a.Name == "cadcraft" && !a.Ready {
			t.Fatalf("cadcraft not ready: %+v", a)
		}
	}
	if got := r.audits(t); got != "ok app=cadcraft account=admin root=Homestead" {
		t.Fatalf("audits = %q", got)
	}

	// Once linked, the next craft app reuses the link.
	next := r.a.filesLinkPreview(r.manifest(t, "pdfcraft"))
	if next == nil || next.NewLink {
		t.Fatalf("second preview = %+v", next)
	}
	rec = r.install(t, "pdfcraft", pathRoute("pdfcraft", "pdfcraft", 8080, true), setupApproval{FilesLink: &setupDigest{Digest: next.Digest}})
	if states(rec) != "address:done nextcloud-link:done" || r.links != 1 {
		t.Fatalf("setup = %s, links = %d", states(rec), r.links)
	}
}

// Nextcloud installed after craft apps links all of them as the last
// step of its own setup; a craft app set to keep documents in the
// browser, and one without file hooks, are left out.
func TestNextcloudSetupLinksTheInstalledCraftApps(t *testing.T) {
	r := newLinkRig(t)
	for _, app := range []string{"cadcraft", "pdfcraft", "gridcraft", "lightcraft"} {
		installApp(t, r.st, app, pathRoute(app, app, 8080, true))
	}
	if err := r.srv.files.Manager.SetLocalOnly("gridcraft", true, "owner"); err != nil {
		t.Fatal(err)
	}
	plan := r.a.filesLinkPreview(r.manifest(t, "nextcloud"))
	if plan == nil || strings.Join(plan.Apps, ",") != "cadcraft,pdfcraft" ||
		strings.Join(plan.Lines, "|") != "Link CADCraft to Nextcloud for files|Link PdfCraft to Nextcloud for files" {
		t.Fatalf("preview = %+v", plan)
	}
	rec := r.install(t, "nextcloud", pathRoute("nextcloud", "nextcloud", 80, false), setupApproval{FilesLink: &setupDigest{Digest: plan.Digest}})
	if states(rec) != "address:done nextcloud-link:done" || rec.Status != setupFinished {
		t.Fatalf("setup = %s %s (%s)", states(rec), rec.Status, rec.step(setupFilesLink).Reason)
	}
	if got := r.audits(t); got != "ok app=cadcraft account=admin root=Homestead\nok app=pdfcraft account=admin root=Homestead" {
		t.Fatalf("audits = %q", got)
	}
	if p := r.a.filesLinkPreview(r.manifest(t, "nextcloud")); p != nil {
		t.Fatalf("linked, but Nextcloud plans another link: %+v", p)
	}
}

// Nextcloud still starting is waited for; a failed link stops the setup
// at the link step and is audited.
func TestCraftLinkRetriesThenStopsTheSetupOnFailure(t *testing.T) {
	r := newLinkRig(t)
	r.installNextcloud(t)
	r.link = func(n int) error {
		if n < 3 {
			return fmt.Errorf("%w: could not reach Nextcloud", craftfiles.ErrUpstream)
		}
		return nil
	}
	plan := r.a.filesLinkPreview(r.manifest(t, "cadcraft"))
	rec := r.install(t, "cadcraft", pathRoute("cadcraft", "cadcraft", 8080, true), setupApproval{FilesLink: &setupDigest{Digest: plan.Digest}})
	if states(rec) != "address:done nextcloud-link:done" || r.links != 3 {
		t.Fatalf("setup = %s, links = %d", states(rec), r.links)
	}

	r2 := newLinkRig(t)
	r2.installNextcloud(t)
	r2.link = func(int) error { return fmt.Errorf("%w: Nextcloud rejected the credential", craftfiles.ErrUpstream) }
	r2.a.linkWait = 0
	plan = r2.a.filesLinkPreview(r2.manifest(t, "cadcraft"))
	rec = r2.install(t, "cadcraft", pathRoute("cadcraft", "cadcraft", 8080, true), setupApproval{FilesLink: &setupDigest{Digest: plan.Digest}})
	if states(rec) != "address:done nextcloud-link:failed" || rec.Status != setupStopped ||
		!strings.Contains(rec.step(setupFilesLink).Reason, "rejected the credential") {
		t.Fatalf("setup = %s %s (%s)", states(rec), rec.Status, rec.step(setupFilesLink).Reason)
	}
	if got := r2.audits(t); !strings.HasPrefix(got, "error apps=cadcraft account=admin root=Homestead") {
		t.Fatalf("audits = %q", got)
	}
	if review := r2.a.review(context.Background(), rec, setupFilesLink); review.FilesLink == nil || review.Digest != plan.Digest {
		t.Fatalf("review = %+v", review)
	}
}

// The lightcraft regression (S326): a craft route that arrives on a name
// of its own, as the dashboard's "Give it an address" sent it, is served
// at its path; the address step of an app already on its own name moves
// it.
func TestCraftRoutesAreAlwaysByPath(t *testing.T) {
	routes := []docker.Route{
		{Host: "homestead.lan", Dashboard: true, Target: docker.Target{Host: "127.0.0.1", Port: 8080}},
		{Host: "lightcraft.lan", Target: docker.Target{Host: "lightcraft", Port: 8080}, Exposure: docker.ExposureLocal,
			TLS: docker.TLSInternal, CraftApp: "lightcraft", Tunnel: []docker.Hostname{"lightcraft.example.com"}, Published: true},
		{Host: "immich.lan", Target: docker.Target{Host: "immich-server", Port: 2283}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal},
	}
	pathCraftRoutes(routes)
	if r := routes[1]; r.Path != "/apps/lightcraft/" || !r.StripPrefix || len(r.Tunnel) != 0 || r.Published {
		t.Fatalf("lightcraft = %+v", r)
	}
	if routes[0].IsPath() || routes[2].IsPath() {
		t.Fatalf("other routes moved: %+v", routes)
	}

	r := newLinkRig(t)
	installApp(t, r.st, "lightcraft", docker.Route{Host: "lightcraft.lan", Target: docker.Target{Host: "lightcraft", Port: 8080},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, CraftApp: "lightcraft"})
	review, err := r.a.reviewAddress(setupRecord{App: "lightcraft", Routes: []docker.Route{pathRoute("lightcraft", "lightcraft", 8080, true)}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range review.add {
		if x.CraftApp == "lightcraft" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the address step leaves lightcraft on its own name: %+v", review.add)
	}
}
