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
	def, err := ConfigureDefinition(configPlan(), dir, "jellyfin", openDriftStore(t))
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
	if _, err := ConfigureDefinition(ConfigPlan{}, dir, "x", openDriftStore(t)); err == nil {
		t.Fatal("planned empty")
	}
	if _, err := ConfigureDefinition(configPlan(), "", "x", openDriftStore(t)); err == nil {
		t.Fatal("planned without dir")
	}
}
