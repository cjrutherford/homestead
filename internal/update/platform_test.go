package update

import (
	"strings"
	"testing"
)

func TestIsPlatformComponent(t *testing.T) {
	for _, name := range []string{ComponentDaemon, ComponentCLI, ComponentDeb} {
		if !IsPlatformComponent(name) {
			t.Fatalf("IsPlatformComponent(%q) = false", name)
		}
	}
	for _, name := range []string{"", "dashboard", "jellyfin", "demo"} {
		if IsPlatformComponent(name) {
			t.Fatalf("IsPlatformComponent(%q) = true", name)
		}
	}
}

func TestPlatformReleaseValidate(t *testing.T) {
	if err := (PlatformRelease{Component: ComponentDaemon, Version: "1.2.3"}).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, release := range []PlatformRelease{
		{Component: "dashboard", Version: "1.2.3"},
		{Component: "jellyfin", Version: "1.2.3"},
		{Component: ComponentDaemon, Version: ""},
		{Component: ComponentDaemon, Version: "latest"},
		{Component: "", Version: "1.2.3"},
	} {
		if err := release.Validate(); err == nil {
			t.Fatalf("Validate(%+v) = nil", release)
		}
	}
}

func TestPlanPlatform(t *testing.T) {
	running := map[string]string{ComponentDaemon: "1.2.3", ComponentCLI: "1.2.3"}
	plan, err := PlanPlatform(running, []PlatformRelease{
		{Component: ComponentCLI, Version: "1.3.0"},
		{Component: ComponentDaemon, Version: "2.0.0"},
	})
	if err != nil {
		t.Fatalf("PlanPlatform: %v", err)
	}
	if len(plan.Components) != 2 {
		t.Fatalf("Components = %+v", plan.Components)
	}
	if plan.Components[0].Component != ComponentDaemon || plan.Components[1].Component != ComponentCLI {
		t.Fatalf("unordered Components = %+v", plan.Components)
	}
	if !plan.Backup || plan.BackupWhy == "" {
		t.Fatalf("Backup = %v (%q)", plan.Backup, plan.BackupWhy)
	}
	joined := strings.Join(plan.Warnings, "\n")
	if !strings.Contains(joined, "jumps major versions") {
		t.Fatalf("Warnings = %q", joined)
	}
	if !strings.Contains(joined, "daemon restarts") {
		t.Fatalf("Warnings = %q", joined)
	}
}

func TestPlanPlatformCurrentPlansEmpty(t *testing.T) {
	running := map[string]string{ComponentDaemon: "1.2.3"}
	plan, err := PlanPlatform(running, []PlatformRelease{
		{Component: ComponentDaemon, Version: "1.2.3"},
	})
	if err != nil {
		t.Fatalf("PlanPlatform: %v", err)
	}
	if len(plan.Components) != 0 || plan.Backup || len(plan.Warnings) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanPlatformRebuildMoves(t *testing.T) {
	running := map[string]string{ComponentDaemon: "0.0.0-s002"}
	plan, err := PlanPlatform(running, []PlatformRelease{
		{Component: ComponentDaemon, Version: "0.0.0-s003"},
	})
	if err != nil {
		t.Fatalf("PlanPlatform: %v", err)
	}
	if len(plan.Components) != 1 {
		t.Fatalf("Components = %+v", plan.Components)
	}
}

func TestPlanPlatformRefusals(t *testing.T) {
	running := map[string]string{ComponentDaemon: "1.2.3", ComponentCLI: "1.2.3"}
	cases := []struct {
		name    string
		running map[string]string
		desired []PlatformRelease
	}{
		{"unknown component", running, []PlatformRelease{{Component: "dashboard", Version: "1.2.4"}}},
		{"catalog name", running, []PlatformRelease{{Component: "jellyfin", Version: "1.2.4"}}},
		{"unparseable desired", running, []PlatformRelease{{Component: ComponentDaemon, Version: "latest"}}},
		{"downgrade", running, []PlatformRelease{{Component: ComponentDaemon, Version: "1.2.2"}}},
		{"duplicate", running, []PlatformRelease{{Component: ComponentDaemon, Version: "1.2.4"}, {Component: ComponentDaemon, Version: "1.2.5"}}},
		{"missing running", map[string]string{ComponentCLI: "1.2.3"}, []PlatformRelease{{Component: ComponentDaemon, Version: "1.2.4"}}},
		{"unparseable running", map[string]string{ComponentDaemon: "soon"}, []PlatformRelease{{Component: ComponentDaemon, Version: "1.2.4"}}},
	}
	for _, tc := range cases {
		if _, err := PlanPlatform(tc.running, tc.desired); err == nil {
			t.Fatalf("%s: PlanPlatform = nil error", tc.name)
		}
	}
}
