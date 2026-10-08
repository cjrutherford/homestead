package catalog

import (
	"net/netip"
	"strings"
	"testing"

	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/storage"
)

func installManifest() Manifest {
	return validManifest()
}

func installRequest() InstallRequest {
	return InstallRequest{
		Manifest:       installManifest(),
		Host:           installHost(),
		Domain:         "lan",
		ContainerPorts: map[string]int{"jellyfin": 8096},
		Storage:        map[storage.Class]string{storage.ClassApplication: "/mnt/data", storage.ClassBulk: "/mnt/media"},
		Volumes: []VolumeSpec{
			{Service: "jellyfin", Class: storage.ClassApplication, Subpath: "config", Target: "/config"},
			{Service: "jellyfin", Class: storage.ClassBulk, Subpath: "media", Target: "/media", ReadOnly: true},
		},
		Networks: []NetworkAttachment{{Name: "homestead-edge", External: true}},
	}
}

func installHost() HostFacts {
	return HostFacts{
		Arch: "amd64", MemoryMB: 4096, CPUCount: 4,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 10 << 30,
			storage.ClassBulk:        500 << 30,
		},
	}
}

func TestPlanInstall(t *testing.T) {
	plan, err := PlanInstall(installRequest())
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if plan.Package != "jellyfin" || !plan.Requirements.Passed {
		t.Fatalf("plan = %+v", plan.Package)
	}
	if len(plan.Routes) != 1 || plan.Routes[0].Host != "jellyfin.lan" ||
		plan.Routes[0].Target.Port != 8096 {
		t.Fatalf("routes = %+v", plan.Routes)
	}
	if !strings.Contains(plan.Compose.ComposeYAML, "jellyfin/jellyfin:10.10.3") ||
		!strings.Contains(plan.Compose.ComposeYAML, "/mnt/media/jellyfin/media") {
		t.Fatalf("compose = %s", plan.Compose.ComposeYAML)
	}
	if len(plan.DNS) != 0 {
		t.Fatalf("dns = %+v", plan.DNS)
	}
	if len(plan.TLS) != 1 || plan.TLS[0].Issuer != cert.IssuerInternal {
		t.Fatalf("tls = %+v", plan.TLS)
	}
	if len(plan.Firewall) != 2 || string(plan.Firewall[0].Sources[0].Zone) != "local" {
		t.Fatalf("firewall = %+v", plan.Firewall)
	}
	if len(plan.Directories) != 2 || plan.Directories[0].Mountpoint != "/mnt/data" ||
		plan.Directories[1].Directory.Path != "jellyfin/media" {
		t.Fatalf("dirs = %+v", plan.Directories)
	}
	if len(plan.Actions) != 2 || plan.Actions[0] != "provision" || plan.Actions[1] != "up" {
		t.Fatalf("actions = %v", plan.Actions)
	}
}

func TestPlanInstallPublic(t *testing.T) {
	req := installRequest()
	req.Hostnames = map[string]string{"web": "jellyfin.example.com"}
	manifest := installManifest()
	manifest.Exposure.Endpoints[0].Recommended = docker.ExposurePublic
	manifest.Exposure.Endpoints[0].AllowWider = true
	req.Manifest = manifest
	req.Zone = "example.com"
	req.PublicIP = netip.MustParseAddr("203.0.113.7")
	plan, err := PlanInstall(req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if len(plan.DNS) != 1 || plan.DNS[0].Name != "jellyfin" || plan.DNS[0].Value != "203.0.113.7" {
		t.Fatalf("dns = %+v", plan.DNS)
	}
	if len(plan.TLS) != 1 || plan.TLS[0].Issuer != cert.IssuerACME {
		t.Fatalf("tls = %+v", plan.TLS)
	}
	if string(plan.Firewall[0].Sources[0].Zone) != "public" {
		t.Fatalf("firewall = %+v", plan.Firewall)
	}
	// Public routes without DNS inputs refuse.
	bare := req
	bare.Zone = ""
	bare.PublicIP = netip.Addr{}
	if _, err := PlanInstall(bare); err == nil ||
		!strings.Contains(err.Error(), "zone and public address") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanInstallRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*InstallRequest)
		want   string
	}{
		{"bad manifest", func(r *InstallRequest) { r.Manifest.Name = "Bad!" }, "package must match"},
		{"no domain", func(r *InstallRequest) { r.Domain = "" }, "domain must not be empty"},
		{"unmet requirements", func(r *InstallRequest) { r.Host.MemoryMB = 1 }, "requirements unmet"},
		{"no container port", func(r *InstallRequest) { r.ContainerPorts = nil }, "no container port"},
		{"bad hostname", func(r *InstallRequest) {
			r.Hostnames = map[string]string{"web": "Bad!"}
		}, "route"},
		{"missing class mount", func(r *InstallRequest) { delete(r.Storage, storage.ClassBulk) }, "no mountpoint"},
		{"bad subpath", func(r *InstallRequest) { r.Volumes[0].Subpath = "../x" }, "contained"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := installRequest()
			tc.mutate(&req)
			_, err := PlanInstall(req)
			if err == nil {
				t.Fatal("planned invalid install")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}
