package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
)

func configPlan() ConfigPlan {
	return ConfigPlan{
		Manifest: validManifest(),
		Changes: []ConfigChange{
			{Service: "jellyfin", Set: map[string]string{"TZ": "UTC", "EMPTY": ""}, Unset: []string{"OLD"}},
		},
	}
}

func TestConfigPlanValidation(t *testing.T) {
	if err := configPlan().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	empty := ConfigPlan{Manifest: validManifest()}
	if err := empty.Validate(); err == nil {
		t.Fatal("validated empty plan")
	}
	bad := configPlan()
	bad.Manifest.Name = "Bad!"
	if err := bad.Validate(); err == nil {
		t.Fatal("validated bad manifest")
	}
	ghost := configPlan()
	ghost.Changes[0].Service = "ghost"
	if err := ghost.Validate(); err == nil {
		t.Fatal("validated ghost service")
	}
	clash := configPlan()
	clash.Changes[0].Unset = []string{"TZ"}
	if err := clash.Validate(); err == nil {
		t.Fatal("validated set/unset clash")
	}
	dup := configPlan()
	dup.Changes = append(dup.Changes, dup.Changes[0])
	if err := dup.Validate(); err == nil {
		t.Fatal("validated duplicate service")
	}
	key := configPlan()
	key.Changes[0].Set = map[string]string{"bad-key": "x"}
	if err := key.Validate(); err == nil {
		t.Fatal("validated bad key")
	}
}

func TestApplyConfig(t *testing.T) {
	dir := t.TempDir()
	if err := ApplyConfig(dir, configPlan()); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, OverrideFileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{"jellyfin:", "TZ: UTC", `EMPTY: ""`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %q:\n%s", want, raw)
		}
	}
	info, err := os.Stat(filepath.Join(dir, OverrideFileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	// Merge keeps foreign services and keys, applies deltas.
	second := configPlan()
	second.Changes = []ConfigChange{
		{Service: "jellyfin", Set: map[string]string{"TZ": "Europe/Berlin"}, Unset: []string{"EMPTY"}},
	}
	if err := ApplyConfig(dir, second); err != nil {
		t.Fatalf("merge: %v", err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, OverrideFileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "Europe/Berlin") || strings.Contains(string(raw), "EMPTY") {
		t.Fatalf("merge wrong:\n%s", raw)
	}
	// Removing the last variable deletes the file.
	last := configPlan()
	last.Changes = []ConfigChange{{Service: "jellyfin", Unset: []string{"TZ"}}}
	if err := ApplyConfig(dir, last); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, OverrideFileName)); !os.IsNotExist(err) {
		t.Fatal("file survived emptying")
	}
	if err := ApplyConfig(dir, ConfigPlan{}); err == nil {
		t.Fatal("applied invalid plan")
	}
}

func TestConfigureDefinition(t *testing.T) {
	dir := t.TempDir()
	calls := stubComposeUp(t, false)
	def, err := ConfigureDefinition(configPlan(), dir, "jellyfin", openDriftStore(t), nil)
	if err != nil {
		t.Fatalf("ConfigureDefinition: %v", err)
	}
	if def.Type != "catalog.configure" || len(def.Steps) != 2 ||
		def.Steps[0].Name != "write-config" || def.Steps[1].Name != "up" {
		t.Fatalf("def = %+v", def)
	}
	runner := openInstallRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	if _, err := os.Stat(filepath.Join(dir, OverrideFileName)); err != nil {
		t.Fatalf("override missing: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %v", *calls)
	}
	if _, err := ConfigureDefinition(ConfigPlan{}, dir, "x", openDriftStore(t), nil); err == nil {
		t.Fatal("planned empty")
	}
	if _, err := ConfigureDefinition(configPlan(), "", "x", openDriftStore(t), nil); err == nil {
		t.Fatal("planned without dir")
	}
}

func TestApplySignInWiringAddsAndRemovesOnlyItsEntries(t *testing.T) {
	dir := t.TempDir()
	if err := ApplyConfig(dir, configPlan()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, OverrideFileName))
	w := SignInWiring{Service: "jellyfin", ExtraHosts: []string{"authentik.lan:host-gateway"},
		Volumes: []string{"./homestead-ca:/etc/homestead/ca:ro"}, Env: map[string]string{"SSL_CERT_DIR": "/etc/ssl/certs:/etc/homestead/ca"}}
	for i := 0; i < 2; i++ {
		if _, err := ApplySignInWiring(dir, w, true); err != nil {
			t.Fatal(err)
		}
	}
	on, _ := os.ReadFile(filepath.Join(dir, OverrideFileName))
	for _, want := range []string{"TZ: UTC", "authentik.lan:host-gateway", "homestead-ca:/etc/homestead/ca:ro", "SSL_CERT_DIR"} {
		if strings.Count(string(on), want) != 1 {
			t.Fatalf("on: %q not once in\n%s", want, on)
		}
	}
	if _, err := ApplySignInWiring(dir, w, false); err != nil {
		t.Fatal(err)
	}
	off, _ := os.ReadFile(filepath.Join(dir, OverrideFileName))
	if string(off) != string(before) {
		t.Fatalf("off:\n%s\nwant:\n%s", off, before)
	}
	if _, err := ApplySignInWiring(dir, SignInWiring{Service: "Bad!"}, true); err == nil {
		t.Fatal("bad service accepted")
	}
}

func TestApplySignInWiringKeepsAndReplacesOwnerValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, OverrideFileName)
	seed := "services:\n  app:\n    environment:\n      A: owner\n      B: owner\n      TZ: UTC\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	w := SignInWiring{Service: "app", Env: map[string]string{"A": "old", "B": "owner"}}
	// On: A differs and is replaced (reported); B already equals.
	notes, err := ApplySignInWiring(dir, w, true)
	if err != nil || len(notes.Replaced) != 1 || notes.Replaced[0] != "A" {
		t.Fatalf("on: %v %+v", err, notes)
	}
	// The owner changes A again; off keeps it, removes B (still sign-in's value).
	raw, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "A: old", "A: mine", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	notes, err = ApplySignInWiring(dir, w, false)
	if err != nil || len(notes.Kept) != 1 || notes.Kept[0] != "A" {
		t.Fatalf("off: %v %+v", err, notes)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "A: mine") || strings.Contains(string(raw), "B:") || !strings.Contains(string(raw), "TZ: UTC") {
		t.Fatalf("override:\n%s", raw)
	}
}
