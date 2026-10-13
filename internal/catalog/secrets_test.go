package catalog

import (
	"context"
	"fmt"
	"homestead/internal/operation"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func authentikManifest(t *testing.T) Manifest {
	t.Helper()
	m, err := repoCatalog(t).Get("authentik")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func writeSecretStack(t *testing.T, m Manifest, sec *memSecrets) (string, StackFiles) {
	t.Helper()
	files, err := Generate(m, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := ensureNames(sec, storeNames(files.SecretFiles)); err != nil {
		t.Fatal(err)
	}
	if err := WriteStack(dir, files, sec); err != nil {
		t.Fatal(err)
	}
	return dir, files
}

func TestSecretFilesAreOwnerOnlyAndReferencedNotInlined(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	dir, files := writeSecretStack(t, m, sec)

	for _, f := range files.SecretFiles {
		info, err := os.Stat(filepath.Join(dir, SecretsDirName, f.Key))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", f.Key, info.Mode().Perm())
		}
		raw, _ := os.ReadFile(filepath.Join(dir, SecretsDirName, f.Key))
		if string(raw) != sec.values[f.StoreName] || len(raw) < 32 {
			t.Fatalf("%s content does not match the store value", f.Key)
		}
	}
	dirInfo, _ := os.Stat(filepath.Join(dir, SecretsDirName))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("secrets dir mode = %v", dirInfo.Mode().Perm())
	}
	// Compose references files; values appear nowhere in the document.
	for _, want := range []string{"secrets:", "file: ./secrets/postgresql_db_password",
		"POSTGRES_PASSWORD_FILE: /run/secrets/postgresql_db_password",
		"AUTHENTIK_SECRET_KEY: file:///run/secrets/server_secret_key"} {
		if !strings.Contains(files.ComposeYAML, want) {
			t.Fatalf("compose missing %q:\n%s", want, files.ComposeYAML)
		}
	}
	for _, value := range sec.values {
		if strings.Contains(files.ComposeYAML, value) {
			t.Fatal("secret value leaked into compose yaml")
		}
	}
	// Fingerprints cover managed files only: no secret content recorded.
	prints, err := Fingerprints(dir)
	if err != nil {
		t.Fatal(err)
	}
	for path := range prints {
		if isSecretPath(path) {
			t.Fatalf("secret file %s fingerprinted", path)
		}
	}
}

func TestSecretOwnershipFollowsConsumer(t *testing.T) {
	m := authentikManifest(t)
	owners := map[string]int{}
	for _, f := range SecretFilesFor(m) {
		if f.UID == nil {
			t.Fatalf("%s has no owner", f.Key)
		}
		owners[f.Key] = *f.UID
	}
	if owners["postgresql_db_password"] != 70 || owners["server_db_password"] != 1000 {
		t.Fatalf("owners = %v", owners)
	}
}

func TestEnsureSecretsNeverOverwrites(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	name := SecretStoreName("authentik", "db_password")
	sec.values[name] = "existing-password"
	if err := EnsureSecrets(sec, m); err != nil {
		t.Fatal(err)
	}
	if sec.values[name] != "existing-password" {
		t.Fatal("existing secret overwritten")
	}
	if len(sec.values[SecretStoreName("authentik", "secret_key")]) != 64 {
		t.Fatal("secret_key not generated as 64 hex characters")
	}
}

func TestWriteSecretFilesMissingSecretNamesIt(t *testing.T) {
	m := authentikManifest(t)
	err := WriteSecretFiles(t.TempDir(), SecretFilesFor(m), newMemSecrets(t))
	if err == nil || !strings.Contains(err.Error(), "app_authentik_") {
		t.Fatalf("err = %v, want a message naming the store secret", err)
	}
}

func TestSecretValidation(t *testing.T) {
	base := func() Manifest { return authentikManifest(t) }
	cases := map[string]func(*Manifest){
		"unknown service":  func(m *Manifest) { m.Secrets[0].Consumers[0].Service = "nope" },
		"bad name":         func(m *Manifest) { m.Secrets[0].Name = "Bad-Name" },
		"no consumers":     func(m *Manifest) { m.Secrets[0].Consumers = nil },
		"unknown dbsecret": func(m *Manifest) { m.Database.PasswordSecret = "ghost" },
		"plain and secret": func(m *Manifest) {
			m.Services[0].Environment = map[string]string{"AUTHENTIK_SECRET_KEY": "x"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := base()
			m.Secrets = append([]SecretDecl(nil), m.Secrets...)
			for i := range m.Secrets {
				m.Secrets[i].Consumers = append([]SecretConsumer(nil), m.Secrets[i].Consumers...)
			}
			services := append([]Service(nil), m.Services...)
			m.Services = services
			db := *m.Database
			m.Database = &db
			mutate(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("want validation error")
			}
		})
	}
}

func TestConfigRefusesSecretBackedVariables(t *testing.T) {
	m := authentikManifest(t)
	for _, key := range []string{"POSTGRES_PASSWORD", "POSTGRES_PASSWORD_FILE"} {
		plan := ConfigPlan{Manifest: m, Changes: []ConfigChange{{Service: "postgresql", Set: map[string]string{key: "x"}}}}
		if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "secret-backed") {
			t.Fatalf("%s: err = %v", key, err)
		}
	}
	plan := ConfigPlan{Manifest: m, Rotate: []string{"secret_key"}}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be rotated") {
		t.Fatalf("immutable rotate err = %v", err)
	}
	if err := (ConfigPlan{Manifest: m, Rotate: []string{"ghost"}}).Validate(); err == nil {
		t.Fatal("unknown secret rotate accepted")
	}
	if err := (ConfigPlan{Manifest: m, Rotate: []string{"db_password"}}).Validate(); err != nil {
		t.Fatalf("db_password rotate: %v", err)
	}
}

type execCall struct {
	container, stdin string
	args             []string
}

func stubDB(t *testing.T, fail func(execCall) error) *[]execCall {
	t.Helper()
	var calls []execCall
	old := dbExec
	dbExec = func(_ context.Context, container, stdin string, args ...string) (string, error) {
		c := execCall{container, stdin, args}
		calls = append(calls, c)
		if fail != nil {
			if err := fail(c); err != nil {
				return "", err
			}
		}
		return "", nil
	}
	t.Cleanup(func() { dbExec = old })
	return &calls
}

func stubCompose(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	old := composeRun
	composeRun = func(_ context.Context, _, _ string, args ...string) error {
		calls = append(calls, args)
		return nil
	}
	t.Cleanup(func() { composeRun = old })
	return &calls
}

func TestRotateDatabasePassword(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	dir, _ := writeSecretStack(t, m, sec)
	name := SecretStoreName("authentik", "db_password")
	old := sec.values[name]
	db := stubDB(t, nil)
	cmp := stubCompose(t)

	def, err := ConfigureDefinition(ConfigPlan{Manifest: m, Rotate: []string{"db_password"}}, dir, "authentik", openDriftStore(t), sec)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range def.Steps {
		if step.Name == "up" {
			continue
		}
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", step.Name, err)
		}
	}
	fresh := sec.values[name]
	if fresh == old || len(fresh) != 64 {
		t.Fatal("secret was not rotated")
	}
	if len(*db) != 1 || !strings.Contains((*db)[0].stdin, "ALTER ROLE \"authentik\"") || !strings.Contains((*db)[0].stdin, fresh) {
		t.Fatalf("db calls = %+v", *db)
	}
	for _, a := range (*db)[0].args {
		if strings.Contains(a, fresh) {
			t.Fatal("password on the command line")
		}
	}
	for _, f := range SecretFilesFor(m) {
		if f.Name != "db_password" {
			continue
		}
		raw, _ := os.ReadFile(filepath.Join(dir, SecretsDirName, f.Key))
		if string(raw) != fresh {
			t.Fatalf("%s not refreshed", f.Key)
		}
	}
	if len(*cmp) != 1 || (*cmp)[0][0] != "restart" {
		t.Fatalf("compose calls = %v", *cmp)
	}
}

func TestRotateRestoresRoleWhenStoreWriteFails(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	dir, _ := writeSecretStack(t, m, sec)
	old := sec.values[SecretStoreName("authentik", "db_password")]
	db := stubDB(t, nil)
	stubCompose(t)
	failing := &failSet{memSecrets: sec}
	err := rotateSecrets(context.Background(), m, dir, "authentik", []string{"db_password"}, failing)
	if err == nil || len(*db) != 2 || !strings.Contains((*db)[1].stdin, old) {
		t.Fatalf("err=%v calls=%d: want ALTER then revert to the old password", err, len(*db))
	}
}

type failSet struct{ *memSecrets }

func (f *failSet) Set(string, string) error { return os.ErrPermission }

func TestRotateRefusedForUnwiredStack(t *testing.T) {
	m := authentikManifest(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ConfigureDefinition(ConfigPlan{Manifest: m, Rotate: []string{"db_password"}}, dir, "authentik", openDriftStore(t), newMemSecrets(t))
	if err == nil || !strings.Contains(err.Error(), "installed before S266") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigureOnInstalledStackRepairsSecretFiles(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	dir, files := writeSecretStack(t, m, sec)
	stubCompose(t)
	path := filepath.Join(dir, SecretsDirName, files.SecretFiles[0].Key)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	plan := ConfigPlan{Manifest: m, Changes: []ConfigChange{{Service: "server", Set: map[string]string{"AUTHENTIK_LOG_LEVEL": "info"}}}}
	def, err := ConfigureDefinition(plan, dir, "authentik", openDriftStore(t), sec)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range def.Steps {
		if step.Name == "up" {
			continue
		}
		if err := step.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file not repaired: %v", err)
	}
	override, _ := os.ReadFile(filepath.Join(dir, OverrideFileName))
	for _, value := range sec.values {
		if strings.Contains(string(override), value) {
			t.Fatal("secret value in override file")
		}
	}
}

func TestDriftTreatsSecretFilesAsOpaque(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	dir, files := writeSecretStack(t, m, sec)
	st := openDriftStore(t)
	if err := RecordFingerprints(st, "authentik", dir); err != nil {
		t.Fatal(err)
	}
	recorded, _ := StoredFingerprints(st, "authentik")
	if drifts, err := Detect(dir, recorded); err != nil || len(drifts) != 0 {
		t.Fatalf("clean stack drifts = %v, %v", drifts, err)
	}
	key := files.SecretFiles[0].Key
	path := filepath.Join(dir, SecretsDirName, key)
	// Content edits are invisible (opaque); mode changes are drift.
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if drifts, _ := Detect(dir, recorded); len(drifts) != 0 {
		t.Fatalf("content change reported: %v", drifts)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	drifts, _ := Detect(dir, recorded)
	if len(drifts) != 1 || drifts[0].Kind != DriftMode || drifts[0].Path != "secrets/"+key {
		t.Fatalf("drifts = %v", drifts)
	}
	review, err := Review(st, "authentik", dir)
	if err != nil || len(review) != 1 || review[0].Current != "" || review[0].Recorded != "" {
		t.Fatalf("review exposes content: %+v %v", review, err)
	}
	if restored, err := Restore(st, "authentik", dir); err != nil || len(restored) != 1 {
		t.Fatalf("restore = %v, %v", restored, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatal("restore did not fix the mode")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	drifts, _ = Detect(dir, recorded)
	if len(drifts) != 1 || drifts[0].Kind != DriftRemoved {
		t.Fatalf("removed drifts = %v", drifts)
	}
}

func TestRegisterSecretVerifiesDatabasePassword(t *testing.T) {
	m := authentikManifest(t)
	sec := newMemSecrets(t)
	rejected := stubDB(t, func(execCall) error { return os.ErrPermission })
	_, err := RegisterSecret(context.Background(), m, "db_password", "hunter2-existing", sec, false)
	if err == nil || len(sec.values) != 0 {
		t.Fatalf("rejected password stored: err=%v", err)
	}
	if strings.Contains(err.Error(), "hunter2-existing") {
		t.Fatal("error leaks the password")
	}
	if len(*rejected) != 1 || (*rejected)[0].stdin != "hunter2-existing\n" {
		t.Fatalf("verify must pass the password on stdin: %+v", *rejected)
	}
	for _, a := range (*rejected)[0].args {
		if strings.Contains(a, "hunter2") {
			t.Fatal("password in argv")
		}
	}
	stubDB(t, nil)
	store, err := RegisterSecret(context.Background(), m, "db_password", "hunter2-existing", sec, false)
	if err != nil || store != "app_authentik_db_password" || sec.values[store] != "hunter2-existing" {
		t.Fatalf("store=%q err=%v", store, err)
	}
	if _, err := RegisterSecret(context.Background(), m, "ghost", "x", sec, true); err == nil {
		t.Fatal("unknown secret registered")
	}
}

func TestImmichGeneratesDatabaseEnvironment(t *testing.T) {
	m, err := repoCatalog(t).Get("immich")
	if err != nil {
		t.Fatal(err)
	}
	files, err := Generate(m, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DB_HOSTNAME: database", "DB_USERNAME: immich", "DB_DATABASE_NAME: immich",
		"REDIS_HOSTNAME: cache", "DB_PASSWORD_FILE: /run/secrets/server_db_password",
		"POSTGRES_PASSWORD_FILE: /run/secrets/database_db_password"} {
		if !strings.Contains(files.ComposeYAML, want) {
			t.Fatalf("immich compose missing %q", want)
		}
	}
	if m.Database == nil || m.Database.PasswordSecret != "db_password" {
		t.Fatal("immich declares no database export")
	}
}

func TestInstallGeneratesSecretsAndLeaksNone(t *testing.T) {
	base := t.TempDir()
	registry := installRegistry(t, base)
	plan := installPlan(t, base)
	m := authentikManifest(t)
	files, err := Generate(m, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	plan.Package, plan.Compose = "authentik", files
	stackDir := t.TempDir()
	stubComposeUp(t, false)
	sec := newMemSecrets(t)

	if _, err := InstallDefinition(plan, registry, stackDir, openDriftStore(t), nil); err == nil {
		t.Fatal("install with secrets and no store accepted")
	}
	def, err := InstallDefinition(plan, registry, stackDir, openDriftStore(t), sec)
	if err != nil {
		t.Fatal(err)
	}
	runner := openInstallRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatal(err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("done = %+v, %v", done, err)
	}
	if len(sec.values) != 2 {
		t.Fatalf("generated %d secrets, want 2", len(sec.values))
	}
	record := fmt.Sprintf("%+v", done)
	for name, value := range sec.values {
		if strings.Contains(record, value) {
			t.Fatalf("operation record leaks %s", name)
		}
		if info, err := os.Stat(filepath.Join(stackDir, SecretsDirName)); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("secrets dir: %v", err)
		}
	}
}
