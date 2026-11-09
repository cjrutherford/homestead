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
	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
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
	moveStart     = catalog.StartStack
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
	// PublicFrom and PublicTo are set when the app is published at a name
	// of its own: the move takes that name away and publishes the path
	// under the dashboard's public name (ADR-061).
	PublicFrom string `json:"public_from,omitempty"`
	PublicTo   string `json:"public_to,omitempty"`
	// Finish is set when an earlier move stopped before its old public
	// name was removed: approving finishes that removal.
	Finish bool `json:"finish,omitempty"`
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
	// SignInDigest is the digest of the app sign-in plan applied after
	// the move (S400); approving this plan approves that change. Empty
	// when sign-in is left alone.
	SignInDigest string `json:"sign_in_digest,omitempty"`
	// Links are other setup that follows the app's address: the craft
	// files link when Nextcloud moves.
	Links []string `json:"links"`
	// PublicFrom is the app's own public address when it is published at
	// a name of its own; the move takes it off the tunnel (ADR-061).
	// PublicTo is where the app answers publicly afterwards, under the
	// dashboard's public name; no DNS record is created for it. Removes
	// lists everything that goes away with the old name, and Stops the
	// URLs that stop working with no redirect.
	PublicFrom string   `json:"public_from,omitempty"`
	PublicTo   string   `json:"public_to,omitempty"`
	Removes    []string `json:"removes"`
	Stops      []string `json:"stops"`
	// Finish marks the plan that completes a move which stopped before
	// the old public name was removed (S479): nothing of the app changes,
	// the new address is checked, then the name goes.
	Finish   bool     `json:"finish,omitempty"`
	Warnings []string `json:"warnings"`
	Digest   string   `json:"digest"`

	manifest catalog.Manifest
	names    []string
	env      []catalog.ConfigChange
	seeds    []catalog.MoveSeed
	// publish keeps the moved path published on the dashboard's tunnel.
	publish bool
	// publicHost and tunnelID name the public name the move removes and
	// the tunnel it was on; empty when the app has no public name of its
	// own.
	publicHost, tunnelID string
	// filesFrom and filesTo are the craft files link's old and new base
	// URLs; empty unless the link moves with Nextcloud.
	filesFrom, filesTo string
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
		Files   [2]string
		Public  [2]string
	}{p.names, p.env, p.seeds, p.publish, [2]string{p.filesFrom, p.filesTo}, [2]string{p.publicHost, p.tunnelID}})
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
	// publicHost is the public name of its own the app holds, and server
	// the dashboard's public name it moves to (ADR-061); tunnelID is the
	// tunnel they are on.
	publicHost string
	server     docker.Hostname
	tunnelID   string
	// leftover is set when an earlier move stopped before its old public
	// name was removed: the offer is then to finish that removal.
	leftover bool
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
	switch {
	case !manifest.ServedByPath() && manifest.PublishForm() == catalog.PublishNever:
		f.reason = manifest.PublicRefusal()
		return f, nil
	case manifest.PublishForm() == catalog.PublishHost || !manifest.ServedByPath():
		f.reason = fmt.Sprintf("%s keeps a name of its own: %s.", manifest.Title, manifest.PublicFormReason())
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
		pending, ok, err := getMovePending(m.st, manifest.Name)
		if err != nil {
			return f, err
		}
		if ok {
			// Only while something of the old name is still held: a stale
			// row for a name that is gone offers nothing.
			left, err := leftoverPublicNames(m.st, set.Routes)
			if err != nil {
				return f, err
			}
			ok = false
			for _, l := range left {
				ok = ok || strings.EqualFold(l.Host, pending.Host)
			}
		}
		if !ok {
			f.reason = fmt.Sprintf("%s is already at %s.", manifest.Title, f.route.Path)
			return f, nil
		}
		f.names = applianceNames(m.st)
		f.publicHost, f.tunnelID, f.leftover = pending.Host, pending.TunnelID, true
		f.server = docker.PublicServerName(set.Routes)
		if f.server == "" {
			f.reason = fmt.Sprintf("%s's old public name %s is still on the tunnel, and the dashboard has no public name for %s to answer on. Publish the dashboard on the Tunnel page first.", manifest.Title, pending.Host, manifest.Title)
		}
		return f, nil
	}
	if _, ok := set.DashboardRoute(); !ok {
		f.reason = "The dashboard has no address on the edge yet."
		return f, nil
	}
	f.names = applianceNames(m.st)
	if len(f.route.Tunnel) > 0 {
		switch {
		case manifest.Name == authentikPackage:
			f.reason = "Authentik has a public name on the tunnel. Tear down the tunnel, move Authentik, then set the tunnel up again."
		case manifest.PublishForm() == catalog.PublishNever:
			f.reason = manifest.PublicRefusal() + " Take its public name off first, then move it."
		case len(f.route.Tunnel) > 1:
			f.reason = fmt.Sprintf("%s has several public names. Take all but one off first, then move it.", manifest.Title)
		default:
			// ADR-061: the move publishes the path on the dashboard's
			// public name and removes the app's own name, in one change.
			f.server = docker.PublicServerName(set.Routes)
			var err error
			if f.tunnelID, _, err = router.RecordedTunnel(m.st); err != nil {
				return f, err
			}
			switch {
			case f.server == "":
				f.reason = fmt.Sprintf("%s has a public name, and the dashboard has none to move it to. Publish the dashboard on the Tunnel page first.", manifest.Title)
			case f.tunnelID == "":
				f.reason = "No tunnel is recorded, so the public name cannot be moved. Set one up, or adopt yours, on the Tunnel page."
			default:
				f.publicHost = string(f.route.Tunnel[0])
			}
		}
		if f.reason != "" {
			return f, nil
		}
	}
	if _, ok, err := (appsso.Records{DB: m.st.DB()}).Get(manifest.Name); err != nil {
		return f, err
	} else {
		f.signIn = ok
	}
	return f, nil
}

// publicFromTo are the app's public address before and after the move, or
// empty when it has no public name of its own.
func (f moveFacts) publicFromTo() (string, string) {
	if f.publicHost == "" {
		return "", ""
	}
	return "https://" + f.publicHost + "/", docker.PathURL(f.server, f.manifest.AppPath())
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
	out.PublicFrom, out.PublicTo = f.publicFromTo()
	out.Finish = f.leftover
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
	if f.leftover {
		return m.buildFinishPlan(f)
	}
	plan := appMovePlan{
		App: mf.Name, Route: string(f.route.Host), From: "https://" + string(f.route.Host) + "/",
		Path: mf.AppPath(), To: f.toURLs(), StripPrefix: mf.StripPrefix,
		Redirects: []string{}, AppSettings: []string{}, SignIn: []string{}, Links: []string{}, Warnings: []string{},
		Removes: []string{}, Stops: []string{}, manifest: mf, names: f.names,
	}
	plan.PublicFrom, plan.PublicTo = f.publicFromTo()
	if f.publicHost != "" {
		if err := m.planPublicRemoval(f, &plan); err != nil {
			return appMovePlan{}, err
		}
	}
	if w := cookiePathWarning(mf); w != "" {
		plan.Warnings = append(plan.Warnings, w)
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
		if !m.planSignIn(f, &plan) {
			plan.SignIn = append(plan.SignIn,
				fmt.Sprintf("%s's sign-in through Authentik names the old address; re-plan it on the app page after the move.", mf.Title))
		}
	}
	if mf.Name == nextcloudPackage {
		m.planFilesLink(&plan)
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
	var def operation.Definition
	if plan.Finish {
		def = operation.Definition{Type: "app.move"}
	} else if def, err = appMoveDefinition(m.st, m.secrets, plan); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if plan.filesTo != "" {
		def.Steps = append(def.Steps, m.filesLinkStep(plan, operatorActor(r)))
	}
	if plan.SignInDigest != "" {
		def.Steps = append(def.Steps, m.signInStep(plan, operatorActor(r)))
	}
	detail := fmt.Sprintf("%s: %s -> %s", plan.App, plan.From, strings.Join(plan.To, ", "))
	if plan.publicHost != "" {
		// The old public name goes last, once the path answers (ADR-061).
		token, err := tunnelToken(m.secrets)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if err := setMovePending(m.st, movePending{App: plan.App, Host: plan.publicHost, TunnelID: plan.tunnelID}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		def.Steps = append(def.Steps, movePublicRemoveSteps(m.st, token, plan)...)
		if plan.Finish {
			detail = fmt.Sprintf("%s: finish removing %s; %s answers at %s", plan.App, plan.publicHost, plan.App, plan.PublicTo)
		}
		detail += fmt.Sprintf("; public %s -> %s; %s removed from tunnel %s, no redirect", plan.PublicFrom, plan.PublicTo, plan.publicHost, plan.tunnelID)
	}
	runner := operation.NewRunner(m.st)
	op, err := runner.Start(r.Context(), def, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m.s.auditOperation(m.st, runner, op.ID, operatorActor(r), "app.move", fmt.Sprintf("operation %d: %s", op.ID, detail))
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
	def := operation.Definition{Type: "app.move"}
	var configure operation.Definition
	if len(plan.env) > 0 {
		var err error
		configure, err = moveConfigure(catalog.ConfigPlan{Manifest: mf, Changes: plan.env}, dir, project, st, secretStore)
		if err != nil {
			return operation.Definition{}, err
		}
	}
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
	if len(plan.env) > 0 {
		def.Steps = append(def.Steps, configure.Steps...)
	} else if len(plan.seeds) > 0 {
		// Nothing to configure, but the seed step stopped the stack: bring
		// it back up with the files it was left with (S400).
		def.Steps = append(def.Steps, operation.StepDef{Name: "up", Run: func(ctx context.Context) error {
			return moveStart(ctx, dir, project)
		}})
	}
	names, publish := plan.names, plan.publish
	address := strings.TrimSuffix(docker.PathURL(docker.Hostname(names[0]), plan.Path), "/")
	trustAdd, trustRemove := append([]string(nil), names...), []string(nil)
	if plan.publicHost != "" {
		// The app's host allow-list follows the public name (ADR-061).
		trustAdd = append(trustAdd, strings.TrimSuffix(strings.TrimPrefix(plan.PublicTo, "https://"), plan.Path))
		trustRemove = []string{plan.publicHost}
	}
	def.Steps = append(def.Steps, operation.StepDef{Name: "app-hosts", Run: func(ctx context.Context) error {
		return trustAppHosts(ctx, mf, address, trustAdd, trustRemove)
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
				if g := set.Routes[i].Gate; g != nil {
					// A gated app (ADR-055) moves its gate inside the
					// dashboard site (ADR-061): bypass regexes take the
					// path prefix, and the gate answers on the dashboard's
					// name. The sign-in re-plan brings Authentik along.
					moved := *g
					moved.Bypass = nil
					for _, b := range g.Bypass {
						if !strings.HasPrefix(b, "^"+plan.Path) {
							b = docker.PathBypass(plan.Path, b)
						}
						moved.Bypass = append(moved.Bypass, b)
					}
					if dash, ok := set.DashboardRoute(); ok {
						moved.Canonical = docker.SharedGateCanonical(set.Routes, dash)
					}
					set.Routes[i].Gate = &moved
				}
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

// cookiePathWarning warns for an app known to set cookies with Path=/ (the
// manifest's cookie_path_root, ADR-061 section 4): on the shared origin the
// cookie reaches the dashboard and every other app. Empty otherwise.
func cookiePathWarning(m catalog.Manifest) string {
	if !m.CookiePathRoot || !m.ServedByPath() {
		return ""
	}
	return fmt.Sprintf("%s is known to set cookies with Path=/, so under %s they are sent to the dashboard and every other app on this origin, and a script in another app could read the ones script can read. The edge removes only Homestead's and Authentik's own cookies from requests, so these stay exposed.",
		m.Title, m.AppPath())
}

// moveOldNameStops is the plain statement every public-name move preview
// carries (ADR-061 section 5): no redirect is left behind.
func moveOldNameStops(title, from, to string) string {
	return fmt.Sprintf("Old bookmarks and links on %s stop working. The name is removed and nothing redirects from it to %s. Phone and desktop clients with %s saved as the server address must be changed by hand to %s.",
		from, to, title, to)
}

// planPublicRemoval puts the removal of the app's own public name in the
// plan (ADR-061): the new public address needs no DNS record because it is
// under the dashboard's public name, and the old name's DNS record, tunnel
// entry and edge route go away last.
func (m *appMove) planPublicRemoval(f moveFacts, plan *appMovePlan) error {
	host := f.publicHost
	if _, err := tunnelToken(m.secrets); err != nil {
		return refusePlan(http.StatusUnprocessableEntity, "%v", err)
	}
	plan.publish = true
	plan.publicHost, plan.tunnelID = host, f.tunnelID
	oldURL := "https://" + host + "/"
	plan.Stops = append(plan.Stops, oldURL)
	owned, err := router.OwnedRecords(m.st, f.tunnelID)
	if err != nil {
		return err
	}
	ours := false
	for _, record := range owned {
		if record.Hostname == host {
			ours = true
		}
	}
	if ours {
		plan.Removes = append(plan.Removes, fmt.Sprintf("DNS: the proxied CNAME %s to the tunnel is deleted (only while it still points at the tunnel)", host))
	} else {
		plan.Warnings = append(plan.Warnings, "Homestead did not create the DNS record for "+host+", so it is left in Cloudflare and answers 404 until you remove it there.")
	}
	plan.Removes = append(plan.Removes,
		fmt.Sprintf("Tunnel: the ingress entry for %s is removed from the connector", host),
		fmt.Sprintf("Edge: %s stops answering through the tunnel and %s answers at %s", host, f.manifest.Title, plan.PublicTo))
	plan.Warnings = append(plan.Warnings,
		moveOldNameStops(f.manifest.Title, oldURL, plan.PublicTo),
		"No DNS record changes are needed for "+plan.PublicTo+": it is under the dashboard's public name the tunnel already carries.",
		openToInternetWarning(f.manifest, f.signIn, plan.PublicTo),
		"It shares one web origin with the dashboard and the other published apps (ADR-044): a flaw in one of them can reach the others in the same browser.",
		"Order: the path is published and the app's settings and sign-in follow it first; the new address is then checked through the public edge, and only if it answers are the old name's connector entry and DNS record removed. If a step or the check fails the run stops and says so, the old name stays, and the move can be finished from the same plan once the address answers.")
	return nil
}

// movePublicRemoveSteps remove the old public name after everything else
// in the move succeeded (ADR-061): a check that the path is published and
// the name is off the edge route, then the connector entry, the CNAME (only
// while it still points at the tunnel) and any record it replaced. The edge
// step of the move already stopped routing the name.
func movePublicRemoveSteps(st *store.Store, apiToken string, plan appMovePlan) []operation.StepDef {
	return []operation.StepDef{
		{Name: "verify-path", Run: func(context.Context) error {
			set, err := loadEdgeRoutes(st)
			if err != nil {
				return err
			}
			for _, r := range set.Routes {
				if strings.EqualFold(string(r.Host), plan.Route) {
					if r.Path == plan.Path && r.Published && len(r.Tunnel) == 0 {
						return nil
					}
					return fmt.Errorf("%s is not published at %s, so %s was not removed", plan.App, plan.PublicTo, plan.publicHost)
				}
			}
			return fmt.Errorf("%s's route %s is gone from the edge, so %s was not removed", plan.App, plan.Route, plan.publicHost)
		}},
		probeStep(plan),
		{Name: "connector", Run: func(context.Context) error {
			if err := router.DeleteRoute(st, plan.publicHost); err != nil {
				return err
			}
			return syncConnector(st, plan.tunnelID)
		}},
		{Name: "delete-dns", Run: func(ctx context.Context) error {
			return deleteOwnedRecordsAt(ctx, st, apiToken, plan.tunnelID, plan.publicHost)
		}},
		{Name: "restore-dns", Run: func(ctx context.Context) error {
			return restoreReplacedRecordsAt(ctx, st, apiToken, plan.tunnelID, plan.publicHost)
		}},
		{Name: "pending-clear", Run: func(context.Context) error { return clearMovePending(st, plan.App) }},
	}
}

// planSignIn projects the app's sign-in after the move and puts what
// changes in the plan (S400), as the public plan does (S399). It returns
// false when the owner has to re-plan: the change cannot be previewed or
// the Authentik API token is not kept.
func (m *appMove) planSignIn(f moveFacts, plan *appMovePlan) bool {
	a := m.s.appSignIn
	if a == nil || (f.manifest.SSO.OIDC == nil && f.manifest.SSO.ForwardAuth == nil) || !a.tokenKept() {
		return false
	}
	set, err := loadEdgeRoutes(m.st)
	if err != nil {
		return false
	}
	routes := append([]docker.Route(nil), set.Routes...)
	for i, r := range routes {
		if strings.EqualFold(string(r.Host), plan.Route) {
			r.Path, r.Tunnel, r.Published = plan.Path, nil, plan.publish
			routes[i] = r
		}
	}
	set.Routes = routes
	in, err := appSignInInputsFrom(m.st, a.m, f.manifest, set)
	if err != nil || in.Applied == nil {
		return false
	}
	next, err := appsso.BuildPlan(in, true)
	if err != nil {
		return false
	}
	var lines []string
	if next.Issuer != in.Applied.Issuer {
		lines = append(lines, fmt.Sprintf("moves to the issuer %s (now %s)", next.Issuer, in.Applied.Issuer))
	}
	added, removed := listChanges(in.Applied.RedirectURIs, next.RedirectURIs)
	if len(added) > 0 {
		lines = append(lines, "adds redirect URI "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		lines = append(lines, "removes redirect URI "+strings.Join(removed, ", "))
	}
	plan.Warnings = append(plan.Warnings, next.Warnings...)
	if len(lines) == 0 {
		plan.SignIn = append(plan.SignIn, f.manifest.Title+"'s sign-in through Authentik needs no change.")
		return true
	}
	plan.SignIn = append(plan.SignIn, "App sign-in is on and is updated in this same change with the kept Authentik API token: it "+strings.Join(lines, "; ")+".")
	plan.SignInDigest = next.Digest
	return true
}

// signInStep applies the approved sign-in change once the app answers at
// its path. A failure leaves the move done and says so.
func (m *appMove) signInStep(plan appMovePlan, actor string) operation.StepDef {
	return operation.StepDef{Name: "sign-in", Run: func(ctx context.Context) error {
		if m.s.appSignIn == nil {
			return fmt.Errorf("%s moved, but app sign-in is not available, so its sign-in still uses the old address; re-plan it from its card", plan.App)
		}
		err := reapplyAppSignIn(ctx, m.s.appSignIn, cert.IssuerMove{App: plan.App, Digest: plan.SignInDigest}, actor)
		if err != nil {
			return fmt.Errorf("%s moved, but its sign-in still uses the old address: %w; re-plan it from its card (homesteadctl apps sign-in plan %s) and approve it", plan.App, err, plan.App)
		}
		return nil
	}}
}

// planFilesLink lists the craft files link following Nextcloud to its
// path (S400). The link is left alone unless it points at the address
// Nextcloud is leaving.
func (m *appMove) planFilesLink(plan *appMovePlan) {
	if m.s.files == nil || m.s.files.Manager == nil {
		return
	}
	link, linked, err := m.s.files.Manager.CurrentLink()
	if err != nil || !linked || strings.TrimSuffix(link.BaseURL, "/") != strings.TrimSuffix(plan.From, "/") {
		return
	}
	names := plan.names
	if len(names) == 0 {
		names = catalog.DefaultApplianceNames
	}
	to := "https://" + names[0] + strings.TrimSuffix(plan.Path, "/")
	plan.filesFrom, plan.filesTo = strings.TrimSuffix(link.BaseURL, "/"), to
	plan.Links = append(plan.Links, "Craft files link moves to "+to+" (same account and credential; no new password).")
}

// filesLinkStep updates only the link's address.
func (m *appMove) filesLinkStep(plan appMovePlan, actor string) operation.StepDef {
	return operation.StepDef{Name: "files-link", Run: func(context.Context) error {
		if err := m.s.files.Manager.MoveBaseURL(plan.filesFrom, plan.filesTo, actor); err != nil {
			return fmt.Errorf("%s moved, but the craft files link still points at %s: %w; relink it on the Craft files page", plan.App, plan.filesFrom, err)
		}
		return nil
	}}
}
