package catalog

import (
	"context"
	"errors"
	"fmt"

	"homestead/internal/identity"
	"homestead/internal/secret"
)

// OIDC activation (S342): the one place that turns a package's declared
// OIDC client into a real client at the provider and the issued
// credentials into catalog secrets. Keeping it here — after the catalog
// declaration and the provider client, before any surface — means the
// CLI, the API, and package install all run the same sequence.

// OIDCActivation is one package's activation request. Everything it
// needs is injected: the provider client for the network call, the
// secret store for the credentials, and the resolved route host the
// provider must redirect back to.
type OIDCActivation struct {
	Manifest    Manifest
	RouteHost   string
	Provider    string
	Endpoint    string
	Credentials identity.Credentials
	Manager     identity.ClientManager
	Secrets     SecretStore
}

// OIDCResult reports what happened without echoing any credential.
type OIDCResult struct {
	// Slug is the provider-side application slug.
	Slug string
	// Created is false when the provider already hosted the client and
	// only the redirect URIs were refreshed.
	Created bool
}

// OIDCCredentialRows returns the secret-store rows that hold a
// package's issued client id and secret.
func OIDCCredentialRows(m Manifest) (id, secretRow string, err error) {
	if m.SSO.Client == nil {
		return "", "", fmt.Errorf("catalog: %s declares no OIDC client", m.Name)
	}
	return SecretStoreName(m.Name, m.SSO.Client.ClientIDSecret),
		SecretStoreName(m.Name, m.SSO.Client.ClientSecretSecret), nil
}

// ActivateOIDC ensures the provider hosts the client and stores the
// issued credentials. It is idempotent: an existing client keeps its
// stored secret and only its redirect URIs are refreshed, so a re-run
// after a route change does not invalidate the app's configuration.
//
// Superseded (S402, ADR-052 amendment): nothing calls this; env-configured
// apps sign in through the appsso env-oidc adapter. Removal is deferred.
func ActivateOIDC(ctx context.Context, req OIDCActivation) (OIDCResult, error) {
	if req.Manager == nil {
		return OIDCResult{}, fmt.Errorf("catalog: provider %q cannot manage OIDC clients", req.Provider)
	}
	if req.Secrets == nil {
		return OIDCResult{}, fmt.Errorf("catalog: OIDC activation needs a secret store")
	}
	if req.Manifest.SSO.Client == nil {
		return OIDCResult{}, fmt.Errorf("catalog: %s declares no OIDC client", req.Manifest.Name)
	}
	if err := req.Manifest.Validate(); err != nil {
		return OIDCResult{}, err
	}
	spec, err := req.Manifest.OIDCClientSpec(req.RouteHost)
	if err != nil {
		return OIDCResult{}, err
	}
	client, err := req.Manager.EnsureClient(ctx, req.Endpoint, req.Credentials, spec)
	if err != nil {
		return OIDCResult{}, err
	}
	if client.ClientID == "" {
		return OIDCResult{}, fmt.Errorf("catalog: provider returned no client id for %q", spec.Slug)
	}
	idRow, secretRow, err := OIDCCredentialRows(req.Manifest)
	if err != nil {
		return OIDCResult{}, err
	}
	if !client.Created {
		// Providers return a secret only once. An existing client is
		// fine as long as we still hold the secret we stored earlier;
		// otherwise the app would start with no usable credential.
		if _, err := req.Secrets.Get(secretRow); err != nil {
			if errors.Is(err, secret.ErrNotFound) {
				return OIDCResult{}, fmt.Errorf(
					"catalog: OIDC client %q exists at the provider but its secret was never stored; remove the client there and activate again",
					spec.Slug)
			}
			return OIDCResult{}, err
		}
		if err := req.Secrets.SetApp(idRow, client.ClientID); err != nil {
			return OIDCResult{}, err
		}
		return OIDCResult{Slug: spec.Slug}, nil
	}
	if client.ClientSecret == "" {
		return OIDCResult{}, fmt.Errorf("catalog: provider returned no client secret for %q", spec.Slug)
	}
	if err := req.Secrets.SetApp(idRow, client.ClientID); err != nil {
		return OIDCResult{}, err
	}
	if err := req.Secrets.SetApp(secretRow, client.ClientSecret); err != nil {
		return OIDCResult{}, err
	}
	return OIDCResult{Slug: spec.Slug, Created: true}, nil
}

// DeactivateOIDC removes the provider client and the stored
// credentials. A missing provider client is not an error, so uninstall
// stays repeatable.
func DeactivateOIDC(ctx context.Context, req OIDCActivation) error {
	if req.Secrets == nil {
		return fmt.Errorf("catalog: OIDC deactivation needs a secret store")
	}
	if req.Manifest.SSO.Client == nil {
		return nil
	}
	slug := req.Manifest.SSO.Client.SlugFor(req.Manifest.Name)
	if req.Manager != nil {
		if err := req.Manager.DeleteClient(ctx, req.Endpoint, req.Credentials, slug); err != nil {
			return err
		}
	}
	idRow, secretRow, err := OIDCCredentialRows(req.Manifest)
	if err != nil {
		return err
	}
	for _, row := range []string{idRow, secretRow} {
		if err := req.Secrets.Delete(row); err != nil && !errors.Is(err, secret.ErrNotFound) {
			return err
		}
	}
	return nil
}
