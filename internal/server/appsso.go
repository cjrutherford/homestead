package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/sso"
	"homestead/internal/store"
)

// App sign-in through Authentik (S298, ADR-039 §3, ADR-040): status, a
// read-only plan, and apply by digest as an operation. Every route is
// session class; the Authentik API token travels only in the apply body
// and stays in memory unless the dashboard sign-in kept it sealed.

// appSignIn holds what the app sign-in routes share with MountSSO.
type appSignIn struct {
	s      *Server
	cfg    SSOConfig
	m      *sso.Manager
	client func() *http.Client
	runner *operation.Runner
	audit  func(actor, action, outcome, detail string)
}

// inputs gathers the appliance state a plan is built from.
func (a *appSignIn) inputs(name string) (appsso.Inputs, error) {
	entries, err := catalog.Load(a.cfg.CatalogDir)
	if err != nil {
		return appsso.Inputs{}, fmt.Errorf("catalog unavailable: %w", err)
	}
	manifest, err := entries.Get(name)
	if err != nil {
		return appsso.Inputs{}, errUnknownApp
	}
	return appSignInInputs(a.cfg.Store, a.m, manifest)
}

var errUnknownApp = errors.New("unknown app")

func appSignInInputs(st *store.Store, m *sso.Manager, manifest catalog.Manifest) (appsso.Inputs, error) {
	in := appsso.Inputs{Manifest: manifest, AuthentikInstalled: authentikInstalled(st)}
	installed, err := catalog.Installed(st, manifest.Name, stackDirFor(manifest.Name))
	if err != nil {
		return in, err
	}
	in.Installed = installed
	status, err := m.Status()
	if err != nil {
		return in, err
	}
	if spec, ok, err := m.Config(); err != nil {
		return in, err
	} else if ok {
		in.Dashboard, in.DashboardOK = spec, status.Configured
	}
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return in, err
	}
	in.Routes = appsso.AppRoutes(manifest, set.Routes)
	rec, ok, err := (appsso.Records{DB: st.DB()}).Get(manifest.Name)
	if err != nil {
		return in, err
	}
	if ok {
		in.Applied = &rec
	}
	// The linked owner (matched at the dashboard's issuer) joins the app's
	// groups (ADR-043 §1).
	if status.Offered {
		in.OwnerSub = status.Subject
	}
	return in, nil
}

// tokenKept says the dashboard sign-in kept its Authentik API token
// sealed (the owner opted in), so apply needs no token in the request.
func (a *appSignIn) tokenKept() bool {
	if a.cfg.Secrets == nil {
		return false
	}
	_, err := a.cfg.Secrets.Get(sso.APITokenName)
	return err == nil
}

func (a *appSignIn) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errUnknownApp):
		http.Error(w, "unknown app", http.StatusNotFound)
	case errors.Is(err, appsso.ErrUnavailable), errors.Is(err, errNoToken):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		a.s.log.Warn("app sign-in request failed", "error", err)
		http.Error(w, "app sign-in request failed", http.StatusInternalServerError)
	}
}

func (a *appSignIn) status(w http.ResponseWriter, r *http.Request) {
	in, err := a.inputs(r.PathValue("name"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, appsso.BuildStatus(in, a.tokenKept()))
}

// appSignInRequest is the plan and apply body. Token is the Authentik API
// token for this apply only; it is never stored or logged.
type appSignInRequest struct {
	Enabled *bool  `json:"enabled"`
	Digest  string `json:"digest,omitempty"`
	Token   string `json:"token,omitempty"`
}

func (a *appSignIn) plan(w http.ResponseWriter, r *http.Request) {
	var req appSignInRequest
	if !decodeResolverRequest(w, r, &req) || !requireEnabled(w, req.Enabled) {
		return
	}
	in, err := a.inputs(r.PathValue("name"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	plan, err := appsso.BuildPlan(in, *req.Enabled)
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, plan)
}

var errNoToken = errors.New("an Authentik API token is needed: paste one with permission to manage providers and applications (it is used for this change only and not kept)")

func (a *appSignIn) apply(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req appSignInRequest
	if !decodeResolverRequest(w, r, &req) || !requireEnabled(w, req.Enabled) {
		return
	}
	in, err := a.inputs(r.PathValue("name"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	plan, err := appsso.BuildPlan(in, *req.Enabled)
	if err != nil {
		a.writeError(w, err)
		return
	}
	if req.Digest == "" || plan.Digest != req.Digest {
		http.Error(w, "the plan changed since it was reviewed; review it again", http.StatusConflict)
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" && a.cfg.Secrets != nil {
		token, _ = a.cfg.Secrets.Get(sso.APITokenName)
	}
	if token == "" {
		a.writeError(w, errNoToken)
		return
	}
	dashboard := in.Dashboard
	if !in.DashboardOK && in.Applied != nil {
		// Turning off after dashboard sign-in went away: the issuer
		// recorded at apply names the Authentik that holds the objects.
		dashboard.AuthentikURL = strings.SplitN(in.Applied.Issuer, "/application/", 2)[0]
	}
	rt := appsso.Runtime{
		Records: appsso.Records{DB: a.cfg.Store.DB()}, Secrets: a.cfg.Secrets, Audit: a.audit,
		StackDir: stackDirFor,
		RootPEM: func() ([]byte, error) {
			if a.cfg.Local == nil {
				return nil, errors.New("the appliance certificate authority is not available")
			}
			return a.cfg.Local.CA.RootPEM()
		},
		Fingerprint: func(pkg, dir string) error { return catalog.RecordFingerprints(a.cfg.Store, pkg, dir) },
	}
	api := &sso.API{Base: dashboard.AuthentikURL, Token: token, Client: a.client}
	actor := operatorActor(r)
	def, err := appsso.Definition(rt, plan, in.Manifest, dashboard, api, actor)
	if err != nil {
		a.writeError(w, err)
		return
	}
	op, err := a.runner.Start(r.Context(), def, "")
	if err != nil {
		http.Error(w, "failed to start the sign-in operation", http.StatusInternalServerError)
		return
	}
	a.s.auditOperation(a.cfg.Store, a.runner, op.ID, actor, "app.sign-in.operation", fmt.Sprintf("%s: operation %d", in.Manifest.Name, op.ID))
	writeJSON(w, map[string]any{"id": op.ID})
}

// access reads back the app's groups, the owner's membership, the
// bindings and the claim mapping from Authentik (ADR-043 §6) and changes
// nothing. A difference is drift and is not reverted.
func (a *appSignIn) access(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req accessRequest
	if !decodeResolverRequest(w, r, &req) {
		return
	}
	in, err := a.inputs(r.PathValue("name"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	if in.Applied == nil {
		http.Error(w, "sign-in with Authentik is off for this app", http.StatusConflict)
		return
	}
	token := resolveToken(req.Token, a.cfg.Secrets)
	if token == "" {
		a.writeError(w, errNoToken)
		return
	}
	// A sign-in from before groups is read against what a re-plan would set
	// up: its application shows as open.
	expected := in.Applied.Access
	if expected == nil {
		plan, err := appsso.BuildPlan(in, true)
		if err != nil {
			a.writeError(w, err)
			return
		}
		expected = plan.Access
	}
	dashboard := in.Dashboard
	if !in.DashboardOK {
		dashboard.AuthentikURL = strings.SplitN(in.Applied.Issuer, "/application/", 2)[0]
	}
	api := &sso.API{Base: dashboard.AuthentikURL, Token: token, Client: a.client}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rep, err := api.ReadAccess(ctx, *expected, in.Applied.ClientID)
	if err != nil {
		a.s.log.Warn("app access read-back failed", "error", err)
		http.Error(w, "Authentik could not be read: "+readBackReason(err), http.StatusBadGateway)
		return
	}
	writeJSON(w, rep)
}

// mountAppSignIn registers the app sign-in routes beside dashboard SSO.
func (s *Server) mountAppSignIn(a *appSignIn) {
	s.appSignIn = a
	s.mux.HandleFunc("GET /api/v1/catalog/{name}/sign-in", a.status)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/sign-in/plan", a.plan)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/sign-in/apply", a.apply)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/sign-in/access", a.access)
}

// signInPreview is the install plan's sign-in offer: the plan applying
// would approve once the app is installed and routed, or why it is not
// offered. The plan's names assume the app's default route, so the
// wizard's chained apply answers 409 if they changed in between.
type signInPreview struct {
	Offered bool         `json:"offered"`
	Reason  string       `json:"reason,omitempty"`
	Plan    *appsso.Plan `json:"plan,omitempty"`
	// TokenKept says apply needs no Authentik API token in the request.
	TokenKept bool `json:"token_kept"`
}

// preview builds the install plan's sign-in offer from the routes the
// wizard adds (the plan's non-public routes, merged by host over the
// current table, as the wizard merges them). A public name reviewed with
// the install (S313) is on the first of them, as the wizard publishes it
// before sign-in.
func (a *appSignIn) preview(manifest catalog.Manifest, install catalog.InstallPlan, public string) *signInPreview {
	if manifest.SSO.OIDC == nil {
		return nil
	}
	in, err := appSignInInputs(a.cfg.Store, a.m, manifest)
	if err != nil {
		return &signInPreview{Reason: "The sign-in state could not be read."}
	}
	planned := map[docker.Hostname]docker.Route{}
	for _, r := range install.Routes {
		if r.Exposure != docker.ExposurePublic {
			planned[r.Host] = r
		}
	}
	set, _ := loadEdgeRoutes(a.cfg.Store)
	table := []docker.Route{}
	for _, r := range set.Routes {
		if _, replaced := planned[r.Host]; !replaced {
			table = append(table, r)
		}
	}
	for _, r := range install.Routes {
		if _, ok := planned[r.Host]; ok {
			if public != "" {
				r.Tunnel = append(append([]docker.Hostname(nil), r.Tunnel...), docker.Hostname(public))
				public = ""
			}
			table = append(table, r)
		}
	}
	in.Installed, in.Routes = true, appsso.AppRoutes(manifest, table)
	if ok, reason := in.Offered(); !ok {
		return &signInPreview{Reason: reason}
	}
	plan, err := appsso.BuildPlan(in, true)
	if err != nil {
		return &signInPreview{Reason: err.Error()}
	}
	return &signInPreview{Offered: true, Plan: &plan, TokenKept: a.tokenKept()}
}
