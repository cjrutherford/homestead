package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OIDC client actuation (S342). The application side declares what it
// needs (redirect paths, scopes); the provider side is this file: create
// or update one OAuth2/OIDC client per catalog package, and remove it
// again. Nothing here stores the result: the caller writes the client
// credentials through the secret store (ADR-021) so the app receives
// them as files, never through a Compose environment entry.

// ClientSpec is one application's OIDC client request. Slug is the
// provider-side application identifier and must be stable for the
// package's lifetime; RedirectURIs are absolute and already resolved
// against the package's route host.
type ClientSpec struct {
	Package      string
	Slug         string
	Name         string
	RedirectURIs []string
	Scopes       []string
}

// Validate checks the spec shape before any call leaves the appliance.
func (s ClientSpec) Validate() error {
	if s.Slug == "" || s.Name == "" {
		return fmt.Errorf("identity: OIDC client needs a slug and a name")
	}
	if len(s.RedirectURIs) == 0 {
		return fmt.Errorf("identity: OIDC client %q needs at least one redirect URI", s.Slug)
	}
	for _, uri := range s.RedirectURIs {
		if !strings.HasPrefix(uri, "https://") && !strings.HasPrefix(uri, "http://") {
			return fmt.Errorf("identity: OIDC redirect %q must be absolute", uri)
		}
	}
	return nil
}

// Client is what a provider hands back after ensuring a client. Secret
// is empty when the client already existed, because providers return a
// secret only once: the caller keeps the one it already stored.
type Client struct {
	ClientID     string
	ClientSecret string
	ProviderPK   int
	Created      bool
}

// ClientManager actuates OIDC clients for one provider. It is separate
// from Provider so read-only metadata and live writes stay separable:
// setup flows can describe a provider without holding write access.
type ClientManager interface {
	// EnsureClient creates the client when absent and updates the
	// redirect URIs when present. It never rotates a secret.
	EnsureClient(ctx context.Context, endpoint string, creds Credentials, spec ClientSpec) (Client, error)
	// DeleteClient removes the provider application and its OAuth2
	// provider. A missing application is not an error, so uninstall is
	// repeatable.
	DeleteClient(ctx context.Context, endpoint string, creds Credentials, slug string) error
}

// TokenSecretName is the secret-store row holding a provider's API
// token. Tokens are secrets (ADR-021) and never appear in config.
func TokenSecretName(provider string) string {
	return "identity_" + provider + "_token"
}

// errNotFound lets provider calls tell "absent" from "unreachable".
var errNotFound = fmt.Errorf("identity: not found")

// Authentik is the Authentik provider: read metadata plus OIDC client
// actuation through Authentik's own HTTP API.
type Authentik struct {
	// HTTP is the client used for every call; nil uses a bounded
	// default so a hung provider cannot stall the daemon.
	HTTP *http.Client
}

// NewAuthentik returns an Authentik provider with a bounded HTTP client.
func NewAuthentik() *Authentik {
	return &Authentik{HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (a *Authentik) Name() string { return "authentik" }

func (a *Authentik) Capabilities() []Capability {
	return []Capability{CapabilityOIDC, CapabilitySAML, CapabilityLDAP}
}

func (a *Authentik) RequiredCredentials() []CredentialField {
	return []CredentialField{{Name: "token", Label: "API token", Secret: true}}
}

func (a *Authentik) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// CheckConnection proves the API answers with the given token by
// reading the version endpoint.
func (a *Authentik) CheckConnection(ctx context.Context, endpoint string, creds Credentials) error {
	token, err := creds.Get("token")
	if err != nil {
		return err
	}
	var out struct {
		Version string `json:"version_current"`
	}
	if err := a.call(ctx, endpoint, token, http.MethodGet, "/api/v3/root/config/", nil, &out); err != nil {
		return fmt.Errorf("identity: authentik not reachable: %w", err)
	}
	return nil
}

// EnsureClient creates or updates one OIDC client and its application.
func (a *Authentik) EnsureClient(ctx context.Context, endpoint string, creds Credentials, spec ClientSpec) (Client, error) {
	if err := spec.Validate(); err != nil {
		return Client{}, err
	}
	token, err := creds.Get("token")
	if err != nil {
		return Client{}, err
	}

	var existing struct {
		Results []struct {
			PK       int `json:"pk"`
			Provider int `json:"provider"`
		} `json:"results"`
	}
	if err := a.call(ctx, endpoint, token, http.MethodGet, "/api/v3/core/applications/?slug="+spec.Slug, nil, &existing); err != nil {
		return Client{}, err
	}
	if len(existing.Results) > 0 {
		app := existing.Results[0]
		if app.Provider > 0 {
			if err := a.updateRedirects(ctx, endpoint, token, app.Provider, spec); err != nil {
				return Client{}, err
			}
			var provider struct {
				ClientID string `json:"client_id"`
			}
			if err := a.call(ctx, endpoint, token, http.MethodGet, fmt.Sprintf("/api/v3/providers/oauth2/%d/", app.Provider), nil, &provider); err != nil {
				return Client{}, err
			}
			return Client{ClientID: provider.ClientID, ProviderPK: app.Provider}, nil
		}
	}

	authFlow, err := a.flowPK(ctx, endpoint, token, "default-provider-authorization-explicit-consent")
	if err != nil {
		return Client{}, err
	}
	invalidationFlow, err := a.flowPK(ctx, endpoint, token, "default-provider-invalidation-flow")
	if err != nil {
		return Client{}, err
	}

	payload := map[string]any{
		"name":               spec.Name,
		"authorization_flow": authFlow,
		"invalidation_flow":  invalidationFlow,
		"client_type":        "confidential",
		"sub_mode":           "user_email",
		"redirect_uris":      redirectEntries(spec.RedirectURIs),
	}
	var provider struct {
		PK           int    `json:"pk"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := a.call(ctx, endpoint, token, http.MethodPost, "/api/v3/providers/oauth2/", payload, &provider); err != nil {
		return Client{}, err
	}

	appPayload := map[string]any{
		"name":     spec.Name,
		"slug":     spec.Slug,
		"provider": provider.PK,
	}
	if err := a.call(ctx, endpoint, token, http.MethodPost, "/api/v3/core/applications/", appPayload, nil); err != nil {
		// Do not leave an orphan provider behind when the application
		// cannot be created.
		_ = a.call(ctx, endpoint, token, http.MethodDelete, fmt.Sprintf("/api/v3/providers/oauth2/%d/", provider.PK), nil, nil)
		return Client{}, err
	}
	return Client{
		ClientID:     provider.ClientID,
		ClientSecret: provider.ClientSecret,
		ProviderPK:   provider.PK,
		Created:      true,
	}, nil
}

// DeleteClient removes the application and its OAuth2 provider.
func (a *Authentik) DeleteClient(ctx context.Context, endpoint string, creds Credentials, slug string) error {
	token, err := creds.Get("token")
	if err != nil {
		return err
	}
	var existing struct {
		Results []struct {
			Provider int `json:"provider"`
		} `json:"results"`
	}
	if err := a.call(ctx, endpoint, token, http.MethodGet, "/api/v3/core/applications/?slug="+slug, nil, &existing); err != nil {
		return err
	}
	if len(existing.Results) == 0 {
		return nil
	}
	providerPK := existing.Results[0].Provider
	if err := a.call(ctx, endpoint, token, http.MethodDelete, "/api/v3/core/applications/"+slug+"/", nil, nil); err != nil {
		return err
	}
	if providerPK > 0 {
		return a.call(ctx, endpoint, token, http.MethodDelete, fmt.Sprintf("/api/v3/providers/oauth2/%d/", providerPK), nil, nil)
	}
	return nil
}

func (a *Authentik) updateRedirects(ctx context.Context, endpoint, token string, providerPK int, spec ClientSpec) error {
	payload := map[string]any{"redirect_uris": redirectEntries(spec.RedirectURIs)}
	return a.call(ctx, endpoint, token, http.MethodPatch, fmt.Sprintf("/api/v3/providers/oauth2/%d/", providerPK), payload, nil)
}

func (a *Authentik) flowPK(ctx context.Context, endpoint, token, slug string) (string, error) {
	var out struct {
		Results []struct {
			PK string `json:"pk"`
		} `json:"results"`
	}
	if err := a.call(ctx, endpoint, token, http.MethodGet, "/api/v3/flows/instances/?slug="+slug, nil, &out); err != nil {
		return "", err
	}
	if len(out.Results) == 0 {
		return "", fmt.Errorf("identity: authentik flow %q not found", slug)
	}
	return out.Results[0].PK, nil
}

// redirectEntries renders redirect URIs in the strict matching mode the
// app expects; Authentik rejects a bare string list.
func redirectEntries(uris []string) []map[string]any {
	out := make([]map[string]any, 0, len(uris))
	for _, uri := range uris {
		out = append(out, map[string]any{"matching_mode": "strict", "url": uri})
	}
	return out
}

// call performs one API call and decodes the body when out is non-nil.
// Provider errors carry the status only: response bodies may echo
// request data and must not reach logs.
func (a *Authentik) call(ctx context.Context, endpoint, token, method, path string, payload, out any) error {
	if endpoint == "" {
		return fmt.Errorf("identity: provider endpoint is empty")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("identity: encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(endpoint, "/")+path, body)
	if err != nil {
		return fmt.Errorf("identity: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return fmt.Errorf("identity: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode >= 300:
		return fmt.Errorf("identity: %s %s returned %s", method, path, resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("identity: decode %s: %w", path, err)
	}
	return nil
}
