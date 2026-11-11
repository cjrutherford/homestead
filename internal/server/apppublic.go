package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// Public app names on the tunnel (S290, ADR-041 §1): status, a read-only
// plan, and apply by digest as an operation. A public name is a tunnel
// name on one of the app's local edge routes, its ingress entry and its
// CNAME, added or removed together. All three routes are session class.

// MountAppPublic registers the public app name routes.
func (s *Server) MountAppPublic(st *store.Store, secrets *secret.Store, catalogDir string) {
	p := &appPublic{s: s, st: st, secrets: secrets, catalogDir: catalogDir}
	s.appPublic = p
	s.mux.HandleFunc("GET /api/v1/catalog/{name}/public", p.status)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/public/plan", p.plan)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/public/apply", p.apply)
}

type appPublic struct {
	s          *Server
	st         *store.Store
	secrets    *secret.Store
	catalogDir string
	// setupZone overrides the setup wizard's zone resolver (tests).
	setupZone func() (provider, zone string)
}

type appPublicStatus struct {
	App       string `json:"app"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// NeverPublish is the manifest's publish: never (S451, ADR-061): adding a name
	// is refused and not offered; an existing name can still be removed.
	NeverPublish bool             `json:"never_publish,omitempty"`
	TunnelID     string           `json:"tunnel_id,omitempty"`
	Tunnel       string           `json:"tunnel,omitempty"`
	Routes       []appPublicRoute `json:"routes"`
	Proposed     string           `json:"proposed,omitempty"`
	// ProposedURL is the public address of a path app (ADR-044).
	ProposedURL string `json:"proposed_url,omitempty"`
	SignIn      bool   `json:"sign_in"`
	// Coverage is the edge certificate lamp for each public name (S312).
	Coverage []publicNameCoverage `json:"coverage"`
}

// appPublicRoute is one of the app's local edge routes and the public
// names the tunnel delivers to it.
type appPublicRoute struct {
	Route  string   `json:"route"`
	Public []string `json:"public"`
	// Path and URL are set for an app served by path (ADR-044): Public
	// then holds the dashboard's public name once the path is published,
	// and URL is the public address it answers at.
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
}

// appPublicFacts is what status and plan read about the app.
type appPublicFacts struct {
	manifest   catalog.Manifest
	routes     []docker.Route
	all        []docker.Route
	tunnelID   string
	tunnelName string
	signIn     bool
	installed  bool
	reason     string
	// server is the dashboard's public name, which a path app is
	// published on (ADR-044); "" while the dashboard has none.
	server docker.Hostname
}

func (p *appPublic) facts(name string) (appPublicFacts, error) {
	entries, err := catalog.Load(p.catalogDir)
	if err != nil {
		return appPublicFacts{}, fmt.Errorf("catalog unavailable: %w", err)
	}
	manifest, err := entries.Get(name)
	if err != nil {
		return appPublicFacts{}, errUnknownApp
	}
	f := appPublicFacts{manifest: manifest}
	installed, err := catalog.Installed(p.st, manifest.Name, stackDirFor(manifest.Name))
	if err != nil {
		return f, err
	}
	f.installed = installed
	set, err := loadEdgeRoutes(p.st)
	if err != nil {
		return f, err
	}
	f.all = set.Routes
	f.routes = appsso.AppRoutes(manifest, set.Routes)
	f.server = docker.PublicServerName(set.Routes)
	if f.tunnelID, f.tunnelName, err = router.RecordedTunnel(p.st); err != nil {
		return f, err
	}
	if _, ok, err := (appsso.Records{DB: p.st.DB()}).Get(manifest.Name); err != nil {
		return f, err
	} else {
		f.signIn = ok
	}
	switch {
	case manifest.Name == authentikPackage:
		f.reason = "Authentik's public name belongs to the tunnel setup (Settings, Tunnel)."
	case !installed:
		f.reason = fmt.Sprintf("Install %s first.", manifest.Title)
	case f.tunnelID == "":
		f.reason = "No tunnel is set up yet. Set one up, or adopt yours, on the Tunnel page."
	case len(f.routes) == 0:
		f.reason = fmt.Sprintf("%s has no address on the edge yet.", manifest.Title)
	case f.routes[0].IsPath() && f.server == "":
		f.reason = pathNeedsServer(manifest)
	case f.routes[0].CraftApp != "" && !f.routes[0].IsPath():
		f.reason = craftNeedsMove(manifest)
	}
	return f, nil
}

// craftNeedsMove explains why a craft app still on a name of its own
// cannot be published: the tunnel carries craft apps only at their
// path under the dashboard's public name (ADR-045).
func craftNeedsMove(m catalog.Manifest) string {
	return fmt.Sprintf("%s goes on the tunnel at %s under the dashboard's public name, and it still answers at a name of its own. Move it to %s first (Review the move, or homesteadctl apps move %s), then publish it here.",
		m.Title, m.AppPath(), m.AppPath(), m.Name)
}

// moveHint tells the owner of a path package still on a name of its own
// how to publish it under the dashboard's public name instead.
func moveHint(m catalog.Manifest) string {
	if !m.ServedByPath() {
		return ""
	}
	return fmt.Sprintf(" To publish %s at %s under the dashboard's public name, move it there first (Review the move, or homesteadctl apps move %s).",
		m.Title, m.AppPath(), m.Name)
}

// pathNeedsServer explains why a path app cannot be published yet.
func pathNeedsServer(m catalog.Manifest) string {
	return fmt.Sprintf("%s answers under the dashboard's public name, at %s, and the dashboard has no public name yet. Publish the dashboard on the Tunnel page first.",
		m.Title, m.AppPath())
}

func (p *appPublic) writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, errUnknownApp) {
		http.Error(w, "unknown app", http.StatusNotFound)
		return
	}
	writePlanError(w, err)
}

func (p *appPublic) status(w http.ResponseWriter, r *http.Request) {
	f, err := p.facts(r.PathValue("name"))
	if err != nil {
		p.writeError(w, err)
		return
	}
	out := appPublicStatus{App: f.manifest.Name, Available: f.reason == "", Reason: f.reason,
		TunnelID: f.tunnelID, Tunnel: f.tunnelName, Routes: []appPublicRoute{}, SignIn: f.signIn}
	for _, route := range f.routes {
		entry := appPublicRoute{Route: string(route.Host), Public: []string{}}
		for _, n := range route.Tunnel {
			entry.Public = append(entry.Public, string(n))
		}
		if route.IsPath() {
			entry.Path = route.Path
			if route.Published && f.server != "" {
				entry.Public = []string{string(f.server)}
				entry.URL = docker.PathURL(f.server, route.Path)
			}
		}
		out.Routes = append(out.Routes, entry)
	}
	names := []string{}
	for _, route := range out.Routes {
		names = append(names, route.Public...)
	}
	out.Coverage = statusCoverage(r.Context(), p.secrets, names)
	if refusal := f.manifest.PublicRefusal(); refusal != "" {
		out.NeverPublish = true
		out.Reason = refusal
		out.Available = len(names) > 0
		writeJSON(w, out)
		return
	}
	// Before the install the name is proposed too, so the install wizard
	// can offer it (S313); planning it needs before_install.
	beforeInstall := !f.installed && f.tunnelID != "" && f.manifest.Name != authentikPackage
	if out.Available || beforeInstall {
		domain, err := p.proposedDomain(f.tunnelID)
		if err != nil {
			p.writeError(w, err)
			return
		}
		switch {
		case f.manifest.ServedByPath():
			// A path app takes the dashboard's public name (ADR-044).
			if f.server != "" {
				out.Proposed = string(f.server)
				out.ProposedURL = docker.PathURL(f.server, f.manifest.AppPath())
			}
		case domain != "":
			out.Proposed = f.manifest.Name + "." + domain
		}
	}
	writeJSON(w, out)
}

// proposedDomain is the Cloudflare zone an own-name app is proposed under
// (S329): the tunnel's stored zone, else the zone the setup wizard's
// domain resolves to (S328). The setup domain itself names the dashboard
// and may be a level below the zone; Cloudflare's free certificate covers
// only one level, so app names go directly under the zone.
func (p *appPublic) proposedDomain(tunnelID string) (string, error) {
	if zone, err := router.TunnelZoneName(p.st, tunnelID); err != nil || zone != "" {
		return zone, err
	}
	resolve := p.setupZone
	if resolve == nil && p.s.certIssuing != nil {
		resolve = p.s.certIssuing.SetupZone
	}
	if resolve != nil {
		if _, zone := resolve(); zone != "" {
			return zone, nil
		}
	}
	return "", nil
}

// belowZone says how many labels a host name sits below its zone.
func belowZone(hostname, zone string) int {
	rest := strings.TrimSuffix(strings.ToLower(hostname), "."+strings.ToLower(zone))
	if rest == strings.ToLower(hostname) || rest == "" {
		return 0
	}
	return strings.Count(rest, ".") + 1
}

type appPublicRequest struct {
	Enabled  *bool  `json:"enabled"`
	Hostname string `json:"hostname,omitempty"`
	Route    string `json:"route,omitempty"`
	Digest   string `json:"digest,omitempty"`
	// BeforeInstall plans a name for an app that is not installed yet,
	// on the route its install creates (S313): the install wizard and
	// `apps install --public` review it with the install plan. It is a
	// plan only. Apply takes the same request without it once the app is
	// installed, and the digest must still match then. Managed installs
	// request the anticipated post-sign-in state for their original approval.
	BeforeInstall bool `json:"before_install,omitempty"`
	// AnticipateSignIn previews publication after managed install sign-in.
	AnticipateSignIn bool `json:"anticipate_sign_in,omitempty"`
}

// appPublicPlan is one public name added to or removed from an app.
type appPublicPlan struct {
	App      string `json:"app"`
	Action   string `json:"action"`
	Hostname string `json:"hostname"`
	Route    string `json:"route"`
	// Path and URL are set for an app served by path (ADR-044): Hostname
	// is then the dashboard's public name, and no DNS record changes.
	Path     string             `json:"path,omitempty"`
	URL      string             `json:"url"`
	Tunnel   string             `json:"tunnel"`
	TunnelID string             `json:"tunnel_id"`
	Zone     string             `json:"zone"`
	DNS      []tunnelPlanChange `json:"dns"`
	// Before and After are the connector's public names around the change.
	Before []string `json:"connector_before"`
	After  []string `json:"connector_after"`
	SignIn string   `json:"sign_in,omitempty"`
	// SignInDigest is the digest of the app sign-in plan the change
	// applies after the edge steps (S399); approving this plan approves
	// that sign-in change. Empty when sign-in is left alone.
	SignInDigest string `json:"sign_in_digest,omitempty"`
	// AppSettings is what the app's own settings get (S313): Nextcloud's
	// trusted domains follow its public names.
	AppSettings []string `json:"app_settings"`
	// Coverage says whether Cloudflare has a certificate for the added
	// name (S312); an uncovered name is also a warning.
	Coverage         []publicNameCoverage `json:"coverage"`
	CertificateState string               `json:"certificate_state,omitempty"`
	Warnings         []string             `json:"warnings"`
	Digest           string               `json:"digest"`

	zoneID   string
	replaces []router.DNSRecord
	keep     *router.DNSRecord
	owned    []router.OwnedRecord
	restores []router.ReplacedRecord
	manifest catalog.Manifest
	// env is the base-URL setting the app's stack gets before sign-in
	// (S407); the same changes are listed in AppSettings.
	env []catalog.ConfigChange
}

func (p appPublicPlan) digest() string {
	h := sha256.New()
	visible := p
	visible.Digest = ""
	data, _ := json.Marshal(visible)
	h.Write(data)
	fmt.Fprintf(h, "\nzone=%s", p.zoneID)
	for _, record := range p.replaces {
		fmt.Fprintf(h, "\nreplace=%s", record.ID)
	}
	if p.keep != nil {
		fmt.Fprintf(h, "\nkeep=%s", p.keep.ID)
	}
	if len(p.env) > 0 {
		env, _ := json.Marshal(p.env)
		fmt.Fprintf(h, "\nenv=%s", env)
	}
	for _, record := range p.owned {
		fmt.Fprintf(h, "\nowned=%s", record.RecordID)
	}
	for _, record := range p.restores {
		fmt.Fprintf(h, "\nrestore=%s", record.Record.ID)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// tunnelNameMap is every edge route's current tunnel names. A published
// path route (ADR-044) is listed under the dashboard's public name, which
// applyTunnelNames reads back as published.
func tunnelNameMap(routes []docker.Route) map[docker.Hostname][]docker.Hostname {
	names := map[docker.Hostname][]docker.Hostname{}
	public := docker.PublicServerName(routes)
	for _, r := range routes {
		if r.IsPath() && r.Published && public != "" {
			names[r.Host] = []docker.Hostname{public}
			continue
		}
		if len(r.Tunnel) > 0 {
			names[r.Host] = append([]docker.Hostname(nil), r.Tunnel...)
		}
	}
	return names
}

// buildPlan reads Cloudflare and the store and changes nothing.
func (p *appPublic) buildPlan(ctx context.Context, name string, req appPublicRequest) (appPublicPlan, error) {
	return p.buildPlanState(ctx, name, req, false)
}

// buildAnticipatedSignInPlan is only a preview of the state after a future
// successful sign-in setup. It is not proof of readiness and cannot apply a
// plan. The caller must verify sign-in and grants before using the original
// approved digest with start, which always reads actual state.
func (p *appPublic) buildAnticipatedSignInPlan(ctx context.Context, name string, req appPublicRequest) (appPublicPlan, error) {
	return p.buildPlanState(ctx, name, req, true)
}

func (p *appPublic) buildPlanState(ctx context.Context, name string, req appPublicRequest, anticipateSignIn bool) (appPublicPlan, error) {
	f, err := p.facts(name)
	if err != nil {
		return appPublicPlan{}, err
	}
	if refusal := f.manifest.PublicRefusal(); refusal != "" && req.Enabled != nil && *req.Enabled {
		return appPublicPlan{}, refusePlan(http.StatusUnprocessableEntity, "%s", refusal)
	}
	if anticipateSignIn {
		if !req.BeforeInstall || f.installed || f.signIn || f.manifest.SSO.OIDC == nil {
			return appPublicPlan{}, refusePlan(http.StatusUnprocessableEntity, "anticipated sign-in requires an uninstalled app with OIDC and no existing sign-in")
		}
		f.signIn = true
	}
	if req.BeforeInstall && !f.installed && f.manifest.Name != authentikPackage {
		if err := beforeInstall(&f, req); err != nil {
			return appPublicPlan{}, err
		}
	}
	if f.reason != "" {
		return appPublicPlan{}, refusePlan(http.StatusUnprocessableEntity, "%s", f.reason)
	}
	hostname := strings.ToLower(strings.TrimSpace(req.Hostname))
	plan := appPublicPlan{App: f.manifest.Name, Tunnel: f.tunnelName, TunnelID: f.tunnelID,
		DNS: []tunnelPlanChange{}, Warnings: []string{}, Coverage: []publicNameCoverage{}, AppSettings: []string{},
		manifest: f.manifest}
	recorded, err := recordedIngress(p.st, f.tunnelID)
	if err != nil {
		return appPublicPlan{}, err
	}
	for _, in := range recorded {
		plan.Before = append(plan.Before, in.Hostname)
	}
	token, err := tunnelToken(p.secrets)
	if err != nil {
		return appPublicPlan{}, refusePlan(http.StatusUnprocessableEntity, "%v", err)
	}
	client, err := tunnelClient()
	if err != nil {
		return appPublicPlan{}, err
	}
	path := len(f.routes) > 0 && f.routes[0].IsPath()
	switch {
	case path && *req.Enabled:
		err = p.planPathAdd(ctx, client, token, f, hostname, req.Route, &plan)
	case path:
		err = p.planPathRemove(f, hostname, &plan)
	case *req.Enabled && alreadyPublicName(f, hostname) != "":
		err = p.planRefresh(f, hostname, alreadyPublicName(f, hostname), &plan)
	case *req.Enabled:
		err = p.planAdd(ctx, client, token, f, hostname, req.Route, &plan)
	default:
		err = p.planRemove(f, hostname, &plan)
		if err == nil {
			err = p.removeZone(ctx, client, token, &plan)
		}
	}
	if err != nil {
		return appPublicPlan{}, err
	}
	for _, h := range plan.Before {
		if plan.Action == "remove" && h == plan.Hostname && !path {
			continue
		}
		plan.After = append(plan.After, h)
	}
	if plan.Action == "add" && !path {
		plan.After = append(plan.After, plan.Hostname)
		sort.Strings(plan.After)
	}
	if plan.Before == nil {
		plan.Before = []string{}
	}
	if plan.After == nil {
		plan.After = []string{}
	}
	if !path {
		if err := p.planAppURL(f, &plan); err != nil {
			return appPublicPlan{}, err
		}
		if plan.Action == "refresh" && len(plan.env) == 0 {
			return appPublicPlan{}, refusePlan(http.StatusUnprocessableEntity, "%s is already on the tunnel and its base URL already uses it", hostname)
		}
	}
	if !path && f.manifest.SingleOriginSignIn && len(f.routes) > 0 && plan.Action != "refresh" {
		plan.Warnings = append(plan.Warnings, singleOriginWarning(f.routes[0], plan))
	}
	if f.signIn && plan.Action != "refresh" {
		if anticipateSignIn && p.s.appSignIn != nil && p.s.appSignIn.tokenKept() {
			// The install applies sign-in with the public name already in its
			// redirect list, so after install, with the Authentik token kept,
			// this step's plan reads "no change". Say the same now, or the
			// approved digest never matches the plan the setup re-checks (S444).
			plan.SignIn = "App sign-in is on and needs no change."
		} else if !p.planSignIn(f, path, &plan) {
			verb := "accepts"
			if plan.Action == "remove" {
				verb = "stops accepting"
			}
			address := "https://" + plan.Hostname
			if path {
				address = plan.URL
			}
			plan.SignIn = "App sign-in is on: afterwards, re-plan it on this page and approve it, so sign-in " +
				verb + " " + address + "."
		}
	}
	if plan.Action == "refresh" {
		// Only the app's base URL changes: the name is already live.
	} else if plan.Action == "add" {
		plan.AppSettings = append(plan.AppSettings, appsso.HostSettings(f.manifest, []string{plan.Hostname}, nil)...)
	} else {
		plan.AppSettings = append(plan.AppSettings, appsso.HostSettings(f.manifest, nil, []string{plan.Hostname})...)
	}
	plan.Digest = plan.digest()
	return plan, nil
}

// alreadyPublicName returns the app route that already carries hostname
// on the tunnel, or "" (S407).
func alreadyPublicName(f appPublicFacts, hostname string) string {
	if hostname == "" {
		return ""
	}
	for _, r := range f.routes {
		if r.IsPath() {
			continue
		}
		for _, n := range r.Tunnel {
			if strings.EqualFold(string(n), hostname) {
				return string(r.Host)
			}
		}
	}
	return ""
}

// planRefresh plans bringing an already-public app's base URL in step
// with its public name (S407): the fix for an app published before its
// base URL followed the public address. No DNS or edge change; planAppURL
// fills the setting and the caller refuses a refresh with nothing to do.
func (p *appPublic) planRefresh(f appPublicFacts, hostname, route string, plan *appPublicPlan) error {
	plan.Action = "refresh"
	plan.Hostname = hostname
	plan.Route = route
	return nil
}

// planAppURL puts the base-URL setting the change calls for in the plan
// (S407): for an installed app whose manifest uses AppURLToken, the
// token's value once the public names are as the plan leaves them,
// compared with the value the stack has now.
func (p *appPublic) planAppURL(f appPublicFacts, plan *appPublicPlan) error {
	if !f.installed || f.manifest.ServedByPath() {
		return nil
	}
	var names []docker.Hostname
	for _, r := range f.routes {
		if strings.EqualFold(string(r.Host), plan.Route) {
			names = r.Tunnel
		}
	}
	public := ""
	for _, n := range names {
		if plan.Action == "remove" && strings.EqualFold(string(n), plan.Hostname) {
			continue
		}
		public = string(n)
		break
	}
	if public == "" && plan.Action != "remove" {
		public = plan.Hostname
	}
	if plan.Action == "refresh" {
		public = plan.Hostname
	}
	changes, err := catalog.AppURLChanges(f.manifest, stackDirFor(f.manifest.Name), plan.Route, public)
	if err != nil {
		return refusePlan(http.StatusUnprocessableEntity, "%v", err)
	}
	bySvc := map[string]*catalog.ConfigChange{}
	for _, c := range changes {
		entry := bySvc[c.Service]
		if entry == nil {
			plan.env = append(plan.env, catalog.ConfigChange{Service: c.Service, Set: map[string]string{}})
			entry = &plan.env[len(plan.env)-1]
			bySvc[c.Service] = entry
		}
		entry.Set[c.Key] = c.New
		old := c.Old
		if old == "" {
			old = "(not set)"
		}
		plan.AppSettings = append(plan.AppSettings, fmt.Sprintf("%s: %s: %s -> %s (the app restarts)", c.Service, c.Key, old, c.New))
	}
	return nil
}

// appURLSteps writes the planned base URL and brings the stack up with
// it, as the app move does (S407); they run before sign-in.
func (p *appPublic) appURLSteps(plan appPublicPlan) ([]operation.StepDef, error) {
	if len(plan.env) == 0 {
		return nil, nil
	}
	var secretStore catalog.SecretStore
	if p.secrets != nil {
		secretStore = p.secrets
	}
	configure, err := moveConfigure(catalog.ConfigPlan{Manifest: plan.manifest, Changes: plan.env},
		stackDirFor(plan.App), plan.App, p.st, secretStore)
	if err != nil {
		return nil, refusePlan(http.StatusUnprocessableEntity, "%v", err)
	}
	return configure.Steps, nil
}

// planSignIn plans the app's sign-in for the names after the change and
// puts what moves in the plan (S399). It returns false when sign-in is
// left for the owner to re-plan: the change cannot be previewed, the
// Authentik API token is not kept, or nothing about sign-in changes.
func (p *appPublic) planSignIn(f appPublicFacts, path bool, plan *appPublicPlan) bool {
	a := p.s.appSignIn
	if a == nil || !f.installed || f.manifest.SSO.OIDC == nil || !a.tokenKept() {
		return false
	}
	set, err := loadEdgeRoutes(p.st)
	if err != nil {
		return false
	}
	routes := append([]docker.Route(nil), set.Routes...)
	for i, r := range routes {
		if !strings.EqualFold(string(r.Host), plan.Route) {
			continue
		}
		switch {
		case path:
			r.Published = plan.Action == "add"
		case plan.Action == "add":
			r.Tunnel = append(append([]docker.Hostname(nil), r.Tunnel...), docker.Hostname(plan.Hostname))
		default:
			var kept []docker.Hostname
			for _, n := range r.Tunnel {
				if !strings.EqualFold(string(n), plan.Hostname) {
					kept = append(kept, n)
				}
			}
			r.Tunnel = kept
		}
		routes[i] = r
	}
	set.Routes = routes
	in, err := appSignInInputsFrom(p.st, a.m, f.manifest, set, p.catalogDir)
	if err != nil || in.Applied == nil {
		return false
	}
	next, err := appsso.BuildPlan(in, true)
	if err != nil {
		return false
	}
	var lines []string
	if next.Issuer != in.Applied.Issuer {
		lines = append(lines, fmt.Sprintf("moves to the %s issuer %s (now %s)", map[bool]string{true: "public", false: "local"}[plan.Action == "add"], next.Issuer, in.Applied.Issuer))
	}
	if added, removed := listChanges(in.Applied.RedirectURIs, next.RedirectURIs); len(added)+len(removed) > 0 {
		if len(added) > 0 {
			lines = append(lines, "adds redirect URI "+strings.Join(added, ", "))
		}
		if len(removed) > 0 {
			lines = append(lines, "removes redirect URI "+strings.Join(removed, ", "))
		}
	}
	if len(lines) == 0 {
		// Nothing changes: no re-plan to ask for, but a public issuer that
		// is not served yet is still worth saying (S400). The sign-in
		// plan's own warnings were shown with the sign-in review, so they
		// are not repeated here (S444: they made the install-time public
		// plan differ from the one approved before install).
		plan.SignIn = "App sign-in is on and needs no change."
		return true
	}
	plan.Warnings = append(plan.Warnings, next.Warnings...)
	plan.SignIn = "App sign-in is on and is updated in this same change with the kept Authentik API token: it " + strings.Join(lines, "; ") + "."
	plan.SignInDigest = next.Digest
	return true
}

// listChanges is what next has that old lacks, and the reverse.
func listChanges(old, next []string) (added, removed []string) {
	has := func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	for _, v := range next {
		if !has(old, v) {
			added = append(added, v)
		}
	}
	for _, v := range old {
		if !has(next, v) {
			removed = append(removed, v)
		}
	}
	return added, removed
}

// signInStep brings the app's sign-in in step once the name is live
// (S399): the plan approved with the public plan, applied through the
// app sign-in path. A failure leaves the name as it is and says so.
func (p *appPublic) signInStep(plan appPublicPlan, actor string) operation.StepDef {
	return operation.StepDef{Name: "sign-in", Run: func(ctx context.Context) error {
		state := "is public"
		if plan.Action == "remove" {
			state = "is no longer public"
		}
		if p.s.appSignIn == nil {
			return fmt.Errorf("%s %s, but app sign-in is not available, so its sign-in still uses the old issuer; re-plan it from its card", plan.App, state)
		}
		err := reapplyAppSignIn(ctx, p.s.appSignIn, cert.IssuerMove{App: plan.App, Digest: plan.SignInDigest}, actor)
		if err != nil {
			return fmt.Errorf("%s %s, but its sign-in still uses the old issuer: %w; re-plan it from its card (homesteadctl apps sign-in plan %s) and approve it", plan.App, state, err, plan.App)
		}
		return nil
	}}
}

// beforeInstall stands the route the install creates in for the app's
// routes (S313), so the plan reads as it will once the app is installed.
func beforeInstall(f *appPublicFacts, req appPublicRequest) error {
	if !*req.Enabled {
		return refusePlan(http.StatusBadRequest, "only adding a public name is planned before the install")
	}
	route := strings.ToLower(strings.TrimSpace(req.Route))
	if route == "" {
		return refusePlan(http.StatusBadRequest, "a plan before the install names the route the install creates")
	}
	for _, r := range f.all {
		for _, n := range r.AllNames() {
			if strings.EqualFold(string(n), route) {
				return refusePlan(http.StatusUnprocessableEntity, "%s is already a name of the edge route %s", route, r.Host)
			}
		}
	}
	f.routes = []docker.Route{{Host: docker.Hostname(route)}}
	if f.manifest.ServedByPath() {
		f.routes[0].Path = f.manifest.AppPath()
	}
	f.reason = ""
	switch {
	case f.tunnelID == "":
		f.reason = "No tunnel is set up yet. Set one up, or adopt yours, on the Tunnel page."
	case f.routes[0].IsPath() && f.server == "":
		f.reason = pathNeedsServer(f.manifest)
	}
	return nil
}

func (p *appPublic) planAdd(ctx context.Context, client *router.TunnelClient, token string, f appPublicFacts, hostname, route string, plan *appPublicPlan) error {
	plan.Action = "add"
	if hostname == "" {
		domain, err := p.proposedDomain(f.tunnelID)
		if err != nil {
			return err
		}
		if domain == "" {
			return refusePlan(http.StatusUnprocessableEntity, "no Cloudflare zone is known yet: set up the tunnel on the Tunnel page, or name the hostname")
		}
		hostname = f.manifest.Name + "." + domain
	}
	plan.Hostname = hostname
	if strings.ContainsAny(hostname, "/:") {
		return refusePlan(http.StatusBadRequest, "%s is not a public name: a public name is a host name only, such as %s.example.com, with no path.%s",
			hostname, f.manifest.Name, moveHint(f.manifest))
	}
	if f.server != "" && strings.EqualFold(hostname, string(f.server)) {
		return refusePlan(http.StatusUnprocessableEntity, "%s is the dashboard's public name; %s cannot also answer there at a name of its own.%s",
			hostname, f.manifest.Title, moveHint(f.manifest))
	}
	if err := (router.TunnelRoute{Hostname: hostname, TunnelID: f.tunnelID, Service: tunnelEntryService}).Validate(); err != nil {
		return refusePlan(http.StatusBadRequest, "%v", err)
	}
	if docker.IsPrivateName(docker.Hostname(hostname)) {
		return refusePlan(http.StatusBadRequest, "%s is a local name; the tunnel carries public names", hostname)
	}
	target := -1
	for i, r := range f.routes {
		if route == "" || strings.EqualFold(string(r.Host), route) {
			target = i
			break
		}
	}
	if target < 0 {
		return refusePlan(http.StatusBadRequest, "%s is not one of %s's edge routes", route, f.manifest.Title)
	}
	plan.Route = string(f.routes[target].Host)
	for _, r := range f.all {
		for _, n := range r.AllNames() {
			if strings.EqualFold(string(n), hostname) {
				return refusePlan(http.StatusUnprocessableEntity, "%s is already a name of the edge route %s", hostname, r.Host)
			}
		}
	}
	for _, h := range plan.Before {
		if h == hostname {
			return refusePlan(http.StatusUnprocessableEntity, "%s is already on the tunnel", hostname)
		}
	}
	zone, err := client.ZoneFor(ctx, token, hostname)
	if errors.Is(err, router.ErrNoZone) {
		return refusePlan(http.StatusUnprocessableEntity, "no Cloudflare zone the token can see contains %s", hostname)
	}
	if err != nil {
		return err
	}
	if belowZone(hostname, zone.Name) > 1 {
		return refusePlan(http.StatusUnprocessableEntity,
			"%s is more than one level below %s; Cloudflare's free certificate only covers names one level below the zone, such as %s.%s",
			hostname, zone.Name, f.manifest.Name, zone.Name)
	}
	tunnelZone, err := router.TunnelZone(p.st, f.tunnelID)
	if err != nil {
		return err
	}
	if tunnelZone != "" && zone.ID != tunnelZone {
		return refusePlan(http.StatusUnprocessableEntity, "%s is not in the tunnel's Cloudflare zone; pick a name in the same zone as its other names", hostname)
	}
	plan.Zone, plan.zoneID = zone.Name, zone.ID
	existing, err := client.RecordsAt(ctx, token, zone.ID, hostname)
	if err != nil {
		return err
	}
	change := tunnelPlanChange{Name: hostname, Type: "CNAME", Content: router.TunnelTarget(f.tunnelID), Proxied: true,
		Action: "create", Replaces: []router.DNSRecord{}}
	for _, record := range existing {
		if record.Type == "CNAME" && strings.EqualFold(record.Content, change.Content) {
			change.Action = "keep"
			kept := record
			plan.keep = &kept
			continue
		}
		switch record.Type {
		case "A", "AAAA", "CNAME":
			change.Replaces = append(change.Replaces, record)
		default:
			return refusePlan(http.StatusUnprocessableEntity,
				"%s already has a %s record, which a tunnel CNAME cannot sit next to; remove it in Cloudflare or pick another hostname",
				hostname, record.Type)
		}
	}
	sort.Slice(change.Replaces, func(i, j int) bool { return change.Replaces[i].ID < change.Replaces[j].ID })
	if len(change.Replaces) > 0 {
		change.Action = "replace"
		plan.Warnings = append(plan.Warnings,
			"Existing records for "+hostname+" are deleted now and put back when this public name is removed, unless something else is at that name by then.")
	}
	plan.replaces = change.Replaces
	plan.DNS = append(plan.DNS, change)
	coverage, warnings, fingerprint, err := publicationCoverage(ctx, client, token, zone, hostname)
	if err != nil {
		return err
	}
	plan.Coverage = coverage
	plan.CertificateState = fingerprint
	plan.Warnings = append(plan.Warnings, warnings...)
	plan.Warnings = append(plan.Warnings,
		openToInternetWarning(f.manifest, f.signIn, "https://"+hostname, ""))
	if !f.signIn && f.manifest.SSO.OIDC != nil {
		plan.Warnings = append(plan.Warnings, "App sign-in through Authentik is off. Turning it on first is recommended.")
	}
	return nil
}

// planPathAdd plans publishing a path app on the dashboard's public name
// (ADR-044 §3): no DNS record and no new ingress, but the same digest,
// review and approval as a name of its own. The coverage check runs on
// the dashboard's name, which the app now shares.
func (p *appPublic) planPathAdd(ctx context.Context, client *router.TunnelClient, token string, f appPublicFacts, hostname, route string, plan *appPublicPlan) error {
	plan.Action = "add"
	r := f.routes[0]
	if route != "" && !strings.EqualFold(route, string(r.Host)) {
		return refusePlan(http.StatusBadRequest, "%s is not one of %s's edge routes", route, f.manifest.Title)
	}
	if hostname != "" && hostname != string(f.server) {
		return refusePlan(http.StatusBadRequest, "%s answers under the dashboard's public name %s, at %s; it takes no name of its own",
			f.manifest.Title, f.server, r.Path)
	}
	if r.Published {
		return refusePlan(http.StatusUnprocessableEntity, "%s is already published at %s", f.manifest.Title, docker.PathURL(f.server, r.Path))
	}
	plan.Hostname, plan.Route, plan.Path = string(f.server), string(r.Host), r.Path
	plan.URL = docker.PathURL(f.server, r.Path)
	zoneID, err := router.TunnelZone(p.st, f.tunnelID)
	if err != nil {
		return err
	}
	zone, err := client.ZoneFor(ctx, token, plan.Hostname)
	if errors.Is(err, router.ErrNoZone) {
		return refusePlan(http.StatusUnprocessableEntity, "no Cloudflare zone the token can see contains %s", plan.Hostname)
	}
	if err != nil {
		return err
	}
	if zoneID != "" && zone.ID != zoneID {
		return refusePlan(http.StatusUnprocessableEntity, "%s is not in the tunnel's Cloudflare zone", plan.Hostname)
	}
	plan.Zone, plan.zoneID = zone.Name, zone.ID
	coverage, warnings, fingerprint, err := publicationCoverage(ctx, client, token, zone, plan.Hostname)
	if err != nil {
		return err
	}
	plan.Coverage = coverage
	plan.CertificateState = fingerprint
	plan.Warnings = append(plan.Warnings, warnings...)
	plan.Warnings = append(plan.Warnings,
		"No DNS record changes: "+f.manifest.Title+" answers at "+plan.URL+", under the dashboard's public name the tunnel already carries.",
		openToInternetWarning(f.manifest, f.signIn, plan.URL, r.Path),
		lanPathRedirectWarning(f, *plan),
		"It shares one web origin with the dashboard and the other published apps (ADR-044): a flaw in one of them can reach the others in the same browser.")
	if w := cookiePathWarning(f.manifest); w != "" {
		plan.Warnings = append(plan.Warnings, w)
	}
	if !f.signIn && f.manifest.SSO.OIDC != nil {
		plan.Warnings = append(plan.Warnings, "App sign-in through Authentik is off. Turning it on first is recommended.")
	}
	return nil
}

// planPathRemove plans taking a path app off the dashboard's public name.
func (p *appPublic) planPathRemove(f appPublicFacts, hostname string, plan *appPublicPlan) error {
	plan.Action = "remove"
	r := f.routes[0]
	if !r.Published {
		return refusePlan(http.StatusUnprocessableEntity, "%s is not published", f.manifest.Title)
	}
	if hostname != "" && hostname != string(f.server) {
		return refusePlan(http.StatusUnprocessableEntity, "%s is not one of %s's public names", hostname, f.manifest.Title)
	}
	plan.Hostname, plan.Route, plan.Path = string(f.server), string(r.Host), r.Path
	plan.URL = docker.PathURL(f.server, r.Path)
	if name, err := router.TunnelZoneName(p.st, f.tunnelID); err == nil {
		plan.Zone = name
	} else {
		return err
	}
	plan.Warnings = append(plan.Warnings,
		"No DNS record changes: "+plan.URL+" answers 404 on the public name afterwards; the app stays on its local addresses.",
		lanPathRedirectWarning(f, *plan))
	plan.Warnings = append(plan.Warnings, p.gateMoveWarnings(f, false)...)
	return nil
}

// gateMoveWarnings name the other gated apps whose local address moves
// because publishing or unpublishing this one moves the shared gate's
// canonical name (S504, ADR-062 section 5). The lines are part of the
// plan, so the digest covers them.
func (p *appPublic) gateMoveWarnings(f appPublicFacts, publish bool) []string {
	r := f.routes[0]
	move := docker.GateCanonicalMove(f.all, r.Path, func(x *docker.Route) { x.Published = publish })
	if !move.Moved() {
		return nil
	}
	titles := gateTitles(p.catalogDir, f.all)
	return move.Lines(func(x docker.Route) string {
		if t := titles[x.Path]; t != "" {
			return t
		}
		return x.Path
	})
}

// removeZone names the zone a removed name lives in: the stored one,
// else the zone Cloudflare finds for the name (adopted tunnels, S302).
func (p *appPublic) removeZone(ctx context.Context, client *router.TunnelClient, token string, plan *appPublicPlan) error {
	name, err := router.TunnelZoneName(p.st, plan.TunnelID)
	if err != nil || name != "" {
		plan.Zone = name
		return err
	}
	zone, err := client.ZoneFor(ctx, token, plan.Hostname)
	if errors.Is(err, router.ErrNoZone) {
		return nil
	}
	if err != nil {
		return err
	}
	plan.Zone = zone.Name
	return nil
}

func (p *appPublic) planRemove(f appPublicFacts, hostname string, plan *appPublicPlan) error {
	plan.Action = "remove"
	var names []string
	for _, r := range f.routes {
		for _, n := range r.Tunnel {
			if hostname == "" || string(n) == hostname {
				names = append(names, string(n))
				plan.Route = string(r.Host)
			}
		}
	}
	switch {
	case len(names) == 0 && hostname == "":
		return refusePlan(http.StatusUnprocessableEntity, "%s has no public name", f.manifest.Title)
	case len(names) == 0:
		return refusePlan(http.StatusUnprocessableEntity, "%s is not one of %s's public names", hostname, f.manifest.Title)
	case len(names) > 1:
		return refusePlan(http.StatusBadRequest, "%s has several public names; name the one to remove", f.manifest.Title)
	}
	plan.Hostname = names[0]
	owned, err := router.OwnedRecords(p.st, f.tunnelID)
	if err != nil {
		return err
	}
	for _, record := range owned {
		if record.Hostname == plan.Hostname {
			plan.owned = append(plan.owned, record)
			plan.zoneID = record.ZoneID
			plan.DNS = append(plan.DNS, tunnelPlanChange{Name: plan.Hostname, Type: "CNAME",
				Content: router.TunnelTarget(f.tunnelID), Proxied: true, Action: "delete", Replaces: []router.DNSRecord{}})
		}
	}
	replaced, err := router.ReplacedRecords(p.st, f.tunnelID)
	if err != nil {
		return err
	}
	for _, r := range replaced {
		if r.Record.Name == plan.Hostname {
			plan.restores = append(plan.restores, r)
			plan.DNS = append(plan.DNS, tunnelPlanChange{Name: r.Record.Name, Type: r.Record.Type,
				Content: r.Record.Content, Proxied: r.Record.Proxied, Action: "restore", Replaces: []router.DNSRecord{}})
		}
	}
	if len(plan.owned) == 0 {
		plan.Warnings = append(plan.Warnings, "Homestead did not create the DNS record for "+plan.Hostname+", so it is left in Cloudflare and answers 404 until you remove it there.")
	} else {
		plan.Warnings = append(plan.Warnings, "The DNS record is deleted only if it still points at the tunnel.")
	}
	return nil
}

func (p *appPublic) decode(w http.ResponseWriter, r *http.Request, apply bool) (appPublicRequest, bool) {
	var req appPublicRequest
	if !decodeResolverRequest(w, r, &req) || !requireEnabled(w, req.Enabled) {
		return req, false
	}
	if !apply && req.Digest != "" {
		http.Error(w, "a plan takes no digest", http.StatusBadRequest)
		return req, false
	}
	if apply && req.BeforeInstall {
		http.Error(w, "a public name is applied once the app is installed: apply the same plan without before_install", http.StatusBadRequest)
		return req, false
	}
	if req.AnticipateSignIn && (apply || !req.BeforeInstall || req.Enabled == nil || !*req.Enabled) {
		http.Error(w, "anticipated sign-in is only available for a public install plan", http.StatusBadRequest)
		return req, false
	}
	if apply && req.Digest == "" {
		http.Error(w, "digest is required: review the plan, then approve it with its digest", http.StatusBadRequest)
		return req, false
	}
	if !*req.Enabled && req.Route != "" {
		http.Error(w, "removing a public name takes no route", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

func (p *appPublic) plan(w http.ResponseWriter, r *http.Request) {
	req, ok := p.decode(w, r, false)
	if !ok {
		return
	}
	build := p.buildPlan
	if req.AnticipateSignIn {
		build = p.buildAnticipatedSignInPlan
	}
	plan, err := build(r.Context(), r.PathValue("name"), req)
	if err != nil {
		p.writeError(w, err)
		return
	}
	writeJSON(w, plan)
}

func (p *appPublic) apply(w http.ResponseWriter, r *http.Request) {
	req, ok := p.decode(w, r, true)
	if !ok {
		return
	}
	id, err := p.start(r.Context(), r.PathValue("name"), req, operatorActor(r))
	if errors.Is(err, errPlanChanged) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		p.writeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

// errPlanChanged is a plan that no longer matches its approved digest.
var errPlanChanged = errors.New("the plan changed since it was reviewed; review it again")

// start plans the public name again, compares it with req.Digest, and
// starts the change as an audited operation (S290). The apply route and
// the appliance-run app setup (ADR-048) both go through it.
func (p *appPublic) start(ctx context.Context, name string, req appPublicRequest, actor string) (int64, error) {
	plan, err := p.buildPlan(ctx, name, req)
	if err != nil {
		return 0, err
	}
	if plan.Digest != req.Digest {
		return 0, errPlanChanged
	}
	token, err := tunnelToken(p.secrets)
	if err != nil {
		return 0, refusePlan(http.StatusUnprocessableEntity, "%s", err.Error())
	}
	def := appPublicAddDefinition(p.st, token, plan)
	if plan.Path != "" {
		def = appPublicPathDefinition(p.st, plan)
	}
	action, detail := "app.public.add", fmt.Sprintf("%s: %s -> %s on tunnel %s", plan.App, plan.Hostname, plan.Route, plan.Tunnel)
	if plan.Action == "remove" && plan.Path != "" {
		action, detail = "app.public.remove", fmt.Sprintf("%s: %s off tunnel %s", plan.App, plan.URL, plan.Tunnel)
	} else if plan.Path != "" {
		detail = fmt.Sprintf("%s: %s on tunnel %s", plan.App, plan.URL, plan.Tunnel)
	} else if plan.Action == "remove" {
		def = appPublicRemoveDefinition(p.st, token, plan)
		action, detail = "app.public.remove", fmt.Sprintf("%s: %s off tunnel %s", plan.App, plan.Hostname, plan.Tunnel)
	}
	if plan.Action == "refresh" {
		def = operation.Definition{Type: "app.public.refresh"}
		action, detail = "app.public.refresh", fmt.Sprintf("%s: base URL follows %s", plan.App, plan.Hostname)
	}
	urlSteps, err := p.appURLSteps(plan)
	if err != nil {
		return 0, err
	}
	def.Steps = append(def.Steps, urlSteps...)
	if plan.SignInDigest != "" {
		def.Steps = append(def.Steps, p.signInStep(plan, actor))
	}
	runner := operation.NewRunner(p.st)
	op, err := runner.Start(ctx, def, "")
	if err != nil {
		return 0, refusePlan(http.StatusInternalServerError, "%s", err.Error())
	}
	p.s.auditOperation(p.st, runner, op.ID, actor, action, fmt.Sprintf("operation %d: %s", op.ID, detail))
	return op.ID, nil
}

// trustAppHosts keeps the app's own host allow-list in step (S313);
// tests replace it.
var trustAppHosts = appsso.TrustHosts

// withTunnelName is the current edge name map with hostname added to, or
// taken from, route.
func withTunnelName(st *store.Store, route, hostname string, add bool) (map[docker.Hostname][]docker.Hostname, error) {
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return nil, err
	}
	names := tunnelNameMap(set.Routes)
	for host, list := range names {
		kept := list[:0]
		for _, n := range list {
			if string(n) != hostname {
				kept = append(kept, n)
			}
		}
		names[host] = kept
	}
	if add {
		names[docker.Hostname(route)] = append(names[docker.Hostname(route)], docker.Hostname(hostname))
	}
	return names, nil
}

// appPublicAddDefinition points the name at the tunnel, records its
// route, gives the app's edge route the name, and rewrites the
// connector. A failure undoes what this run did.
func appPublicAddDefinition(st *store.Store, apiToken string, plan appPublicPlan) operation.Definition {
	var created string
	undoDNS := func(ctx context.Context) {
		if client, err := tunnelClient(); err == nil && created != "" {
			if client.DeleteRecord(ctx, apiToken, plan.zoneID, created) == nil {
				_ = router.ForgetOwned(st, created)
			}
		}
		if plan.keep != nil {
			_ = router.ForgetOwned(st, plan.keep.ID)
		}
		_ = restoreReplacedRecordsAt(ctx, st, apiToken, plan.TunnelID, plan.Hostname)
	}
	undoRoute := func(ctx context.Context) {
		_ = router.DeleteRoute(st, plan.Hostname)
		if names, err := withTunnelName(st, plan.Route, plan.Hostname, false); err == nil {
			_ = applyTunnelNames(ctx, st, names)
		}
		undoDNS(ctx)
	}
	return operation.Definition{Type: "app.public.add", Steps: []operation.StepDef{
		// The app's own host allow-list first (S313): adding a name there
		// is harmless, and a Nextcloud still installing itself is waited
		// for before anything is published.
		{Name: "app-hosts", Run: func(ctx context.Context) error {
			names := []string{}
			set, err := loadEdgeRoutes(st)
			if err != nil {
				return err
			}
			for _, r := range set.Routes {
				if strings.EqualFold(string(r.Host), plan.Route) {
					for _, n := range r.Names() {
						names = append(names, string(n))
					}
				}
			}
			return trustAppHosts(ctx, plan.manifest, "https://"+plan.Route, append(names, plan.Hostname), nil)
		}},
		{Name: "dns", Run: func(ctx context.Context) error {
			client, err := tunnelClient()
			if err != nil {
				return err
			}
			for _, record := range plan.replaces {
				if err := router.RecordReplaced(st, router.ReplacedRecord{TunnelID: plan.TunnelID, ZoneID: plan.zoneID, Record: record}); err != nil {
					undoDNS(ctx)
					return err
				}
				if err := client.DeleteRecord(ctx, apiToken, plan.zoneID, record.ID); err != nil {
					_ = router.ForgetReplaced(st, plan.TunnelID, record.ID)
					undoDNS(ctx)
					return fmt.Errorf("tunnel: delete %s record for %s: %w", record.Type, record.Name, err)
				}
			}
			recordID := ""
			if plan.keep != nil {
				recordID = plan.keep.ID
			} else {
				record, err := client.CreateTunnelCNAME(ctx, apiToken, plan.zoneID, plan.Hostname, plan.TunnelID)
				if err != nil {
					undoDNS(ctx)
					return fmt.Errorf("tunnel: create DNS record for %s: %w", plan.Hostname, err)
				}
				created, recordID = record.ID, record.ID
			}
			if err := router.RecordOwned(st, router.OwnedRecord{RecordID: recordID, ZoneID: plan.zoneID,
				Hostname: plan.Hostname, TunnelID: plan.TunnelID}); err != nil {
				undoDNS(ctx)
				return err
			}
			return nil
		}},
		{Name: "record-route", Run: func(ctx context.Context) error {
			account, err := router.TunnelAccount(st, plan.TunnelID)
			if err == nil {
				err = router.RecordRoute(st, router.TunnelRoute{Hostname: plan.Hostname, TunnelID: plan.TunnelID,
					Service: tunnelEntryService}, plan.Tunnel)
			}
			if err == nil && account != "" {
				err = router.SetTunnelAccount(st, plan.TunnelID, account, plan.zoneID, plan.Zone)
			}
			if err != nil {
				undoRoute(ctx)
			}
			return err
		}},
		{Name: "edge", Run: func(ctx context.Context) error {
			names, err := withTunnelName(st, plan.Route, plan.Hostname, true)
			if err == nil {
				err = applyTunnelNames(ctx, st, names)
			}
			if err != nil {
				undoRoute(ctx)
			}
			return err
		}},
		{Name: "connector", Run: func(ctx context.Context) error {
			if err := syncConnector(st, plan.TunnelID); err != nil {
				undoRoute(ctx)
				_ = syncConnector(st, plan.TunnelID)
				return err
			}
			return nil
		}},
	}}
}

// appPublicRemoveDefinition stops delivering the name first, then takes
// it off the edge route, then deletes its CNAME (only while it still
// points at the tunnel) and puts back the records it replaced.
func appPublicRemoveDefinition(st *store.Store, apiToken string, plan appPublicPlan) operation.Definition {
	return operation.Definition{Type: "app.public.remove", Steps: []operation.StepDef{
		{Name: "connector", Run: func(ctx context.Context) error {
			if err := router.DeleteRoute(st, plan.Hostname); err != nil {
				return err
			}
			return syncConnector(st, plan.TunnelID)
		}},
		{Name: "edge", Run: func(ctx context.Context) error {
			names, err := withTunnelName(st, plan.Route, plan.Hostname, false)
			if err != nil {
				return err
			}
			return applyTunnelNames(ctx, st, names)
		}},
		{Name: "delete-dns", Run: func(ctx context.Context) error {
			return deleteOwnedRecordsAt(ctx, st, apiToken, plan.TunnelID, plan.Hostname)
		}},
		{Name: "restore-dns", Run: func(ctx context.Context) error {
			return restoreReplacedRecordsAt(ctx, st, apiToken, plan.TunnelID, plan.Hostname)
		}},
		{Name: "app-hosts", Run: func(ctx context.Context) error {
			if err := trustAppHosts(ctx, plan.manifest, "https://"+plan.Route, nil, []string{plan.Hostname}); err != nil {
				return fmt.Errorf("%s is off the tunnel, but the app still lists it: %w", plan.Hostname, err)
			}
			return nil
		}},
	}}
}

// appPublicPathDefinition publishes or unpublishes a path app on the
// dashboard's public name (ADR-044 §3): the app's own host allow-list,
// then the edge, which starts or stops answering at the path on the
// public name. No DNS record or connector change; the dashboard's name
// is already on the tunnel.
func appPublicPathDefinition(st *store.Store, plan appPublicPlan) operation.Definition {
	add := plan.Action == "add"
	localNames := func() []string {
		set, err := loadEdgeRoutes(st)
		if err != nil {
			return nil
		}
		names := []string{}
		for _, r := range docker.ServedRoutes(set.Routes, set.Routes) {
			if strings.EqualFold(string(r.Host), plan.Route) || (r.IsPath() && r.Path == plan.Path) {
				for _, n := range r.Names() {
					names = append(names, string(n))
				}
			}
		}
		return names
	}
	address := strings.TrimSuffix(plan.URL, "/")
	hosts := operation.StepDef{Name: "app-hosts", Run: func(ctx context.Context) error {
		if add {
			return trustAppHosts(ctx, plan.manifest, address, append(localNames(), plan.Hostname), nil)
		}
		if err := trustAppHosts(ctx, plan.manifest, address, nil, []string{plan.Hostname}); err != nil {
			return fmt.Errorf("%s is off the public name, but the app still lists it: %w", plan.URL, err)
		}
		return nil
	}}
	edge := operation.StepDef{Name: "edge", Run: func(ctx context.Context) error {
		// The dashboard's name stays on the dashboard and every other
		// published path: only this route's entry changes.
		set, err := loadEdgeRoutes(st)
		if err != nil {
			return err
		}
		names := tunnelNameMap(set.Routes)
		if add {
			names[docker.Hostname(plan.Route)] = []docker.Hostname{docker.Hostname(plan.Hostname)}
		} else {
			delete(names, docker.Hostname(plan.Route))
		}
		return applyTunnelNames(ctx, st, names)
	}}
	typ := "app.public.add"
	steps := []operation.StepDef{hosts, edge}
	if !add {
		// Stop answering first, then drop the name from the app.
		typ, steps = "app.public.remove", []operation.StepDef{edge, hosts}
	}
	return operation.Definition{Type: typ, Steps: steps}
}

// singleOriginWarning previews the edge redirect of a single-origin app
// (ADR-060): its local names redirect to the public name while it is
// published, and stop when it is not.
func singleOriginWarning(r docker.Route, plan appPublicPlan) string {
	names := make([]string, 0, len(r.Names()))
	for _, n := range r.Names() {
		names = append(names, string(n))
	}
	local := strings.Join(names, " and ")
	if plan.Action == "remove" {
		return "Edge change: " + local + " stop redirecting to " + plan.Hostname + " (ADR-060); the app is served on its local names again."
	}
	return "Edge change: " + local + " redirect (307, path and query kept) to https://" + plan.Hostname +
		", because this app can only sign in on one address (ADR-060). Local clients then reach it through the tunnel and the internet, and cannot use it if either is down; unpublish it to bring the local names back."
}

// lanPathRedirectWarning previews the edge change for a path app's local
// names (S498, ADR-062 amendment): published, a request on
// the local name answers 307 to the public address, path and query kept;
// unpublished, the local names serve the app again. Craft apps keep the LAN
// open, so for them the warning says nothing changes. An API client on the LAN
// (Komga's OPDS, an app on a phone) that does not follow a 307 needs the
// public address.
func lanPathRedirectWarning(f appPublicFacts, plan appPublicPlan) string {
	r := f.routes[0]
	local := "the local address " + r.Path
	if r.CraftApp != "" {
		return "Your local address " + r.Path + " stays open: " + f.manifest.Title + " keeps no redirect on the local network (ADR-062)."
	}
	if plan.Action == "remove" {
		return "Edge change: the redirect from " + local + " to " + plan.URL + " is removed; the app is served on its local address again."
	}
	return "Edge change: requests for " + local + " are redirected (307, path and query kept) to " + plan.URL +
		". Local apps and API clients that do not follow redirects must use the public address; unpublish " + f.manifest.Title + " to serve the local address again (S498)."
}

// openToInternetWarning says who stands in front of a published app. An app
// gated by Authentik forward-auth (ADR-055) is planned with its gate in the
// same install (S450), so the warning says when the gate arrives.
func openToInternetWarning(m catalog.Manifest, signIn bool, url, path string) string {
	if accountlessInstallApps[m.Name] {
		// A craft app has no sign-in of its own (ADR-062 section 6).
		if !signIn {
			return m.Title + " has no sign-in of its own, so " + url + " answers nothing on the internet until its Authentik gate is on (plan it next: homesteadctl apps sign-in plan " + m.Name + "). Your local address stays open."
		}
		return "Anyone on the internet who signs in to Authentik and belongs to " + m.Title + "'s group (or is the owner) can reach " + m.Title + " at " + url + "; everyone else gets 403. Your local address stays open."
	}
	if m.SSO.ForwardAuth != nil && !signIn {
		return "Anyone on the internet can reach " + m.Title + " at " + url + ". Until the Authentik gate is on, only " + m.Title + "'s own login protects it; the gate is planned with its install, and the app's login is never left open."
	}
	if m.SSO.ForwardAuth != nil {
		msg := "Anyone on the internet can reach " + m.Title + " at " + url + ", but every request is checked with Authentik first"
		if len(m.SSO.ForwardAuth.Bypass) == 0 {
			return msg + "."
		}
		// The gate has paths it leaves open (S504); name them as the
		// sign-in plan does.
		var open []string
		for _, b := range m.SSO.ForwardAuth.Bypass {
			p := b.Path
			if path != "" {
				p = docker.PathBypass(path, p)
			}
			open = append(open, appsso.OpenWithoutGateLine(p, b.Reason))
		}
		return msg + ", except the paths it leaves open. " + strings.Join(open, "; ") + "."
	}
	return "Anyone on the internet can reach " + m.Title + " at " + url + ". Homestead puts no Authentik check in front of it: the app's own sign-in is what protects it."
}
