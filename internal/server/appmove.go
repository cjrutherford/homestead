package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// Moving an app installed under a name of its own to its path under the
// appliance's names (ADR-044 §5). Never silent: the owner reviews a plan
// with a digest and approves it. The app's serving settings change, its
// route moves into the dashboard site, and sign-in is re-planned by the
// owner afterwards (the app's sign-in status raises the re-plan flag).

// MountAppMove registers the move offer, plan and apply.
func (s *Server) MountAppMove(st *store.Store, secrets *secret.Store, catalogDir string) {
	m := &appMove{s: s, st: st, secrets: secrets, catalogDir: catalogDir}
	s.mux.HandleFunc("GET /api/v1/catalog/{name}/move", m.status)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/move/plan", m.plan)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/move/apply", m.apply)
}

type appMove struct {
	s          *Server
	st         *store.Store
	secrets    *secret.Store
	catalogDir string
}

// Seams for tests: the configuration write and stack restarts.
var (
	moveConfigure = catalog.ConfigureDefinition
	moveStop      = catalog.StopStack
	moveSeeds     = catalog.MoveSeeds
	moveApplySeed = catalog.ApplyMoveSeed
)

type appMoveStatus struct {
	App     string `json:"app"`
	Offered bool   `json:"offered"`
	Reason  string `json:"reason,omitempty"`
	// From is the app's current address; Path and To are where it moves.
	From string   `json:"from,omitempty"`
	Path string   `json:"path,omitempty"`
	To   []string `json:"to"`
}

type appMovePlan struct {
	App         string   `json:"app"`
	Route       string   `json:"route"`
	From        string   `json:"from"`
	Path        string   `json:"path"`
	To          []string `json:"to"`
	StripPrefix bool     `json:"strip_prefix"`
	Redirects   []string `json:"redirects"`
	// AppSettings are the app's own serving settings the move writes.
	AppSettings []string `json:"app_settings"`
	SignIn      []string `json:"sign_in"`
	Warnings    []string `json:"warnings"`
	Digest      string   `json:"digest"`

	manifest catalog.Manifest
	names    []string
	env      []catalog.ConfigChange
	seeds    []catalog.MoveSeed
	// publish keeps the moved path published on the dashboard's tunnel.
	publish bool
}

func (p appMovePlan) digest() string {
	visible := p
	visible.Digest = ""
	data, _ := json.Marshal(visible)
	h := sha256.New()
	h.Write(data)
	hidden, _ := json.Marshal(struct {
		Names   []string
		Env     []catalog.ConfigChange
		Seeds   []catalog.MoveSeed
		Publish bool
	}{p.names, p.env, p.seeds, p.publish})
	h.Write(hidden)
	return hex.EncodeToString(h.Sum(nil))
}

// moveFacts is what the offer and the plan read.
// keepsAuthentikPublic reports whether Authentik, once moved from route
// to its path, is published on the dashboard's tunnel as a fresh tunnel
// plan would publish it (buildTunnelPlan, via authentikPathPublished).
// The move never creates a tunnel: the dashboard must already have
// public names.
func keepsAuthentikPublic(routes []docker.Route, route, path string, signInLinked bool) bool {
	projected := append([]docker.Route(nil), routes...)
	for i := range projected {
		if strings.EqualFold(string(projected[i].Host), route) {
			projected[i].Path = path
		}
	}
	dash := dashboardRoute(projected)
	return dash >= 0 && len(projected[dash].Tunnel) > 0 && authentikPathPublished(projected, signInLinked)
}

func (m *appMove) signInLinked() bool {
	return m.s.signInOffered != nil && m.s.signInOffered()
}

type moveFacts struct {
	manifest catalog.Manifest
	route    docker.Route
	names    []string
	signIn   bool
	reason   string
}

func (m *appMove) facts(name string) (moveFacts, error) {
	entries, err := catalog.Load(m.catalogDir)
	if err != nil {
		return moveFacts{}, fmt.Errorf("catalog unavailable: %w", err)
	}
	manifest, err := entries.Get(name)
	if err != nil {
		return moveFacts{}, errUnknownApp
	}
	f := moveFacts{manifest: manifest}
	if !manifest.ServedByPath() {
		f.reason = fmt.Sprintf("%s keeps a name of its own.", manifest.Title)
		return f, nil
	}
	installed, err := catalog.Installed(m.st, manifest.Name, stackDirFor(manifest.Name))
	if err != nil {
		return f, err
	}
	if !installed {
		f.reason = fmt.Sprintf("%s is not installed; an install serves it at %s.", manifest.Title, manifest.AppPath())
		return f, nil
	}
	set, err := loadEdgeRoutes(m.st)
	if err != nil {
		return f, err
	}
	routes := appsso.AppRoutes(manifest, set.Routes)
	if len(routes) == 0 {
		f.reason = fmt.Sprintf("%s has no address on the edge.", manifest.Title)
		return f, nil
	}
	f.route = routes[0]
	if f.route.IsPath() {
		f.reason = fmt.Sprintf("%s is already at %s.", manifest.Title, f.route.Path)
		return f, nil
	}
	if _, ok := set.DashboardRoute(); !ok {
		f.reason = "The dashboard has no address on the edge yet."
		return f, nil
	}
	f.names = applianceNames(m.st)
	if len(f.route.Tunnel) > 0 {
		f.reason = fmt.Sprintf("%s has a public name. Take its public name off first, then move it.", manifest.Title)
		if manifest.Name == authentikPackage {
			f.reason = "Authentik has a public name on the tunnel. Tear down the tunnel, move Authentik, then set the tunnel up again."
		}
		return f, nil
	}
	if _, ok, err := (appsso.Records{DB: m.st.DB()}).Get(manifest.Name); err != nil {
		return f, err
	} else {
		f.signIn = ok
	}
	return f, nil
}

func (f moveFacts) toURLs() []string {
	out := []string{}
	for _, n := range f.names {
		out = append(out, docker.PathURL(docker.Hostname(n), f.manifest.AppPath()))
	}
	return out
}

func (m *appMove) status(w http.ResponseWriter, r *http.Request) {
	f, err := m.facts(r.PathValue("name"))
	if err != nil {
		(&appPublic{}).writeError(w, err)
		return
	}
	out := appMoveStatus{App: f.manifest.Name, Offered: f.reason == "", Reason: f.reason, To: []string{}}
	if f.manifest.ServedByPath() {
		out.Path = f.manifest.AppPath()
	}
	if f.route.Host != "" && !f.route.IsPath() {
		out.From = "https://" + string(f.route.Host) + "/"
		out.To = f.toURLs()
	}
	writeJSON(w, out)
}

// buildPlan reads the store and the stack and changes nothing.
func (m *appMove) buildPlan(name string) (appMovePlan, error) {
	f, err := m.facts(name)
	if err != nil {
		return appMovePlan{}, err
	}
	if f.reason != "" {
		return appMovePlan{}, refusePlan(http.StatusUnprocessableEntity, "%s", f.reason)
	}
	mf := f.manifest
	plan := appMovePlan{
		App: mf.Name, Route: string(f.route.Host), From: "https://" + string(f.route.Host) + "/",
		Path: mf.AppPath(), To: f.toURLs(), StripPrefix: mf.StripPrefix,
		Redirects: []string{}, AppSettings: []string{}, SignIn: []string{}, Warnings: []string{},
		manifest: mf, names: f.names,
	}
	for _, rd := range mf.Redirects {
		plan.Redirects = append(plan.Redirects, fmt.Sprintf("%s -> %s", rd.From, rd.To))
	}
	plan.env = catalog.MoveEnv(mf, f.names)
	for _, change := range plan.env {
		keys := make([]string, 0, len(change.Set))
		for k := range change.Set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			plan.AppSettings = append(plan.AppSettings, fmt.Sprintf("%s: %s=%s", change.Service, k, change.Set[k]))
		}
	}
	if len(mf.Seeds) > 0 {
		seeds, err := moveSeeds(mf, stackDirFor(mf.Name))
		if err != nil {
			return appMovePlan{}, refusePlan(http.StatusUnprocessableEntity, "%v", err)
		}
		plan.seeds = seeds
		for _, seed := range seeds {
			keys := make([]string, 0, len(seed.Elements))
			for k := range seed.Elements {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				plan.AppSettings = append(plan.AppSettings, fmt.Sprintf("%s: %s sets <%s>%s</%s> (the app is stopped while it is written)", seed.Service, seed.Target, k, seed.Elements[k], k))
			}
		}
	}
	if extra := appsso.HostSettings(mf, f.names, nil); len(extra) > 0 {
		plan.AppSettings = append(plan.AppSettings, extra...)
	}
	plan.Warnings = append(plan.Warnings,
		fmt.Sprintf("%s stops answering at %s; bookmarks and app clients pointing there need the new address.", mf.Title, plan.From),
		fmt.Sprintf("%s restarts to take its new settings.", mf.Title),
		"Apps served by path share one web origin with the dashboard (ADR-044).")
	if mf.Name == authentikPackage {
		set, err := loadEdgeRoutes(m.st)
		if err != nil {
			return appMovePlan{}, err
		}
		plan.publish = keepsAuthentikPublic(set.Routes, plan.Route, plan.Path, m.signInLinked())
		if plan.publish {
			plan.SignIn = append(plan.SignIn,
				"Public reachability: Authentik's path stays published on the tunnel with the dashboard, so public sign-in and recovery links keep answering at the dashboard's public name.")
		} else if dash, ok := set.DashboardRoute(); ok && len(dash.Tunnel) > 0 {
			plan.SignIn = append(plan.SignIn,
				"Public reachability: the dashboard is on the tunnel but sign-in is not linked, so Authentik's path is not published; public sign-in is not offered.")
		}
		plan.SignIn = append(plan.SignIn,
			"Dashboard sign-in and every app's sign-in use Authentik's old address and stop working until you set them up again at the new address (Settings, Sign-in, then each app). Password sign-in to the dashboard keeps working.")
	} else if f.signIn {
		plan.SignIn = append(plan.SignIn,
			fmt.Sprintf("%s's sign-in through Authentik names the old address; re-plan it on the app page after the move.", mf.Title))
	}
	plan.Digest = plan.digest()
	return plan, nil
}

type appMoveRequest struct {
	Digest string `json:"digest"`
}

func (m *appMove) plan(w http.ResponseWriter, r *http.Request) {
	plan, err := m.buildPlan(r.PathValue("name"))
	if err != nil {
		(&appPublic{}).writeError(w, err)
		return
	}
	writeJSON(w, plan)
}

func (m *appMove) apply(w http.ResponseWriter, r *http.Request) {
	var req appMoveRequest
	if !decodeResolverRequest(w, r, &req) {
		return
	}
	if req.Digest == "" {
		http.Error(w, "digest is required: review the plan, then approve it with its digest", http.StatusBadRequest)
		return
	}
	plan, err := m.buildPlan(r.PathValue("name"))
	if err != nil {
		(&appPublic{}).writeError(w, err)
		return
	}
	if plan.Digest != req.Digest {
		http.Error(w, "the plan changed since it was reviewed; review it again", http.StatusConflict)
		return
	}
	def, err := appMoveDefinition(m.st, m.secrets, plan)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	runner := operation.NewRunner(m.st)
	op, err := runner.Start(r.Context(), def, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m.s.auditOperation(m.st, runner, op.ID, operatorActor(r), "app.move",
		fmt.Sprintf("operation %d: %s: %s -> %s", op.ID, plan.App, plan.From, strings.Join(plan.To, ", ")))
	writeJSON(w, map[string]any{"id": op.ID})
}

// appMoveDefinition: the app's first-start files (stopped while they are
// written), its environment and a restart, its host allow-list, then the
// edge, which serves it at its path and no longer at its own name.
func appMoveDefinition(st *store.Store, secrets *secret.Store, plan appMovePlan) (operation.Definition, error) {
	mf := plan.manifest
	dir, project := stackDirFor(mf.Name), mf.Name
	var secretStore catalog.SecretStore
	if secrets != nil {
		secretStore = secrets
	}
	configure, err := moveConfigure(catalog.ConfigPlan{Manifest: mf, Changes: plan.env}, dir, project, st, secretStore)
	if err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: "app.move"}
	if len(plan.seeds) > 0 {
		seeds := plan.seeds
		def.Steps = append(def.Steps, operation.StepDef{Name: "seed-path", Run: func(ctx context.Context) error {
			if err := moveStop(ctx, dir, project); err != nil {
				return err
			}
			for _, seed := range seeds {
				if err := moveApplySeed(seed); err != nil {
					return err
				}
			}
			return nil
		}})
	}
	def.Steps = append(def.Steps, configure.Steps...)
	names, publish := plan.names, plan.publish
	address := strings.TrimSuffix(docker.PathURL(docker.Hostname(names[0]), plan.Path), "/")
	def.Steps = append(def.Steps, operation.StepDef{Name: "app-hosts", Run: func(ctx context.Context) error {
		return trustAppHosts(ctx, mf, address, names, nil)
	}})
	def.Steps = append(def.Steps, operation.StepDef{Name: "edge", Run: func(ctx context.Context) error {
		set, err := loadEdgeRoutes(st)
		if err != nil {
			return err
		}
		found := false
		for i := range set.Routes {
			if strings.EqualFold(string(set.Routes[i].Host), plan.Route) {
				set.Routes[i].Path = plan.Path
				set.Routes[i].StripPrefix = mf.StripPrefix
				set.Routes[i].Redirects = append([]docker.PathRedirect(nil), mf.Redirects...)
				set.Routes[i].Tunnel = nil
				// Authentik stays published when a fresh tunnel plan would
				// publish its path (S395).
				set.Routes[i].Published = publish
				if err := set.Routes[i].Validate(); err != nil {
					return err
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s's route %s is gone from the edge", mf.Title, plan.Route)
		}
		return reconcileEdgeSet(ctx, st, set)
	}})
	return def, nil
}
