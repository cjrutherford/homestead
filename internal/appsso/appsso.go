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
	// BundleFile is the combined trust bundle (S410): it lives in the
	// same directory, so the existing read-only mount delivers it.
	BundleFile   = CAMount + "/" + bundleHostFn
	bundleHostFn = "bundle.pem"
	// JavaStoreFile is the Java trust store (S411), beside the bundle.
	JavaStoreFile   = CAMount + "/" + javaStoreHostFn
	javaStoreHostFn = "bundle.jks"
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
	// Access is the groups and bindings the last apply set up (S314). Nil
	// on a sign-in from before: it binds nothing and is open to every
	// Authentik user until the owner approves a re-plan.
	Access *sso.Access `json:"access,omitempty"`
}

// Records keeps the app_sign_in rows.
type Records struct{ DB *sql.DB }

// Get returns the package's record; ok is false when sign-in is off.
func (r Records) Get(pkg string) (rec Record, ok bool, err error) {
	var uris, updated, access string
	err = r.DB.QueryRow(`SELECT package, adapter, client_id, issuer, redirect_uris, plugin_version, actor, updated_at, access
		FROM app_sign_in WHERE package = ?`, pkg).
		Scan(&rec.Package, &rec.Adapter, &rec.ClientID, &rec.Issuer, &uris, &rec.PluginVersion, &rec.Actor, &updated, &access)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("appsso: read %s: %w", pkg, err)
	}
	_ = json.Unmarshal([]byte(uris), &rec.RedirectURIs)
	if access != "" {
		var x sso.Access
		if json.Unmarshal([]byte(access), &x) == nil {
			rec.Access = &x
		}
	}
	rec.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return rec, true, nil
}

// Save stores rec.
func (r Records) Save(rec Record) error {
	uris, _ := json.Marshal(rec.RedirectURIs)
	access := ""
	if rec.Access != nil {
		raw, _ := json.Marshal(rec.Access)
		access = string(raw)
	}
	_, err := r.DB.Exec(`INSERT INTO app_sign_in (package, adapter, client_id, issuer, redirect_uris, plugin_version, actor, updated_at, access)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(package) DO UPDATE SET adapter = excluded.adapter, client_id = excluded.client_id,
		issuer = excluded.issuer, redirect_uris = excluded.redirect_uris, plugin_version = excluded.plugin_version,
		actor = excluded.actor, updated_at = excluded.updated_at, access = excluded.access`,
		rec.Package, rec.Adapter, rec.ClientID, rec.Issuer, string(uris), rec.PluginVersion, rec.Actor,
		time.Now().UTC().Format(time.RFC3339Nano), access)
	if err != nil {
		return fmt.Errorf("appsso: save %s: %w", rec.Package, err)
	}
	return nil
}

// Packages lists the packages with sign-in recorded, sorted.
func (r Records) Packages() ([]string, error) {
	rows, err := r.DB.Query(`SELECT package FROM app_sign_in ORDER BY package`)
	if err != nil {
		return nil, fmt.Errorf("appsso: list: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var pkg string
		if err := rows.Scan(&pkg); err != nil {
			return nil, fmt.Errorf("appsso: list: %w", err)
		}
		out = append(out, pkg)
	}
	return out, rows.Err()
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
// names, all https. A path app (ADR-044, routes resolved with
// docker.ServedRoutes) has its path appended without the trailing slash,
// so an adapter's origin+"/..." lands inside it.
func Origins(routes []docker.Route) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range routes {
		for _, n := range r.AllNames() {
			o := "https://" + strings.ToLower(string(n)) + strings.TrimSuffix(r.Path, "/")
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
	// OwnerSub is the linked owner's subject at the dashboard's issuer;
	// empty when no owner is linked (S314).
	OwnerSub string
	// PublicAuthentikURL is Authentik's public address while the edge
	// answers it on this box (ADR-047): an app with a public name signs
	// in there. PublicWaiting, when set, says why a published app still
	// signs in at the local address.
	PublicAuthentikURL string
	PublicWaiting      string
	// StackNeedsSecret is set when the installed stack's Compose file
	// lacks the wiring for the app's declared sign-in secret (installed
	// before the manifest declared it, S403): applying rewrites it.
	StackNeedsSecret bool
	// OwnerEnv is the app service's environment in the override file
	// now, so the plan can name values that sign-in replaces (S404).
	OwnerEnv map[string]string
	// EdgeSource is the edge network's address range, what a gated app
	// trusts identity headers from (S418). Accounts and PeopleUsernames
	// feed the forward-auth refusals.
	EdgeSource string
	// HostBlockRequired is set for an app that declares
	// forwardauth.requireshostblock; HostBlockProblem is why Homestead
	// cannot keep other users off its port now ("" when it can, S421).
	HostBlockRequired bool
	HostBlockProblem  string
	Accounts          *AppAccounts
	// AccountsFn reads the accounts when a plan needs them, so a status
	// that is not planning never opens the app's database.
	AccountsFn      func() *AppAccounts
	PeopleUsernames []string
	// OtherGates are the other apps at a path behind the shared Authentik
	// gate (ADR-062), read from the edge table.
	OtherGates []OtherGate
}

// published reports an app with a public name: its own tunnel names, or
// a path published on the dashboard's public name (ADR-044).
func published(routes []docker.Route) bool {
	for _, r := range routes {
		if len(r.Tunnel) > 0 {
			return true
		}
	}
	return false
}

// Offered reports whether the app can be signed in to through Authentik
// right now, and if not, why.
func (in Inputs) Offered() (bool, string) {
	if in.Manifest.SSO.ForwardAuth != nil {
		return offeredForward(in)
	}
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
	Enabled     bool   `json:"enabled"`
	Adapter     string `json:"adapter"`
	Provider    string `json:"provider"`
	Application string `json:"application"`
	ClientID    string `json:"client_id"`
	Issuer      string `json:"issuer"`
	// ReplacedIssuer is the applied issuer this plan moves away from
	// (ADR-047: publishing moves an app to Authentik's public address).
	ReplacedIssuer string                `json:"replaced_issuer,omitempty"`
	RedirectURIs   []string              `json:"redirect_uris"`
	AppSettings    []string              `json:"app_settings"`
	Plugin         *catalog.PinnedPlugin `json:"plugin,omitempty"`
	SignInURL      string                `json:"sign_in_url,omitempty"`
	// PublicSignInURL is where visitors through the tunnel start, set
	// while the app has a public name.
	PublicSignInURL string `json:"public_sign_in_url,omitempty"`
	// Access is the groups, owner membership, bindings and groups claim
	// mapping (ADR-043), and AdminRoles says how the app's admin role is
	// set from the admin group.
	Access     *sso.Access `json:"access,omitempty"`
	AdminRoles string      `json:"admin_roles,omitempty"`
	// Gate is set for a forward-auth app (S418): the edge gate.
	Gate     *GatePlan `json:"gate,omitempty"`
	Changes  []string  `json:"changes"`
	Warnings []string  `json:"warnings"`
	Digest   string    `json:"digest"`
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
		Access:       p.Access,
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
	if m.SSO.ForwardAuth != nil {
		return buildForwardPlan(in, enable)
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
	exclusive := false
	if x, ok := adapter.(interface{ Exclusive() bool }); ok {
		exclusive = x.Exclusive()
	}
	if !enable {
		if in.Applied == nil {
			p.Changes = append(p.Changes, "Nothing changes: sign-in with Authentik is already off.")
		} else {
			p.Issuer, p.RedirectURIs = in.Applied.Issuer, in.Applied.RedirectURIs
			p.AppSettings = adapter.OffSettings()
			x := removeAccess(m.Name, m.Title)
			p.Access = &x
			p.Changes = append(p.Changes,
				fmt.Sprintf("Turn off sign-in with Authentik in %s.", m.Title),
				fmt.Sprintf("Delete the application %s and the provider %q from Authentik.", p.Application, p.Provider),
				"Forget the sealed client secret and take the Authentik host entry and appliance root out of the app's override file.",
				fmt.Sprintf("Delete the Authentik groups %s and %s and the groups claim mapping %q. The shared group %s stays.", x.Groups[0], x.Groups[1], x.ClaimMapping, sso.OwnerGroup),
				fmt.Sprintf("People who signed in through Authentik keep their %s accounts; the local admin keeps working.", m.Title))
			if exclusive {
				p.Changes[len(p.Changes)-1] = fmt.Sprintf("People who signed in through Authentik keep their %s accounts. %s returns to its own login form, so its local password login works again.", m.Title, m.Title)
			}
		}
		p.Digest = p.digest()
		return p, nil
	}
	if ok, why := in.Offered(); !ok {
		return Plan{}, unavailable("%s", why)
	}
	base := strings.TrimSuffix(in.Dashboard.AuthentikURL, "/")
	if published(in.Routes) || m.ServedByPath() {
		// Browsers through the tunnel cannot reach the local name, so a
		// published app signs in at Authentik's public address; the edge
		// answers it on this box for the app's own calls (ADR-047). An
		// app served by path shares the dashboard's origin, so it signs
		// in there whether or not it is published itself (S408).
		if in.PublicAuthentikURL != "" {
			base = strings.TrimSuffix(in.PublicAuthentikURL, "/")
		} else if in.PublicWaiting != "" && published(in.Routes) {
			p.Warnings = append(p.Warnings, fmt.Sprintf("Visitors through the tunnel cannot sign in to %s with Authentik yet: %s", m.Title, in.PublicWaiting))
		}
	}
	p.Issuer = sso.IssuerFor(base, p.ClientID)
	if in.Applied != nil && in.Applied.Issuer != p.Issuer {
		p.ReplacedIssuer = in.Applied.Issuer
	}
	origins := Origins(in.Routes)
	p.RedirectURIs = adapter.RedirectURIs(origins)
	p.SignInURL = signInURL(adapter, m, origins[0])
	for _, o := range origins {
		if u, err := url.Parse(o); err == nil && !docker.IsPrivateName(docker.Hostname(u.Hostname())) {
			p.PublicSignInURL = signInURL(adapter, m, o)
			break
		}
	}
	p.AppSettings = adapter.OnSettings(p.Issuer, p.ClientID)
	roles := adapter.Roles(groupNames(m.Name))
	access := buildAccess(m.Name, m.Title, in.OwnerSub, roles)
	p.Access, p.AdminRoles = &access, roles.Note
	noteRoles(&p, roles)
	authHost := hostOf(base)
	p.Warnings = append(p.Warnings, replacedOwnerEnv(in, m, adapter, p)...)
	if in.StackNeedsSecret {
		p.AppSettings = append(p.AppSettings, fmt.Sprintf("%s's stack file is rewritten to deliver the sign-in secret: only the secret's entry, its reference and the file-path variable are added to compose.yaml, then the app restarts.", m.Title))
	}
	p.AppSettings = append(p.AppSettings,
		fmt.Sprintf("Service %s resolves %s to the appliance edge and trusts the appliance root (%s, read-only).", m.SSO.OIDC.Service, authHost, CAFile))
	switch {
	case in.Applied == nil:
		p.Changes = append(p.Changes,
			fmt.Sprintf("Create the OAuth2/OpenID provider %q (client id %s, confidential) and the application %s in Authentik.", p.Provider, p.ClientID, p.Application),
			"Generate a client secret and seal it in the secret store; it is never shown or logged.",
			fmt.Sprintf("Set up %s to sign people in through Authentik. Accounts are created at first sign-in; the local admin keeps working.", m.Title))
		if exclusive {
			p.Changes[len(p.Changes)-1] = fmt.Sprintf("Set up %s to sign people in through Authentik only. Accounts are created at first sign-in. %s's own login form stops working while sign-in is on.", m.Title, m.Title)
		}
	case sameList(in.Applied.RedirectURIs, p.RedirectURIs) && in.Applied.Issuer == p.Issuer && pluginState(p.Plugin) == in.Applied.PluginVersion && sameAccess(in.Applied.Access, p.Access):
		p.Changes = append(p.Changes, "Nothing changes: sign-in with Authentik is on and matches the app's names. Applying checks Authentik and the app again.")
	default:
		p.Changes = append(p.Changes, "Update the provider and the app to match the app's current names:")
		if p.ReplacedIssuer != "" {
			p.Changes = append(p.Changes, fmt.Sprintf("Sign in at %s: the issuer %s replaces %s.", base, p.Issuer, p.ReplacedIssuer))
		}
		for _, line := range listDiff(in.Applied.RedirectURIs, p.RedirectURIs) {
			p.Changes = append(p.Changes, line)
		}
		if was, _, _ := strings.Cut(in.Applied.PluginVersion, "+"); pluginVersion(p.Plugin) != was {
			p.Changes = append(p.Changes, fmt.Sprintf("Plugin %s %s replaces %s.", p.Plugin.Name, p.Plugin.Version, was))
		} else if pluginState(p.Plugin) != in.Applied.PluginVersion {
			p.Changes = append(p.Changes, fmt.Sprintf("Plugin %s settings and the login page's Sign in with Authentik button are rewritten.", p.Plugin.Name))
		}
		if in.Applied.Access == nil {
			p.Changes = append(p.Changes, "This sign-in is open to every Authentik user today. Applying closes it to the owner and the people granted below.")
		}
	}
	p.Changes = append(p.Changes, "Who can sign in: the owner, and members of the app's two groups (admins are also users). Grant people by adding them to the group in Authentik; the People page (coming next) will do it here.")
	p.Changes = append(p.Changes, access.Lines()...)
	if p.Plugin != nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s uses the third-party plugin %s %s, downloaded from %s and refused unless its SHA-256 is %s.",
			m.Title, p.Plugin.Name, p.Plugin.Version, p.Plugin.URL, p.Plugin.SHA256))
	}
	if w, ok := adapter.(interface{ PlanWarnings() []string }); ok {
		p.Warnings = append(p.Warnings, w.PlanWarnings()...)
	}
	p.Warnings = append(p.Warnings, "Applying needs an Authentik API token, which can do anything an Authentik administrator can. It is used for this apply only, unless you kept the dashboard sign-in token.")
	p.Digest = p.digest()
	return p, nil
}

// noteRoles puts the adapter's admin mapping in the plan, or says the app
// cannot map it and its admins stay local (ADR-043 §3).
func noteRoles(p *Plan, roles RoleMapping) {
	if !roles.Mapped {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s cannot take its admin role from a group: %s Its admins stay local accounts.", p.Title, roles.Note))
		return
	}
	p.AppSettings = append(p.AppSettings, roles.Note)
}

func pluginVersion(p *catalog.PinnedPlugin) string {
	if p == nil {
		return ""
	}
	return p.Version
}

// pluginSettingsRev names the generation of the plugin settings Homestead
// writes. It is stored after the version so that changing the settings
// replans an app that was set up with the old ones.
const pluginSettingsRev = "start3"

// pluginState is what a record keeps in PluginVersion: the plugin version
// and the settings generation, "4.0.0.4+start3".
func pluginState(p *catalog.PinnedPlugin) string {
	if p == nil {
		return ""
	}
	return p.Version + "+" + pluginSettingsRev
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
	// PublicSignInURL is the sign-in address on the app's public name.
	PublicSignInURL string `json:"public_sign_in_url,omitempty"`
	// TokenKept says the dashboard sign-in token is sealed, so applying
	// needs no token.
	TokenKept bool `json:"token_kept"`
	// OpenToAll is true for a sign-in made before groups: Authentik has no
	// binding for it, so every Authentik user can sign in. Nothing changes
	// until the owner approves a re-plan (ADR-043 §2).
	OpenToAll bool `json:"open_to_all"`
}

// BuildStatus reports sign-in for one app.
func BuildStatus(in Inputs, tokenKept bool) Status {
	st := Status{Package: in.Manifest.Name, Applied: in.Applied, Enabled: in.Applied != nil, TokenKept: tokenKept}
	if in.Manifest.SSO.OIDC != nil {
		st.Adapter, st.Plugin = in.Manifest.SSO.OIDC.Adapter, in.Manifest.SSO.OIDC.Plugin
	}
	if in.Manifest.SSO.ForwardAuth != nil {
		st.Adapter = ForwardAdapter
	}
	st.Offered, st.Reason = in.Offered()
	if !in.Installed {
		st.Offered, st.Reason = false, fmt.Sprintf("%s is not installed.", in.Manifest.Title)
	}
	st.OpenToAll = st.Enabled && in.Applied.Access == nil
	if st.Enabled && st.Offered {
		if p, err := BuildPlan(in, true); err == nil {
			st.SignInURL, st.PublicSignInURL = p.SignInURL, p.PublicSignInURL
			want := pluginState(p.Plugin)
			if p.Gate != nil {
				want = p.Gate.Revision
			}
			st.Replan = !sameList(in.Applied.RedirectURIs, p.RedirectURIs) || in.Applied.Issuer != p.Issuer ||
				in.Applied.PluginVersion != want || !sameAccess(in.Applied.Access, p.Access)
		}
	}
	return st
}

// signInURL is where a person starts signing in. Jellyfin serves under
// its app path (BaseUrl) even on a name of its own, so an origin without
// that path gets it (S400); one that has it already is left alone.
func signInURL(a Adapter, m catalog.Manifest, origin string) string {
	if _, ok := a.(jellyfin); ok {
		if path := strings.TrimSuffix(m.AppPath(), "/"); path != "" && !strings.HasSuffix(origin, path) {
			origin += path
		}
	}
	return a.SignInURL(origin)
}

// replacedOwnerEnv names the variables the owner set in the override file
// whose value differs from what sign-in sets: sign-in's value wins while
// it is on (S404). Values equal to what an earlier apply set are not the
// owner's.
func replacedOwnerEnv(in Inputs, m catalog.Manifest, adapter Adapter, p Plan) []string {
	if len(in.OwnerEnv) == 0 {
		return nil
	}
	settings := Settings{Issuer: p.Issuer, ClientID: p.ClientID, Groups: groupNames(m.Name)}
	want := wiring(m, adapter, p.Issuer, settings).Env
	var earlier map[string]string
	if in.Applied != nil {
		old := settings
		old.Issuer = in.Applied.Issuer
		earlier = wiring(m, adapter, old.Issuer, old).Env
	}
	var keys []string
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		cur, ok := in.OwnerEnv[k]
		if !ok || cur == want[k] || cur == earlier[k] {
			continue
		}
		out = append(out, fmt.Sprintf("%s set by the owner is replaced while sign-in is on.", k))
	}
	return out
}
