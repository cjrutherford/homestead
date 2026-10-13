package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"homestead/internal/operation"
)

// fakeDB models the database container: one role password, ALTER ROLE
// over psql stdin, and password authentication over `sh -c` (which
// reads the password from stdin). trust makes it accept anything.
type fakeDB struct {
	password     string
	trust        bool
	alterFail    func(newPassword string) error
	rejectAuthOf string // password that authenticates no more (simulates an unhealthy app)
	calls        []execCall
	env          map[string]string
}

var alterRE = regexp.MustCompile(`PASSWORD '([^']*)'`)

func (f *fakeDB) install(t *testing.T) {
	t.Helper()
	old := dbExec
	dbExec = func(_ context.Context, container, stdin string, args ...string) (string, error) {
		f.calls = append(f.calls, execCall{container, stdin, args})
		switch args[0] {
		case "psql":
			m := alterRE.FindStringSubmatch(stdin)
			if m == nil {
				return "", errors.New("bad sql")
			}
			if f.alterFail != nil {
				if err := f.alterFail(m[1]); err != nil {
					return "", err
				}
			}
			f.password = m[1]
			return "", nil
		case "sh":
			pw := strings.TrimSuffix(stdin, "\n")
			if f.trust || (pw == f.password && pw != f.rejectAuthOf) {
				return "1\n", nil
			}
			return "", errors.New("password authentication failed")
		case "printenv":
			if v, ok := f.env[args[1]]; ok {
				return v + "\n", nil
			}
			return "", errors.New("exit status 1")
		}
		return "", fmt.Errorf("unexpected exec %v", args)
	}
	t.Cleanup(func() { dbExec = old })
}

func (f *fakeDB) alters() []string {
	var out []string
	for _, c := range f.calls {
		if c.args[0] == "psql" {
			out = append(out, alterRE.FindStringSubmatch(c.stdin)[1])
		}
	}
	return out
}

// stubComposeFail records compose calls; failOn names a subcommand to
// fail once.
func stubComposeFail(t *testing.T, failOn string, times int) *[][]string {
	t.Helper()
	var calls [][]string
	old := composeRun
	composeRun = func(_ context.Context, _, _ string, args ...string) error {
		calls = append(calls, args)
		if args[0] == failOn && times > 0 {
			times--
			return errors.New("compose failed")
		}
		return nil
	}
	t.Cleanup(func() { composeRun = old })
	return &calls
}

type rotationFixture struct {
	m      Manifest
	sec    *memSecrets
	dir    string
	store  string
	old    string
	db     *fakeDB
	files  []SecretFile
	pendin string
}

func newRotationFixture(t *testing.T) *rotationFixture {
	t.Helper()
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	dir, files := writeSecretStack(t, m, sec)
	store := SecretStoreName("authentik", "db_password")
	f := &rotationFixture{m: m, sec: sec, dir: dir, store: store, old: sec.values[store],
		db: &fakeDB{password: sec.values[store]}, files: files.SecretFiles, pendin: pendingName(store)}
	f.db.install(t)
	return f
}

func (f *rotationFixture) fileValue(t *testing.T, service string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, SecretsDirName, service+"_db_password"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (f *rotationFixture) assertReverted(t *testing.T) {
	t.Helper()
	if f.db.password != f.old {
		t.Fatalf("database role = %q, want the old password restored", f.db.password)
	}
	if f.sec.values[f.store] != f.old {
		t.Fatal("store lost the old value")
	}
	if _, pending := f.sec.values[f.pendin]; pending {
		t.Fatal("pending journal row left behind after a clean revert")
	}
	for _, svc := range []string{"postgresql", "server", "worker"} {
		if f.fileValue(t, svc) != f.old {
			t.Fatalf("%s file does not hold the old value", svc)
		}
	}
}

func TestRotateJournalsBeforeAlterAndSettles(t *testing.T) {
	f := newRotationFixture(t)
	cmp := stubComposeFail(t, "", 0)
	var sawPending, storeStillOld bool
	f.db.alterFail = func(newPW string) error {
		sawPending = f.sec.values[f.pendin] == newPW
		storeStillOld = f.sec.values[f.store] == f.old
		return nil
	}
	if err := rotateSecret(context.Background(), f.m, f.dir, "authentik", "db_password", f.sec); err != nil {
		t.Fatal(err)
	}
	if !sawPending || !storeStillOld {
		t.Fatal("pending value must be journaled before ALTER ROLE, with the store still on the old value")
	}
	fresh := f.sec.values[f.store]
	if fresh == f.old || len(fresh) != 64 || f.db.password != fresh {
		t.Fatal("rotation did not settle on the fresh value")
	}
	if _, pending := f.sec.values[f.pendin]; pending {
		t.Fatal("pending row not cleared")
	}
	for _, svc := range []string{"postgresql", "server", "worker"} {
		if f.fileValue(t, svc) != fresh {
			t.Fatalf("%s file stale", svc)
		}
	}
	if len(*cmp) != 1 || (*cmp)[0][0] != "restart" {
		t.Fatalf("compose = %v", *cmp)
	}
	// ALTER ROLE carries the value on stdin only; the health check ran.
	for _, c := range f.db.calls {
		for _, a := range c.args {
			if strings.Contains(a, fresh) || strings.Contains(a, f.old) {
				t.Fatal("password in argv")
			}
		}
	}
	last := f.db.calls[len(f.db.calls)-1]
	if last.args[0] != "sh" || last.stdin != fresh+"\n" {
		t.Fatalf("last call = %+v, want the health check with the new password", last)
	}
}

func TestRotateRevertsAtEveryFailingStep(t *testing.T) {
	cases := map[string]func(f *rotationFixture, t *testing.T){
		"alter role": func(f *rotationFixture, t *testing.T) {
			stubComposeFail(t, "", 0)
			f.db.alterFail = func(pw string) error {
				if pw != f.old {
					return errors.New("alter failed")
				}
				return nil
			}
		},
		"write files": func(f *rotationFixture, t *testing.T) {
			stubComposeFail(t, "", 0)
			n := 0
			chownSecret = func(*os.File, int, int) error {
				n++
				if n == 1 {
					return errors.New("chown failed")
				}
				return nil
			}
		},
		"promote": func(f *rotationFixture, t *testing.T) {
			stubComposeFail(t, "", 0)
		},
		"restart": func(f *rotationFixture, t *testing.T) {
			stubComposeFail(t, "restart", 1)
		},
		"health check": func(f *rotationFixture, t *testing.T) {
			stubComposeFail(t, "", 0)
			f.db.alterFail = func(pw string) error {
				if pw != f.old {
					f.db.rejectAuthOf = pw
				}
				return nil
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRotationFixture(t)
			setup(f, t)
			var src SecretStore = f.sec
			if name == "promote" {
				src = &flakySecrets{memSecrets: f.sec, failSet: func(n, v string) bool { return n == f.store && v != f.old }}
			}
			err := rotateSecret(context.Background(), f.m, f.dir, "authentik", "db_password", src)
			if err == nil || !strings.Contains(err.Error(), "reverted") {
				t.Fatalf("err = %v, want a reverted failure", err)
			}
			f.assertReverted(t)
		})
	}
}

type flakySecrets struct {
	*memSecrets
	failSet func(name, value string) bool
}

func (f *flakySecrets) SetApp(name, value string) error {
	if f.failSet != nil && f.failSet(name, value) {
		return errors.New("store write failed")
	}
	return f.memSecrets.SetApp(name, value)
}

func TestRotateRevertFailureIsLoudAndKeepsJournal(t *testing.T) {
	f := newRotationFixture(t)
	stubComposeFail(t, "restart", 1)
	f.db.alterFail = func(pw string) error {
		if pw == f.old {
			return errors.New("revert refused")
		}
		return nil
	}
	err := rotateSecret(context.Background(), f.m, f.dir, "authentik", "db_password", f.sec)
	if err == nil || !strings.Contains(err.Error(), "AND the database role could not be reverted") ||
		!strings.Contains(err.Error(), "docker exec -i authentik-postgresql psql") ||
		!strings.Contains(err.Error(), f.pendin) {
		t.Fatalf("err = %v", err)
	}
	if f.sec.values[f.pendin] == "" || f.db.password != f.sec.values[f.pendin] {
		t.Fatal("journal must keep the value the role actually holds")
	}
	for _, v := range f.sec.values {
		if strings.Contains(err.Error(), v) {
			t.Fatal("error leaks a secret value")
		}
	}
}

func TestRotateRefusesUnknownOldValueAndPendingRows(t *testing.T) {
	f := newRotationFixture(t)
	delete(f.sec.values, f.store)
	if err := rotateSecret(context.Background(), f.m, f.dir, "authentik", "db_password", f.sec); err == nil ||
		!strings.Contains(err.Error(), "register it before rotating") || len(f.db.calls) != 0 {
		t.Fatalf("err=%v calls=%d", err, len(f.db.calls))
	}
	f.sec.values[f.store] = f.old
	f.sec.values[f.pendin] = "left-over"
	if err := rotateSecret(context.Background(), f.m, f.dir, "authentik", "db_password", f.sec); err == nil ||
		!strings.Contains(err.Error(), "pending") || len(f.db.calls) != 0 {
		t.Fatalf("err=%v", err)
	}
	two := ConfigPlan{Manifest: f.m, Rotate: []string{"db_password", "secret_key"}}
	if err := two.Validate(); err == nil {
		t.Fatal("multi-secret rotation accepted")
	}
}

func TestReconcileInterruptedRotation(t *testing.T) {
	t.Run("database took the new password", func(t *testing.T) {
		f := newRotationFixture(t)
		stubComposeFail(t, "", 0)
		f.sec.values[f.pendin] = "crashed-new-value"
		f.db.password = "crashed-new-value"
		msg, err := ReconcileRotation(context.Background(), f.m, f.dir, "authentik", "db_password", f.sec)
		if err != nil || !strings.Contains(msg, "settled to it") {
			t.Fatalf("msg=%q err=%v", msg, err)
		}
		if f.sec.values[f.store] != "crashed-new-value" || f.fileValue(t, "server") != "crashed-new-value" {
			t.Fatal("store and files must follow the database")
		}
		if _, ok := f.sec.values[f.pendin]; ok {
			t.Fatal("pending not cleared")
		}
	})
	t.Run("database kept the old password", func(t *testing.T) {
		f := newRotationFixture(t)
		stubComposeFail(t, "", 0)
		f.sec.values[f.pendin] = "never-applied"
		msg, err := ReconcileRotation(context.Background(), f.m, f.dir, "authentik", "db_password", f.sec)
		if err != nil || !strings.Contains(msg, "old password") || f.sec.values[f.store] != f.old {
			t.Fatalf("msg=%q err=%v", msg, err)
		}
		if _, ok := f.sec.values[f.pendin]; ok {
			t.Fatal("pending not cleared")
		}
	})
	t.Run("database unreachable", func(t *testing.T) {
		f := newRotationFixture(t)
		stubComposeFail(t, "", 0)
		f.sec.values[f.pendin] = "unknown"
		f.db.password = "something-else"
		if _, err := ReconcileRotation(context.Background(), f.m, f.dir, "authentik", "db_password", f.sec); err == nil {
			t.Fatal("reconciled without evidence")
		}
		if f.sec.values[f.pendin] != "unknown" {
			t.Fatal("pending must survive for a later retry")
		}
	})
	t.Run("nothing pending", func(t *testing.T) {
		f := newRotationFixture(t)
		if msg, err := ReconcileRotation(context.Background(), f.m, f.dir, "authentik", "db_password", f.sec); msg != "" || err != nil {
			t.Fatalf("msg=%q err=%v", msg, err)
		}
	})
}

func TestVerifyRefusesTrustAuthentication(t *testing.T) {
	m := authentikManifest(t)
	db := &fakeDB{password: "pw", trust: true}
	db.install(t)
	if err := VerifyDatabasePassword(context.Background(), m, "pw"); !errors.Is(err, ErrCannotVerify) {
		t.Fatalf("err = %v", err)
	}
	sec := newMemSecrets(t)
	if _, err := RegisterSecret(context.Background(), m, "db_password", "pw", sec, RegisterOptions{}); !errors.Is(err, ErrCannotVerify) || len(sec.values) != 0 {
		t.Fatalf("register err = %v", err)
	}
	if _, err := RegisterSecret(context.Background(), m, "db_password", "pw", sec, RegisterOptions{SkipVerify: true}); err != nil {
		t.Fatalf("skip-verify with no existing value: %v", err)
	}
}

func TestRegisterSecretRules(t *testing.T) {
	ctx := context.Background()
	m := authentikManifest(t)
	db := &fakeDB{password: "old-password-xyz"}
	db.install(t)
	stubComposeFail(t, "", 0)
	sec := newMemSecrets(t)

	// Wrong password: rejected, nothing stored, no leak.
	_, err := RegisterSecret(ctx, m, "db_password", "wrong-guess-1", sec, RegisterOptions{})
	if err == nil || len(sec.values) != 0 || strings.Contains(err.Error(), "wrong-guess-1") {
		t.Fatalf("err = %v", err)
	}
	if call := db.calls[len(db.calls)-1]; call.stdin != "wrong-guess-1\n" {
		t.Fatal("verification must pass the password on stdin")
	}
	// Verified registration stores it.
	res, err := RegisterSecret(ctx, m, "db_password", "old-password-xyz", sec, RegisterOptions{})
	if err != nil || !res.Changed || res.StoreName != "app_authentik_db_password" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// Same value again is a no-op.
	if res, err := RegisterSecret(ctx, m, "db_password", "old-password-xyz", sec, RegisterOptions{}); err != nil || res.Changed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// Overwriting without verification needs force.
	if _, err := RegisterSecret(ctx, m, "db_password", "other", sec, RegisterOptions{SkipVerify: true}); err == nil {
		t.Fatal("unverified overwrite accepted")
	}
	if res, err := RegisterSecret(ctx, m, "db_password", "other", sec, RegisterOptions{SkipVerify: true, Force: true}); err != nil || !res.Changed {
		t.Fatalf("forced overwrite: %+v %v", res, err)
	}
	// Immutable secrets with a value refuse, force or not.
	if _, err := RegisterSecret(ctx, m, "secret_key", "first-key", sec, RegisterOptions{}); err != nil {
		t.Fatalf("first secret_key: %v", err)
	}
	if _, err := RegisterSecret(ctx, m, "secret_key", "second-key", sec, RegisterOptions{Force: true}); err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("err = %v", err)
	}
	// Non-database overwrite needs force.
	m2 := m
	m2.Secrets = append([]SecretDecl(nil), m.Secrets...)
	m2.Secrets[1].Immutable = false
	if _, err := RegisterSecret(ctx, m2, "secret_key", "third-key", sec, RegisterOptions{}); err == nil {
		t.Fatal("unverifiable overwrite accepted without force")
	}
}

func TestApplyRegisteredRestartsConsumersOfWiredStacks(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	dir, _ := writeSecretStack(t, m, sec)
	cmp := stubComposeFail(t, "", 0)
	sec.values[SecretStoreName("authentik", "db_password")] = "registered-new"
	if err := ApplyRegistered(context.Background(), m, dir, "authentik", "db_password", sec); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, SecretsDirName, "server_db_password"))
	if string(raw) != "registered-new" || len(*cmp) != 1 || (*cmp)[0][0] != "restart" {
		t.Fatalf("file=%q compose=%v", raw, *cmp)
	}
	// Unwired stacks are left alone.
	bare := t.TempDir()
	_ = os.WriteFile(filepath.Join(bare, "compose.yaml"), []byte("services: {}\n"), 0o644)
	if err := ApplyRegistered(context.Background(), m, bare, "authentik", "db_password", sec); err != nil || len(*cmp) != 1 {
		t.Fatalf("unwired stack touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bare, SecretsDirName)); err == nil {
		t.Fatal("secret files written into an unwired stack")
	}
}

func TestSecretFileWritesAreAtomicAndRefuseSymlinks(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	files := SecretFilesFor(m)
	if err := ensureNames(sec, storeNames(files)); err != nil {
		t.Fatal(err)
	}

	t.Run("fixes a pre-existing loose file and directory", func(t *testing.T) {
		dir := t.TempDir()
		secDir := filepath.Join(dir, SecretsDirName)
		if err := os.Mkdir(secDir, 0o755); err != nil {
			t.Fatal(err)
		}
		loose := filepath.Join(secDir, files[0].Key)
		if err := os.WriteFile(loose, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := WriteSecretFiles(dir, files, sec); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(loose)
		dinfo, _ := os.Stat(secDir)
		if info.Mode().Perm() != 0o600 || dinfo.Mode().Perm() != 0o700 {
			t.Fatalf("modes file=%v dir=%v", info.Mode().Perm(), dinfo.Mode().Perm())
		}
		entries, _ := os.ReadDir(secDir)
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp-") {
				t.Fatalf("temp file left behind: %s", e.Name())
			}
		}
	})
	t.Run("refuses a symlinked file", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "victim")
		_ = os.WriteFile(target, []byte("untouched"), 0o600)
		_ = os.Mkdir(filepath.Join(dir, SecretsDirName), 0o700)
		_ = os.Symlink(target, filepath.Join(dir, SecretsDirName, files[0].Key))
		if err := WriteSecretFiles(dir, files, sec); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("err = %v", err)
		}
		if raw, _ := os.ReadFile(target); string(raw) != "untouched" {
			t.Fatal("symlink target overwritten")
		}
	})
	t.Run("refuses a symlinked secrets directory", func(t *testing.T) {
		dir := t.TempDir()
		elsewhere := t.TempDir()
		_ = os.Symlink(elsewhere, filepath.Join(dir, SecretsDirName))
		if err := WriteSecretFiles(dir, files, sec); err == nil {
			t.Fatal("symlinked directory accepted")
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Fatal("secrets written through the symlink")
		}
	})
}

func TestUninstallRemovesSecretFilesButKeepsStoreValues(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	dir, _ := writeSecretStack(t, m, sec)
	old := composeDown
	composeDown = func(context.Context, string, string) error { return nil }
	t.Cleanup(func() { composeDown = old })
	def, err := RemoveDefinition(Removal{Package: "authentik"}, dir, openDriftStore(t))
	if err != nil {
		t.Fatal(err)
	}
	runner := openInstallRunner(t)
	op, _ := runner.Start(context.Background(), def, "")
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("done=%+v err=%v", done, err)
	}
	if _, err := os.Stat(filepath.Join(dir, SecretsDirName)); !os.IsNotExist(err) {
		t.Fatalf("secrets dir survived uninstall: %v", err)
	}
	if len(sec.values) != 2 {
		t.Fatal("store values must remain for a reinstall")
	}
}

func TestInstallRefusesNewPasswordOverInitialisedCluster(t *testing.T) {
	base := t.TempDir()
	registry := installRegistry(t, base)
	plan := installPlan(t, base)
	m := authentikManifest(t)
	files, _ := Generate(m, Generation{})
	plan.Package, plan.Compose = "authentik", files
	pgData := filepath.Join(t.TempDir(), "postgres")
	_ = os.MkdirAll(pgData, 0o700)
	_ = os.WriteFile(filepath.Join(pgData, "PG_VERSION"), []byte("16\n"), 0o600)
	plan.Mounts = append(plan.Mounts, PlannedMount{Service: "postgresql", Target: "/var/lib/postgresql/data", Source: pgData, Database: true})
	stubComposeUp(t, false)
	sec := newMemSecrets(t)

	def, err := InstallDefinition(plan, registry, t.TempDir(), openDriftStore(t), sec)
	if err != nil {
		t.Fatal(err)
	}
	if def.Steps[0].Name != "check-existing-database" {
		t.Fatalf("first step = %s", def.Steps[0].Name)
	}
	err = def.Steps[0].Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Register the existing password first") || len(sec.values) != 0 {
		t.Fatalf("err=%v generated=%d", err, len(sec.values))
	}
	// With the password already registered the install may proceed.
	sec.values[SecretStoreName("authentik", "db_password")] = "registered-pw"
	if err := def.Steps[0].Run(context.Background()); err != nil {
		t.Fatalf("registered password blocked: %v", err)
	}
	// An empty data directory (no PG_VERSION) installs normally.
	_ = os.Remove(filepath.Join(pgData, "PG_VERSION"))
	delete(sec.values, SecretStoreName("authentik", "db_password"))
	if err := def.Steps[0].Run(context.Background()); err != nil {
		t.Fatalf("empty data dir blocked: %v", err)
	}
}

// preS266Stack builds the stack an older Homestead wrote: no secret
// wiring, plaintext credentials in the override file.
func preS266Stack(t *testing.T) (Manifest, string, string) {
	t.Helper()
	m := authentikManifest(t)
	old := m
	old.Secrets = nil
	old.Database = &DatabaseBackup{Service: m.Database.Service, Kind: m.Database.Kind, Database: m.Database.Database, Username: m.Database.Username}
	old.Services = append([]Service(nil), m.Services...)
	for i := range old.Services {
		old.Services[i].Environment = nil
	}
	files, err := Generate(old, Generation{Services: []ServiceGeneration{{
		Service: "postgresql", Volumes: []VolumeMapping{{Source: "/srv/pg", Target: "/var/lib/postgresql/data"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := WriteStack(dir, files, nil); err != nil {
		t.Fatal(err)
	}
	override := "services:\n  postgresql:\n    environment:\n      POSTGRES_PASSWORD: legacy-pg-pass\n      POSTGRES_USER: authentik\n" +
		"  server:\n    environment:\n      AUTHENTIK_POSTGRESQL__PASSWORD: legacy-pg-pass\n      AUTHENTIK_SECRET_KEY: legacy-key\n      AUTHENTIK_LOG_LEVEL: info\n"
	if err := os.WriteFile(filepath.Join(dir, OverrideFileName), []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}
	return m, dir, files.ComposeYAML
}

func runSteps(t *testing.T, def operation.Definition) error {
	t.Helper()
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			return fmt.Errorf("%s: %w", step.Name, err)
		}
	}
	return nil
}

func TestAdoptSecretsRegeneratesOnlyWiring(t *testing.T) {
	m, dir, original := preS266Stack(t)
	sec := newMemSecrets(t)
	st := openDriftStore(t)
	if err := RecordFingerprints(st, "authentik", dir); err != nil {
		t.Fatal(err)
	}
	ups := stubComposeUp(t, false)

	// Preflight: the existing database password must be registered first.
	if _, err := ConfigureDefinition(ConfigPlan{Manifest: m, Adopt: true}, dir, "authentik", st, sec); err == nil ||
		!strings.Contains(err.Error(), "register the existing database password first") {
		t.Fatalf("err = %v", err)
	}
	sec.values[SecretStoreName("authentik", "db_password")] = "legacy-pg-pass"
	db := &fakeDB{password: "legacy-pg-pass", env: map[string]string{"AUTHENTIK_SECRET_KEY": "legacy-key"}}
	db.install(t)

	def, err := ConfigureDefinition(ConfigPlan{Manifest: m, Adopt: true}, dir, "authentik", st, sec)
	if err != nil {
		t.Fatal(err)
	}
	if err := runSteps(t, def); err != nil {
		t.Fatal(err)
	}
	if sec.values[SecretStoreName("authentik", "secret_key")] != "legacy-key" {
		t.Fatal("secret_key was not captured from the running container")
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	for _, want := range []string{"POSTGRES_PASSWORD_FILE: /run/secrets/postgresql_db_password",
		"file: ./secrets/server_db_password", "AUTHENTIK_POSTGRESQL__HOST: postgresql",
		"source: /srv/pg", "container_name: authentik-postgresql"} {
		if !strings.Contains(string(compose), want) {
			t.Fatalf("adopted compose missing %q:\n%s", want, compose)
		}
	}
	if strings.Contains(string(compose), "legacy-pg-pass") || strings.Contains(string(compose), "legacy-key") {
		t.Fatal("secret value in compose")
	}
	override, _ := os.ReadFile(filepath.Join(dir, OverrideFileName))
	if strings.Contains(string(override), "legacy") || strings.Contains(string(override), "POSTGRES_PASSWORD") ||
		!strings.Contains(string(override), "AUTHENTIK_LOG_LEVEL") {
		t.Fatalf("override not scrubbed correctly:\n%s", override)
	}
	for _, key := range []string{"postgresql_db_password", "server_secret_key", "worker_db_password"} {
		info, err := os.Stat(filepath.Join(dir, SecretsDirName, key))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v", key, err)
		}
	}
	recorded, _ := StoredFingerprints(st, "authentik")
	if drifts, _ := Detect(dir, recorded); len(drifts) != 0 {
		t.Fatalf("fingerprints not updated: %v", drifts)
	}
	if len(*ups) != 1 {
		t.Fatalf("up calls = %v", *ups)
	}
	if !composeSecretsWired(dir, m) {
		t.Fatal("stack not wired after adopt")
	}
	// No data was touched: the original bind survives and a second adopt refuses.
	if !strings.Contains(original, "/srv/pg") {
		t.Fatal("fixture lost its bind")
	}
	if _, err := ConfigureDefinition(ConfigPlan{Manifest: m, Adopt: true}, dir, "authentik", st, sec); err == nil {
		t.Fatal("adopting twice accepted")
	}
}

func TestAdoptRollsBackWhenVerificationFails(t *testing.T) {
	m, dir, original := preS266Stack(t)
	sec := newMemSecrets(t)
	st := openDriftStore(t)
	_ = RecordFingerprints(st, "authentik", dir)
	stubComposeUp(t, false)
	sec.values[SecretStoreName("authentik", "db_password")] = "registered-but-wrong"
	db := &fakeDB{password: "the-real-password", env: map[string]string{"AUTHENTIK_SECRET_KEY": "legacy-key"}}
	db.install(t)
	override, _ := os.ReadFile(filepath.Join(dir, OverrideFileName))

	def, err := ConfigureDefinition(ConfigPlan{Manifest: m, Adopt: true}, dir, "authentik", st, sec)
	if err != nil {
		t.Fatal(err)
	}
	err = runSteps(t, def)
	if err == nil || !strings.Contains(err.Error(), "previous configuration restored") {
		t.Fatalf("err = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	afterOverride, _ := os.ReadFile(filepath.Join(dir, OverrideFileName))
	if string(after) != original || string(afterOverride) != string(override) {
		t.Fatal("previous compose/override not restored")
	}
	if _, err := os.Stat(filepath.Join(dir, SecretsDirName)); !os.IsNotExist(err) {
		t.Fatal("adopted secret files left behind")
	}
	recorded, _ := StoredFingerprints(st, "authentik")
	if drifts, _ := Detect(dir, recorded); len(drifts) != 0 {
		t.Fatalf("fingerprints drifted after rollback: %v", drifts)
	}
}

func TestAdoptNeedsUnreadableSecretRegistered(t *testing.T) {
	m, dir, _ := preS266Stack(t)
	sec := newMemSecrets(t)
	st := openDriftStore(t)
	stubComposeUp(t, false)
	sec.values[SecretStoreName("authentik", "db_password")] = "pw"
	(&fakeDB{password: "pw"}).install(t) // no env to read
	def, err := ConfigureDefinition(ConfigPlan{Manifest: m, Adopt: true}, dir, "authentik", st, sec)
	if err != nil {
		t.Fatal(err)
	}
	err = runSteps(t, def)
	if err == nil || !strings.Contains(err.Error(), "register it") {
		t.Fatalf("err = %v", err)
	}
	if composeSecretsWired(dir, m) {
		t.Fatal("stack rewired despite a missing secret")
	}
}

func TestRestoreReportsMissingSecretFiles(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	dir, files := writeSecretStack(t, m, sec)
	st := openDriftStore(t)
	_ = RecordFingerprints(st, "authentik", dir)
	_ = os.Remove(filepath.Join(dir, SecretsDirName, files.SecretFiles[0].Key))
	restored, err := Restore(st, "authentik", dir)
	if err == nil || !strings.Contains(err.Error(), "secret files missing") || !strings.Contains(err.Error(), files.SecretFiles[0].Key) {
		t.Fatalf("restored=%v err=%v", restored, err)
	}
	if got := MissingSecretFiles(dir); len(got) != 1 {
		t.Fatalf("missing = %v", got)
	}
}
