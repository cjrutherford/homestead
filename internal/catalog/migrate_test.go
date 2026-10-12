package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/storage"
	"homestead/internal/store"
)

func volumeMount(name, source, dest string) docker.Mount {
	return docker.Mount{Type: "volume", Name: name, Source: source, Destination: dest, RW: true}
}

func projectBox(pkg, service, state, image string, mounts ...docker.Mount) docker.Container {
	return docker.Container{
		ID: pkg + "-" + service, Names: []string{pkg + "-" + service}, State: state, Image: image, Status: state,
		Labels: map[string]string{docker.LabelProject: pkg, docker.LabelService: service},
		Mounts: mounts,
	}
}

func repoManifest(t *testing.T, name string) Manifest {
	t.Helper()
	manifest, err := repoCatalog(t).Get(name)
	if err != nil {
		t.Fatalf("Get %s: %v", name, err)
	}
	return manifest
}

func both(base string) map[storage.Class]string {
	return map[storage.Class]string{storage.ClassApplication: base, storage.ClassBulk: base}
}

func TestDetectDataStates(t *testing.T) {
	manifest := repoManifest(t, "nextcloud")
	mounts := both("/mnt/x")
	t.Run("needs migration for nested volume", func(t *testing.T) {
		status := DetectData(manifest, []docker.Container{
			projectBox("nextcloud", "nextcloud", "running", "nextcloud", volumeMount("v1", "/var/lib/docker/volumes/v1/_data", "/var/www/html")),
		}, mounts)
		if status.State != DataNeedsMigration {
			t.Fatalf("state = %s (%s)", status.State, status.Reason)
		}
		kinds := map[string]string{}
		for _, finding := range status.Findings {
			kinds[finding.Target] = finding.Kind
		}
		if kinds["/var/www/html"] != KindVolume || kinds["/var/www/html/data"] != KindNestedVolume {
			t.Fatalf("kinds = %v", kinds)
		}
	})
	t.Run("placed", func(t *testing.T) {
		status := DetectData(manifest, []docker.Container{
			projectBox("nextcloud", "nextcloud", "running", "nextcloud",
				docker.Mount{Type: "bind", Source: "/mnt/x/nextcloud/html", Destination: "/var/www/html"},
				docker.Mount{Type: "bind", Source: "/mnt/x/nextcloud/data", Destination: "/var/www/html/data"}),
		}, mounts)
		if status.State != DataPlaced {
			t.Fatalf("state = %s (%s)", status.State, status.Reason)
		}
	})
	t.Run("unknown without containers", func(t *testing.T) {
		if status := DetectData(manifest, nil, mounts); status.State != DataUnknown {
			t.Fatalf("state = %s", status.State)
		}
	})
	t.Run("unknown for relocated bind", func(t *testing.T) {
		status := DetectData(manifest, []docker.Container{
			projectBox("nextcloud", "nextcloud", "running", "nextcloud",
				docker.Mount{Type: "bind", Source: "/elsewhere", Destination: "/var/www/html"},
				docker.Mount{Type: "bind", Source: "/mnt/x/nextcloud/data", Destination: "/var/www/html/data"}),
		}, mounts)
		if status.State != DataUnknown {
			t.Fatalf("state = %s", status.State)
		}
	})
	t.Run("unknown for undeclared volume", func(t *testing.T) {
		status := DetectData(manifest, []docker.Container{
			projectBox("nextcloud", "nextcloud", "running", "nextcloud",
				volumeMount("v1", "/v/1", "/var/www/html"), volumeMount("v2", "/v/2", "/opt/odd")),
		}, mounts)
		if status.State != DataUnknown || !strings.Contains(status.Reason, "does not declare") {
			t.Fatalf("state = %s reason = %q", status.State, status.Reason)
		}
	})
	t.Run("unknown for unassigned class", func(t *testing.T) {
		status := DetectData(manifest, []docker.Container{
			projectBox("nextcloud", "nextcloud", "running", "nextcloud", volumeMount("v1", "/v/1", "/var/www/html")),
		}, map[storage.Class]string{})
		if status.State != DataUnknown {
			t.Fatalf("state = %s", status.State)
		}
	})
}

// writeTree creates files under root from relative name -> content.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlanMigration(t *testing.T) {
	manifest := repoManifest(t, "nextcloud")
	base := t.TempDir()
	vol := filepath.Join(t.TempDir(), "_data")
	writeTree(t, vol, map[string]string{"config/config.php": "x", "data/alice/file.txt": "0123456789", "index.php": "ab"})
	containers := []docker.Container{
		projectBox("nextcloud", "nextcloud", "exited", "nextcloud", volumeMount("v1", vol, "/var/www/html")),
	}
	req := MigrationRequest{
		Manifest: manifest, Containers: containers, Mountpoints: both(base),
		FreeBytes: map[string]uint64{base: 1 << 30},
	}
	plan, err := PlanMigration(req)
	if err != nil {
		t.Fatalf("PlanMigration: %v", err)
	}
	if len(plan.Items) != 2 || plan.Digest == "" || len(plan.OldVolumes) != 1 || plan.EstimatedDowntimeSeconds == 0 {
		t.Fatalf("plan = %+v", plan)
	}
	var html, data MigrationItem
	for _, item := range plan.Items {
		if item.Target == "/var/www/html" {
			html = item
		} else {
			data = item
		}
	}
	if len(html.Excludes) != 1 || html.Excludes[0] != "data" || html.SizeBytes != 3 || data.SizeBytes != 10 {
		t.Fatalf("html = %+v data = %+v", html, data)
	}
	if data.Source != filepath.Join(vol, "data") || data.Dest != base+"/nextcloud/data" {
		t.Fatalf("data = %+v", data)
	}
	again, _ := PlanMigration(req)
	if again.Digest != plan.Digest {
		t.Fatal("digest not stable")
	}

	t.Run("refuses low space", func(t *testing.T) {
		low := req
		low.FreeBytes = map[string]uint64{base: 5}
		_, err := PlanMigration(low)
		if !errors.Is(err, ErrInsufficientSpace) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("refuses unknown free space", func(t *testing.T) {
		unknown := req
		unknown.FreeBytes = nil
		if _, err := PlanMigration(unknown); !errors.Is(err, ErrInsufficientSpace) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("refuses non-empty target", func(t *testing.T) {
		writeTree(t, filepath.Join(base, "nextcloud", "html"), map[string]string{"stray": "1"})
		_, err := PlanMigration(req)
		if err == nil || !strings.Contains(err.Error(), "already holds data") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("refuses placed apps", func(t *testing.T) {
		placed := req
		placed.Containers = []docker.Container{projectBox("nextcloud", "nextcloud", "running", "nextcloud",
			docker.Mount{Type: "bind", Source: base + "/nextcloud/html", Destination: "/var/www/html"},
			docker.Mount{Type: "bind", Source: base + "/nextcloud/data", Destination: "/var/www/html/data"})}
		if _, err := PlanMigration(placed); err == nil || !strings.Contains(err.Error(), "nothing to migrate") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestApplyPlacementKeepsStack(t *testing.T) {
	manifest := repoManifest(t, "nextcloud")
	files, err := Generate(manifest, Generation{Networks: []NetworkAttachment{{Name: "edge", External: true}}})
	if err != nil {
		t.Fatal(err)
	}
	mounts, _, err := ResolveData(manifest, both("/mnt/x"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := ApplyPlacement(files.ComposeYAML, mounts)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Generated by Homestead", "source: /mnt/x/nextcloud/html", "create_host_path: false", "external: true", "restart: unless-stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Re-applying replaces rather than duplicating.
	again, err := ApplyPlacement(out, mounts)
	if err != nil || strings.Count(again, "target: /var/www/html\n") != 1 {
		t.Fatalf("reapply: %v\n%s", err, again)
	}
	pinned, err := PinOriginalVolumes(files.ComposeYAML, []PinVolume{{Service: "nextcloud", Target: "/var/www/html", Volume: "v1"}})
	if err != nil || !strings.Contains(pinned, "source: v1") || !strings.Contains(pinned, "v1:\n    external: true") {
		t.Fatalf("pin: %v\n%s", err, pinned)
	}
}

// fakeEngine is a tiny docker stand-in: container state moves with
// compose calls, and any call can be made to fail.
type fakeEngine struct {
	mu         sync.Mutex
	pkg        string
	containers []docker.Container
	calls      []string
	fail       map[string]error
	// afterUp mounts become what the new stack reports.
	newMounts []docker.Mount
	status    string
}

func (f *fakeEngine) setState(state string, mounts []docker.Mount) {
	for i := range f.containers {
		f.containers[i].State = state
		f.containers[i].Status = state
		if f.status != "" && state == "running" {
			f.containers[i].Status = f.status
		}
		if mounts != nil {
			f.containers[i].Mounts = mounts
		}
	}
}

func (f *fakeEngine) call(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	return f.fail[name]
}

func (f *fakeEngine) install(t *testing.T) {
	t.Helper()
	oldStop, oldStart, oldUp, oldUpServices, oldProject, oldExec, oldCopy := composeStop, composeStart, composeUp, composeUpServices, projectContainers, execIn, migrateCopy
	oldTimeout, oldStable, oldPoll := healthTimeout, healthStable, healthPoll
	healthTimeout, healthStable, healthPoll = 80*time.Millisecond, 0, time.Millisecond
	t.Cleanup(func() {
		composeStop, composeStart, composeUp, composeUpServices, projectContainers, execIn, migrateCopy = oldStop, oldStart, oldUp, oldUpServices, oldProject, oldExec, oldCopy
		healthTimeout, healthStable, healthPoll = oldTimeout, oldStable, oldPoll
	})
	composeStop = func(context.Context, string, string) error {
		if err := f.call("stop"); err != nil {
			return err
		}
		f.setState("exited", nil)
		return nil
	}
	composeStart = func(context.Context, string, string) error {
		if err := f.call("start"); err != nil {
			return err
		}
		f.setState("running", nil)
		return nil
	}
	composeUp = func(_ context.Context, dir, _ string) error {
		if err := f.call("up"); err != nil {
			return err
		}
		f.setState("running", f.newMounts)
		return nil
	}
	composeUpServices = func(context.Context, string, string, ...string) error {
		if err := f.call("up-services"); err != nil {
			return err
		}
		f.setState("running", f.newMounts)
		return nil
	}
	projectContainers = func(context.Context, string) ([]docker.Container, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return append([]docker.Container(nil), ProjectContainers(f.containers, f.pkg)...), nil
	}
	execIn = func(_ context.Context, container string, args ...string) error {
		return f.call("exec:" + args[0])
	}
}

type migrationFixture struct {
	t        *testing.T
	st       *store.Store
	base     string
	dir      string
	manifest Manifest
	vol      string
	engine   *fakeEngine
	original string
	plan     MigrationPlan
	registry *storage.Registry
}

func newMigrationFixture(t *testing.T, name string) *migrationFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	base := t.TempDir()
	registry := storage.NewRegistry(st)
	var discovered discover.Storage
	discovered.Mounts = append(discovered.Mounts, discover.Mount{Mountpoint: base, FSType: "ext4", Accessible: true})
	if _, err := registry.Register(base, "m", "owner", discovered); err != nil {
		t.Fatal(err)
	}
	manifest := repoManifest(t, name)
	// Tests run unprivileged: keep modes, drop the chown to image users.
	services := append([]Service(nil), manifest.Services...)
	for i := range services {
		services[i].Data = append([]DataPath(nil), services[i].Data...)
		for j := range services[i].Data {
			services[i].Data[j].UID, services[i].Data[j].GID = nil, nil
		}
	}
	manifest.Services = services
	files, err := Generate(manifest, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := WriteStack(dir, files); err != nil {
		t.Fatal(err)
	}
	if err := RecordFingerprints(st, name, dir); err != nil {
		t.Fatal(err)
	}
	f := &migrationFixture{t: t, st: st, base: base, dir: dir, manifest: manifest, registry: registry, original: files.ComposeYAML}
	f.vol = filepath.Join(t.TempDir(), "_data")
	f.engine = &fakeEngine{pkg: name, fail: map[string]error{}}
	return f
}

// nextcloud wires a pre-S276 nextcloud: one html volume holding data/.
func newNextcloudFixture(t *testing.T) *migrationFixture {
	f := newMigrationFixture(t, "nextcloud")
	writeTree(t, f.vol, map[string]string{"config/config.php": "<?php", "data/alice/file.txt": "hello", "index.php": "ab"})
	f.engine.containers = []docker.Container{
		projectBox("nextcloud", "nextcloud", "running", "nextcloud", volumeMount("v1", f.vol, "/var/www/html")),
	}
	f.engine.newMounts = []docker.Mount{
		{Type: "bind", Source: f.base + "/nextcloud/html", Destination: "/var/www/html"},
		{Type: "bind", Source: f.base + "/nextcloud/data", Destination: "/var/www/html/data"},
	}
	f.engine.install(t)
	f.plan = f.mustPlan()
	return f
}

func (f *migrationFixture) request() MigrationRequest {
	containers, _ := projectContainers(context.Background(), f.engine.pkg)
	return MigrationRequest{
		Manifest: f.manifest, Containers: containers, Mountpoints: both(f.base),
		FreeBytes: map[string]uint64{f.base: 1 << 30},
	}
}

func (f *migrationFixture) mustPlan() MigrationPlan {
	plan, err := PlanMigration(f.request())
	if err != nil {
		f.t.Fatalf("PlanMigration: %v", err)
	}
	return plan
}

func (f *migrationFixture) options(backup func(context.Context) error) (MigrationOptions, *[]string) {
	var audits []string
	return MigrationOptions{
		Manifest: f.manifest, Plan: f.plan, Dir: f.dir, Registry: f.registry, Store: f.st,
		Backup: backup, AckNoBackup: backup == nil, Actor: "owner",
		Replan: func(context.Context) (MigrationPlan, error) { return PlanMigration(f.request()) },
		Audit:  func(outcome, detail string) { audits = append(audits, outcome+": "+detail) },
	}, &audits
}

func (f *migrationFixture) run(opts MigrationOptions) operation.Operation {
	f.t.Helper()
	def, err := MigrateDefinition(opts)
	if err != nil {
		f.t.Fatalf("MigrateDefinition: %v", err)
	}
	runner := operation.NewRunner(f.st)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		f.t.Fatal(err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return done
}

func (f *migrationFixture) composeText() string {
	raw, err := os.ReadFile(filepath.Join(f.dir, "compose.yaml"))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(raw)
}

func TestMigrationSuccess(t *testing.T) {
	f := newNextcloudFixture(t)
	backups := 0
	opts, audits := f.options(func(context.Context) error { backups++; return nil })
	done := f.run(opts)
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %s: %s", done.Status, done.Error)
	}
	if backups != 1 {
		t.Fatalf("backups = %d", backups)
	}
	// Data landed where declared; nested path split from its parent.
	got, err := os.ReadFile(filepath.Join(f.base, "nextcloud", "data", "alice", "file.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("data file: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(f.base, "nextcloud", "html", "config", "config.php")); err != nil {
		t.Fatalf("config missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.base, "nextcloud", "html", "data", "alice")); err == nil {
		t.Fatal("nested data copied into the parent bind as well")
	}
	// The source volume is untouched.
	if _, err := os.Stat(filepath.Join(f.vol, "data", "alice", "file.txt")); err != nil {
		t.Fatalf("old volume modified: %v", err)
	}
	text := f.composeText()
	if !strings.Contains(text, "source: "+f.base+"/nextcloud/html") || !hasDeclaredBinds(f.dir, f.manifest) {
		t.Fatalf("compose not rewritten:\n%s", text)
	}
	recorded, err := StoredFingerprints(f.st, "nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	if drifts, err := Detect(f.dir, recorded); err != nil || len(drifts) != 0 {
		t.Fatalf("drift = %v err = %v", drifts, err)
	}
	// Old volume tracked, not removed.
	old, err := ListOldVolumes(f.st, "nextcloud")
	if err != nil || len(old) != 1 || old[0].Volume != "v1" || old[0].State != VolumePendingRemoval {
		t.Fatalf("old = %+v err = %v", old, err)
	}
	if len(*audits) != 1 || !strings.HasPrefix((*audits)[0], "ok:") {
		t.Fatalf("audits = %v", *audits)
	}
	// And it now reads as placed.
	containers, _ := projectContainers(context.Background(), "nextcloud")
	if status := DetectData(f.manifest, containers, both(f.base)); status.State != DataPlaced {
		t.Fatalf("after: %s %s", status.State, status.Reason)
	}
}

func TestMigrationRequiresBackupDecision(t *testing.T) {
	f := newNextcloudFixture(t)
	opts, _ := f.options(nil)
	opts.AckNoBackup = false
	if _, err := MigrateDefinition(opts); err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("err = %v", err)
	}
}

func TestMigrationFailureRollsBack(t *testing.T) {
	cases := []struct {
		step   string
		inject func(f *migrationFixture, opts *MigrationOptions)
		pinned bool
		copied bool
	}{
		{step: "preflight", inject: func(f *migrationFixture, opts *MigrationOptions) {
			opts.Plan.Digest = "stale"
		}},
		{step: "stop", inject: func(f *migrationFixture, _ *MigrationOptions) {
			f.engine.fail["stop"] = errors.New("engine hiccup")
		}},
		{step: "verify-stopped", inject: func(f *migrationFixture, _ *MigrationOptions) {
			// Stop "succeeds" but a container keeps running.
			f.engine.fail["stop"] = nil
			composeStop = func(context.Context, string, string) error {
				f.engine.calls = append(f.engine.calls, "stop")
				return nil
			}
		}},
		{step: "backup", inject: func(f *migrationFixture, opts *MigrationOptions) {
			opts.Backup = func(context.Context) error { return errors.New("destination full") }
		}},
		{step: "copy:nextcloud:/var/www/html/data", copied: true, inject: func(f *migrationFixture, _ *MigrationOptions) {
			migrateCopy = func(ctx context.Context, src, dst string, ex []string) error {
				if strings.HasSuffix(dst, "/data") {
					writeTree(t, dst, map[string]string{"partial": "1"})
					return errors.New("disk error")
				}
				return tarCopy(ctx, src, dst, ex)
			}
		}},
		{step: "verify-copy", copied: true, inject: func(f *migrationFixture, _ *MigrationOptions) {
			migrateCopy = func(context.Context, string, string, []string) error { return nil }
		}},
		{step: "up", pinned: true, copied: true, inject: func(f *migrationFixture, _ *MigrationOptions) {
			calls := 0
			f.engine.fail["up"] = nil
			prior := composeUp
			composeUp = func(ctx context.Context, dir, project string) error {
				calls++
				if calls == 1 {
					f.engine.calls = append(f.engine.calls, "up")
					return errors.New("port in use")
				}
				return prior(ctx, dir, project)
			}
		}},
		{step: "health-check", pinned: true, copied: true, inject: func(f *migrationFixture, _ *MigrationOptions) {
			calls := 0
			prior := projectContainers
			projectContainers = func(ctx context.Context, p string) ([]docker.Container, error) {
				out, err := prior(ctx, p)
				if calls++; calls > 2 && f.engine.calls[len(f.engine.calls)-1] != "start" {
					for i := range out {
						if out[i].State == "running" && len(out[i].Mounts) > 0 && out[i].Mounts[0].Type == "bind" {
							out[i].Status = "Up (unhealthy)"
						}
					}
				}
				return out, err
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.step, func(t *testing.T) {
			f := newNextcloudFixture(t)
			opts, audits := f.options(func(context.Context) error { return nil })
			tc.inject(f, &opts)
			done := f.run(opts)
			if done.Status != operation.StatusFailed {
				t.Fatalf("status = %s", done.Status)
			}
			if !strings.Contains(done.Error, "stopped at step \""+tc.step+"\"") {
				t.Fatalf("error does not name the step: %s", done.Error)
			}
			if strings.Contains(done.Error, "ROLLBACK FAILED") {
				t.Fatalf("rollback failed: %s", done.Error)
			}
			text := f.composeText()
			if tc.pinned {
				if !strings.Contains(text, "source: v1") || !strings.Contains(text, "external: true") {
					t.Fatalf("compose not pinned to the original volume:\n%s", text)
				}
			} else if text != f.original {
				t.Fatalf("original compose.yaml not restored:\n%s", text)
			}
			if tc.copied {
				for _, dest := range []string{"data", "html"} {
					if holds, _ := dirHasEntries(filepath.Join(f.base, "nextcloud", dest)); holds {
						t.Fatalf("partial copy left in %s", dest)
					}
				}
			}
			// The app is running again and the old volume is intact.
			containers, _ := projectContainers(context.Background(), "nextcloud")
			if len(containers) != 1 || containers[0].State != "running" {
				t.Fatalf("app not running: %+v", containers)
			}
			if _, err := os.Stat(filepath.Join(f.vol, "data", "alice", "file.txt")); err != nil {
				t.Fatalf("old volume damaged: %v", err)
			}
			if old, _ := ListOldVolumes(f.st, "nextcloud"); len(old) != 0 {
				t.Fatalf("old volumes recorded after failure: %+v", old)
			}
			if len(*audits) != 1 || !strings.HasPrefix((*audits)[0], "error:") {
				t.Fatalf("audits = %v", *audits)
			}
		})
	}
}

// TestMigrationPostgres covers a database-backed app: copying only
// with the stack stopped, and verifying by starting the database.
func TestMigrationPostgres(t *testing.T) {
	f := newMigrationFixture(t, "authentik")
	pg := filepath.Join(t.TempDir(), "pg")
	writeTree(t, pg, map[string]string{"PG_VERSION": "16", "base/1/1": "pagefile"})
	f.engine.containers = []docker.Container{
		projectBox("authentik", "postgresql", "running", "docker.io/library/postgres:16-alpine", volumeMount("pgv", pg, "/var/lib/postgresql/data")),
		projectBox("authentik", "server", "running", "ghcr.io/goauthentik/server:2026.8.3"),
		projectBox("authentik", "worker", "running", "ghcr.io/goauthentik/server:2026.8.3"),
	}
	f.engine.newMounts = nil
	f.engine.install(t)
	f.plan = f.mustPlan()
	if len(f.plan.DatabaseServices) != 1 || f.plan.DatabaseServices[0] != "postgresql" {
		t.Fatalf("plan = %+v", f.plan)
	}
	layered := false
	for _, note := range f.plan.Notes {
		if strings.Contains(note, "container-layer") {
			layered = true
		}
	}
	if !layered {
		t.Fatalf("notes = %v", f.plan.Notes)
	}
	t.Run("success starts the database alone first", func(t *testing.T) {
		opts, _ := f.options(func(context.Context) error { return nil })
		done := f.run(opts)
		if done.Status != operation.StatusSucceeded {
			t.Fatalf("status = %s: %s", done.Status, done.Error)
		}
		order := strings.Join(f.engine.calls, ",")
		if !strings.Contains(order, "stop,up-services,exec:pg_isready,up") {
			t.Fatalf("calls = %s", order)
		}
		if _, err := os.Stat(filepath.Join(f.base, "authentik", "postgres", "base", "1", "1")); err != nil {
			t.Fatalf("database copy: %v", err)
		}
	})
}

func TestMigrationPostgresVerifyFailureRestores(t *testing.T) {
	f := newMigrationFixture(t, "authentik")
	pg := filepath.Join(t.TempDir(), "pg")
	writeTree(t, pg, map[string]string{"PG_VERSION": "16"})
	f.engine.containers = []docker.Container{
		projectBox("authentik", "postgresql", "running", "postgres:16-alpine", volumeMount("pgv", pg, "/var/lib/postgresql/data")),
		projectBox("authentik", "server", "running", "x"),
		projectBox("authentik", "worker", "running", "x"),
	}
	f.engine.install(t)
	f.plan = f.mustPlan()
	f.engine.fail["exec:pg_isready"] = errors.New("database system is starting up")
	opts, _ := f.options(nil)
	done := f.run(opts)
	if done.Status != operation.StatusFailed || !strings.Contains(done.Error, `step "verify-database"`) {
		t.Fatalf("status = %s err = %s", done.Status, done.Error)
	}
	if !strings.Contains(f.composeText(), "source: pgv") {
		t.Fatalf("not pinned:\n%s", f.composeText())
	}
	if holds, _ := dirHasEntries(filepath.Join(f.base, "authentik", "postgres")); holds {
		t.Fatal("copy left behind")
	}
}

func TestOldVolumeRemoval(t *testing.T) {
	f := newNextcloudFixture(t)
	opts, _ := f.options(func(context.Context) error { return nil })
	if done := f.run(opts); done.Status != operation.StatusSucceeded {
		t.Fatalf("migrate: %s", done.Error)
	}
	req := RemoveOldVolumesRequest{Package: "nextcloud", Volumes: []string{"v1"}, Actor: "owner"}
	t.Run("refused while referenced", func(t *testing.T) {
		still := []docker.Container{projectBox("nextcloud", "nextcloud", "exited", "nextcloud", volumeMount("v1", f.vol, "/var/www/html"))}
		err := CheckOldVolumeRemoval(f.st, req, still)
		if err == nil || !strings.Contains(err.Error(), "still in use") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("refused for unknown volume", func(t *testing.T) {
		bad := RemoveOldVolumesRequest{Package: "nextcloud", Volumes: []string{"someone-elses"}}
		if err := CheckOldVolumeRemoval(f.st, bad, nil); err == nil {
			t.Fatal("untracked volume accepted")
		}
	})
	t.Run("removes when unreferenced", func(t *testing.T) {
		var removed []string
		oldRemove, oldAll := volumeRemove, allContainers
		volumeRemove = func(_ context.Context, name string) error { removed = append(removed, name); return nil }
		allContainers = func(context.Context) ([]docker.Container, error) {
			containers, _ := projectContainers(context.Background(), "nextcloud")
			return containers, nil
		}
		t.Cleanup(func() { volumeRemove, allContainers = oldRemove, oldAll })
		def, err := RemoveOldVolumesDefinition(f.st, req, nil)
		if err != nil {
			t.Fatal(err)
		}
		runner := operation.NewRunner(f.st)
		op, _ := runner.Start(context.Background(), def, "")
		done, _ := runner.Wait(context.Background(), op.ID)
		if done.Status != operation.StatusSucceeded || len(removed) != 1 {
			t.Fatalf("status = %s err = %s removed = %v", done.Status, done.Error, removed)
		}
		old, _ := ListOldVolumes(f.st, "nextcloud")
		if old[0].State != VolumeRemoved {
			t.Fatalf("old = %+v", old)
		}
		if err := CheckOldVolumeRemoval(f.st, req, nil); err == nil {
			t.Fatal("second removal accepted")
		}
	})
}
