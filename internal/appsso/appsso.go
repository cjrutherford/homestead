// Package appsso signs people in to catalog apps through the appliance's
// Authentik (S298, ADR-039 §3, ADR-040): one OAuth2/OpenID provider and
// application per app, a sealed client secret, and a first-party adapter
// that sets the app's own OIDC settings. Every change is planned,
// approved by digest and audited without secret values.
package appsso

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/sso"
)

// Names of what Homestead creates for an app; fixed per package so a
// re-plan finds what an earlier apply made.
func ClientID(pkg string) string       { return "homestead-" + pkg }
func ProviderName(title string) string { return "Homestead: " + title }

// SecretName is the sealed client secret's name (ADR-021).
func SecretName(pkg string) string {
	return "sys_sso_app_" + strings.ReplaceAll(pkg, "-", "_") + "_secret"
}

// CAMount is where the appliance root is mounted, read-only, in the
// service that talks to Authentik; CAFile is the root inside it.
const (
	CAMount  = "/etc/homestead/ca"
	CAFile   = CAMount + "/ca.crt"
	CADir    = "homestead-ca"
	caHostFn = "ca.crt"
)

// ErrUnavailable marks a plan that cannot be made (HTTP 422).
var ErrUnavailable = errors.New("app sign-in unavailable")

func unavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnavailable, fmt.Sprintf(format, args...))
}

// Record is what an apply stored (migration 50).
type Record struct {
	Package       string    `json:"package"`
	Adapter       string    `json:"adapter"`
	ClientID      string    `json:"client_id"`
	Issuer        string    `json:"issuer"`
	RedirectURIs  []string  `json:"redirect_uris"`
	PluginVersion string    `json:"plugin_version,omitempty"`
	Actor         string    `json:"actor,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Records keeps the app_sign_in rows.
type Records struct{ DB *sql.DB }

// Get returns the package's record; ok is false when sign-in is off.
func (r Records) Get(pkg string) (rec Record, ok bool, err error) {
	var uris, updated string
	err = r.DB.QueryRow(`SELECT package, adapter, client_id, issuer, redirect_uris, plugin_version, actor, updated_at
		FROM app_sign_in WHERE package = ?`, pkg).
		Scan(&rec.Package, &rec.Adapter, &rec.ClientID, &rec.Issuer, &uris, &rec.PluginVersion, &rec.Actor, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("appsso: read %s: %w", pkg, err)
	}
	_ = json.Unmarshal([]byte(uris), &rec.RedirectURIs)
	rec.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return rec, true, nil
}

// Save stores rec.
func (r Records) Save(rec Record) error {
	uris, _ := json.Marshal(rec.RedirectURIs)
	_, err := r.DB.Exec(`INSERT INTO app_sign_in (package, adapter, client_id, issuer, redirect_uris, plugin_version, actor, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(package) DO UPDATE SET adapter = excluded.adapter, client_id = excluded.client_id,
		issuer = excluded.issuer, redirect_uris = excluded.redirect_uris, plugin_version = excluded.plugin_version,
		actor = excluded.actor, updated_at = excluded.updated_at`,
		rec.Package, rec.Adapter, rec.ClientID, rec.Issuer, string(uris), rec.PluginVersion, rec.Actor,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("appsso: save %s: %w", rec.Package, err)
	}
	return nil
}

// Delete removes the package's record.
func (r Records) Delete(pkg string) error {
	if _, err := r.DB.Exec(`DELETE FROM app_sign_in WHERE package = ?`, pkg); err != nil {
		return fmt.Errorf("appsso: delete %s: %w", pkg, err)
	}
	return nil
}

// AppRoutes are the edge routes that serve the app: a route to one of
// its served services on a host named for the app, the installer's
// default names (the dashboard's appRoutes uses the same rule; service
// names alone are not enough, Authentik and Immich both say "server").
func AppRoutes(m catalog.Manifest, routes []docker.Route) []docker.Route {
	served := map[string]bool{}
	for _, s := range m.Services {
		if s.Exposes != "" {
			served[s.Name] = true
		}
	}
	var out []docker.Route
	for _, r := range routes {
		host := strings.ToLower(string(r.Host))
		if r.Dashboard || !served[r.Target.Host] {
			continue
		}
		if strings.HasPrefix(host, m.Name+".") || strings.Contains(host, "-"+m.Name+".") {
			out = append(out, r)
		}
	}
	return out
}

// Origins are the app's browser origins: every local name its routes
// answer on (the .lan host and its .local name) and their public tunnel
// names, all https.
func Origins(routes []docker.Route) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range routes {
		for _, n := range r.AllNames() {
			o := "https://" + strings.ToLower(string(n))
			if !seen[o] {
				seen[o] = true
				out = append(out, o)
			}
		}
	}
	return out
}

// Inputs is everything a plan is made from; the server gathers it.
type Inputs struct {
	Manifest  catalog.Manifest
	Installed bool
	// AuthentikInstalled and Dashboard (with DashboardOK) are the
	// preconditions: Authentik installed and dashboard sign-in set up.
	AuthentikInstalled bool
	Dashboard          sso.Spec
	DashboardOK        bool
	// Routes are the app's routes (AppRoutes over the edge table, or an
	// install plan's routes for the wizard's preview).
	Routes  []docker.Route
	Applied *Record
}

// Offered reports whether the app can be signed in to through Authentik
// right now, and if not, why.
func (in Inputs) Offered() (bool, string) {
	switch {
	case in.Manifest.SSO.OIDC == nil:
		return false, fmt.Sprintf("%s has no Authentik sign-in adapter.", in.Manifest.Title)
	case !in.AuthentikInstalled:
		return false, "Install Authentik from the catalog first."
	case !in.DashboardOK:
		return false, "Set up dashboard sign-in with Authentik first (Settings, Sign-in)."
	case len(in.Routes) == 0:
		return false, fmt.Sprintf("%s has no address on the edge yet.", in.Manifest.Title)
	}
	return true, ""
}

// Plan is what approving would do. It names objects and settings, never
// a secret value.
type Plan struct {
	Package string `json:"package"`
	Title   string `json:"title"`
	// Enabled is the state the plan leads to.
	Enabled      bool                  `json:"enabled"`
	Adapter      string                `json:"adapter"`
	Provider     string                `json:"provider"`
	Application  string                `json:"application"`
	ClientID     string                `json:"client_id"`
	Issuer       string                `json:"issuer"`
	RedirectURIs []string              `json:"redirect_uris"`
	AppSettings  []string              `json:"app_settings"`
	Plugin       *catalog.PinnedPlugin `json:"plugin,omitempty"`
	SignInURL    string                `json:"sign_in_url,omitempty"`
	Changes      []string              `json:"changes"`
	Warnings     []string              `json:"warnings"`
	Digest       string                `json:"digest"`
}

func (p Plan) digest() string {
	v := p
	v.Digest = ""
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Spec is the Authentik side for the plan: the dashboard's Authentik
// address, flows and signing key, with the app's own names.
func (p Plan) Spec(dashboard sso.Spec) sso.Spec {
	return sso.Spec{
		AuthentikURL: dashboard.AuthentikURL, Issuer: p.Issuer, ClientID: p.ClientID,
		Origins: originsOf(p.RedirectURIs), RedirectURIs: p.RedirectURIs,
		AuthorizationFlow: dashboard.AuthorizationFlow, InvalidationFlow: dashboard.InvalidationFlow,
		SigningKey: dashboard.SigningKey, Scopes: append([]string(nil), sso.Scopes...), SubMode: "hashed_user_id",
		ProviderName: p.Provider, AppName: p.Title, AppSlug: p.ClientID,
	}
}

func originsOf(uris []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, u := range uris {
		parsed, err := url.Parse(u)
		if err != nil || parsed.Scheme != "https" {
			continue
		}
		o := parsed.Scheme + "://" + parsed.Host
		if !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	return out
}

// BuildPlan plans turning sign-in on (refreshing the redirect URIs when
// it is already on) or off.
func BuildPlan(in Inputs, enable bool) (Plan, error) {
	m := in.Manifest
	if !in.Installed {
		return Plan{}, unavailable("%s is not installed", m.Name)
	}
	if m.SSO.OIDC == nil {
		return Plan{}, unavailable("%s has no Authentik sign-in adapter", m.Title)
	}
	adapter, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{
		Package: m.Name, Title: m.Title, Enabled: enable, Adapter: m.SSO.OIDC.Adapter,
		Provider: ProviderName(m.Title), Application: m.Title + " (" + ClientID(m.Name) + ")",
		ClientID: ClientID(m.Name), Plugin: m.SSO.OIDC.Plugin,
		RedirectURIs: []string{}, AppSettings: []string{}, Changes: []string{}, Warnings: []string{},
	}
	if !enable {
		if in.Applied == nil {
			p.Changes = append(p.Changes, "Nothing changes: sign-in with Authentik is already off.")
		} else {
			p.Issuer, p.RedirectURIs = in.Applied.Issuer, in.Applied.RedirectURIs
			p.AppSettings = adapter.OffSettings()
			p.Changes = append(p.Changes,
				fmt.Sprintf("Turn off sign-in with Authentik in %s.", m.Title),
				fmt.Sprintf("Delete the application %s and the provider %q from Authentik.", p.Application, p.Provider),
				"Forget the sealed client secret and take the Authentik host entry and appliance root out of the app's override file.",
				fmt.Sprintf("People who signed in through Authentik keep their %s accounts; the local admin keeps working.", m.Title))
		}
		p.Digest = p.digest()
		return p, nil
	}
	if ok, why := in.Offered(); !ok {
		return Plan{}, unavailable("%s", why)
	}
	base := strings.TrimSuffix(in.Dashboard.AuthentikURL, "/")
	p.Issuer = sso.IssuerFor(base, p.ClientID)
	origins := Origins(in.Routes)
	p.RedirectURIs = adapter.RedirectURIs(origins)
	p.SignInURL = adapter.SignInURL(origins[0])
	p.AppSettings = adapter.OnSettings(p.Issuer, p.ClientID)
	authHost := hostOf(base)
	p.AppSettings = append(p.AppSettings,
		fmt.Sprintf("Service %s resolves %s to the appliance edge and trusts the appliance root (%s, read-only).", m.SSO.OIDC.Service, authHost, CAFile))
	switch {
	case in.Applied == nil:
		p.Changes = append(p.Changes,
			fmt.Sprintf("Create the OAuth2/OpenID provider %q (client id %s, confidential) and the application %s in Authentik.", p.Provider, p.ClientID, p.Application),
			"Generate a client secret and seal it in the secret store; it is never shown or logged.",
			fmt.Sprintf("Set up %s to sign people in through Authentik. Accounts are created at first sign-in; the local admin keeps working.", m.Title))
	case sameList(in.Applied.RedirectURIs, p.RedirectURIs) && in.Applied.Issuer == p.Issuer && pluginVersion(p.Plugin) == in.Applied.PluginVersion:
		p.Changes = append(p.Changes, "Nothing changes: sign-in with Authentik is on and matches the app's names. Applying checks Authentik and the app again.")
	default:
		p.Changes = append(p.Changes, "Update the provider and the app to match the app's current names:")
		for _, line := range listDiff(in.Applied.RedirectURIs, p.RedirectURIs) {
			p.Changes = append(p.Changes, line)
		}
		if pluginVersion(p.Plugin) != in.Applied.PluginVersion {
			p.Changes = append(p.Changes, fmt.Sprintf("Plugin %s %s replaces %s.", p.Plugin.Name, p.Plugin.Version, in.Applied.PluginVersion))
		}
	}
	if p.Plugin != nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s uses the third-party plugin %s %s, downloaded from %s and refused unless its SHA-256 is %s.",
			m.Title, p.Plugin.Name, p.Plugin.Version, p.Plugin.URL, p.Plugin.SHA256))
	}
	p.Warnings = append(p.Warnings, "Applying needs an Authentik API token, which can do anything an Authentik administrator can. It is used for this apply only, unless you kept the dashboard sign-in token.")
	p.Digest = p.digest()
	return p, nil
}

func pluginVersion(p *catalog.PinnedPlugin) string {
	if p == nil {
		return ""
	}
	return p.Version
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func sameList(a, b []string) bool {
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return strings.Join(x, "\n") == strings.Join(y, "\n")
}

func listDiff(have, want []string) []string {
	in := func(list []string, v string) bool {
		for _, s := range list {
			if s == v {
				return true
			}
		}
		return false
	}
	var out []string
	for _, w := range want {
		if !in(have, w) {
			out = append(out, "Add redirect URI "+w)
		}
	}
	for _, h := range have {
		if !in(want, h) {
			out = append(out, "Remove redirect URI "+h)
		}
	}
	return out
}

// Status is GET /api/v1/catalog/{name}/sign-in.
type Status struct {
	Package string `json:"package"`
	// Offered is whether sign-in can be turned on now; Reason says why not.
	Offered bool    `json:"offered"`
	Reason  string  `json:"reason,omitempty"`
	Enabled bool    `json:"enabled"`
	Applied *Record `json:"applied,omitempty"`
	// Replan is true when the app's names no longer match the applied
	// redirect URIs: a plan is waiting for approval.
	Replan    bool                  `json:"replan"`
	Adapter   string                `json:"adapter,omitempty"`
	Plugin    *catalog.PinnedPlugin `json:"plugin,omitempty"`
	SignInURL string                `json:"sign_in_url,omitempty"`
	// TokenKept says the dashboard sign-in token is sealed, so applying
	// needs no token.
	TokenKept bool `json:"token_kept"`
}

// BuildStatus reports sign-in for one app.
func BuildStatus(in Inputs, tokenKept bool) Status {
	st := Status{Package: in.Manifest.Name, Applied: in.Applied, Enabled: in.Applied != nil, TokenKept: tokenKept}
	if in.Manifest.SSO.OIDC != nil {
		st.Adapter, st.Plugin = in.Manifest.SSO.OIDC.Adapter, in.Manifest.SSO.OIDC.Plugin
	}
	st.Offered, st.Reason = in.Offered()
	if !in.Installed {
		st.Offered, st.Reason = false, fmt.Sprintf("%s is not installed.", in.Manifest.Title)
	}
	if st.Enabled && st.Offered {
		if p, err := BuildPlan(in, true); err == nil {
			st.SignInURL = p.SignInURL
			st.Replan = !sameList(in.Applied.RedirectURIs, p.RedirectURIs) || in.Applied.Issuer != p.Issuer ||
				in.Applied.PluginVersion != pluginVersion(p.Plugin)
		}
	}
	return st
}
