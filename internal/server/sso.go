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

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/catalog"
	"homestead/internal/cert"
	"homestead/internal/claim"
	"homestead/internal/craftfiles"
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
	h := &ssoHandlers{s: s, m: m, cfg: c, runner: operation.NewRunner(c.Store), audit: record}
	s.mux.HandleFunc("GET /api/v1/sso/options", h.options)
	s.mux.HandleFunc("GET /api/v1/sso/login", h.login)
	s.mux.HandleFunc("GET /api/v1/sso/callback", h.callback)
	s.mux.HandleFunc("GET /api/v1/sso", h.status)
	s.mux.HandleFunc("POST /api/v1/sso/plan", h.plan)
	s.mux.HandleFunc("POST /api/v1/sso/approve", h.approve)
	s.mux.HandleFunc("POST /api/v1/sso/apply", h.apply)
	s.mux.HandleFunc("POST /api/v1/sso/link", h.link)
	s.mux.HandleFunc("DELETE /api/v1/sso/link", h.unlink)
	s.mux.HandleFunc("DELETE /api/v1/sso", h.disable)
	return m
}

type ssoHandlers struct {
	s      *Server
	m      *sso.Manager
	cfg    SSOConfig
	runner *operation.Runner
	audit  func(actor, action, outcome, detail string)
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
			if d.AuthentikURL == "" && r.Target.Host == "server" &&
				(strings.HasPrefix(host, authentikPackage+".") || strings.Contains(host, "-"+authentikPackage+".")) {
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
	if err != nil {
		code := sso.CodeToken
		var fe *sso.FlowError
		if errors.As(err, &fe) {
			code = fe.Code
		}
		action := "sso.login"
		actor := "anonymous"
		if out.Kind == sso.KindLink {
			action, actor = "sso.link", orAnon(out.Actor)
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

func (h *ssoHandlers) statusView(r *http.Request) (ssoView, error) {
	st, err := h.m.Status()
	if err != nil {
		return ssoView{}, err
	}
	installed := authentikInstalled(h.cfg.Store)
	v := ssoView{Status: st, Defaults: ssoDefaults(h.cfg.Store, h.cfg.Port), Installed: installed}
	if st.Configured {
		v.ReplanReason = replanReason(st.Spec, v.Defaults)
		v.Replan = v.ReplanReason != ""
	}
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
