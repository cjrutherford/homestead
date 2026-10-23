package catalog

import (
	"fmt"
	"strings"

	"homestead/internal/identity"
)

// OIDC client declaration (S342). S203 says whether a package can
// delegate login and through which protocols; this records what an
// OIDC provider must be told to serve that package: where the app
// returns after login, which scopes it expects, and which catalog
// secrets receive the issued client credentials.
//
// The credentials themselves never enter the manifest. The issued
// client id and secret are written to the secret store at actuation and
// reach the container as files under the supervisor (ADR-052), so a
// template, a plan, or a generated Compose document never holds them.

// OIDCClient declares one package's OIDC client requirements.
type OIDCClient struct {
	// Slug is the provider-side application identifier. Empty uses the
	// package name, which is unique across the catalog.
	Slug string
	// RedirectPaths are absolute paths on the package's route host that
	// the provider must accept, e.g.
	// /accounts/oidc/paperless/login/callback/.
	RedirectPaths []string
	// Scopes the provider should issue. Empty keeps the provider
	// defaults (openid, email, profile).
	Scopes []string
	// ClientIDSecret and ClientSecretSecret name manifest secrets that
	// hold the issued values. Both must be declared and exported, so
	// the app reads them through the same supervisor path as every
	// other secret.
	ClientIDSecret     string
	ClientSecretSecret string
}

// Validate checks the declaration shape. Cross-references to the
// manifest's secret declarations are checked by Manifest.Validate,
// which has both halves.
func (o OIDCClient) Validate() error {
	if len(o.RedirectPaths) == 0 {
		return fmt.Errorf("catalog: OIDC declaration needs at least one redirect path")
	}
	seen := map[string]bool{}
	for _, path := range o.RedirectPaths {
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
			return fmt.Errorf("catalog: OIDC redirect path %q must be absolute and local", path)
		}
		if strings.ContainsAny(path, " \t\r\n") {
			return fmt.Errorf("catalog: OIDC redirect path %q contains whitespace", path)
		}
		if seen[path] {
			return fmt.Errorf("catalog: duplicate OIDC redirect path %q", path)
		}
		seen[path] = true
	}
	for _, scope := range o.Scopes {
		if scope == "" || strings.ContainsAny(scope, " \t\r\n") {
			return fmt.Errorf("catalog: OIDC scope %q invalid", scope)
		}
	}
	for _, name := range []string{o.ClientIDSecret, o.ClientSecretSecret} {
		if !validSecretName.MatchString(name) {
			return fmt.Errorf("catalog: OIDC client secret name %q invalid", name)
		}
	}
	if o.ClientIDSecret == o.ClientSecretSecret {
		return fmt.Errorf("catalog: OIDC client id and secret must be different secrets")
	}
	return nil
}

// SlugFor resolves the provider-side slug, defaulting to the package
// name.
func (o OIDCClient) SlugFor(pkg string) string {
	if o.Slug != "" {
		return o.Slug
	}
	return pkg
}

// Slug returns the provider-side application slug for a package.
func (m Manifest) OIDCSlug() (string, bool) {
	if m.SSO.Client == nil {
		return "", false
	}
	return m.SSO.Client.SlugFor(m.Name), true
}

// RedirectURIs resolves the declared paths against the package's route
// host. A package without a host cannot be actuated: the provider would
// accept a redirect the app never serves.
func (m Manifest) RedirectURIs(routeHost string) ([]string, error) {
	if m.SSO.Client == nil {
		return nil, fmt.Errorf("catalog: %s declares no OIDC client", m.Name)
	}
	if routeHost == "" {
		return nil, fmt.Errorf("catalog: %s has no route host for OIDC", m.Name)
	}
	uris := make([]string, 0, len(m.SSO.Client.RedirectPaths))
	for _, path := range m.SSO.Client.RedirectPaths {
		uris = append(uris, "https://"+routeHost+path)
	}
	return uris, nil
}

// OIDCClientSpec builds the provider-neutral actuation request for a
// package served at routeHost.
func (m Manifest) OIDCClientSpec(routeHost string) (identity.ClientSpec, error) {
	if m.SSO.Client == nil {
		return identity.ClientSpec{}, fmt.Errorf("catalog: %s declares no OIDC client", m.Name)
	}
	uris, err := m.RedirectURIs(routeHost)
	if err != nil {
		return identity.ClientSpec{}, err
	}
	spec := identity.ClientSpec{
		Package:      m.Name,
		Slug:         m.SSO.Client.SlugFor(m.Name),
		Name:         m.Title,
		RedirectURIs: uris,
		Scopes:       append([]string{}, m.SSO.Client.Scopes...),
	}
	if err := spec.Validate(); err != nil {
		return identity.ClientSpec{}, err
	}
	return spec, nil
}

// validateOIDC checks that an OIDC block agrees with the rest of the
// manifest: an OIDC client only makes sense on a package whose SSO class
// and protocols admit it, and its credential secrets must exist and be
// delivered by the supervisor.
func validateOIDC(m Manifest) error {
	client := m.SSO.Client
	if client == nil {
		return nil
	}
	if m.SSO.Class.Effective() == SSOUnsupported {
		return fmt.Errorf("catalog: %s declares an OIDC client but unsupported SSO", m.Name)
	}
	hasOIDC := false
	for _, protocol := range m.SSO.Protocols {
		if protocol == identity.CapabilityOIDC {
			hasOIDC = true
		}
	}
	if !hasOIDC {
		return fmt.Errorf("catalog: %s declares an OIDC client but no oidc protocol", m.Name)
	}
	if err := client.Validate(); err != nil {
		return err
	}
	declared := map[string]bool{}
	for _, secret := range m.Secrets {
		declared[secret.Name] = true
	}
	for _, name := range []string{client.ClientIDSecret, client.ClientSecretSecret} {
		if !declared[name] {
			return fmt.Errorf("catalog: OIDC client secret %q is not declared", name)
		}
	}
	web := false
	for _, svc := range m.Services {
		if svc.Exposes == "web" {
			web = true
		}
	}
	if !web {
		return fmt.Errorf("catalog: %s declares an OIDC client but serves no web endpoint", m.Name)
	}
	return nil
}
