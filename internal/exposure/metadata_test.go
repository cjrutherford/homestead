package exposure

import (
	"strings"
	"testing"

	"homestead/internal/docker"
)

func metadataPackage() PackageExposure {
	return PackageExposure{
		Package: "jellyfin",
		Endpoints: []EndpointExposure{
			{
				Endpoint:    "web",
				Recommended: docker.ExposureLocal,
				AllowWider:  true,
				Warnings:    []string{"Media libraries leave the LAN when widened."},
			},
			{
				Endpoint:    "admin",
				Recommended: docker.ExposureLocal,
				Warnings:    []string{"Admin surface carries full control; keep it local."},
			},
		},
	}
}

func TestPackageExposureValid(t *testing.T) {
	declared := metadataPackage()
	if err := declared.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	endpoint, ok := declared.Find("web")
	if !ok {
		t.Fatal("Find missed web")
	}
	if !endpoint.Allows(docker.ExposureLocal) || !endpoint.Allows(docker.ExposurePublic) {
		t.Fatalf("web allows wrong: %+v", endpoint)
	}
	admin, ok := declared.Find("admin")
	if !ok {
		t.Fatal("Find missed admin")
	}
	if !admin.Allows(docker.ExposureLocal) || admin.Allows(docker.ExposureOverlay) || admin.Allows(docker.ExposurePublic) {
		t.Fatalf("admin allows wrong: %+v", admin)
	}
	if _, ok := declared.Find("missing"); ok {
		t.Fatal("found missing endpoint")
	}
	overlay := endpoint
	overlay.Recommended = docker.ExposureOverlay
	if !overlay.Allows(docker.ExposureLocal) || !overlay.Allows(docker.ExposureOverlay) {
		t.Fatal("overlay recommendation misranks")
	}
}

func TestPackageExposureInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PackageExposure)
		want   string
	}{
		{"bad package", func(p *PackageExposure) { p.Package = "Bad!" }, "package must match"},
		{"no endpoints", func(p *PackageExposure) { p.Endpoints = nil }, "no endpoints"},
		{"dup endpoints", func(p *PackageExposure) {
			p.Endpoints = append(p.Endpoints, p.Endpoints[0])
		}, "duplicate endpoint"},
		{"bad endpoint", func(p *PackageExposure) { p.Endpoints[0].Endpoint = "Bad!" }, "invalid endpoint"},
		{"bad exposure", func(p *PackageExposure) { p.Endpoints[0].Recommended = "dmz" }, "local, overlay, or public"},
		{"empty warning", func(p *PackageExposure) { p.Endpoints[0].Warnings = []string{""} }, "warning #0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			declared := metadataPackage()
			tc.mutate(&declared)
			err := declared.Validate()
			if err == nil {
				t.Fatal("accepted invalid declaration")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
	if (EndpointExposure{Endpoint: "x", Recommended: "dmz"}).Allows("dmz") {
		t.Fatal("allowed unknown exposure")
	}
}
