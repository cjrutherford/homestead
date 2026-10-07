package docker

import (
	"strings"
	"testing"

	"homestead/internal/firewall"
)

func officePolicy() *Policy {
	return &Policy{
		Name: "office-only",
		Sources: []firewall.Source{
			{Zone: firewall.ZoneLocal, CIDR: "192.168.1.0/24"},
			{Zone: firewall.ZoneLocal, CIDR: "192.168.2.5/32"},
		},
	}
}

func TestPolicyValidation(t *testing.T) {
	if err := officePolicy().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	empty := &Policy{Name: "parked"}
	if err := empty.Validate(); err != nil {
		t.Fatalf("empty Validate: %v", err)
	}
	if empty.enforcement() != "deny" {
		t.Fatal("empty policy not denying")
	}
	if officePolicy().enforcement() != "allow" {
		t.Fatal("listed policy not allowing")
	}
	if (*Policy)(nil).enforcement() != "open" {
		t.Fatal("nil policy not open")
	}
	zoneOnly := &Policy{Name: "lan", Sources: []firewall.Source{{Zone: firewall.ZoneLocal}}}
	if err := zoneOnly.Validate(); err != nil {
		t.Fatalf("zone Validate: %v", err)
	}
	if zoneOnly.enforcement() != "open" {
		t.Fatal("zone-wide policy not open")
	}
	cases := []struct {
		name   string
		policy Policy
		want   string
	}{
		{"bad name", Policy{Name: "Bad!"}, "policy.name"},
		{"bad zone", Policy{Name: "x", Sources: []firewall.Source{{Zone: "dmz"}}}, "source #0"},
		{"bad cidr", Policy{Name: "x", Sources: []firewall.Source{{Zone: firewall.ZoneLocal, CIDR: "nope"}}}, "source #0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Validate()
			if err == nil {
				t.Fatal("accepted invalid policy")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func policyRoute() Route {
	return Route{
		Host:     "app.example.com",
		Target:   Target{Host: "app", Port: 8080},
		Exposure: ExposurePublic,
		TLS:      TLSAuto,
		Policy:   officePolicy(),
	}
}

func TestRenderAllowlist(t *testing.T) {
	text, err := RenderRoute(policyRoute())
	if err != nil {
		t.Fatalf("RenderRoute: %v", err)
	}
	for _, want := range []string{
		"app.example.com {",
		"@allowed remote_ip 192.168.1.0/24 192.168.2.5/32",
		"handle @allowed {",
		"\t\treverse_proxy app:8080",
		"handle {",
		"abort",
		"Exposure policy: office-only",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
}

func TestRenderDenyAll(t *testing.T) {
	r := policyRoute()
	r.Policy = &Policy{Name: "parked"}
	text, err := RenderRoute(r)
	if err != nil {
		t.Fatalf("RenderRoute: %v", err)
	}
	if !strings.Contains(text, "denies all origins") || !strings.Contains(text, "abort") {
		t.Fatalf("not denying:\n%s", text)
	}
	if strings.Contains(text, "reverse_proxy") {
		t.Fatalf("deny serves upstream:\n%s", text)
	}
}

func TestRenderPolicyPassthrough(t *testing.T) {
	// Nil policy renders exactly as before.
	r := policyRoute()
	r.Policy = nil
	plain, err := RenderRoute(r)
	if err != nil {
		t.Fatalf("RenderRoute: %v", err)
	}
	if strings.Contains(plain, "handle") || strings.Contains(plain, "abort") {
		t.Fatalf("nil policy restricted:\n%s", plain)
	}
	// Zone-wide policy enforces at the firewall, not the snippet.
	r.Policy = &Policy{Name: "lan", Sources: []firewall.Source{{Zone: firewall.ZoneLocal}}}
	zonal, err := RenderRoute(r)
	if err != nil {
		t.Fatalf("RenderRoute: %v", err)
	}
	if zonal != plain {
		t.Fatalf("zone policy changed render:\n%s\n---\n%s", zonal, plain)
	}
	// Internal TLS stays site-level above the allowlist.
	r.Policy = officePolicy()
	r.TLS = TLSInternal
	r.Host = "app.lan"
	r.Exposure = ExposureLocal
	guarded, err := RenderRoute(r)
	if err != nil {
		t.Fatalf("RenderRoute: %v", err)
	}
	tlsAt := strings.Index(guarded, "tls internal")
	handleAt := strings.Index(guarded, "handle @allowed")
	if tlsAt < 0 || handleAt < 0 || tlsAt > handleAt {
		t.Fatalf("tls misplaced:\n%s", guarded)
	}
	// Invalid policies refuse at validation.
	r.Policy = &Policy{Name: "Bad!"}
	if _, err := RenderRoute(r); err == nil {
		t.Fatal("rendered invalid policy")
	}
}
