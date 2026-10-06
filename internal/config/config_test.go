package config

import (
	"os"
	"path/filepath"
	"testing"

	"homestead/internal/desired"
	"homestead/internal/schema"
)

func exampleService(t *testing.T) *Service {
	t.Helper()
	s := schema.Schema{Fields: []schema.Field{
		{
			Name:        "hostname",
			Type:        schema.TypeString,
			Description: "Local friendly name.",
			Required:    true,
		},
		{
			Name:        "web_port",
			Type:        schema.TypeInteger,
			Description: "Dashboard port.",
			Default:     8080,
			Min:         schema.Int(1),
			Max:         schema.Int(65535),
		},
	}}
	if err := s.ValidateDefinition(); err != nil {
		t.Fatalf("ValidateDefinition: %v", err)
	}
	return New(s, filepath.Join(t.TempDir(), desired.FileName))
}

func TestGetFreshReturnsEmptyCurrentState(t *testing.T) {
	svc := exampleService(t)
	got, err := svc.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != desired.CurrentVersion {
		t.Fatalf("Version = %d, want %d", got.Version, desired.CurrentVersion)
	}
	if len(got.Settings) != 0 {
		t.Fatalf("Settings = %v, want empty", got.Settings)
	}
}

func TestPlanMergesDefaultsWithoutPersisting(t *testing.T) {
	svc := exampleService(t)
	plan := svc.Plan(map[string]any{"hostname": "homestead"})
	if !plan.Valid {
		t.Fatalf("Plan errors = %v, want none", plan.Errors)
	}
	if plan.Effective["web_port"] != 8080 {
		t.Fatalf("Effective = %v, want default web_port 8080", plan.Effective)
	}
	got, err := svc.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Settings) != 0 {
		t.Fatalf("Plan persisted %v, want untouched store", got.Settings)
	}
}

func TestPlanReportsInvalid(t *testing.T) {
	svc := exampleService(t)
	plan := svc.Plan(map[string]any{"web_port": 70000})
	if plan.Valid {
		t.Fatal("Plan valid, want errors")
	}
	if len(plan.Errors) != 2 { // web_port bound + missing hostname
		t.Fatalf("Plan errors = %v, want 2", plan.Errors)
	}
}

func TestApplyPersistsEffective(t *testing.T) {
	svc := exampleService(t)
	stored, errs, err := svc.Apply(map[string]any{"hostname": "homestead"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("Apply errors = %v, want none", errs)
	}
	if stored.Settings["web_port"] != 8080 {
		t.Fatalf("stored = %v, want default web_port persisted", stored.Settings)
	}
	got, err := svc.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Settings["hostname"] != "homestead" {
		t.Fatalf("Get after Apply = %v, want hostname", got.Settings)
	}
}

func TestApplyInvalidTouchesNothing(t *testing.T) {
	svc := exampleService(t)
	if _, errs, err := svc.Apply(map[string]any{"web_port": "lots"}); err != nil || len(errs) == 0 {
		t.Fatalf("Apply = errs %v, err %v; want validation errors", errs, err)
	}
	got, err := svc.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Settings) != 0 {
		t.Fatalf("invalid Apply persisted %v", got.Settings)
	}
}

func TestGetSurfacesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, desired.FileName)
	if err := os.WriteFile(path, []byte("version: [unclosed\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	svc := New(schema.Schema{}, path)
	if _, err := svc.Get(); err == nil {
		t.Fatal("Get of corrupt file succeeded, want error")
	}
}
