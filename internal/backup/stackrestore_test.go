package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStackRestoreKeepsLiveSecrets(t *testing.T) {
	for _, withSecrets := range []bool{false, true} {
		stacks, dest := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(stacks, "photos", "compose.yaml"), "services: {}\n")
		writeFile(t, filepath.Join(stacks, "photos", "secrets", "db"), "backed-up-secret")
		sources := []Source{{Mountpoint: stacks, Path: "photos", Class: StackClass, Exclude: []string{"secrets"}}}
		if withSecrets {
			sources = append(sources, Source{Mountpoint: stacks, Path: "photos/secrets", Class: SecretsClass, Sensitive: true})
		}
		keys := newKeys()
		recipients, _ := Recipients(keys)
		ids, _ := Identities(keys)
		def, err := Definition(Request{Package: "photos", Destination: dest, Recipients: recipients, Sources: sources}, fixedJobTime)
		if err != nil {
			t.Fatal(err)
		}
		runDef(t, def)

		// Live state diverges: new secret contents, a stale file, a new secret.
		writeFile(t, filepath.Join(stacks, "photos", "compose.yaml"), "changed\n")
		writeFile(t, filepath.Join(stacks, "photos", "stale.yaml"), "stale")
		writeFile(t, filepath.Join(stacks, "photos", "secrets", "db"), "live-secret")
		writeFile(t, filepath.Join(stacks, "photos", "secrets", "extra"), "live-extra")

		runs, _ := ListRuns(dest, "photos")
		plan, err := PlanRestore(runs[0], nil)
		if err != nil {
			t.Fatal(err)
		}
		restore, err := RestoreDefinition(plan, ids...)
		if err != nil {
			t.Fatal(err)
		}
		runDef(t, restore)

		stack := filepath.Join(stacks, "photos")
		if readFile(t, filepath.Join(stack, "compose.yaml")) != "services: {}\n" {
			t.Fatal("compose.yaml not restored")
		}
		if _, err := os.Stat(filepath.Join(stack, "stale.yaml")); !os.IsNotExist(err) {
			t.Fatal("stale stack file survived")
		}
		wantDB, wantExtra := "live-secret", "live-extra"
		if withSecrets {
			// The secrets entry runs after and replaces the tree.
			wantDB = "backed-up-secret"
		}
		if readFile(t, filepath.Join(stack, "secrets", "db")) != wantDB {
			t.Fatalf("secrets/db wrong (withSecrets=%v)", withSecrets)
		}
		_, err = os.Stat(filepath.Join(stack, "secrets", "extra"))
		if withSecrets == (err == nil) {
			t.Fatalf("secrets/extra presence wrong (withSecrets=%v), want %q", withSecrets, wantExtra)
		}
		if entries, _ := os.ReadDir(stacks); len(entries) != 1 {
			t.Fatalf("staging left: %v", entries)
		}
	}
}
