package catalog

import (
	"strings"
	"testing"

	"homestead/internal/storage"
)

func requireManifest() Manifest {
	manifest := validManifest()
	manifest.Requirements.Architectures = []string{"amd64", "arm64"}
	manifest.Requirements.CPUCount = 2
	manifest.Requirements.NeedsGPU = true
	return manifest
}

func requireHost() HostFacts {
	return HostFacts{
		Arch: "amd64", MemoryMB: 4096, CPUCount: 4,
		GPUs:      []string{"nvidia-t400"},
		FreeSpace: map[storage.Class]uint64{storage.ClassApplication: 10 << 30, storage.ClassBulk: 200 << 30},
		UsedPorts: map[int]bool{443: true},
	}
}

func TestEvaluatePass(t *testing.T) {
	report, err := Evaluate(requireManifest(), requireHost())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed || report.Package != "jellyfin" {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Checks) != 7 {
		t.Fatalf("checks = %+v", report.Checks)
	}
	for _, check := range report.Checks {
		if !check.OK {
			t.Fatalf("check = %+v", check)
		}
	}
}

func TestEvaluateFailures(t *testing.T) {
	starved := requireHost()
	starved.MemoryMB = 512
	starved.CPUCount = 1
	starved.GPUs = nil
	starved.FreeSpace = map[storage.Class]uint64{storage.ClassApplication: 1}
	starved.UsedPorts = map[int]bool{8096: true}
	starved.Arch = "riscv64"
	report, err := Evaluate(requireManifest(), starved)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if report.Passed {
		t.Fatalf("report = %+v", report)
	}
	byName := map[string]RequirementCheck{}
	for _, check := range report.Checks {
		byName[check.Name] = check
	}
	for name, want := range map[string]string{
		"architecture":        "not a supported platform",
		"cpu":                 "1 cores for 2 required",
		"memory":              "512 MB for 1024",
		"gpu":                 "lacks",
		"storage:bulk":        "no space data",
		"storage:application": "1 bytes for",
		"port:8096":           "already bound",
	} {
		got, ok := byName[name]
		if !ok || got.OK || !strings.Contains(got.Detail, want) {
			t.Fatalf("%s = %+v, want %q", name, got, want)
		}
	}
	if _, err := Evaluate(Manifest{}, requireHost()); err == nil {
		t.Fatal("evaluated invalid manifest")
	}
}

func TestEvaluateArchMismatch(t *testing.T) {
	manifest := requireManifest()
	host := requireHost()
	host.Arch = "arm64"
	report, err := Evaluate(manifest, host)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
	host.Arch = "amd64"
	manifest.Requirements.Architectures = []string{"arm64"}
	report, err = Evaluate(manifest, host)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

func TestRequirementFields(t *testing.T) {
	manifest := validManifest()
	manifest.Requirements.Architectures = []string{"amd64", "amd64"}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate architecture") {
		t.Fatalf("err = %v", err)
	}
	manifest = validManifest()
	manifest.Requirements.Architectures = []string{"riscv64"}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "amd64 or arm64") {
		t.Fatalf("err = %v", err)
	}
	manifest = validManifest()
	manifest.Requirements.CPUCount = -1
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("err = %v", err)
	}
	// Zero-valued floors keep older manifests valid.
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("backward compat: %v", err)
	}
}
