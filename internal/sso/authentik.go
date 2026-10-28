package sso

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Names of the objects Homestead creates in Authentik. They are fixed so
// a re-plan finds what a previous apply made.
const (
	ProviderName = "Homestead"
	AppName      = "Homestead"
	AppSlug      = "homestead"
	ClientID     = "homestead"

	// DefaultAuthorizationFlow, DefaultInvalidationFlow and
	// DefaultSigningKey are Authentik's stock objects.
	DefaultAuthorizationFlow = "default-provider-authorization-implicit-consent"
	DefaultInvalidationFlow  = "default-provider-invalidation-flow"
	DefaultSigningKey        = "authentik Self-signed Certificate"

	// CallbackPath is appended to each dashboard origin.
	CallbackPath = "/api/v1/sso/callback"
)

// Scopes are the standard OIDC scope mappings bound to the provider.
var Scopes = []string{"openid", "profile", "email"}

// Spec is the desired Authentik-side configuration and, once applied, the
// configuration Homestead signs in against. It holds no secret.
type Spec struct {
	AuthentikURL string   `json:"authentik_url"`
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id"`
	Origins      []string `json:"origins"`
	// PublicOrigins (a subset of Origins) are reached through the tunnel;
	// a sign-in there is sent to PublicAuthentikURL (ADR-037). Both are
	// empty on an appliance without a public dashboard, which keeps older
	// specs' digests unchanged.
	PublicOrigins      []string `json:"public_origins,omitempty"`
	PublicAuthentikURL string   `json:"public_authentik_url,omitempty"`
	RedirectURIs       []string `json:"redirect_uris"`
	AuthorizationFlow  string   `json:"authorization_flow"`
	InvalidationFlow   string   `json:"invalidation_flow"`
	SigningKey         string   `json:"signing_key"`
	Scopes             []string `json:"scopes"`
	SubMode            string   `json:"sub_mode"`
	ProviderName       string   `json:"provider_name"`
	AppName            string   `json:"application_name"`
	AppSlug            string   `json:"application_slug"`
	// AuthentikVersion is the version the catalog pins (the API this
	// plan was written against).
	AuthentikVersion string `json:"authentik_version"`
	// Access is the groups, bindings and claim mapping (ADR-043). Nil in
	// specs from before S314, which keep their digests.
	Access *Access `json:"access,omitempty"`
	// RecoveryFlow is the slug of the recovery flow Homestead adds when
	// the default brand has none (ADR-049 §3). Empty in specs from before
	// S333 and in app sign-in specs, which never touch it.
	RecoveryFlow string `json:"recovery_flow,omitempty"`
}

// IssuerFor is the per-provider issuer Authentik publishes for the
// application slug: <base>/application/o/<slug>/.
func IssuerFor(base, slug string) string {
	return strings.TrimSuffix(base, "/") + "/application/o/" + slug + "/"
}

// API is an Authentik admin API client. The token is held for the life of
// one operation and never written anywhere.
type API struct {
	Base   string
	Token  string
	Client func() *http.Client
}

// APIError is a non-success answer from Authentik. Its text never
// carries the request or the token.
type APIError struct {
	Method string
	Path   string
	Status int
	Detail string
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("Authentik answered %d to %s %s: %s", e.Status, e.Method, e.Path, e.Detail)
	}
	return fmt.Sprintf("Authentik answered %d to %s %s", e.Status, e.Method, e.Path)
}

func (a *API) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(a.Base, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.Client().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, urlErr(err))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail := ""
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
			detail = scrub(string(data))
		}
		return &APIError{Method: method, Path: path, Status: resp.StatusCode, Detail: detail}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("Authentik returned an unreadable answer to %s %s", method, path)
		}
	}
	return nil
}

// scrub keeps a short validation excerpt and drops anything that looks
// like it quotes a credential.
func scrub(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(strings.ToLower(s), "secret") || strings.Contains(strings.ToLower(s), "token") {
		return ""
	}
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}

type listResult[T any] struct {
	Results []T `json:"results"`
}

// Version reads the running Authentik version.
func (a *API) Version(ctx context.Context) (string, error) {
	var out struct {
		Current string `json:"version_current"`
	}
	if err := a.do(ctx, http.MethodGet, "/api/v3/admin/version/", nil, &out); err != nil {
		return "", err
	}
	return out.Current, nil
}

// remoteRedirect is Authentik's redirect URI shape.
type remoteRedirect struct {
	Mode string `json:"matching_mode"`
	URL  string `json:"url"`
}

// RemoteProvider is the OAuth2 provider as Authentik returns it.
type RemoteProvider struct {
	PK            int              `json:"pk"`
	Name          string           `json:"name"`
	ClientType    string           `json:"client_type"`
	GrantTypes    []string         `json:"grant_types"`
	ClientID      string           `json:"client_id"`
	ClientSecret  string           `json:"client_secret"`
	RedirectURIs  []remoteRedirect `json:"redirect_uris"`
	AuthFlow      string           `json:"authorization_flow"`
	SigningKey    string           `json:"signing_key"`
	Mappings      []string         `json:"property_mappings"`
	SubMode       string           `json:"sub_mode"`
	IssuerMode    string           `json:"issuer_mode"`
	InvalidFlow   string           `json:"invalidation_flow"`
	IncludeClaims bool             `json:"include_claims_in_id_token"`
}

// Application is the Authentik application as returned.
type Application struct {
	PK         string `json:"pk"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	Provider   *int   `json:"provider"`
	LaunchURL  string `json:"meta_launch_url"`
	OpenNewTab bool   `json:"open_in_new_tab"`
}

func (a *API) flowPK(ctx context.Context, slug string) (string, error) {
	var out listResult[struct {
		PK   string `json:"pk"`
		Slug string `json:"slug"`
	}]
	if err := a.do(ctx, http.MethodGet, "/api/v3/flows/instances/?slug="+url.QueryEscape(slug), nil, &out); err != nil {
		return "", err
	}
	for _, f := range out.Results {
		if f.Slug == slug {
			return f.PK, nil
		}
	}
	return "", fmt.Errorf("Authentik has no flow %q: pick an existing one in the plan", slug)
}

func (a *API) keyPK(ctx context.Context, name string) (string, error) {
	var out listResult[struct {
		PK   string `json:"pk"`
		Name string `json:"name"`
	}]
	if err := a.do(ctx, http.MethodGet, "/api/v3/crypto/certificatekeypairs/?has_key=true&name="+url.QueryEscape(name), nil, &out); err != nil {
		return "", err
	}
	for _, k := range out.Results {
		if k.Name == name {
			return k.PK, nil
		}
	}
	return "", fmt.Errorf("Authentik has no signing key %q: pick an existing one in the plan", name)
}

func (a *API) scopePKs(ctx context.Context, scopes []string) ([]string, error) {
	var out listResult[struct {
		PK    string `json:"pk"`
		Scope string `json:"scope_name"`
	}]
	if err := a.do(ctx, http.MethodGet, "/api/v3/propertymappings/provider/scope/?page_size=200", nil, &out); err != nil {
		return nil, err
	}
	byScope := map[string]string{}
	for _, m := range out.Results {
		if _, seen := byScope[m.Scope]; !seen {
			byScope[m.Scope] = m.PK
		}
	}
	var pks []string
	for _, s := range scopes {
		pk, ok := byScope[s]
		if !ok {
			return nil, fmt.Errorf("Authentik has no %q scope mapping", s)
		}
		pks = append(pks, pk)
	}
	return pks, nil
}

// FindProvider returns the provider named name, or nil.
func (a *API) FindProvider(ctx context.Context, name string) (*RemoteProvider, error) {
	var out listResult[RemoteProvider]
	if err := a.do(ctx, http.MethodGet, "/api/v3/providers/oauth2/?name="+url.QueryEscape(name), nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Results {
		if out.Results[i].Name == name {
			return &out.Results[i], nil
		}
	}
	return nil, nil
}

// FindApplication returns the application with slug, or nil.
func (a *API) FindApplication(ctx context.Context, slug string) (*Application, error) {
	var out Application
	err := a.do(ctx, http.MethodGet, "/api/v3/core/applications/"+url.PathEscape(slug)+"/", nil, &out)
	var ae *APIError
	if asAPIError(err, &ae) && ae.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if ae, ok := err.(*APIError); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func redirectsOf(uris []string) []remoteRedirect {
	out := make([]remoteRedirect, 0, len(uris))
	for _, u := range uris {
		out = append(out, remoteRedirect{Mode: "strict", URL: u})
	}
	return out
}

func sameSet(a, b []string) bool {
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return strings.Join(x, "\n") == strings.Join(y, "\n")
}

// Diff lists, by name, what an existing provider would change. The client
// secret is compared in constant time and reported only as "differs".
func providerDiff(have *RemoteProvider, spec Spec, secret string, flowPK, keyPK, invalidPK string, mappings []string) []string {
	var changes []string
	if have.ClientType != "confidential" {
		changes = append(changes, "client type becomes confidential")
	}
	// Authentik 2026.x creates a provider with no grant types and then
	// refuses every authorize request ("Invalid grant_type for provider"),
	// so the code flow must be named explicitly (S286 KVM review).
	if !sameSet(have.GrantTypes, []string{"authorization_code"}) {
		changes = append(changes, "grant types")
	}
	if have.ClientID != spec.ClientID {
		changes = append(changes, "client id")
	}
	var haveURIs []string
	strict := true
	for _, r := range have.RedirectURIs {
		haveURIs = append(haveURIs, r.URL)
		if r.Mode != "strict" {
			strict = false
		}
	}
	if !sameSet(haveURIs, spec.RedirectURIs) || !strict {
		changes = append(changes, "redirect URIs")
	}
	if flowPK != "" && have.AuthFlow != flowPK {
		changes = append(changes, "authorization flow")
	}
	if invalidPK != "" && have.InvalidFlow != invalidPK {
		changes = append(changes, "invalidation flow")
	}
	if keyPK != "" && have.SigningKey != keyPK {
		changes = append(changes, "signing key")
	}
	if mappings != nil && !sameSet(have.Mappings, mappings) {
		changes = append(changes, "scopes")
	}
	if have.SubMode != spec.SubMode {
		changes = append(changes, "subject mode")
	}
	if have.IssuerMode != "per_provider" {
		changes = append(changes, "issuer mode")
	}
	if secret != "" && subtle.ConstantTimeCompare([]byte(have.ClientSecret), []byte(secret)) != 1 {
		changes = append(changes, "client secret")
	}
	return changes
}

// Inspect reports, without changing anything, what applying spec would do
// in Authentik. secret may be empty (no secret sealed yet).
func (a *API) Inspect(ctx context.Context, spec Spec, secret string) ([]Object, error) {
	flow, err := a.flowPK(ctx, spec.AuthorizationFlow)
	if err != nil {
		return nil, err
	}
	invalid, err := a.flowPK(ctx, spec.InvalidationFlow)
	if err != nil {
		return nil, err
	}
	key, err := a.keyPK(ctx, spec.SigningKey)
	if err != nil {
		return nil, err
	}
	mappings, err := a.scopePKs(ctx, spec.Scopes)
	if err != nil {
		return nil, err
	}
	if spec.Access != nil && spec.Access.ClaimMapping != "" {
		m, err := a.findClaimMapping(ctx, spec.Access.ClaimMapping)
		if err != nil {
			return nil, err
		}
		if m != nil {
			mappings = append(mappings, m.PK)
		} else {
			mappings = append(mappings, "(new groups claim mapping)")
		}
	}
	prov, err := a.FindProvider(ctx, spec.ProviderName)
	if err != nil {
		return nil, err
	}
	app, err := a.FindApplication(ctx, spec.AppSlug)
	if err != nil {
		return nil, err
	}
	var objects []Object
	po := Object{Kind: "OAuth2/OpenID provider", Name: spec.ProviderName}
	switch {
	case prov == nil:
		po.Action = ActionCreate
	default:
		po.Changes = providerDiff(prov, spec, secret, flow, key, invalid, mappings)
		po.Action = ActionUnchanged
		if len(po.Changes) > 0 {
			po.Action = ActionUpdate
		}
	}
	objects = append(objects, po)
	ao := Object{Kind: "Application", Name: spec.AppName + " (" + spec.AppSlug + ")"}
	switch {
	case app == nil:
		ao.Action = ActionCreate
	default:
		ao.Action = ActionUnchanged
		if prov == nil || app.Provider == nil || *app.Provider != prov.PK {
			ao.Action = ActionUpdate
			ao.Changes = []string{"bound provider"}
		}
	}
	objects = append(objects, ao)
	if spec.Access != nil {
		objs, err := a.inspectAccess(ctx, *spec.Access, app)
		if err != nil {
			return nil, err
		}
		objects = append(objects, objs...)
	}
	if spec.RecoveryFlow != "" {
		o, err := a.inspectRecovery(ctx, spec.RecoveryFlow)
		if err != nil {
			return nil, err
		}
		objects = append(objects, o)
	}
	return objects, nil
}

// Reconcile creates or updates the provider and the application so they
// match spec, and is a no-op when they already do.
func (a *API) Reconcile(ctx context.Context, spec Spec, secret string, step func(string)) error {
	if err := a.reconcileApp(ctx, spec, secret, step); err != nil {
		return err
	}
	if spec.RecoveryFlow != "" {
		return a.ensureRecovery(ctx, spec.RecoveryFlow, step)
	}
	return nil
}

func (a *API) reconcileApp(ctx context.Context, spec Spec, secret string, step func(string)) error {
	flow, err := a.flowPK(ctx, spec.AuthorizationFlow)
	if err != nil {
		return err
	}
	invalid, err := a.flowPK(ctx, spec.InvalidationFlow)
	if err != nil {
		return err
	}
	key, err := a.keyPK(ctx, spec.SigningKey)
	if err != nil {
		return err
	}
	mappings, err := a.scopePKs(ctx, spec.Scopes)
	if err != nil {
		return err
	}
	if spec.Access != nil {
		if err := spec.Access.Validate(); err != nil {
			return err
		}
		if spec.Access.ClaimMapping != "" {
			// Last, so its groups claim is the one that stands.
			pk, err := a.ensureClaimMapping(ctx, *spec.Access, step)
			if err != nil {
				return err
			}
			mappings = append(mappings, pk)
		}
	}
	prov, err := a.FindProvider(ctx, spec.ProviderName)
	if err != nil {
		return err
	}
	body := map[string]any{
		"name":                       spec.ProviderName,
		"authorization_flow":         flow,
		"invalidation_flow":          invalid,
		"client_type":                "confidential",
		"grant_types":                []string{"authorization_code"},
		"client_id":                  spec.ClientID,
		"client_secret":              secret,
		"redirect_uris":              redirectsOf(spec.RedirectURIs),
		"signing_key":                key,
		"property_mappings":          mappings,
		"sub_mode":                   spec.SubMode,
		"issuer_mode":                "per_provider",
		"include_claims_in_id_token": true,
	}
	var provPK int
	switch {
	case prov == nil:
		var created RemoteProvider
		if err := a.do(ctx, http.MethodPost, "/api/v3/providers/oauth2/", body, &created); err != nil {
			return err
		}
		provPK = created.PK
		step("created the provider")
	case len(providerDiff(prov, spec, secret, flow, key, invalid, mappings)) == 0:
		provPK = prov.PK
		step("provider already matches")
	default:
		if err := a.do(ctx, http.MethodPatch, fmt.Sprintf("/api/v3/providers/oauth2/%d/", prov.PK), body, nil); err != nil {
			return err
		}
		provPK = prov.PK
		step("updated the provider")
	}
	app, err := a.FindApplication(ctx, spec.AppSlug)
	if err != nil {
		return err
	}
	launch := ""
	if len(spec.Origins) > 0 {
		launch = spec.Origins[0] + "/"
	}
	appBody := map[string]any{
		"name": spec.AppName, "slug": spec.AppSlug, "provider": provPK,
		"meta_launch_url": launch, "open_in_new_tab": false,
	}
	switch {
	case app == nil:
		if err := a.do(ctx, http.MethodPost, "/api/v3/core/applications/", appBody, nil); err != nil {
			return err
		}
		step("created the application")
	case app.Provider != nil && *app.Provider == provPK:
		step("application already matches")
	default:
		if err := a.do(ctx, http.MethodPatch, "/api/v3/core/applications/"+url.PathEscape(spec.AppSlug)+"/", appBody, nil); err != nil {
			return err
		}
		step("rebound the application")
	}
	if spec.Access != nil {
		if app == nil || app.PK == "" {
			if app, err = a.FindApplication(ctx, spec.AppSlug); err != nil {
				return err
			}
		}
		if app == nil {
			return fmt.Errorf("Authentik has no application %q after creating it", spec.AppSlug)
		}
		return a.reconcileAccess(ctx, *spec.Access, app.PK, step)
	}
	return nil
}

// Remove deletes the application with slug and the provider named name,
// the objects a Reconcile with the same spec created (S298). Objects that
// are already gone are fine; nothing else in Authentik is touched.
func (a *API) Remove(ctx context.Context, spec Spec, step func(string)) error {
	app, err := a.FindApplication(ctx, spec.AppSlug)
	if err != nil {
		return err
	}
	if app == nil {
		step("application already gone")
	} else {
		if err := a.do(ctx, http.MethodDelete, "/api/v3/core/applications/"+url.PathEscape(spec.AppSlug)+"/", nil, nil); err != nil {
			return err
		}
		step("deleted the application")
	}
	prov, err := a.FindProvider(ctx, spec.ProviderName)
	if err != nil {
		return err
	}
	if prov == nil {
		step("provider already gone")
		if spec.Access != nil {
			return a.removeAccess(ctx, *spec.Access, step)
		}
		return nil
	}
	if prov.ClientID != spec.ClientID {
		return fmt.Errorf("the Authentik provider %q has client id %q, not %q: it is not the one Homestead created, so it is left alone", spec.ProviderName, prov.ClientID, spec.ClientID)
	}
	if err := a.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v3/providers/oauth2/%d/", prov.PK), nil, nil); err != nil {
		return err
	}
	step("deleted the provider")
	if spec.Access != nil {
		return a.removeAccess(ctx, *spec.Access, step)
	}
	return nil
}
