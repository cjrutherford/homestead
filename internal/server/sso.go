package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/catalog"
	"homestead/internal/cert"
	"homestead/internal/claim"
	"homestead/internal/craftfiles"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/secret"
	"homestead/internal/sso"
	"homestead/internal/store"
)

// Dashboard sign-in with Authentik (S286, ADR-033). Three routes are
// public (options, login, callback) and answer 404 before claim and unless
// sign-in is configured and linked; every other SSO route is session class.

// SSOConfig wires MountSSO.
type SSOConfig struct {
	Store      *store.Store
	Secrets    *secret.Store
	Auth       *auth.Service
	Claim      *claim.Service
	Local      *cert.LocalTLS // optional: trust the appliance CA for Authentik
	CatalogDir string
	YAMLPath   string
	Port       int
	Now        func() time.Time
}

// authentikPackage is the catalog package that provides the identity
// provider.
const authentikPackage = "authentik"

// authentikInstalled is a seam for tests: whether the Authentik stack exists.
var authentikInstalled = func(st *store.Store) bool {
	ok, _ := catalog.Installed(st, authentikPackage, stackDirFor(authentikPackage))
	return ok
}

// ssoCookie holds the login state binding (Lax so it returns on the
// cross-site navigation back from Authentik; HttpOnly; scoped to the SSO
// routes).
const ssoCookie = "homestead_sso"

// MountSSO registers the SSO routes and returns the manager.
func (s *Server) MountSSO(c SSOConfig) *sso.Manager {
	auditSvc := audit.New(c.Store)
	record := func(actor, action, outcome, detail string) {
		if _, err := auditSvc.Record(audit.Entry{Actor: actor, Action: action, Outcome: outcome, Detail: detail}); err != nil {
			s.log.Warn("audit record failed", "action", action, "error", err)
		}
	}
	// The back channel: the appliance CA is trusted (ADR-030) and a name
	// served by this appliance's own edge is dialled on the loopback edge,
	// exactly like the Nextcloud link (S254). The same client carries the
	// Authentik admin API calls.
	up := &craftfiles.Upstream{EdgeHosts: func() map[string]bool {
		set, err := loadEdgeRoutes(c.Store)
		if err != nil {
			return nil
		}
		hosts := map[string]bool{}
		for _, r := range set.Routes {
			hosts[strings.ToLower(string(r.Host))] = true
		}
		return hosts
	}}
	if c.Local != nil {
		up.Roots = func() []byte {
			pem, err := c.Local.CA.RootPEM()
			if err != nil {
				return nil
			}
			return pem
		}
	}
	m := &sso.Manager{
		DB: c.Store.DB(), Secrets: c.Secrets, Client: up.Client, YAMLPath: c.YAMLPath, Now: c.Now,
		Audit:     record,
		Defaults:  func() sso.Defaults { return ssoDefaults(c.Store, c.Port) },
		Installed: func() bool { return authentikInstalled(c.Store) },
		Version:   func() string { return authentikVersion(c.CatalogDir) },
		RevokeOIDC: func(id int64) error {
			if name, err := c.Auth.Username(id); err == nil {
				s.RevokeCraftSessions(name)
			}
			return c.Auth.RevokeMethodSessions(id, auth.MethodOIDC)
		},
	}
	s.signInOffered = m.Offered
	s.signInStatus = m.Status
	s.updateSignIn = func(ctx context.Context, actor, tunnelDigest string, want tunnelSignIn) error {
		return updateSignInForTunnel(ctx, m, record, actor, tunnelDigest, want)
	}
	h := &ssoHandlers{s: s, m: m, cfg: c, runner: operation.NewRunner(c.Store), audit: record}
	s.mux.HandleFunc("GET /api/v1/sso/options", h.options)
	s.mux.HandleFunc("GET /api/v1/sso/login", h.login)
	s.mux.HandleFunc("GET /api/v1/sso/callback", h.callback)
	s.mux.HandleFunc("GET /api/v1/sso", h.status)
	s.mux.HandleFunc("POST /api/v1/sso/access", h.access)
	s.mux.HandleFunc("POST /api/v1/sso/plan", h.plan)
	s.mux.HandleFunc("POST /api/v1/sso/approve", h.approve)
	s.mux.HandleFunc("POST /api/v1/sso/apply", h.apply)
	s.mux.HandleFunc("POST /api/v1/sso/link", h.link)
	s.mux.HandleFunc("DELETE /api/v1/sso/link", h.unlink)
	s.mux.HandleFunc("POST /api/v1/sso/step-up", h.stepUp)
	s.mux.HandleFunc("DELETE /api/v1/sso", h.disable)
	s.mountPeople(c, m, up.Client, record)
	s.mountAppSignIn(&appSignIn{s: s, cfg: c, m: m, client: up.Client, runner: h.runner, audit: record})
	return m
}

type ssoHandlers struct {
	s      *Server
	m      *sso.Manager
	cfg    SSOConfig
	runner *operation.Runner
	audit  func(actor, action, outcome, detail string)
}

// isAuthentikRoute recognises the Authentik package's route: its server
// service, recorded under authentik.<domain> (or <endpoint>-authentik.).
func isAuthentikRoute(r docker.Route) bool {
	host := strings.ToLower(string(r.Host))
	return !r.Dashboard && r.Target.Host == "server" &&
		(strings.HasPrefix(host, authentikPackage+".") || strings.Contains(host, "-"+authentikPackage+"."))
}

// authentikPathURLs is Authentik's address when it is served by path
// (ADR-044): under the dashboard's own name locally, and under its
// public name when the path is published. The daemon and the apps' back
// channel use the local one; the browser through the tunnel the public.
func authentikPathURLs(set docker.RouteSet, r docker.Route) (local, public string) {
	dash, ok := set.DashboardRoute()
	if !ok {
		return "", ""
	}
	path := strings.TrimSuffix(r.Path, "/")
	local = "https://" + strings.ToLower(string(dash.Host)) + path
	if r.Published && len(dash.Tunnel) > 0 {
		public = "https://" + strings.ToLower(string(dash.Tunnel[0])) + path
	}
	return local, public
}

// ssoDefaults derives plan defaults from the appliance's own state: the
// Authentik route in the edge table, and the origins the owner reaches the
// dashboard at (the loopback daemon, edge routes and tunnel hostnames that
// point at the daemon port). Nothing here comes from a request.
func ssoDefaults(st *store.Store, port int) sso.Defaults {
	var d sso.Defaults
	if port <= 0 {
		port = 8080
	}
	set, err := loadEdgeRoutes(st)
	if err == nil {
		for _, r := range set.Routes {
			host := strings.ToLower(string(r.Host))
			if d.AuthentikURL == "" && isAuthentikRoute(r) && r.IsPath() {
				d.AuthentikURL, d.PublicAuthentikURL = authentikPathURLs(set, r)
			} else if d.AuthentikURL == "" && isAuthentikRoute(r) {
				scheme := "https"
				if r.TLS == "off" {
					scheme = "http"
				}
				d.AuthentikURL = scheme + "://" + host
				// Through the tunnel the browser reaches Authentik at its
				// public name (ADR-037); the daemon keeps the local one.
				if len(r.Tunnel) > 0 {
					d.PublicAuthentikURL = "https://" + strings.ToLower(string(r.Tunnel[0]))
				}
			}
			if r.Dashboard {
				// The dashboard route answers on every name it carries
				// (ADR-035), always over HTTPS, and on its tunnel names,
				// which sign in through Authentik's public name (ADR-037).
				for _, n := range r.AllNames() {
					d.Origins = append(d.Origins, "https://"+strings.ToLower(string(n)))
				}
				for _, n := range r.Tunnel {
					d.PublicOrigins = append(d.PublicOrigins, "https://"+strings.ToLower(string(n)))
				}
				continue
			}
			if r.Target.Port == port && (r.Target.Host == "127.0.0.1" || r.Target.Host == "localhost") {
				scheme := "https"
				if r.TLS == "off" {
					scheme = "http"
				}
				d.Origins = append(d.Origins, scheme+"://"+host)
			}
		}
	}
	if routes, err := router.ListRoutes(st); err == nil {
		for _, tr := range routes {
			if u, err := url.Parse(tr.Service); err == nil && u.Port() == strconv.Itoa(port) && (loopbackName(u.Hostname())) {
				d.Origins = append(d.Origins, "https://"+strings.ToLower(tr.Hostname))
			}
		}
	}
	d.Origins = append([]string{"http://127.0.0.1:" + strconv.Itoa(port)}, d.Origins...)
	return d
}

// updateSignInForTunnel applies the sign-in change the owner approved
// with a tunnel plan (ADR-046): it plans sign-in again from the
// configured one plus the public origin, checks the plan is the change
// that was approved, records the approval, and applies it with the kept
// Authentik API token. The edge already carries the public name, so the
// defaults name Authentik's public address.
func updateSignInForTunnel(ctx context.Context, m *sso.Manager, record func(actor, action, outcome, detail string), actor, tunnelDigest string, want tunnelSignIn) (err error) {
	short := tunnelDigest
	if len(short) > 12 {
		short = short[:12]
	}
	defer func() {
		if err != nil {
			record(actor, "tunnel.sign-in", audit.OutcomeError, "sign-in for "+want.Origin+" (tunnel plan "+short+"): "+err.Error())
			return
		}
		record(actor, "tunnel.sign-in", audit.OutcomeOK, "sign-in updated for "+want.Origin+", approved with tunnel plan "+short)
	}()
	st, err := m.Status()
	if err != nil {
		return err
	}
	if !st.Configured || st.Spec == nil {
		return errors.New("sign-in with Authentik is no longer set up")
	}
	if !st.TokenKept {
		return errors.New("the Authentik API token is no longer kept")
	}
	spec := st.Spec
	origins := slices.Clone(spec.Origins)
	if !slices.Contains(origins, want.Origin) {
		origins = append(origins, want.Origin)
	}
	plan, err := m.Plan(ctx, sso.PlanRequest{
		AuthentikURL: spec.AuthentikURL, Origins: origins,
		SigningKey: spec.SigningKey, AuthorizationFlow: spec.AuthorizationFlow,
	}, actor)
	if err != nil {
		return err
	}
	if !slices.Contains(plan.Spec.PublicOrigins, want.Origin) || !slices.Contains(plan.Spec.RedirectURIs, want.RedirectURI) ||
		plan.Spec.PublicAuthentikURL != want.AuthentikURL {
		return fmt.Errorf("the new sign-in plan does not match the one in the tunnel plan (it sends browsers to %q)", plan.Spec.PublicAuthentikURL)
	}
	if err := m.Approve(sso.ApproveRequest{Digest: plan.Digest, KeepToken: true}, actor); err != nil {
		return err
	}
	def, err := m.ApplyDefinition(actor)
	if err != nil {
		return err
	}
	for _, step := range def.Steps {
		if err := step.Run(ctx); err != nil {
			return err
		}
	}
	return nil
}

// clientHost is the peer address as the daemon sees it. Forwarded headers
// are never consulted on TCP; on the dashboard socket the front wrapper has
// already validated X-Forwarded-For and stored the client address.
func clientHost(r *http.Request) string {
	// Only the dashboard socket's front wrapper sets this, after it has
	// validated X-Forwarded-For (ADR-035). TCP never reads the header.
	if a, ok := edgeClientFrom(r.Context()); ok {
		return a
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func loopbackName(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// authentikVersion reads the version the catalog pins from the server
// image tag.
func authentikVersion(catalogDir string) string {
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return ""
	}
	manifest, err := entries.Get(authentikPackage)
	if err != nil {
		return ""
	}
	for _, svc := range manifest.Services {
		if svc.Exposes != "" {
			if i := strings.LastIndex(svc.Image, ":"); i >= 0 {
				return svc.Image[i+1:]
			}
		}
	}
	return manifest.Version
}

// claimed reports whether the appliance is claimed.
func (h *ssoHandlers) claimed() bool {
	state, err := h.cfg.Claim.CurrentState()
	return err == nil && state == claim.StateClaimed
}

// available is the 404 rule of the public routes: claimed, configured and
// linked, from local state only.
func (h *ssoHandlers) available() bool {
	return h.claimed() && h.m.Offered()
}

func (h *ssoHandlers) options(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.available() {
		http.NotFound(w, r)
		return
	}
	st, err := h.m.Status()
	if err != nil || st.Spec == nil {
		http.NotFound(w, r)
		return
	}
	// The button is offered only at an address the plan covers.
	_, ok := sso.OriginFor(*st.Spec, r.Host)
	// On a public dashboard name only Authentik signs in (ADR-037).
	writeJSON(w, map[string]any{"enabled": ok, "password": !onPublicHost(r)})
}

func (h *ssoHandlers) failRedirect(w http.ResponseWriter, r *http.Request, path, code string) {
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/api/v1/sso", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, path+"?sso_error="+url.QueryEscape(code), http.StatusFound)
}

func (h *ssoHandlers) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.available() {
		http.NotFound(w, r)
		return
	}
	if !h.m.Allow(clientHost(r)) {
		h.failRedirect(w, r, "/login", sso.CodeRate)
		return
	}
	start, err := h.m.StartLogin(r.Context(), r.Host)
	if err != nil {
		code := sso.CodeUnavailable
		var fe *sso.FlowError
		switch {
		case errors.As(err, &fe):
			code = fe.Code
		case errors.Is(err, sso.ErrOrigin):
			code = sso.CodeOrigin
		case errors.Is(err, sso.ErrNotConfigured):
			http.NotFound(w, r)
			return
		}
		h.audit("anonymous", "sso.login", audit.OutcomeError, "start refused: "+code)
		h.failRedirect(w, r, "/login", code)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: ssoCookie, Value: start.State, Path: "/api/v1/sso", MaxAge: int(sso.StateTTL.Seconds()),
		HttpOnly: true, Secure: start.Secure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, start.URL, http.StatusFound)
}

func (h *ssoHandlers) callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	if !h.available() {
		// The first link is completed by a callback that arrives while
		// nothing is linked yet, so a live link intent also opens it.
		if !h.claimed() || !h.configured() || !h.m.PendingLink(q.Get("state")) {
			http.NotFound(w, r)
			return
		}
	}
	back := "/login"
	if !h.m.Allow(clientHost(r)) {
		h.failRedirect(w, r, back, sso.CodeRate)
		return
	}
	cookieState := ""
	if c, err := r.Cookie(ssoCookie); err == nil {
		cookieState = c.Value
	}
	out, err := h.m.Callback(r.Context(), q.Get("state"), q.Get("code"), q.Get("error"), cookieState)
	if out.Kind == sso.KindLink {
		back = "/sign-in"
	}
	if out.Kind == sso.KindStepUp {
		back = "/"
	}
	if err != nil {
		code := sso.CodeToken
		var fe *sso.FlowError
		if errors.As(err, &fe) {
			code = fe.Code
		}
		action := "sso.login"
		actor := "anonymous"
		switch out.Kind {
		case sso.KindLink:
			action, actor = "sso.link", orAnon(out.Actor)
		case sso.KindStepUp:
			action, actor = "sso.step-up", orAnon(out.Actor)
		}
		detail := "refused: " + code
		if out.Subject != "" {
			detail += " subject=" + out.Subject
		}
		h.audit(actor, action, audit.OutcomeError, detail)
		h.failRedirect(w, r, back, code)
		return
	}
	if out.Kind == sso.KindLink {
		h.audit(orAnon(out.Actor), "sso.link", audit.OutcomeOK, "method=password subject="+out.Subject)
		http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/api/v1/sso", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/sign-in?linked=1", http.StatusFound)
		return
	}
	if out.Kind == sso.KindStepUp {
		h.finishStepUp(w, r, out)
		return
	}
	session, err := h.cfg.Auth.MintOIDCSession(out.IdentityID)
	if err != nil {
		h.audit("anonymous", "sso.login", audit.OutcomeError, "session could not be created")
		h.failRedirect(w, r, back, sso.CodeToken)
		return
	}
	name, _ := h.cfg.Auth.Username(out.IdentityID)
	h.audit(name, "sso.login", audit.OutcomeOK, "method=oidc subject="+out.Subject)
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/api/v1/sso", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	setSessionCookiesAs(w, out.Secure || requestIsTLS(r), session)
	http.Redirect(w, r, "/login?sso=done", http.StatusFound)
}

// finishStepUp marks the bound oidc session stepped up. It creates no
// session and sets no session cookie.
func (h *ssoHandlers) finishStepUp(w http.ResponseWriter, r *http.Request, out sso.Outcome) {
	clear := func() {
		http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/api/v1/sso", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	until, err := h.cfg.Auth.MarkSteppedUp(out.SessionID, out.IdentityID)
	if err != nil || !sso.ValidReturnTo(out.ReturnTo) {
		h.audit(orAnon(out.Actor), "sso.step-up", audit.OutcomeError, "refused: "+sso.CodeSession+" subject="+out.Subject)
		h.failRedirect(w, r, "/", sso.CodeSession)
		return
	}
	h.audit(orAnon(out.Actor), "sso.step-up", audit.OutcomeOK,
		"method=oidc subject="+out.Subject+" until="+until.UTC().Format(time.RFC3339))
	clear()
	http.Redirect(w, r, out.ReturnTo, http.StatusFound)
}

// stepUp starts the fresh Authentik sign-in that steps the caller's oidc
// session up for the People actions on the public dashboard name (ADR-049).
func (h *ssoHandlers) stepUp(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body struct {
		ReturnTo string `json:"return_to"`
	}
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !sso.ValidReturnTo(body.ReturnTo) {
		http.Error(w, "return_to must be a dashboard path", http.StatusBadRequest)
		return
	}
	if op.Session.Method != auth.MethodOIDC {
		http.Error(w, "only a session signed in with Authentik steps up this way", http.StatusConflict)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	start, err := h.m.StartStepUp(r.Context(), op.Session.ID, op.Session.IdentityID, op.Session.Username, r.Host, body.ReturnTo)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.audit(op.Session.Username, "sso.step-up.start", audit.OutcomeOK, "fresh Authentik sign-in, single use, 5 minutes")
	http.SetCookie(w, &http.Cookie{
		Name: ssoCookie, Value: start.State, Path: "/api/v1/sso", MaxAge: int(sso.StepUpStateTTL.Seconds()),
		HttpOnly: true, Secure: start.Secure, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"url": start.URL, "expires_at": start.ExpiresAt.UTC().Format(time.RFC3339)})
}

func orAnon(s string) string {
	if s == "" {
		return "anonymous"
	}
	return s
}

func (h *ssoHandlers) configured() bool {
	st, err := h.m.Status()
	return err == nil && st.Configured
}

// ssoView is the status response: local state plus the plan defaults and
// whether Authentik is installed.
type ssoView struct {
	sso.Status
	Defaults  sso.Defaults `json:"defaults"`
	Installed bool         `json:"authentik_installed"`
	Method    string       `json:"session_method"`
	// Replan says the configured sign-in no longer covers a public
	// dashboard name or Authentik's public address (ADR-037): the owner
	// re-plans and approves before sign-in works on the tunnel.
	Replan       bool   `json:"replan,omitempty"`
	ReplanReason string `json:"replan_reason,omitempty"`
	// OpenApps are the apps whose Authentik sign-in binds no group, so
	// every Authentik user can sign in to them (ADR-043 §2). Nothing is
	// changed until the owner approves a re-plan.
	OpenApps []string `json:"open_apps"`
}

// replanReason compares the configured spec with today's defaults and
// names what a fresh plan would add for the tunnel, or "".
func replanReason(spec *sso.Spec, d sso.Defaults) string {
	if spec == nil {
		return ""
	}
	for _, o := range d.PublicOrigins {
		if !slices.Contains(spec.Origins, o) || !slices.Contains(spec.PublicOrigins, o) {
			return "Sign-in does not yet cover the public dashboard address " + o + ". Re-plan Sign-in and approve it."
		}
	}
	if len(d.PublicOrigins) > 0 && d.PublicAuthentikURL != "" && spec.PublicAuthentikURL != d.PublicAuthentikURL {
		return "Sign-in does not yet send browsers to Authentik's public address " + d.PublicAuthentikURL + ". Re-plan Sign-in and approve it."
	}
	return ""
}

// accessReplanReason says the dashboard application is not yet bound to
// the owner group, or the owner was linked after it was planned.
func accessReplanReason(st sso.Status) string {
	switch {
	case st.Spec == nil:
		return ""
	case st.Spec.Access == nil:
		return "Sign-in was set up before access groups: the dashboard application is not bound to " + sso.OwnerGroup + ". Re-plan Sign-in and approve it."
	case st.Linked && st.Spec.Access.OwnerSub != st.Subject:
		return "The owner is linked now: plan Sign-in again to add them to " + sso.OwnerGroup + " and bind the application to it."
	case st.Spec.RecoveryFlow == "":
		return "Sign-in was set up before Homestead added Authentik's password recovery flow, so one-time sign-in links for people fail. Re-plan Sign-in and approve it."
	}
	return ""
}

// openApps lists the apps with Authentik sign-in recorded without groups.
func openApps(st *store.Store) []string {
	out := []string{}
	records := appsso.Records{DB: st.DB()}
	pkgs, err := records.Packages()
	if err != nil {
		return out
	}
	for _, pkg := range pkgs {
		if rec, ok, err := records.Get(pkg); err == nil && ok && rec.Access == nil {
			out = append(out, pkg)
		}
	}
	return out
}

// accessRequest is the read-back body: the Authentik token for this read
// only, or empty to use the kept one. It is never stored or logged.
type accessRequest struct {
	Token string `json:"token,omitempty"`
}

// access reads back the dashboard application's group, membership and
// binding (ADR-043 §6) without changing anything.
func (h *ssoHandlers) access(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req accessRequest
	if !decodeResolverRequest(w, r, &req) {
		return
	}
	spec, ok, err := h.m.Config()
	if err != nil {
		http.Error(w, "failed to read sign-in state", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, sso.ErrNotConfigured.Error(), http.StatusConflict)
		return
	}
	if spec.Access == nil {
		http.Error(w, "dashboard sign-in was set up before access groups: plan it again", http.StatusUnprocessableEntity)
		return
	}
	token := resolveToken(req.Token, h.cfg.Secrets)
	if token == "" {
		http.Error(w, errNoToken.Error(), http.StatusUnprocessableEntity)
		return
	}
	api := &sso.API{Base: spec.AuthentikURL, Token: token, Client: h.m.Client}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rep, err := api.ReadAccess(ctx, *spec.Access, spec.AppSlug)
	if err != nil {
		h.s.log.Warn("sso access read-back failed", "error", err)
		http.Error(w, "Authentik could not be read: "+readBackReason(err), http.StatusBadGateway)
		return
	}
	writeJSON(w, rep)
}

// resolveToken is the request's token, else the kept one, else "".
func resolveToken(given string, secrets *secret.Store) string {
	if t := strings.TrimSpace(given); t != "" {
		return t
	}
	if secrets != nil {
		t, _ := secrets.Get(sso.APITokenName)
		return t
	}
	return ""
}

// readBackReason is a short, secret-free reason for a failed read.
func readBackReason(err error) string {
	var ae *sso.APIError
	switch {
	case errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden):
		return "the token was refused"
	case errors.Is(err, sso.ErrUnreachable):
		return "Authentik did not answer"
	}
	return "unexpected answer"
}

func (h *ssoHandlers) statusView(r *http.Request) (ssoView, error) {
	st, err := h.m.Status()
	if err != nil {
		return ssoView{}, err
	}
	installed := authentikInstalled(h.cfg.Store)
	v := ssoView{Status: st, Defaults: ssoDefaults(h.cfg.Store, h.cfg.Port), Installed: installed}
	if st.Configured {
		v.ReplanReason = replanReason(st.Spec, v.Defaults)
		if v.ReplanReason == "" {
			v.ReplanReason = accessReplanReason(st)
		}
		v.Replan = v.ReplanReason != ""
	}
	v.OpenApps = openApps(h.cfg.Store)
	if op, ok := operatorFromContext(r.Context()); ok {
		v.Method = op.Session.Method
	}
	return v, nil
}

func (h *ssoHandlers) status(w http.ResponseWriter, r *http.Request) {
	v, err := h.statusView(r)
	if err != nil {
		http.Error(w, "failed to read sign-in state", http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

func (h *ssoHandlers) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sso.ErrInput):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case errors.Is(err, sso.ErrNoPlan), errors.Is(err, sso.ErrDigest), errors.Is(err, sso.ErrNotApproved),
		errors.Is(err, sso.ErrAlreadyLinked), errors.Is(err, sso.ErrNotLinked), errors.Is(err, sso.ErrNotConfigured):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, sso.ErrOrigin):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case errors.Is(err, sso.ErrUnreachable), errors.Is(err, sso.ErrDiscovery):
		http.Error(w, "Authentik did not answer. Is it running?", http.StatusBadGateway)
	default:
		var fe *sso.FlowError
		if errors.As(err, &fe) && fe.Code == sso.CodeUnavailable {
			http.Error(w, "Authentik did not answer. Is it running?", http.StatusBadGateway)
			return
		}
		h.s.log.Warn("sso request failed", "error", err)
		http.Error(w, "sign-in request failed", http.StatusInternalServerError)
	}
}

func (h *ssoHandlers) plan(w http.ResponseWriter, r *http.Request) {
	var req sso.PlanRequest
	if err := decodeBody(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	plan, err := h.m.Plan(ctx, req, operatorActor(r))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, plan)
}

func (h *ssoHandlers) approve(w http.ResponseWriter, r *http.Request) {
	var req sso.ApproveRequest
	if err := decodeBody(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if err := h.m.Approve(req, operatorActor(r)); err != nil {
		h.writeError(w, err)
		return
	}
	v, err := h.statusView(r)
	if err != nil {
		http.Error(w, "failed to read sign-in state", http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

func (h *ssoHandlers) apply(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor := operatorActor(r)
	def, err := h.m.ApplyDefinition(actor)
	if err != nil {
		h.writeError(w, err)
		return
	}
	op, err := h.runner.Start(r.Context(), def, "")
	if err != nil {
		http.Error(w, "failed to start the apply operation", http.StatusInternalServerError)
		return
	}
	h.s.auditOperation(h.cfg.Store, h.runner, op.ID, actor, "sso.apply.operation", fmt.Sprintf("operation %d", op.ID))
	writeJSON(w, map[string]any{"id": op.ID})
}

// freshPassword is the in-request re-authentication link and unlink need
// from every session (ADR-033): the cached session is not enough.
func (h *ssoHandlers) freshPassword(w http.ResponseWriter, r *http.Request, op operatorSession) bool {
	password := r.Header.Get(ReauthHeader)
	if password == "" {
		w.Header().Set(ErrorCodeHeader, ErrorCodeReauthRequired)
		http.Error(w, "enter your local password to continue", http.StatusForbidden)
		return false
	}
	switch err := h.cfg.Auth.VerifyPassword(op.Session.Username, password); {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrAccountLocked):
		w.Header().Set(ErrorCodeHeader, ErrorCodeReauthLocked)
		http.Error(w, "account temporarily locked after repeated failures", http.StatusTooManyRequests)
	case errors.Is(err, auth.ErrInvalidCredentials):
		h.audit(op.Session.Username, "sso.reauth", audit.OutcomeError, "wrong password for "+r.Method+" "+r.URL.Path)
		w.Header().Set(ErrorCodeHeader, ErrorCodeReauthInvalid)
		http.Error(w, "that password is not right", http.StatusForbidden)
	default:
		http.Error(w, "re-authentication unavailable", http.StatusInternalServerError)
	}
	return false
}

func (h *ssoHandlers) link(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body struct {
		Origin string `json:"origin"`
	}
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if !h.freshPassword(w, r, op) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if body.Origin != "" {
		origin, err := sso.ValidateOrigin(body.Origin)
		if err != nil {
			h.writeError(w, err)
			return
		}
		body.Origin = origin
	}
	start, err := h.m.CreateLinkIntent(r.Context(), op.Session.IdentityID, op.Session.Username, r.Host, body.Origin)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.audit(op.Session.Username, "sso.link.intent", audit.OutcomeOK, "method=password, single use, 10 minutes")
	writeJSON(w, map[string]any{"url": start.URL, "expires_at": start.ExpiresAt.UTC().Format(time.RFC3339)})
}

func (h *ssoHandlers) unlink(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	if !h.freshPassword(w, r, op) {
		return
	}
	if err := h.m.Unlink(op.Session.Username); err != nil {
		h.writeError(w, err)
		return
	}
	v, err := h.statusView(r)
	if err != nil {
		http.Error(w, "failed to read sign-in state", http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

func (h *ssoHandlers) disable(w http.ResponseWriter, r *http.Request) {
	if err := h.m.Disable(operatorActor(r)); err != nil {
		h.writeError(w, err)
		return
	}
	v, err := h.statusView(r)
	if err != nil {
		http.Error(w, "failed to read sign-in state", http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

// ssoYAMLPath is where the non-secret mirror of the configuration lives.
func ssoYAMLPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "sso.yaml")
}
