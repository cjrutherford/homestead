package update

import (
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/exposure"
)

func planManifest() catalog.Manifest {
	return catalog.Manifest{
		Name: "photos", Version: "1.0.0", Title: "Photos", Blurb: "Photos.",
		Description: "Photos.", Category: "media", Icon: "mdi:image",
		Services: []catalog.Service{
			{Name: "web", Image: "example/web:2.1.0"},
			{Name: "worker", Image: "example/worker:1.0.0"},
		},
		Requirements: catalog.Requirements{MemoryMB: 64},
		Exposure: exposure.PackageExposure{Package: "photos", Endpoints: []exposure.EndpointExposure{
			{Endpoint: "web", Recommended: "local"},
		}},
		BackupPaths: []string{"config"},
	}
}

func TestPlanUpdate(t *testing.T) {
	running := map[string]string{
		"web":    "example/web:1.9.0",
		"worker": "example/worker:1.0.0",
	}
	registry := map[string][]string{
		"web": {"2.2.0", "2.1.0"},
	}
	plan, err := PlanUpdate(planManifest(), running, registry)
	if err != nil {
		t.Fatalf("PlanUpdate: %v", err)
	}
	if len(plan.Services) != 1 {
		t.Fatalf("services = %+v", plan.Services)
	}
	move := plan.Services[0]
	if move.Service != "web" || move.Current != "example/web:1.9.0" ||
		move.Target != "example/web:2.2.0" || move.Source != SourceRegistry {
		t.Fatalf("move = %+v", move)
	}
	if !plan.Backup || !strings.Contains(plan.BackupWhy, "backup paths") {
		t.Fatalf("backup = %v %q", plan.Backup, plan.BackupWhy)
	}
	if len(plan.Affected) != 1 || plan.Affected[0] != "web" {
		t.Fatalf("affected = %v", plan.Affected)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "1.9.0 -> 2.2.0") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
}

func TestPlanUpdateManifestPin(t *testing.T) {
	// Silent registries fall back to manifest pins.
	plan, err := PlanUpdate(planManifest(),
		map[string]string{"web": "example/web:1.0.0", "worker": "example/worker:1.0.0"}, nil)
	if err != nil {
		t.Fatalf("PlanUpdate: %v", err)
	}
	if len(plan.Services) != 1 || plan.Services[0].Target != "example/web:2.1.0" ||
		plan.Services[0].Source != SourceManifest {
		t.Fatalf("services = %+v", plan.Services)
	}
	// Current everything plans cleanly empty.
	plan, err = PlanUpdate(planManifest(),
		map[string]string{"web": "example/web:2.1.0", "worker": "example/worker:1.0.0"}, nil)
	if err != nil {
		t.Fatalf("PlanUpdate: %v", err)
	}
	if len(plan.Services) != 0 || len(plan.Affected) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if !plan.Backup {
		t.Fatal("backup flag dropped on empty plan")
	}
}

func TestPlanUpdateMajorWarning(t *testing.T) {
	manifest := planManifest()
	manifest.Database = &catalog.DatabaseBackup{Service: "worker", Kind: catalog.DatabaseSQLite, Path: "db.sqlite"}
	plan, err := PlanUpdate(manifest,
		map[string]string{"web": "example/web:1.9.0", "worker": "example/worker:1.0.0"}, nil)
	if err != nil {
		t.Fatalf("PlanUpdate: %v", err)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "major versions") ||
		!strings.Contains(plan.Warnings[0], "1.9.0 -> 2.1.0") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
	if !strings.Contains(plan.BackupWhy, "database") {
		t.Fatalf("backup why = %q", plan.BackupWhy)
	}
	if _, err := PlanUpdate(catalog.Manifest{}, nil, nil); err == nil {
		t.Fatal("planned invalid manifest")
	}
}
