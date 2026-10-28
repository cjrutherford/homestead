package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"homestead/internal/cert"
	"homestead/internal/craftfiles"
	"homestead/internal/docker"
	"homestead/internal/exposure"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// MountEdge serves edge-proxy operations (S237): Caddy deployment
// and route-table reconciliation. Both run through the operation
// engine with progress on the existing operation endpoints, so
// routes return operation IDs instead of blocking. GET
// /api/v1/edge/routes reads back the table the reconciler last wrote.
// The reconciler owns exactly those hosts; snippets placed by the
// exposure flow or by hand are never removed by it.
//
// With a local-TLS issuer (S280), local-only internal-TLS routes get a
// leaf from the persisted internal CA placed before the snippets render,
// so the edge presents it from the first reload.
func (s *Server) MountEdge(st *store.Store, local ...*cert.LocalTLS) {
	var issuer *cert.LocalTLS
	if len(local) > 0 {
		issuer = local[0]
	}
	runner := operation.NewRunner(st)
	s.mux.HandleFunc("POST /api/v1/edge/deploy", handleEdgeDeploy(runner))
	s.mux.HandleFunc("GET /api/v1/edge/routes", handleEdgeRoutesGet(st))
	s.mux.HandleFunc("POST /api/v1/edge/routes", handleEdgeRoutes(runner, st, issuer))
}

// loadEdgeRoutes reads the recorded reconcile table; none yet is empty.
func loadEdgeRoutes(st *store.Store) (docker.RouteSet, error) {
	var encoded string
	err := st.DB().QueryRow(`SELECT routes FROM edge_routes WHERE id = 1`).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return docker.RouteSet{Routes: []docker.Route{}}, nil
	}
	if err != nil {
		return docker.RouteSet{}, fmt.Errorf("edge: read routes: %w", err)
	}
	var set docker.RouteSet
	if err := json.Unmarshal([]byte(encoded), &set.Routes); err != nil {
		return docker.RouteSet{}, fmt.Errorf("edge: decode routes: %w", err)
	}
	if set.Routes == nil {
		set.Routes = []docker.Route{}
	}
	return set, nil
}

func saveEdgeRoutes(st *store.Store, set docker.RouteSet) error {
	routes := set.Routes
	if routes == nil {
		routes = []docker.Route{}
	}
	encoded, err := json.Marshal(routes)
	if err != nil {
		return fmt.Errorf("edge: encode routes: %w", err)
	}
	if _, err := st.DB().Exec(
		`INSERT INTO edge_routes (id, routes, updated_at) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET routes = excluded.routes, updated_at = excluded.updated_at`,
		string(encoded), time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("edge: record routes: %w", err)
	}
	return nil
}

func handleEdgeRoutesGet(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		set, err := loadEdgeRoutes(st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"routes": set.Routes})
	}
}

var errPublicRefused = errors.New("public exposure refused")

// requirePublicApproved refuses a public route unless the exposure flow
// holds a current approval for exactly this route (ADR-006): public
// exposure is planned, previewed and approved there, never restated
// here. Other exposures need no approval.
func requirePublicApproved(st *store.Store, routes []docker.Route) error {
	for _, route := range routes {
		if route.Exposure != docker.ExposurePublic {
			continue
		}
		approval, err := exposure.ApprovalFor(st, route.Host)
		if errors.Is(err, exposure.ErrNoApproval) {
			return fmt.Errorf("%w: public route %q has no exposure approval: review and approve it on the exposure flow (GET /api/v1/exposure/review, POST /api/v1/exposure/approvals), then reconcile", errPublicRefused, route.Host)
		}
		if err != nil {
			return err
		}
		want, _ := json.Marshal(approval.Plan.Edge)
		got, _ := json.Marshal(route)
		if !bytes.Equal(want, got) {
			return fmt.Errorf("%w: public route %q differs from its approved plan: re-review and re-approve it on the exposure flow", errPublicRefused, route.Host)
		}
	}
	return nil
}

func handleEdgeDeploy(runner *operation.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		def, err := docker.DeployDefinition(docker.CaddyStack())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
}

// routeTableBody carries the desired route table: capitalized keys
// matching the docker model, the same convention as update moves
// (S193).
type routeTableBody struct {
	Routes []docker.Route `json:"routes"`
}

// inferCraftApps marks routes that front a hooked craft app (S254,
// ADR-032) when the table predates the CraftApp field: craft packages
// name their only service after the app, so a route whose upstream is
// that service is a craft route. Install plans carry CraftApp already;
// this only keeps tables applied earlier working after an upgrade.
func inferCraftApps(routes []docker.Route) {
	for i := range routes {
		if routes[i].CraftApp != "" {
			continue
		}
		if _, ok := craftfiles.HookedApp(routes[i].Target.Host); ok && routes[i].Target.Port == 8080 {
			routes[i].CraftApp = routes[i].Target.Host
		}
	}
}

// pathCraftRoutes serves every craft app at its path (ADR-045 §1). A
// craft app is never on a name of its own: a route that would give it
// one, from any client or an older table, is moved to /apps/<name>/ with
// the prefix stripped, as every craft package declares, before it is
// checked or saved (S326).
func pathCraftRoutes(routes []docker.Route) {
	for i, r := range routes {
		if r.CraftApp == "" || r.Dashboard || r.IsPath() {
			continue
		}
		r.Path = docker.AppPathFor(r.CraftApp)
		r.StripPrefix = true
		r.Tunnel = nil
		r.Published = false
		routes[i] = r
	}
}

// requireLocalDashboardNames refuses a dashboard route whose name or alias
// is outside the suffixes the internal root can vouch for, the same rule
// PUT /api/v1/dashboard/address applies.
func requireLocalDashboardNames(routes []docker.Route) error {
	for _, r := range routes {
		if !r.Dashboard {
			continue
		}
		for _, n := range r.Names() {
			if !cert.LocalNameAllowed(strings.ToLower(string(n))) {
				return fmt.Errorf("dashboard name %q is outside the suffixes the appliance root certificate can vouch for (%s)", n, strings.Join(cert.LocalPermittedDomains, ", "))
			}
		}
	}
	return nil
}

// preserveTunnelNames gives each posted route the tunnel names recorded
// for its host and drops any the client sent: only tunnel setup and
// teardown change them (ADR-037), so replacing the table neither adds a
// public name nor loses one.
func preserveTunnelNames(st *store.Store, routes []docker.Route) []docker.Route {
	stored := map[docker.Hostname][]docker.Hostname{}
	if set, err := loadEdgeRoutes(st); err == nil {
		for _, r := range set.Routes {
			stored[r.Host] = r.Tunnel
		}
	}
	out := make([]docker.Route, len(routes))
	for i, r := range routes {
		r.Tunnel = stored[r.Host]
		out[i] = r
	}
	return out
}

// preserveDashboardRoute keeps the recorded dashboard route when a posted
// table omits it: the route is owned by PUT /api/v1/dashboard/address
// (ADR-035), so an older client replacing the table cannot drop it.
func preserveDashboardRoute(st *store.Store, routes []docker.Route) []docker.Route {
	for _, r := range routes {
		if r.Dashboard {
			return routes
		}
	}
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return routes
	}
	if dash, ok := set.DashboardRoute(); ok {
		return append(routes, dash)
	}
	return routes
}

func handleEdgeRoutes(runner *operation.Runner, st *store.Store, local *cert.LocalTLS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body routeTableBody
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id, status, err := startEdgeReconcile(r.Context(), runner, st, local, body.Routes, operatorActor(r))
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, map[string]any{"id": id})
	}
}

// startEdgeReconcile checks a desired route table and starts its
// reconcile as an operation, returning the status for a refusal. The
// edge routes route and the appliance-run app setup (ADR-048) both go
// through it.
func startEdgeReconcile(ctx context.Context, runner *operation.Runner, st *store.Store, local *cert.LocalTLS, routes []docker.Route, actor string) (int64, int, error) {
	if routes == nil {
		routes = []docker.Route{}
	}
	inferCraftApps(routes)
	pathCraftRoutes(routes)
	routes = preserveDashboardRoute(st, routes)
	routes = preserveTunnelNames(st, routes)
	if err := requireLocalDashboardNames(routes); err != nil {
		return 0, http.StatusUnprocessableEntity, err
	}
	if err := docker.ValidateRenderable(docker.RouteSet{Routes: routes}); err != nil {
		return 0, http.StatusUnprocessableEntity, err
	}
	if err := requirePublicApproved(st, routes); err != nil {
		status := http.StatusUnprocessableEntity
		if !errors.Is(err, errPublicRefused) {
			status = http.StatusInternalServerError
		}
		return 0, status, err
	}
	if local != nil {
		// A leaf that cannot be issued never blocks the reconcile: the
		// route falls back to the edge's own internal issuance and the
		// failure is audited (CA and per-host errors, by EnsureLeaves)
		// and shown by doctor.
		_, _, _ = local.EnsureLeaves(routes, false, actor)
	}
	state := docker.RouteState{
		Load: func() (docker.RouteSet, error) { return loadEdgeRoutes(st) },
		Save: func(set docker.RouteSet) error { return saveEdgeRoutes(st, set) },
	}
	def, err := docker.ReconcileDefinition(docker.CaddyStack(), docker.RouteSet{Routes: routes}, state)
	if err != nil {
		return 0, http.StatusUnprocessableEntity, err
	}
	op, err := runner.Start(ctx, def, "")
	if err != nil {
		return 0, http.StatusInternalServerError, err
	}
	return op.ID, 0, nil
}
