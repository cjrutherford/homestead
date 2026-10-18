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
