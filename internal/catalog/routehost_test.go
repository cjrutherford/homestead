package catalog

import (
	"strings"
	"testing"

	"homestead/internal/storage"
)

func TestNextcloudTrustsItsRouteHostname(t *testing.T) {
	manifest, err := repoCatalog(t).Get("nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	req := InstallRequest{
		Manifest: manifest, Host: installHost(), Domain: "lan",
		ContainerPorts: map[string]int{"nextcloud": 80},
		Storage:        map[storage.Class]string{storage.ClassApplication: "/mnt/data", storage.ClassBulk: "/mnt/bulk"},
	}
	plan, err := PlanInstall(req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if !strings.Contains(plan.Compose.ComposeYAML, "NEXTCLOUD_TRUSTED_DOMAINS: nextcloud.lan") {
		t.Fatalf("default host not trusted:\n%s", plan.Compose.ComposeYAML)
	}
	req.Hostnames = map[string]string{"web": "files.example.home"}
	plan, err = PlanInstall(req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if !strings.Contains(plan.Compose.ComposeYAML, "NEXTCLOUD_TRUSTED_DOMAINS: files.example.home") ||
		strings.Contains(plan.Compose.ComposeYAML, "{{") {
		t.Fatalf("owner host not trusted:\n%s", plan.Compose.ComposeYAML)
	}
}

func TestRouteHostTokenWithoutRouteIsLeftOut(t *testing.T) {
	manifest, err := repoCatalog(t).Get("nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	files, err := Generate(manifest, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(files.ComposeYAML, "NEXTCLOUD_TRUSTED_DOMAINS") || strings.Contains(files.ComposeYAML, "{{") {
		t.Fatalf("unrouted render carries the variable:\n%s", files.ComposeYAML)
	}
}

func TestRouteHostTokenNeedsAnEndpoint(t *testing.T) {
	svc := Service{Name: "db", Image: "postgres:16", Environment: map[string]string{"X": RouteHostToken}}
	if err := svc.Validate(map[string]bool{}); err == nil || !strings.Contains(err.Error(), "exposes no endpoint") {
		t.Fatalf("err = %v", err)
	}
}

// The edge proxy holds ports 80 and 443 on the host; Nextcloud publishes
// no host port, so it must still pass its requirements.
func TestNextcloudRequirementsPassWithTheEdgeOnPorts80And443(t *testing.T) {
	manifest, err := repoCatalog(t).Get("nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Requirements.Ports) != 0 {
		t.Fatalf("nextcloud declares host ports: %v", manifest.Requirements.Ports)
	}
	host := installHost()
	host.UsedPorts = map[int]bool{80: true, 443: true}
	report, err := Evaluate(manifest, host)
	if err != nil || !report.Passed {
		t.Fatalf("requirements with 80 and 443 bound = %+v, %v", report, err)
	}
}
