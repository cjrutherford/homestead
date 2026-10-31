package catalog

import (
	"strings"
	"testing"

	"homestead/internal/exposure"
	"homestead/internal/identity"
	"homestead/internal/storage"
)

func validManifest() Manifest {
	return Manifest{
		Name: "jellyfin", Version: "10.10.3",
		Title: "Jellyfin", Blurb: "Media server you control.",
		Description: "Streams your media library across the LAN and beyond.",
		Category:    "media",
		Icon:        "mdi:plex",
		Services: []Service{
			{Name: "jellyfin", Image: "jellyfin/jellyfin:10.10.3", Exposes: "web"},
		},
		Requirements: Requirements{
			MemoryMB: 1024,
			Storage: []StorageNeed{
				{Class: storage.ClassApplication, MinBytes: 1 << 30},
				{Class: storage.ClassBulk, MinBytes: 100 << 30},
			},
			Ports: []int{8096},
		},
		Exposure: exposure.PackageExposure{
			Package: "jellyfin",
			Endpoints: []exposure.EndpointExposure{
				{Endpoint: "web", Recommended: "local", AllowWider: true},
			},
		},
		BackupPaths: []string{"config", "data/db"},
	}
}

func TestManifestValid(t *testing.T) {
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestManifestInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{"bad name", func(m *Manifest) { m.Name = "Bad!" }, "package must match"},
		{"bad version", func(m *Manifest) { m.Version = "10" }, "numeric dotted"},
		{"no title", func(m *Manifest) { m.Title = "" }, "title must be"},
		{"long blurb", func(m *Manifest) { m.Blurb = strings.Repeat("x", 201) }, "blurb must be"},
		{"no services", func(m *Manifest) { m.Services = nil }, "at least one service"},
		{"latest image", func(m *Manifest) { m.Services[0].Image = "x:latest" }, "non-latest tag"},
		{"untagged image", func(m *Manifest) { m.Services[0].Image = "x" }, "non-latest tag"},
		{"dangling endpoint", func(m *Manifest) { m.Services[0].Exposes = "ghost" }, "unknown endpoint"},
		{"dup service", func(m *Manifest) {
			m.Services = append(m.Services, m.Services[0])
		}, "duplicate service"},
		{"package drift", func(m *Manifest) { m.Exposure.Package = "other" }, "must equal manifest"},
		{"no memory", func(m *Manifest) { m.Requirements.MemoryMB = 0 }, "memory must exceed zero"},
		{"zero storage", func(m *Manifest) {
			m.Requirements.Storage[0].MinBytes = 0
		}, "must exceed zero"},
		{"bad port", func(m *Manifest) { m.Requirements.Ports = []int{0} }, "port must be"},
		{"dup port", func(m *Manifest) { m.Requirements.Ports = []int{8096, 8096} }, "duplicate port"},
		{"absolute backup", func(m *Manifest) { m.BackupPaths = []string{"/etc"} }, "relative and contained"},
		{"escape backup", func(m *Manifest) { m.BackupPaths = []string{"../etc"} }, "relative and contained"},
		{"bad icon", func(m *Manifest) { m.Icon = "Bad Icon!" }, "invalid icon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := validManifest()
			tc.mutate(&manifest)
			err := manifest.Validate()
			if err == nil {
				t.Fatal("accepted invalid manifest")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func oidcManifest() Manifest {
	m := validManifest()
	m.Integrations = []Integration{{Kind: IntegrationIdentity, Target: "authentik", Note: "Sign in through Authentik."}}
	m.SSO = SSODeclaration{Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC},
		OIDC: &OIDCAdapter{Adapter: AdapterJellyfinSSO, Service: "jellyfin", Plugin: &PinnedPlugin{
			Name: "SSO Authentication", Version: "4.0.0.4", SHA256: strings.Repeat("a", 64),
			URL: "https://github.com/9p4/jellyfin-plugin-sso/releases/download/v4.0.0.4/sso-authentication_4.0.0.4.zip"}}}
	return m
}

func TestOIDCAdapterValidation(t *testing.T) {
	if err := oidcManifest().Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*OIDCAdapter, *Manifest)
		want   string
	}{
		{"unknown adapter", func(a *OIDCAdapter, _ *Manifest) { a.Adapter = "saml-thing" }, "unknown"},
		{"no oidc protocol", func(_ *OIDCAdapter, m *Manifest) { m.SSO.Protocols = []identity.Capability{identity.CapabilitySAML} }, "oidc protocol"},
		{"ghost service", func(a *OIDCAdapter, _ *Manifest) { a.Service = "ghost" }, "serves an endpoint"},
		{"no pin", func(a *OIDCAdapter, _ *Manifest) { a.Plugin = nil }, "must pin"},
		{"pin elsewhere", func(a *OIDCAdapter, _ *Manifest) { a.Adapter = AdapterImmich }, "only the"},
		{"http url", func(a *OIDCAdapter, _ *Manifest) { a.Plugin.URL = strings.Replace(a.Plugin.URL, "https", "http", 1) }, "https .zip"},
		{"other release", func(a *OIDCAdapter, _ *Manifest) { a.Plugin.Version = "4.0.0.5" }, "https .zip"},
		{"bad sum", func(a *OIDCAdapter, _ *Manifest) { a.Plugin.SHA256 = strings.Repeat("A", 64) }, "lowercase hex"},
		{"bad version", func(a *OIDCAdapter, _ *Manifest) { a.Plugin.Version = "latest" }, "release number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := oidcManifest()
			tc.mutate(m.SSO.OIDC, &m)
			if err := m.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func envOIDCManifest() Manifest {
	m := validManifest()
	m.Integrations = []Integration{{Kind: IntegrationIdentity, Target: "authentik", Note: "Sign in through Authentik."}}
	m.Secrets = []SecretDecl{{Name: "oidc_client_secret", Consumers: []SecretConsumer{{Service: "jellyfin", Env: "OIDC_CLIENT_SECRET_FILE"}}}}
	m.SSO = SSODeclaration{Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC},
		OIDC: &OIDCAdapter{Adapter: AdapterEnvOIDC, Service: "jellyfin", Env: &EnvOIDC{
			RedirectPaths: []string{"/login"}, SignInPath: "/login", Secret: "oidc_client_secret", Trust: "OIDC_TLS_CACERTFILE",
			Set: map[string]string{"OIDC_CLIENT_ID": "{{client_id}}", "OIDC_URL": "{{issuer}}.well-known/openid-configuration", "OIDC_ADMIN_GROUP": "{{ admin_group }}"}}}}
	return m
}

func TestEnvOIDCValidation(t *testing.T) {
	if err := envOIDCManifest().Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*OIDCAdapter)
		want   string
	}{
		{"no env", func(a *OIDCAdapter) { a.Env = nil }, "takes env settings"},
		{"env elsewhere", func(a *OIDCAdapter) { a.Adapter = AdapterImmich }, "takes env settings"},
		{"client secret placeholder", func(a *OIDCAdapter) { a.Env.Set["X"] = "{{client_secret}}" }, "placeholder"},
		{"unknown placeholder", func(a *OIDCAdapter) { a.Env.Set["X"] = "{{nope}}" }, "placeholder"},
		{"malformed placeholder", func(a *OIDCAdapter) { a.Env.Set["X"] = "{{issuer" }, "malformed"},
		{"bad key", func(a *OIDCAdapter) { a.Env.Set["1 BAD"] = "x" }, "invalid"},
		{"relative redirect", func(a *OIDCAdapter) { a.Env.RedirectPaths = []string{"login"} }, "absolute"},
		{"relative sign in", func(a *OIDCAdapter) { a.Env.SignInPath = "login" }, "absolute"},
		{"no redirect", func(a *OIDCAdapter) { a.Env.RedirectPaths = nil }, "redirect path"},
		{"undeclared secret", func(a *OIDCAdapter) { a.Env.Secret = "ghost" }, "declared secret"},
		{"bad trust", func(a *OIDCAdapter) { a.Env.Trust = "bad name" }, "trust"},
		{"secret variable collision", func(a *OIDCAdapter) { a.Env.Set["OIDC_CLIENT_SECRET_FILE"] = "x" }, "secret variable"},
		{"no set", func(a *OIDCAdapter) { a.Env.Set = nil }, "sets no variables"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := envOIDCManifest()
			tc.mutate(m.SSO.OIDC)
			if err := m.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
	// A secret consumed by another service does not count.
	m := envOIDCManifest()
	m.Services = append(m.Services, Service{Name: "other", Image: "o:1"})
	m.Secrets[0].Consumers[0].Service = "other"
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "consumed by service") {
		t.Fatalf("other consumer: %v", err)
	}
}

func TestRenderEnvTemplateTrimsSpaces(t *testing.T) {
	vals := map[string]string{"issuer": "https://a/", "admin_group": "g"}
	if got := RenderEnvTemplate("{{ issuer }}x-{{admin_group}}-{{  admin_group  }}", vals); got != "https://a/x-g-g" {
		t.Fatalf("got %q", got)
	}
	if !EnvTemplateUses("a {{ issuer }}", "issuer") || EnvTemplateUses("{{issuer}}", "client_id") {
		t.Fatal("uses wrong")
	}
}

func TestEnvOIDCRefusesManifestEnvCollision(t *testing.T) {
	m := envOIDCManifest()
	m.Services[0].Environment = map[string]string{"OIDC_CLIENT_ID": "fixed"}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "already in") {
		t.Fatalf("error %v", err)
	}
}
