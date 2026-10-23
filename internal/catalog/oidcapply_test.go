package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"

	"homestead/internal/identity"
)

// fakeManager records actuation calls so the catalog sequence can be
// asserted without a live provider.
type fakeManager struct {
	client  identity.Client
	ensured []identity.ClientSpec
	deleted []string
	err     error
}

func (f *fakeManager) EnsureClient(_ context.Context, _ string, _ identity.Credentials, spec identity.ClientSpec) (identity.Client, error) {
	f.ensured = append(f.ensured, spec)
	return f.client, f.err
}

func (f *fakeManager) DeleteClient(_ context.Context, _ string, _ identity.Credentials, slug string) error {
	f.deleted = append(f.deleted, slug)
	return f.err
}

func activationFor(m Manifest, mgr identity.ClientManager, sec SecretStore) OIDCActivation {
	return OIDCActivation{
		Manifest: m, RouteHost: "archive.home.example",
		Provider: "authentik", Endpoint: "https://auth.home.example",
		Credentials: identity.Credentials{"token": "t"},
		Manager:     mgr, Secrets: sec,
	}
}

func TestActivateOIDCStoresIssuedCredentials(t *testing.T) {
	m := clientManifest()
	mgr := &fakeManager{client: identity.Client{ClientID: "cid", ClientSecret: "csec", Created: true}}
	sec := newMemSecrets(t)

	res, err := ActivateOIDC(context.Background(), activationFor(m, mgr, sec))
	if err != nil {
		t.Fatalf("ActivateOIDC: %v", err)
	}
	if !res.Created || res.Slug != "paperless-ngx" {
		t.Fatalf("result = %+v", res)
	}
	if len(mgr.ensured) != 1 {
		t.Fatalf("specs = %+v", mgr.ensured)
	}
	if got := mgr.ensured[0].RedirectURIs[0]; got != "https://archive.home.example/accounts/oidc/paperless/login/callback/" {
		t.Fatalf("redirect = %q", got)
	}
	idRow, secretRow, _ := OIDCCredentialRows(m)
	if sec.values[idRow] != "cid" || sec.values[secretRow] != "csec" {
		t.Fatalf("stored rows = %v", sec.values)
	}
	// The generated stack must consume exactly those rows.
	files, err := Generate(m, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files.SecretFiles {
		if f.StoreName != idRow && f.StoreName != secretRow {
			continue
		}
		if f.Key == "" {
			t.Fatalf("secret %q has no file", f.StoreName)
		}
	}
}

func TestActivateOIDCReusesAnExistingClientSecret(t *testing.T) {
	m := clientManifest()
	mgr := &fakeManager{client: identity.Client{ClientID: "cid"}}
	sec := newMemSecrets(t)
	_, secretRow, _ := OIDCCredentialRows(m)
	sec.values[secretRow] = "kept"

	res, err := ActivateOIDC(context.Background(), activationFor(m, mgr, sec))
	if err != nil {
		t.Fatalf("ActivateOIDC: %v", err)
	}
	if res.Created || sec.values[secretRow] != "kept" {
		t.Fatalf("result = %+v, stored = %v", res, sec.values)
	}
}

func TestActivateOIDCFailsWhenExistingSecretIsLost(t *testing.T) {
	m := clientManifest()
	mgr := &fakeManager{client: identity.Client{ClientID: "cid"}}
	sec := newMemSecrets(t)
	if _, err := ActivateOIDC(context.Background(), activationFor(m, mgr, sec)); err == nil {
		t.Fatal("want an error when the provider holds a client whose secret we lost")
	}
}

func TestActivateOIDCSurfacesProviderFailure(t *testing.T) {
	m := clientManifest()
	mgr := &fakeManager{err: errors.New("provider down")}
	if _, err := ActivateOIDC(context.Background(), activationFor(m, mgr, newMemSecrets(t))); err == nil ||
		!strings.Contains(err.Error(), "provider down") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeactivateOIDCRemovesClientAndRows(t *testing.T) {
	m := clientManifest()
	mgr := &fakeManager{}
	sec := newMemSecrets(t)
	idRow, secretRow, _ := OIDCCredentialRows(m)
	sec.values[idRow] = "cid"
	sec.values[secretRow] = "csec"

	if err := DeactivateOIDC(context.Background(), activationFor(m, mgr, sec)); err != nil {
		t.Fatalf("DeactivateOIDC: %v", err)
	}
	if len(mgr.deleted) != 1 || mgr.deleted[0] != "paperless-ngx" {
		t.Fatalf("deleted = %v", mgr.deleted)
	}
	if _, ok := sec.values[idRow]; ok {
		t.Fatal("client id row not removed")
	}
	if _, ok := sec.values[secretRow]; ok {
		t.Fatal("client secret row not removed")
	}
}

func TestDeactivateOIDCIsRepeatable(t *testing.T) {
	m := clientManifest()
	if err := DeactivateOIDC(context.Background(), activationFor(m, &fakeManager{}, newMemSecrets(t))); err != nil {
		t.Fatalf("DeactivateOIDC: %v", err)
	}
}

func TestActivateOIDCRequiresADeclaration(t *testing.T) {
	m := exportingManifest()
	if _, err := ActivateOIDC(context.Background(), activationFor(m, &fakeManager{}, newMemSecrets(t))); err == nil {
		t.Fatal("want an error without an OIDC declaration")
	}
}
