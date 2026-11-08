package appsso

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/sso"
)

// Forward-auth sign-in (S418, ADR-055): an app with no OIDC is gated at the
// edge by Authentik's embedded outpost. The same app.sign-in operation,
// digest and record carry it; only the steps differ.

// ForwardAdapter is the Record and Plan adapter name of a gated app.
const ForwardAdapter = "forwardauth"

// GatePlan is what the gate does, listed in the plan (ADR-055 §7).
type GatePlan struct {
	// Canonical is the one name the app answers on while gated; Redirects
	// are the other names, which answer 308 to it.
	Canonical string   `json:"canonical"`
	Redirects []string `json:"redirects"`
	// Bypass are the paths that skip the gate, each with why it is safe.
	Bypass []catalog.ForwardBypass `json:"bypass"`
	// CopyHeaders are the identity headers the app receives.
	CopyHeaders []string `json:"copy_headers"`
	// AuthentikHost is where the browser is sent to sign in.
	AuthentikHost string `json:"authentik_host"`
	// ExistingUsers are the app's usernames now (lower case).
	ExistingUsers []string `json:"existing_users"`
	// Env are the trust settings written to the app (not secret).
	Env map[string]string `json:"env"`
	// Shared is set for an app at a path (ADR-062): it sits behind the one
	// proxy provider every such app shares, and the edge, not Authentik,
	// decides who may open it, by Groups.
	Shared bool `json:"shared,omitempty"`
	// Groups are the Authentik groups the edge admits to the app (exact
	// match against the outpost's groups header). Only with Shared.
	Groups []string `json:"groups,omitempty"`
	// TunnelOnly is set for a craft app (ADR-062 section 6): the gate
	// covers its public path only and the local names stay open.
	TunnelOnly bool `json:"tunnel_only,omitempty"`
	// ProviderBypass are the shared provider's unauthenticated paths: this
	// app's and those of every other app behind the gate.
	ProviderBypass []string `json:"provider_bypass,omitempty"`
	// OtherApps are the paths of the other apps behind the shared gate.
	// The provider is deleted only when turning this one off leaves none.
	OtherApps []string `json:"other_apps,omitempty"`
	// Revision changes when the gate's rules change (bypass, env, names),
	// so a manifest bump shows as a re-plan.
	Revision string `json:"revision"`
}

// canonicalRoute is the route the gate protects and the one name it
// answers on: the public name when published, otherwise the route's host.
// A path route (ADR-061; routes resolved with docker.ServedRoutes) is
// served on the dashboard's names, so its host and tunnel names are those.
func canonicalRoute(routes []docker.Route) (docker.Route, docker.Hostname, bool) {
	for _, r := range routes {
		if r.Dashboard {
			continue
		}
		if len(r.Tunnel) > 0 {
			return r, r.Tunnel[0], true
		}
		return r, r.Host, true
	}
	return docker.Route{}, "", false
}

func countNamed(routes []docker.Route) int {
	n := 0
	for _, r := range routes {
		if !r.Dashboard {
			n++
		}
	}
	return n
}

// forwardEnv expands the manifest's trust settings.
func forwardEnv(fa *catalog.ForwardAuth, edge string) map[string]string {
	out := map[string]string{}
	for k, v := range fa.Env {
		v = strings.ReplaceAll(v, "{{edge_source}}", edge)
		v = strings.ReplaceAll(v, "{{user_header}}", fa.UserHeader)
		out[k] = v
	}
	return out
}

func forwardAccess(fa *catalog.ForwardAuth, pkg, ownerSub string) sso.Access {
	g := groupNames(pkg)
	all := []string{g.Users, g.Admins, g.Owner}
	if fa.Access == catalog.AccessAdminOnly {
		// No users group is created: the app is for administrators only.
		all = []string{g.Admins, g.Owner}
	}
	return sso.Access{Groups: all, OwnerSub: ownerSub, OwnerOf: append([]string(nil), all...), Bindings: append([]string(nil), all...)}
}

func removeForwardAccess(pkg string) sso.Access {
	g := groupNames(pkg)
	return sso.Access{Groups: []string{g.Users, g.Admins}, OwnerOf: []string{}, Bindings: []string{}}
}

func forwardRevision(fa *catalog.ForwardAuth, bypass []catalog.ForwardBypass, canonical string, redirects []string, env map[string]string, groups []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%v|", canonical, strings.Join(redirects, ","), fa.CopyHeaders)
	if len(groups) > 0 {
		fmt.Fprintf(h, "shared:%s|", strings.Join(groups, ","))
	}
	for _, b := range bypass {
		fmt.Fprintf(h, "%s|", b.Path)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s|", k, env[k])
	}
	return "gate:" + hex.EncodeToString(h.Sum(nil))[:16]
}

// gateFor is the docker gate the plan leads to.
func (g GatePlan) gate() *docker.Gate {
	paths := make([]string, 0, len(g.Bypass))
	for _, b := range g.Bypass {
		paths = append(paths, b.Path)
	}
	return &docker.Gate{Canonical: docker.Hostname(g.Canonical), CopyHeaders: append([]string(nil), g.CopyHeaders...), Bypass: paths,
		Groups: append([]string(nil), g.Groups...), TunnelOnly: g.TunnelOnly}
}

// The one Authentik proxy provider, application and slug that every app at
// a path shares (ADR-062). Authentik matches a request to a provider by
// one external host, and every such app answers on the dashboard's.
const (
	SharedGateProvider = "Homestead: apps gate"
	SharedGateApp      = "Homestead apps gate (homestead-apps-gate)"
	SharedGateSlug     = "homestead-apps-gate"
)

// OtherGate is another app behind the shared gate: its path and the
// bypass regexes it keeps written with that path.
type OtherGate struct {
	Path   string   `json:"path"`
	Bypass []string `json:"bypass"`
}

// providerBypass is the shared provider's unauthenticated paths: mine and
// every other app's, each once, sorted.
func providerBypass(mine []string, others []OtherGate, myPath string) ([]string, []string) {
	seen := map[string]bool{}
	var out, apps []string
	add := func(list []string) {
		for _, b := range list {
			if !seen[b] {
				seen[b] = true
				out = append(out, b)
			}
		}
	}
	add(mine)
	for _, o := range others {
		if o.Path == myPath {
			continue
		}
		apps = append(apps, o.Path)
		add(o.Bypass)
	}
	sort.Strings(out)
	sort.Strings(apps)
	if out == nil {
		out = []string{}
	}
	return out, apps
}

func (g GatePlan) bypassPaths() []string {
	out := make([]string, 0, len(g.Bypass))
	for _, b := range g.Bypass {
		out = append(out, b.Path)
	}
	return out
}

func offeredForward(in Inputs) (bool, string) {
	switch {
	case !in.AuthentikInstalled:
		return false, "Install Authentik from the catalog first."
	case !in.DashboardOK:
		return false, "Set up dashboard sign-in with Authentik first (Settings, Sign-in)."
	case len(in.Routes) == 0:
		return false, fmt.Sprintf("%s has no address on the edge yet.", in.Manifest.Title)
	}
	return true, ""
}

func buildForwardPlan(in Inputs, enable bool) (Plan, error) {
	m := in.Manifest
	fa := m.SSO.ForwardAuth
	p := Plan{
		Package: m.Name, Title: m.Title, Enabled: enable, Adapter: ForwardAdapter,
		Provider: ProviderName(m.Title), Application: m.Title + " (" + ClientID(m.Name) + ")", ClientID: ClientID(m.Name),
		RedirectURIs: []string{}, AppSettings: []string{}, Changes: []string{}, Warnings: []string{},
	}
	if !enable {
		if in.Applied == nil {
			p.Changes = append(p.Changes, "Nothing changes: the Authentik gate is already off.")
			p.Digest = p.digest()
			return p, nil
		}
		p.Issuer, p.RedirectURIs = in.Applied.Issuer, in.Applied.RedirectURIs
		x := removeForwardAccess(m.Name)
		p.Access = &x
		canonical := ""
		if len(p.RedirectURIs) > 0 {
			canonical = hostOf(p.RedirectURIs[0])
		}
		p.Gate = &GatePlan{Canonical: canonical, Redirects: []string{}, Bypass: []catalog.ForwardBypass{}, CopyHeaders: []string{}, ExistingUsers: []string{}, Env: map[string]string{}}
		if route, _, ok := canonicalRoute(in.Routes); ok && route.IsPath() {
			// ADR-062: the provider is shared; it is deleted only with the
			// last app behind it, otherwise this app leaves its paths.
			p.Gate.Shared = true
			p.Gate.ProviderBypass, p.Gate.OtherApps = providerBypass(nil, in.OtherGates, route.Path)
			p.Provider, p.Application = SharedGateProvider, SharedGateApp
		}
		for _, k := range sortedKeys(fa.Env) {
			if v, ok := in.OwnerEnv[k]; ok {
				p.Gate.Env[k] = v
			}
			p.AppSettings = append(p.AppSettings, fmt.Sprintf("%s is removed from %s.", k, m.Title))
		}
		if fa.OwnLoginOff != nil {
			p.Changes = append(p.Changes, fmt.Sprintf("Turn %s's own login back on first, before the gate is removed, so it is never open to anyone who reaches it. To sign in to it afterwards, set a password with: docker exec -it %s npm run reset-password.", m.Title, container(m, fa.Service)))
		}
		if len(fa.Env) > 0 {
			p.Changes = append(p.Changes, fmt.Sprintf("Take the trust settings out of %s first, so it ignores identity headers again.", m.Title))
		}
		removal := fmt.Sprintf("Remove the provider from the embedded outpost, delete the application %s and the provider %q from Authentik.", p.Application, p.Provider)
		if p.Gate.Shared && len(p.Gate.OtherApps) > 0 {
			removal = fmt.Sprintf("Take %s's open paths out of the shared Authentik gate. The gate stays for %s.", m.Title, strings.Join(p.Gate.OtherApps, ", "))
		}
		p.Changes = append(p.Changes,
			fmt.Sprintf("Remove the Authentik gate from the edge: %s returns to a plain reverse proxy and the other names stop redirecting.", m.Title),
			removal,
			fmt.Sprintf("Delete the Authentik groups %s and %s. The shared group %s stays.", x.Groups[0], x.Groups[1], sso.OwnerGroup),
			fmt.Sprintf("Anyone who can reach %s's address can open its own login again, as before the gate. People's %s accounts stay.", m.Title, m.Title))
		if canonical != "" && !docker.IsPrivateName(docker.Hostname(canonical)) {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s is published at %s: turning the gate off leaves only its own login in front of it.", m.Title, canonical))
		}
		p.Digest = p.digest()
		return p, nil
	}
	if ok, why := offeredForward(in); !ok {
		return Plan{}, unavailable("%s", why)
	}
	if in.OwnerSub == "" {
		return Plan{}, unavailable("Link the appliance owner's Authentik account before gating %s, so the owner is not locked out.", m.Title)
	}
	route, canonical, ok := canonicalRoute(in.Routes)
	if !ok {
		return Plan{}, unavailable("%s has no address of its own on the edge yet.", m.Title)
	}
	craft := route.CraftApp != "" && route.IsPath()
	if craft && docker.IsPrivateName(canonical) {
		// ADR-062 section 6: a craft app is gated only where it is public.
		return Plan{}, unavailable("%s is gated on its public path only, and it is not published. Publish it first (it answers 404 on the internet until this gate is on); its local address stays open.", m.Title)
	}
	if n := countNamed(in.Routes); n > 1 {
		return Plan{}, unavailable("%s has %d addresses on the edge; the gate protects one. Remove the extra routes, then plan again.", m.Title, n)
	}
	if in.EdgeSource == "" {
		return Plan{}, unavailable("The edge network's address could not be read; deploy the edge and plan again.")
	}
	if fa.RequiresHostBlock && in.HostBlockProblem != "" {
		return Plan{}, unavailable("%s turns its own login off, which is safe only when nothing but the edge can reach it. %s", m.Title, in.HostBlockProblem)
	}
	var existing []string
	if fa.AccountsFrom != "" {
		if in.Accounts == nil && in.AccountsFn != nil {
			in.Accounts = in.AccountsFn()
		}
		if in.Accounts == nil || in.Accounts.Err != "" {
			why := "not read"
			if in.Accounts != nil {
				why = in.Accounts.Err
			}
			return Plan{}, unavailable("%s's accounts could not be read (%s); the app must be running to plan the gate.", m.Title, why)
		}
		existing = append([]string{}, in.Accounts.Names...)
		if in.Accounts.Admins == 0 {
			return Plan{}, unavailable("%s has no administrator yet: open %s, create the administrator account on its setup screen, then plan again. Otherwise the first person through the gate would become the administrator.", m.Title, m.Title)
		}
		if in.Applied == nil {
			// Once the gate is on, people's accounts are created by it, so
			// a collision is only meaningful for the first plan.
			people := map[string]bool{}
			for _, n := range in.PeopleUsernames {
				people[strings.ToLower(strings.TrimSpace(n))] = true
			}
			var clash []string
			for _, n := range existing {
				if people[n] {
					clash = append(clash, n)
				}
			}
			if len(clash) > 0 {
				return Plan{}, unavailable("%s already has an account named %s, and an active person has the same Authentik username: through the gate that person would sign in as that account. Rename the person or the %s account, then plan again.",
					m.Title, strings.Join(clash, ", "), m.Title)
			}
		}
	}
	// Where the browser is sent to sign in (ADR-055 §8).
	publicName := !docker.IsPrivateName(canonical)
	base := strings.TrimSuffix(in.Dashboard.AuthentikURL, "/")
	if in.PublicAuthentikURL != "" {
		base = strings.TrimSuffix(in.PublicAuthentikURL, "/")
	} else if publicName {
		why := "Authentik has no public address on this box"
		if in.PublicWaiting != "" {
			why = in.PublicWaiting
		}
		return Plan{}, unavailable("%s answers on the public name %s, but visitors could not sign in: %s", m.Title, canonical, why)
	}
	p.Issuer = base
	if in.Applied != nil && in.Applied.Issuer != p.Issuer {
		p.ReplacedIssuer = in.Applied.Issuer
	}
	p.RedirectURIs = []string{"https://" + strings.ToLower(string(canonical))}
	p.SignInURL = p.RedirectURIs[0] + "/"
	bypass := append([]catalog.ForwardBypass{}, fa.Bypass...)
	if route.IsPath() {
		// The app answers at its path on the dashboard's name (ADR-061):
		// sign-in starts there and every bypass is matched with the prefix.
		p.SignInURL = p.RedirectURIs[0] + route.Path
		for i := range bypass {
			bypass[i].Path = docker.PathBypass(route.Path, bypass[i].Path)
		}
	}
	redirects := []string{}
	for _, n := range route.AllNames() {
		if n != canonical && !craft {
			redirects = append(redirects, strings.ToLower(string(n)))
		}
	}
	sort.Strings(redirects)
	var groups []string
	if route.IsPath() {
		// ADR-062: every app at a path shares one Authentik gate on the
		// dashboard's name; the edge admits the app's groups only.
		gn := groupNames(m.Name)
		groups = []string{gn.Users, gn.Admins, gn.Owner}
		if fa.Access == catalog.AccessAdminOnly {
			groups = []string{gn.Admins, gn.Owner}
		}
		p.Provider, p.Application = SharedGateProvider, SharedGateApp
	}
	env := forwardEnv(fa, in.EdgeSource)
	for _, k := range sortedKeys(env) {
		p.AppSettings = append(p.AppSettings, fmt.Sprintf("%s=%s", k, env[k]))
	}
	if existing == nil {
		existing = []string{}
	}
	g := &GatePlan{
		Canonical: strings.ToLower(string(canonical)), Redirects: redirects,
		Bypass: bypass, CopyHeaders: append([]string{}, fa.CopyHeaders...),
		AuthentikHost: base + "/", ExistingUsers: existing,
		Env: env, Revision: forwardRevision(fa, bypass, string(canonical), redirects, env, groups),
	}
	if route.IsPath() {
		g.Shared, g.Groups = true, groups
		g.TunnelOnly = craft
		paths := make([]string, 0, len(bypass))
		for _, b := range bypass {
			paths = append(paths, b.Path)
		}
		g.ProviderBypass, g.OtherApps = providerBypass(paths, in.OtherGates, route.Path)
	}
	p.Gate = g
	access := forwardAccess(fa, m.Name, in.OwnerSub)
	if g.Shared {
		// Authentik binds nobody to the shared application: it signs in
		// anyone with an account, and the edge decides by group.
		access.Bindings = []string{}
	}
	p.Access = &access
	p.AdminRoles = fmt.Sprintf("Who is an administrator inside %s is chosen in %s itself; the gate only decides who can reach it.", m.Title, m.Title)
	sharedLine := fmt.Sprintf("Use the one shared proxy provider %q (single application, external host %s), creating it with the application %s if it is missing, and add it to the embedded outpost (its other providers stay). If %s has a provider of its own from before (ADR-055), it is deleted first and the shared one takes its place; its groups and their members stay.", p.Provider, p.RedirectURIs[0], p.Application, m.Title)
	switch {
	case in.Applied == nil:
		create := fmt.Sprintf("Create the proxy provider %q (single application, external host %s) and the application %s in Authentik, and add the provider to the embedded outpost (its other providers stay).", p.Provider, p.RedirectURIs[0], p.Application)
		if g.Shared {
			create = sharedLine
		}
		p.Changes = append(p.Changes,
			create,
			fmt.Sprintf("Gate %s at the edge: every request is checked with Authentik first. Client-sent identity headers are deleted before anything else, and the gate fails closed if Authentik does not answer.", g.Canonical))
	case sameList(in.Applied.RedirectURIs, p.RedirectURIs) && in.Applied.Issuer == p.Issuer && in.Applied.PluginVersion == g.Revision && sameAccess(in.Applied.Access, p.Access):
		p.Changes = append(p.Changes, "Nothing changes: the Authentik gate is on and matches the app's names. Applying checks Authentik, the edge and the app again.")
	default:
		p.Changes = append(p.Changes, "Update the gate to match the app's current names and rules.")
		if g.Shared {
			p.Changes = append(p.Changes, sharedLine)
		}
		if p.ReplacedIssuer != "" {
			p.Changes = append(p.Changes, fmt.Sprintf("People sign in at %s: it replaces %s.", base, p.ReplacedIssuer))
		}
		for _, line := range listDiff(in.Applied.RedirectURIs, p.RedirectURIs) {
			p.Changes = append(p.Changes, line)
		}
	}
	if len(redirects) > 0 {
		p.Changes = append(p.Changes, fmt.Sprintf("%s answers only on %s; %s answer with a permanent redirect to it.", m.Title, g.Canonical, strings.Join(redirects, ", ")))
	}
	for _, b := range g.Bypass {
		p.Changes = append(p.Changes, fmt.Sprintf("Open without the gate: %s (%s)", b.Path, b.Reason))
	}
	if len(p.AppSettings) > 0 {
		p.Changes = append(p.Changes, fmt.Sprintf("Trust settings written to %s: %s.", m.Title, strings.Join(p.AppSettings, ", ")))
	} else if fa.OwnLoginOff != nil {
		p.Changes = append(p.Changes, fmt.Sprintf("%s is given no identity header and no trust settings.", m.Title))
	} else {
		p.Changes = append(p.Changes, fmt.Sprintf("%s is given no identity header and no trust settings; it keeps its own login behind the gate.", m.Title))
	}
	if fa.OwnLoginOff != nil {
		p.Changes = append(p.Changes,
			fmt.Sprintf("Turn %s's own login off, so you sign in once, to Authentik. This runs after the gate is on and is safe because only administrators and the owner pass the gate, and Homestead keeps every other host user and container off its port (the host block); sign-in is refused if the block cannot be kept.", m.Title),
			fmt.Sprintf("Use %s's built-in SQLite database (chosen now if it has not been set up), create one %s account named after you with a random password nobody knows if it has none, and restart %s. An existing account and its password are kept.", m.Title, m.Title, m.Title),
			fmt.Sprintf("Running a command inside %s (pinned to version %s; it refuses any other) changes its own database: %s.", m.Title, m.Version, describeArgv(fa.OwnLoginOff.OnApply)))
	}
	if g.TunnelOnly {
		p.Changes = append(p.Changes, fmt.Sprintf("%s's local address stays open as before (no sign-in on your network); the gate covers its public path only.", m.Title))
	}
	if g.Shared {
		p.Changes = append(p.Changes, fmt.Sprintf("Who can reach it: Authentik signs the person in, then the edge admits only members of %s (an exact match on the group name) and answers 403 to everyone else. Client-sent X-Authentik-* headers are deleted first.", strings.Join(g.Groups, ", ")))
	} else if fa.Access == catalog.AccessAdminOnly {
		p.Changes = append(p.Changes, fmt.Sprintf("Who can reach it: the owner and members of the admins group only. No users group is created; a person with the user role is refused at Authentik."))
	} else {
		p.Changes = append(p.Changes, "Who can reach it: the owner, and members of the app's two groups (admins are also users).")
	}
	p.Changes = append(p.Changes, access.Lines()...)
	if len(existing) > 0 {
		p.Changes = append(p.Changes, fmt.Sprintf("Accounts %s already has: %s. A person whose Authentik username matches one signs in as that account.", m.Title, strings.Join(existing, ", ")))
	}
	p.Warnings = append(p.Warnings, fa.Warnings...)
	if strings.HasPrefix(base, "https://") && !docker.IsPrivateName(docker.Hostname(hostOf(base))) {
		p.Warnings = append(p.Warnings, fmt.Sprintf("Signing in sends the browser to Authentik's public address %s, so that name must resolve and answer from this network. Without it the gated app is unreachable (it fails closed); this is the same need path apps already have.", hostOf(base)))
	}
	p.Warnings = append(p.Warnings, "Applying needs an Authentik API token, which can do anything an Authentik administrator can. It is used for this apply only, unless you kept the dashboard sign-in token.")
	p.Digest = p.digest()
	return p, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ProxyAuthentik is the proxy-provider slice of the admin API (sso.API).
type ProxyAuthentik interface {
	ReconcileProxy(ctx context.Context, spec sso.ProxySpec, step func(string)) error
	RemoveProxy(ctx context.Context, spec sso.ProxySpec, step func(string)) error
}

// AccessRemover deletes an app's own Authentik groups. Turning one app off
// while others stay behind the shared gate (ADR-062) removes only these.
type AccessRemover interface {
	RemoveAccess(ctx context.Context, x sso.Access, step func(string)) error
}

func (p Plan) proxySpec(dashboard sso.Spec) sso.ProxySpec {
	spec := sso.ProxySpec{
		ProviderName: p.Provider, AppName: p.Provider, AppSlug: p.ClientID,
		AuthorizationFlow: dashboard.AuthorizationFlow, InvalidationFlow: dashboard.InvalidationFlow, Access: p.Access,
	}
	if len(p.RedirectURIs) > 0 {
		spec.ExternalHost = p.RedirectURIs[0]
	}
	if p.Gate != nil {
		spec.Bypass, spec.AuthentikHost = p.Gate.bypassPaths(), p.Gate.AuthentikHost
		if p.Gate.Shared {
			spec.ProviderName, spec.AppName, spec.AppSlug = SharedGateProvider, "Homestead apps gate", SharedGateSlug
			spec.Bypass = append([]string{}, p.Gate.ProviderBypass...)
		}
	}
	return spec
}

func forwardDefinition(rt Runtime, plan Plan, m catalog.Manifest, dashboard sso.Spec, api Authentik, actor string) (operation.Definition, error) {
	proxy, ok := api.(ProxyAuthentik)
	if !ok {
		return operation.Definition{}, unavailable("this Authentik client cannot manage proxy providers")
	}
	if rt.SetGate == nil {
		return operation.Definition{}, unavailable("the edge cannot be gated on this appliance")
	}
	if plan.Gate == nil {
		return operation.Definition{}, unavailable("the plan has no gate")
	}
	fa := m.SSO.ForwardAuth
	dir := rt.StackDir(m.Name)
	spec := plan.proxySpec(dashboard)
	failed := func(what string, err error) error {
		rt.audit(actor, "app.sign-in", "error", fmt.Sprintf("%s: %s failed", m.Name, what))
		return err
	}
	// env writes or removes the app's trust settings and recreates it.
	env := func(ctx context.Context, on bool) error {
		notes, err := catalog.ApplySignInWiring(dir, catalog.SignInWiring{Service: fa.Service, Env: plan.Gate.Env}, on)
		if err != nil {
			return err
		}
		if len(notes.Replaced) > 0 {
			rt.audit(actor, "app.sign-in.wire", "ok", fmt.Sprintf("%s: %s set by the owner replaced while the gate is on", m.Name, strings.Join(notes.Replaced, ", ")))
		}
		if len(notes.Kept) > 0 {
			rt.audit(actor, "app.sign-in.wire", "ok", fmt.Sprintf("%s: %s left in place, the owner changed it since the gate set it", m.Name, strings.Join(notes.Kept, ", ")))
		}
		if rt.Fingerprint != nil {
			if err := rt.Fingerprint(m.Name, dir); err != nil {
				return err
			}
		}
		return up(ctx, dir, m.Name)
	}
	reach := operation.StepDef{Name: "reach-authentik", Run: func(ctx context.Context) error {
		if _, err := api.Version(ctx); err != nil {
			return failed("reach Authentik", err)
		}
		return nil
	}}
	// The app's own login goes back on before anything else is undone, so
	// it is never open with neither the gate nor a login (S422).
	loginOn := operation.StepDef{Name: "login-on", Run: func(ctx context.Context) error {
		if err := ownLoginOn(ctx, container(m, fa.Service), *fa.OwnLoginOff); err != nil {
			return failed("turn the app's own login back on", err)
		}
		rt.audit(actor, "app.sign-in.login", "ok", m.Name+": the app's own login is on")
		return nil
	}}
	loginOff := operation.StepDef{Name: "login-off", Run: func(ctx context.Context) error {
		if err := ownLoginOff(ctx, container(m, fa.Service), actor, *fa.OwnLoginOff); err != nil {
			return failed("turn the app's own login off", err)
		}
		rt.audit(actor, "app.sign-in.login", "ok", m.Name+": the app's own login is off behind the gate")
		return nil
	}}
	def := operation.Definition{Type: "app.sign-in"}
	if !plan.Enabled {
		// Never a window where the app trusts a header the edge no longer
		// strips: the trust settings go first, then the gate, then
		// Authentik (ADR-055 §11; the ADR lists the gate first, which
		// would leave that window).
		unwire := operation.StepDef{Name: "unwire", Run: func(ctx context.Context) error {
			if err := env(ctx, false); err != nil {
				return failed("remove the app's trust settings", err)
			}
			return nil
		}}
		def.Steps = []operation.StepDef{
			reach,
			loginOn,
			unwire,
			{Name: "ungate", Run: func(ctx context.Context) error {
				if err := rt.SetGate(ctx, m, nil); err != nil {
					return failed("remove the gate from the edge", err)
				}
				return nil
			}},
			{Name: "remove-from-authentik", Run: func(ctx context.Context) error {
				var steps []string
				note := func(s string) { steps = append(steps, s) }
				if plan.Gate.Shared && len(plan.Gate.OtherApps) > 0 {
					// Other apps stay behind the shared gate: only this
					// app's open paths and groups go.
					keep := spec
					keep.Access = nil
					if err := proxy.ReconcileProxy(ctx, keep, note); err != nil {
						return failed("update the shared gate in Authentik", err)
					}
					rm, ok := api.(AccessRemover)
					if !ok {
						return failed("remove the app's groups", fmt.Errorf("this Authentik client cannot remove groups"))
					}
					if err := rm.RemoveAccess(ctx, *spec.Access, note); err != nil {
						return failed("remove the app's groups from Authentik", err)
					}
				} else if err := proxy.RemoveProxy(ctx, spec, note); err != nil {
					return failed("remove from Authentik", err)
				}
				rt.audit(actor, "app.sign-in.authentik", "ok", m.Name+": "+strings.Join(steps, "; "))
				return nil
			}},
			{Name: "forget", Run: func(context.Context) error {
				if err := rt.Records.Delete(m.Name); err != nil {
					return err
				}
				rt.audit(actor, "app.sign-in.off", "ok", fmt.Sprintf("%s: Authentik gate off; provider %q and application %s deleted", m.Name, plan.Provider, plan.ClientID))
				return nil
			}},
		}
		def.Steps = withoutEmptyWiring(def.Steps, len(fa.Env) == 0)
		def.Steps = withoutOwnLogin(def.Steps, fa.OwnLoginOff == nil)
		return def, nil
	}
	// On: Authentik first (so the outpost knows the provider), then the
	// gate (reloaded and probed), and only then the trust settings, so
	// the app never trusts a header the edge is not yet stripping.
	var retire []operation.StepDef
	if plan.Gate.Shared {
		// An app that had a provider of its own (ADR-055) leaves it first:
		// two providers on one external host would be ambiguous. The gate
		// fails closed for the moment between the two steps.
		legacy := sso.ProxySpec{ProviderName: ProviderName(m.Title), AppName: m.Title, AppSlug: plan.ClientID}
		retire = []operation.StepDef{{Name: "retire-own-provider", Run: func(ctx context.Context) error {
			var steps []string
			if err := proxy.RemoveProxy(ctx, legacy, func(s string) { steps = append(steps, s) }); err != nil {
				return failed("retire the app's own Authentik provider", err)
			}
			rt.audit(actor, "app.sign-in.authentik", "ok", m.Name+": "+strings.Join(steps, "; "))
			return nil
		}}}
	}
	def.Steps = append([]operation.StepDef{reach}, retire...)
	def.Steps = append(def.Steps, []operation.StepDef{
		{Name: "configure-authentik", Run: func(ctx context.Context) error {
			var steps []string
			if err := proxy.ReconcileProxy(ctx, spec, func(s string) { steps = append(steps, s) }); err != nil {
				return failed("configure Authentik", err)
			}
			rt.audit(actor, "app.sign-in.authentik", "ok", m.Name+": "+strings.Join(steps, "; "))
			return nil
		}},
		restorePeople(rt, m, plan, actor, failed),
		{Name: "gate", Run: func(ctx context.Context) error {
			if err := rt.SetGate(ctx, m, plan.Gate.gate()); err != nil {
				return failed("gate the app at the edge", err)
			}
			return nil
		}},
		{Name: "wire", Run: func(ctx context.Context) error {
			if err := env(ctx, true); err != nil {
				return failed("write the app's trust settings", err)
			}
			return nil
		}},
		loginOff,
		{Name: "record", Run: func(context.Context) error {
			rec := Record{Package: m.Name, Adapter: plan.Adapter, ClientID: plan.ClientID, Issuer: plan.Issuer,
				RedirectURIs: plan.RedirectURIs, PluginVersion: plan.Gate.Revision, Actor: actor, Access: plan.Access}
			if err := rt.Records.Save(rec); err != nil {
				return err
			}
			rt.audit(actor, "app.sign-in.on", "ok", fmt.Sprintf("%s: Authentik gate on at %s; %d open paths", m.Name, plan.Gate.Canonical, len(plan.Gate.Bypass)))
			return nil
		}},
	}...)
	def.Steps = withoutEmptyWiring(def.Steps, len(fa.Env) == 0)
	def.Steps = withoutOwnLogin(def.Steps, fa.OwnLoginOff == nil)
	return def, nil
}

// withoutOwnLogin drops the own-login steps of an app whose login stays on.
func withoutOwnLogin(steps []operation.StepDef, none bool) []operation.StepDef {
	if !none {
		return steps
	}
	out := make([]operation.StepDef, 0, len(steps))
	for _, st := range steps {
		if st.Name != "login-on" && st.Name != "login-off" {
			out = append(out, st)
		}
	}
	return out
}

// withoutEmptyWiring drops the trust-settings steps of an app that has none
// (Uptime Kuma, S419), so applying does not recreate its container for nothing.
func withoutEmptyWiring(steps []operation.StepDef, none bool) []operation.StepDef {
	if !none {
		return steps
	}
	out := make([]operation.StepDef, 0, len(steps))
	for _, st := range steps {
		if st.Name != "wire" && st.Name != "unwire" {
			out = append(out, st)
		}
	}
	return out
}
