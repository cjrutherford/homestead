package catalog

import (
	"strings"
	"testing"

	"homestead/internal/identity"
)

// clientManifest is exportingManifest plus an OIDC client declaration and
// the two secrets that receive the issued credentials.
func clientManifest() Manifest {
	m := exportingManifest()
	m.SSO = SSODeclaration{
		Class:     SSORecommended,
		Protocols: []identity.Capability{identity.CapabilityOIDC},
		Client: &OIDCClient{
			RedirectPaths:      []string{"/accounts/oidc/paperless/login/callback/"},
			Scopes:             []string{"openid", "email", "profile"},
			ClientIDSecret:     "oidc_client_id",
			ClientSecretSecret: "oidc_client_secret",
		},
	}
	m.Secrets = append(m.Secrets,
		SecretDecl{Name: "oidc_client_id", Consumers: []SecretConsumer{
			{Service: "webserver", Env: "PAPERLESS_OIDC_CLIENT_ID_FILE", Export: true},
		}},
		SecretDecl{Name: "oidc_client_secret", Consumers: []SecretConsumer{
			{Service: "webserver", Env: "PAPERLESS_OIDC_CLIENT_SECRET_FILE", Export: true},
		}},
	)
	return m
}

func TestOIDCManifestValid(t *testing.T) {
	if err := clientManifest().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestOIDCRedirectURIsUseTheRouteHost(t *testing.T) {
	m := clientManifest()
	uris, err := m.RedirectURIs("archive.home.example")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://archive.home.example/accounts/oidc/paperless/login/callback/"
	if len(uris) != 1 || uris[0] != want {
		t.Fatalf("uris = %v, want %q", uris, want)
	}
	if _, err := m.RedirectURIs(""); err == nil {
		t.Fatal("a package without a route host must not resolve")
	}
}

func TestOIDCClientSpecDefaultsSlugToPackage(t *testing.T) {
	spec, err := clientManifest().OIDCClientSpec("archive.home.example")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Slug != "paperless-ngx" || spec.Package != "paperless-ngx" || spec.Name != "Paperless" {
		t.Fatalf("spec = %+v", spec)
	}
	if len(spec.Scopes) != 3 {
		t.Fatalf("scopes = %v", spec.Scopes)
	}
}

func TestOIDCDeclaredSlugWins(t *testing.T) {
	m := clientManifest()
	m.SSO.Client.Slug = "paperless"
	spec, err := m.OIDCClientSpec("archive.home.example")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Slug != "paperless" {
		t.Fatalf("slug = %q", spec.Slug)
	}
	if slug, ok := m.OIDCSlug(); !ok || slug != "paperless" {
		t.Fatalf("OIDCSlug = %q, %v", slug, ok)
	}
}

func TestOIDCRejectsDeclarationsThatCannotWork(t *testing.T) {
	cases := map[string]func(*Manifest){
		"unsupported class": func(m *Manifest) {
			m.SSO.Class = SSOUnsupported
		},
		"no oidc protocol": func(m *Manifest) {
			m.SSO.Protocols = []identity.Capability{identity.CapabilitySAML}
		},
		"undeclared client secret": func(m *Manifest) {
			m.SSO.Client.ClientSecretSecret = "absent"
		},
		"no web endpoint": func(m *Manifest) {
			m.Services[0].Exposes = ""
		},
		"relative redirect": func(m *Manifest) {
			m.SSO.Client.RedirectPaths = []string{"callback"}
		},
		"duplicate redirect": func(m *Manifest) {
			m.SSO.Client.RedirectPaths = []string{"/cb", "/cb"}
		},
		"same secret twice": func(m *Manifest) {
			m.SSO.Client.ClientIDSecret = m.SSO.Client.ClientSecretSecret
		},
	}
	for name, mutate := range cases {
		m := clientManifest()
		mutate(&m)
		if err := m.Validate(); err == nil {
			t.Fatalf("%s: want a rejection", name)
		}
	}
}

func TestOIDCBlockWithoutClassIsRejected(t *testing.T) {
	m := clientManifest()
	m.SSO.Class = ""
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "SSO") {
		t.Fatalf("err = %v", err)
	}
}
