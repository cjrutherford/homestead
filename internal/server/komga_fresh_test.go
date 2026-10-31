package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
)

func TestFreshKomgaDataStepRefusesReusedConfig(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "komga", "config")
	plan := catalog.InstallPlan{Package: "komga", Mounts: []catalog.PlannedMount{{Service: "komga", Target: "/config", Source: config}}}
	step := freshKomgaDataStep(plan)
	if err := step.Run(context.Background()); err != nil {
		t.Fatalf("new config: %v", err)
	}
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := step.Run(context.Background()); err != nil {
		t.Fatalf("empty config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(config, "database.sqlite"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := step.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "already contains data") {
		t.Fatalf("reused config accepted: %v", err)
	}
	plan.Mounts = nil
	if err := freshKomgaDataStep(plan).Run(context.Background()); err == nil {
		t.Fatal("missing config mount accepted")
	}
}
