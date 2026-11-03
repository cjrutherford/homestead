package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// Public exposure of Everything read links (S433, ADR-058): a read-only
// plan, and approval by digest as an audited operation. Only
// <app path>_homestead/read/<token> and <app path>_homestead/view/<token>
// are ever opened on the dashboard's public name; the app itself, its data,
// events and source never are. Both POST routes and the status are session
// class.

// ReadLinksOriginEnv is the bridge's environment value naming the public
// origin that may mint links (ADR-058). The override file carries it, so a
// restart without the daemon keeps it.
const ReadLinksOriginEnv = "HOMESTEAD_EVERYTHING_PUBLIC_READ_ORIGIN"

// readLinkExpiryCapDays is the bridge's default longest link lifetime.
const readLinkExpiryCapDays = 365

// MountReadLinks registers the read-link exposure routes.
func (s *Server) MountReadLinks(st *store.Store, catalogDir string) {
	p := &readLinks{s: s, st: st, catalogDir: catalogDir}
	s.mux.HandleFunc("GET /api/v1/apps/{name}/read-links", p.status)
	s.mux.HandleFunc("POST /api/v1/apps/{name}/read-links/plan", p.plan)
	s.mux.HandleFunc("POST /api/v1/apps/{name}/read-links/approve", p.approve)
}

type readLinks struct {
	s          *Server
	st         *store.Store
	catalogDir string
}

// readLinksUp recreates the app's stack after its override changed, and
// readLinksFingerprint re-baselines drift; tests replace both.
var (
	readLinksUp          = catalog.UpStack
	readLinksFingerprint = catalog.RecordFingerprints
)

// bridgeLinkCount reads the number of live links from the bridge tool in
// the app's container; tests replace it. -1 means it could not be read.
var bridgeLinkCount = func(ctx context.Context, container, app string) int {
	out, err := dockerExec(ctx, container, "/everything-bridge", "links", "list", "-db", "/data/bridge.db", "-app", app)
	if err != nil {
		return -1
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) == 2 && fields[1] == "links" {
		if n, err := strconv.Atoi(fields[0]); err == nil {
			return n
		}
	}
	return -1
}

type readLinksFacts struct {
	manifest  catalog.Manifest
	service   string
	container string
	route     docker.Route
	server    docker.Hostname
	signIn    bool
	reason    string
}

// facts reads what status and plan are made from. An unknown or non
// Everything app is errUnknownApp: the routes exist only for bridge apps.
func (p *readLinks) facts(name string) (readLinksFacts, error) {
	entries, err := catalog.Load(p.catalogDir)
	if err != nil {
		return readLinksFacts{}, fmt.Errorf("catalog unavailable: %w", err)
	}
	m, err := entries.Get(name)
	if err != nil {
		return readLinksFacts{}, errUnknownApp
	}
	f := readLinksFacts{manifest: m}
	for _, svc := range m.Services {
		if strings.HasPrefix(svc.Image, everythingBridgeImage) {
			f.service, f.container = svc.Name, m.Name+"-"+svc.Name
			break
		}
	}
	if f.service == "" {
		return readLinksFacts{}, errUnknownApp
	}
	installed, err := catalog.Installed(p.st, m.Name, stackDirFor(m.Name))
	if err != nil {
		return f, err
	}
	set, err := loadEdgeRoutes(p.st)
	if err != nil {
		return f, err
	}
	f.server = docker.PublicServerName(set.Routes)
	if routes := appsso.AppRoutes(m, set.Routes); len(routes) > 0 {
		f.route = routes[0]
	}
	if _, ok, err := (appsso.Records{DB: p.st.DB()}).Get(m.Name); err != nil {
		return f, err
	} else {
		f.signIn = ok
	}
	switch {
	case !installed:
		f.reason = fmt.Sprintf("Install %s first.", m.Title)
	case !f.route.IsPath() || f.route.CraftApp != "" || f.route.StripPrefix:
		f.reason = fmt.Sprintf("%s has no address under the appliance's own name yet.", m.Title)
	}
	return f, nil
}

// noPublicName is why read links cannot be exposed now; an already exposed
// app can still be taken off without a public name.
const noPublicName = "The dashboard has no public name. Set up the tunnel first (Settings, Tunnel)."

func (f readLinksFacts) origin() string {
	if f.server == "" {
		return ""
	}
	return "https://" + string(f.server)
}

// patterns are the public URL patterns the plan opens, and nothing else.
func (f readLinksFacts) patterns() []string {
	out := make([]string, 0, len(docker.ReadLinkPaths))
	for _, rel := range docker.ReadLinkPaths {
		out = append(out, f.origin()+f.route.Path+rel+"*")
	}
	return out
}

type readLinksStatus struct {
	App       string   `json:"app"`
	Title     string   `json:"title"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
	Exposed   bool     `json:"exposed"`
	Origin    string   `json:"origin,omitempty"`
	Patterns  []string `json:"patterns,omitempty"`
	Links     int      `json:"links"`
	SignIn    bool     `json:"sign_in"`
}

type readLinksPlan struct {
	App    string `json:"app"`
	Title  string `json:"title"`
	Action string `json:"action"` // expose or unexpose
	// Origin is the public origin that may mint links; Patterns are the
	// only public URLs; Refused is what stays refused on the public name.
	Origin   string   `json:"origin"`
	Route    string   `json:"route"`
	Patterns []string `json:"patterns"`
	Refused  []string `json:"refused"`
	// Links is the live link count read from the bridge (-1 when it could
	// not be read). It is not part of the digest.
	Links         int      `json:"links"`
	ExpiryCapDays int      `json:"expiry_cap_days"`
	Warnings      []string `json:"warnings"`
	Digest        string   `json:"digest"`
	service       string
	path          string
}

func (pl readLinksPlan) digest() string {
	visible := pl
	visible.Digest, visible.Links = "", 0
	data, _ := json.Marshal(visible)
	sum := sha256.Sum256(append(data, []byte("\nservice="+pl.service+"\npath="+pl.path)...))
	return hex.EncodeToString(sum[:])
}

func (p *readLinks) writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, errUnknownApp) {
		http.Error(w, "unknown app", http.StatusNotFound)
		return
	}
	writePlanError(w, err)
}

func (p *readLinks) status(w http.ResponseWriter, r *http.Request) {
	f, err := p.facts(r.PathValue("name"))
	if err != nil {
		p.writeError(w, err)
		return
	}
	if f.reason == "" && f.server == "" && !f.route.ReadLinks {
		f.reason = noPublicName
	}
	out := readLinksStatus{App: f.manifest.Name, Title: f.manifest.Title, Available: f.reason == "", Reason: f.reason,
		Exposed: f.route.ReadLinks, Links: -1, SignIn: f.signIn}
	if f.reason == "" {
		out.Origin = f.origin()
		if f.server != "" {
			out.Patterns = f.patterns()
		}
		out.Links = bridgeLinkCount(r.Context(), f.container, f.manifest.Name)
	}
	writeJSON(w, out)
}

func (p *readLinks) buildPlan(ctx context.Context, name string, enabled bool) (readLinksPlan, error) {
	f, err := p.facts(name)
	if err != nil {
		return readLinksPlan{}, err
	}
	if f.reason != "" {
		return readLinksPlan{}, refusePlan(http.StatusUnprocessableEntity, "%s", f.reason)
	}
	if enabled && f.server == "" {
		return readLinksPlan{}, refusePlan(http.StatusUnprocessableEntity, "%s", noPublicName)
	}
	if enabled && f.route.ReadLinks {
		return readLinksPlan{}, refusePlan(http.StatusUnprocessableEntity, "read links of %s are already exposed on %s", f.manifest.Title, f.server)
	}
	if !enabled && !f.route.ReadLinks {
		return readLinksPlan{}, refusePlan(http.StatusUnprocessableEntity, "read links of %s are not exposed", f.manifest.Title)
	}
	pl := readLinksPlan{App: f.manifest.Name, Title: f.manifest.Title, Action: "expose", Origin: f.origin(), Route: string(f.route.Host),
		ExpiryCapDays: readLinkExpiryCapDays, service: f.service, path: f.route.Path,
		Refused: []string{
			"Every other path of " + f.manifest.Title + " (its pages, data, events, source and sign-in) stays 404 on the public name" + publishedNote(f.route) + ".",
			"Only GET and HEAD of a path ending in one 43 character link; anything else answers 404 at the edge.",
		}}
	if f.server != "" {
		pl.Patterns = f.patterns()
	}
	if !enabled {
		pl.Action = "unexpose"
	}
	if env, err := catalog.OverrideEnv(stackDirFor(f.manifest.Name), f.service); err == nil {
		if v, ok := env["HOMESTEAD_EVERYTHING_READ_LINK_MAX_DAYS"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n < pl.ExpiryCapDays {
				pl.ExpiryCapDays = n
			}
		}
	}
	pl.Links = bridgeLinkCount(ctx, f.container, f.manifest.Name)
	if enabled {
		pl.Warnings = append(pl.Warnings,
			"Anyone who holds a link can read that one file until the link expires or is revoked. Links are bearer secrets: treat them like a password.",
			fmt.Sprintf("A link lasts at most %d days. Deleting, renaming or revoking a file ends its links at once.", pl.ExpiryCapDays),
			"Nothing but the link opens. There is no listing, no sign-in page and no way to name another person's file.",
			"The bridge is told the public origin "+pl.Origin+" through "+f.manifest.Title+"'s Compose override, and the app is recreated once so it takes effect.")
		if !f.signIn {
			pl.Warnings = append(pl.Warnings, "App sign-in is off, so "+f.manifest.Title+" serves nothing and cannot make links yet.")
		}
		if f.route.Published {
			pl.Warnings = append(pl.Warnings, f.manifest.Title+" is already published on the public name as a whole; these routes add nothing at the edge.")
		}
	} else {
		pl.Warnings = append(pl.Warnings, "The public routes are removed first. Existing links keep working on the appliance's own names until they expire; revoke them there if you want them gone.")
	}
	pl.Digest = pl.digest()
	// The live count is advice, not part of what is approved.
	if enabled && pl.Links > 0 {
		pl.Warnings = append(pl.Warnings, fmt.Sprintf("%d links already exist; they become reachable from the internet only when their URL is on the public name.", pl.Links))
	}
	return pl, nil
}

func publishedNote(r docker.Route) string {
	if r.Published {
		return " unless the app itself is published (it is)"
	}
	return " unless the app itself is published (it is not)"
}

type readLinksRequest struct {
	Enabled *bool  `json:"enabled"`
	Digest  string `json:"digest"`
}

func (p *readLinks) decode(w http.ResponseWriter, r *http.Request, approve bool) (readLinksRequest, bool) {
	var req readLinksRequest
	if !decodeResolverRequest(w, r, &req) || !requireEnabled(w, req.Enabled) {
		return req, false
	}
	if !approve && req.Digest != "" {
		http.Error(w, "a plan takes no digest", http.StatusBadRequest)
		return req, false
	}
	if approve && req.Digest == "" {
		http.Error(w, "digest is required: review the plan, then approve it with its digest", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

func (p *readLinks) plan(w http.ResponseWriter, r *http.Request) {
	req, ok := p.decode(w, r, false)
	if !ok {
		return
	}
	plan, err := p.buildPlan(r.Context(), r.PathValue("name"), *req.Enabled)
	if err != nil {
		p.writeError(w, err)
		return
	}
	writeJSON(w, plan)
}

func (p *readLinks) approve(w http.ResponseWriter, r *http.Request) {
	req, ok := p.decode(w, r, true)
	if !ok {
		return
	}
	plan, err := p.buildPlan(r.Context(), r.PathValue("name"), *req.Enabled)
	if err != nil {
		p.writeError(w, err)
		return
	}
	if plan.Digest != req.Digest {
		http.Error(w, errPlanChanged.Error(), http.StatusConflict)
		return
	}
	def := p.definition(plan)
	runner := operation.NewRunner(p.st)
	op, err := runner.Start(r.Context(), def, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	action := "readlinks.expose"
	if plan.Action == "unexpose" {
		action = "readlinks.unexpose"
	}
	p.s.auditOperation(p.st, runner, op.ID, operatorActor(r), action,
		fmt.Sprintf("operation %d: %s read and view paths on %s", op.ID, plan.App, plan.Origin))
	writeJSON(w, map[string]any{"id": op.ID})
}

// definition is the change: the bridge learns the origin before the edge
// opens the routes, and the edge closes before the bridge forgets it.
func (p *readLinks) definition(plan readLinksPlan) operation.Definition {
	expose := plan.Action == "expose"
	dir := stackDirFor(plan.App)
	bridge := operation.StepDef{Name: "bridge-origin", Run: func(ctx context.Context) error {
		value := plan.Origin
		if !expose {
			// Take out whatever origin is recorded, even one from before a
			// public name change.
			env, err := catalog.OverrideEnv(dir, plan.service)
			if err != nil {
				return err
			}
			v, ok := env[ReadLinksOriginEnv]
			if !ok {
				return nil
			}
			value = v
		}
		if _, err := catalog.ApplySignInWiring(dir, catalog.SignInWiring{Service: plan.service, Env: map[string]string{ReadLinksOriginEnv: value}}, expose); err != nil {
			return err
		}
		if err := readLinksFingerprint(p.st, plan.App, dir); err != nil {
			return err
		}
		return readLinksUp(ctx, dir, plan.App)
	}}
	edge := operation.StepDef{Name: "edge", Run: func(ctx context.Context) error {
		set, err := loadEdgeRoutes(p.st)
		if err != nil {
			return err
		}
		found := false
		for i := range set.Routes {
			if strings.EqualFold(string(set.Routes[i].Host), plan.Route) && set.Routes[i].IsPath() && set.Routes[i].Path == plan.path {
				set.Routes[i].ReadLinks = expose
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s's route %s is gone from the edge", plan.Title, plan.Route)
		}
		return reconcileEdgeSet(ctx, p.st, set)
	}}
	if expose {
		return operation.Definition{Type: "readlinks.expose", Steps: []operation.StepDef{bridge, edge}}
	}
	return operation.Definition{Type: "readlinks.unexpose", Steps: []operation.StepDef{edge, bridge}}
}
