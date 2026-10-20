package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"homestead/internal/audit"
	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/mdns"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// Local dashboard address (S289, ADR-035). The owner sets a local name
// (default homestead.lan); Homestead writes the one dashboard edge route,
// which also answers the appliance's mDNS name, with internal-CA TLS. The
// route is local or overlay only; the tunnel is the public path.

// DashboardAddress is the API view of the dashboard route.
type DashboardAddress struct {
	// Name is the owner-set local name; Default is what an unset
	// appliance uses.
	Name    string `json:"name"`
	Default string `json:"default"`
	// Configured is true once the dashboard route is recorded.
	Configured bool     `json:"configured"`
	Exposure   string   `json:"exposure"`
	MDNSName   string   `json:"mdns_name"`
	Names      []string `json:"names"`
	URLs       []string `json:"urls"`
	// RootPath is the public route a device fetches the root from.
	RootPath string `json:"root_path"`
}

// DashboardAddressService owns the dashboard route.
type DashboardAddressService struct {
	st     *store.Store
	runner *operation.Runner
	local  *cert.LocalTLS
	audit  *audit.Service
	log    interface{ Warn(string, ...any) }
	// failedAudited limits the ensure loop to one failure audit entry.
	failedAudited bool
}

// MountDashboardAddress registers the address routes and wires the
// dashboard socket's Host and Origin names to the recorded route.
func (s *Server) MountDashboardAddress(st *store.Store, local *cert.LocalTLS) *DashboardAddressService {
	svc := &DashboardAddressService{st: st, runner: operation.NewRunner(st), local: local, audit: audit.New(st), log: s.log}
	s.SetDashboardNames(svc.Names)
	s.mux.HandleFunc("GET /api/v1/dashboard/address", svc.handleGet)
	s.mux.HandleFunc("PUT /api/v1/dashboard/address", svc.handlePut)
	return svc
}

// MDNSName is the appliance's .local name.
func MDNSName() docker.Hostname { return docker.Hostname(mdns.DefaultHostname + ".local") }

// Names lists the names the dashboard route answers on, lower case; empty
// while no dashboard route is recorded.
func (d *DashboardAddressService) Names() []string {
	set, err := loadEdgeRoutes(d.st)
	if err != nil {
		return nil
	}
	r, ok := set.DashboardRoute()
	if !ok {
		return nil
	}
	var out []string
	for _, n := range r.Names() {
		out = append(out, strings.ToLower(string(n)))
	}
	return out
}

// View reads the current address state.
func (d *DashboardAddressService) View() (DashboardAddress, error) {
	set, err := loadEdgeRoutes(d.st)
	if err != nil {
		return DashboardAddress{}, err
	}
	v := DashboardAddress{
		Name: string(docker.DefaultDashboardName), Default: string(docker.DefaultDashboardName),
		Exposure: string(docker.ExposureLocal), MDNSName: string(MDNSName()),
		Names: []string{}, URLs: []string{}, RootPath: "/api/v1/tls/local-ca/root.pem",
	}
	if r, ok := set.DashboardRoute(); ok {
		v.Configured, v.Name, v.Exposure = true, string(r.Host), string(r.Exposure)
		for _, n := range r.Names() {
			v.Names = append(v.Names, string(n))
		}
	}
	// The .local name is listed first: it needs no DNS setup.
	if v.Configured {
		for _, n := range v.Names {
			if n == v.MDNSName {
				v.URLs = append(v.URLs, "https://"+n)
			}
		}
		for _, n := range v.Names {
			if n != v.MDNSName {
				v.URLs = append(v.URLs, "https://"+n)
			}
		}
	}
	return v, nil
}

// errNameConflict marks a name used by another route.
var errNameConflict = errors.New("name in use")

// BuildDashboardRoute validates a requested name and exposure and returns
// the route. Public exposure and public TLS are refused.
func BuildDashboardRoute(name string, exposure docker.Exposure) (docker.Route, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if exposure == "" {
		exposure = docker.ExposureLocal
	}
	if exposure == docker.ExposurePublic {
		return docker.Route{}, errors.New(docker.ErrDashboardPublic)
	}
	if exposure != docker.ExposureLocal && exposure != docker.ExposureOverlay {
		return docker.Route{}, errors.New("exposure must be local or overlay")
	}
	host := docker.Hostname(name)
	if err := host.Validate(); err != nil {
		return docker.Route{}, fmt.Errorf("name: %w", err)
	}
	if net.ParseIP(name) != nil {
		return docker.Route{}, errors.New("name must be a DNS name, not an IP address")
	}
	if !cert.LocalNameAllowed(name) {
		return docker.Route{}, fmt.Errorf("name %q is outside the suffixes the appliance root certificate can vouch for (%s): pick a name under one of them so the internal CA can issue its certificate", name, strings.Join(cert.LocalPermittedDomains, ", "))
	}
	route := docker.Route{Host: host, Exposure: exposure, TLS: docker.TLSInternal, Dashboard: true}
	if host != MDNSName() {
		route.Aliases = []docker.Hostname{MDNSName()}
	}
	return route, route.Validate()
}

// withDashboardRoute returns the table with the dashboard route replaced
// by route (or added).
func withDashboardRoute(set docker.RouteSet, route docker.Route) (docker.RouteSet, error) {
	out := docker.RouteSet{Routes: []docker.Route{}}
	for _, r := range set.Routes {
		if r.Dashboard {
			continue
		}
		for _, n := range route.Names() {
			for _, rn := range r.Names() {
				if rn == n {
					return docker.RouteSet{}, fmt.Errorf("%w: %q is already an edge route", errNameConflict, n)
				}
			}
		}
		out.Routes = append(out.Routes, r)
	}
	out.Routes = append(out.Routes, route)
	return out, out.Validate()
}

// Definition plans setting the dashboard route: deploy the edge (so the
// socket mount exists), place the leaf, and reconcile the table. Both parts
// are idempotent.
func (d *DashboardAddressService) Definition(route docker.Route, actor string) (operation.Definition, error) {
	return d.definition(route, actor, false)
}

// definition is Definition; ifAbsent makes the write step stand down when a
// dashboard route is recorded by the time it runs, so the default never
// reverts a name the owner set during the deploy window.
func (d *DashboardAddressService) definition(route docker.Route, actor string, ifAbsent bool) (operation.Definition, error) {
	set, err := loadEdgeRoutes(d.st)
	if err != nil {
		return operation.Definition{}, err
	}
	next, err := withDashboardRoute(set, route)
	if err != nil {
		return operation.Definition{}, err
	}
	if err := requirePublicApproved(d.st, next.Routes); err != nil {
		return operation.Definition{}, err
	}
	if d.local != nil {
		// A leaf that cannot be issued never blocks the reconcile; doctor
		// shows it (same rule as the edge route table).
		_, _, _ = d.local.EnsureLeaves(next.Routes, false, actor)
	}
	stack := docker.CaddyStack()
	deploy, err := docker.DeployDefinition(stack)
	if err != nil {
		return operation.Definition{}, err
	}
	state := docker.RouteState{
		Load: func() (docker.RouteSet, error) { return loadEdgeRoutes(d.st) },
		Save: func(s docker.RouteSet) error { return saveEdgeRoutes(d.st, s) },
	}
	if ifAbsent {
		state.Skip = func(cur docker.RouteSet) bool { _, ok := cur.DashboardRoute(); return ok }
	}
	rec, err := docker.ReconcileDefinition(stack, next, state)
	if err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: "dashboard.address"}
	for _, step := range deploy.Steps {
		step.Name = "deploy-" + step.Name
		def.Steps = append(def.Steps, step)
	}
	def.Steps = append(def.Steps, rec.Steps...)
	return def, nil
}

func (d *DashboardAddressService) record(actor, outcome, detail string) {
	if _, err := d.audit.Record(audit.Entry{Actor: actor, Action: "dashboard.address", Outcome: outcome, Detail: detail}); err != nil {
		d.log.Warn("audit record failed", "action", "dashboard.address", "error", err)
	}
}

func (d *DashboardAddressService) handleGet(w http.ResponseWriter, r *http.Request) {
	v, err := d.View()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

func (d *DashboardAddressService) handlePut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Exposure string `json:"exposure"`
	}
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	route, err := BuildDashboardRoute(body.Name, docker.Exposure(body.Exposure))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	actor := operatorActor(r)
	def, err := d.Definition(route, actor)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, errNameConflict) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	op, err := d.runner.Start(r.Context(), def, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d.record(actor, audit.OutcomeOK, viaEdge(r, fmt.Sprintf("name=%s exposure=%s operation=%d", route.Host, route.Exposure, op.ID)))
	writeJSON(w, map[string]any{"id": op.ID})
}

// EnsureDefault records the default dashboard route when none exists, so a
// fresh appliance can be claimed from the LAN (ADR-035). It never replaces
// an existing route and never fails the daemon: Docker may not be ready at
// install, so Run retries.
func (d *DashboardAddressService) EnsureDefault(ctx context.Context) error {
	set, err := loadEdgeRoutes(d.st)
	if err != nil {
		return err
	}
	if _, ok := set.DashboardRoute(); ok {
		return nil
	}
	route, err := BuildDashboardRoute(string(docker.DefaultDashboardName), docker.ExposureLocal)
	if err != nil {
		return err
	}
	def, err := d.definition(route, "installer", true)
	if err != nil {
		return err
	}
	op, err := d.runner.Start(ctx, def, "")
	if err != nil {
		return err
	}
	done, err := d.runner.Wait(ctx, op.ID)
	if err != nil {
		return err
	}
	if done.Status != operation.StatusSucceeded {
		// Audit the first failure only: the retry loop would otherwise
		// write one entry per attempt while Docker is absent.
		if !d.failedAudited {
			d.failedAudited = true
			d.record("installer", audit.OutcomeError, fmt.Sprintf("default dashboard route not created: operation %d %s", op.ID, done.Status))
		}
		return fmt.Errorf("dashboard: default route operation %d ended %s", op.ID, done.Status)
	}
	d.record("installer", audit.OutcomeOK, "default dashboard route "+string(route.Host)+" created before claim")
	return nil
}

// RunEnsure keeps trying EnsureDefault until it succeeds or ctx ends:
// Docker may arrive after the daemon at first install. retry is the
// interval between attempts.
func (d *DashboardAddressService) RunEnsure(ctx context.Context, retry time.Duration, warn func(error)) {
	warnedConflict := false
	for {
		err := d.EnsureDefault(ctx)
		if err == nil {
			return
		}
		// A name conflict only ends when the owner changes the other
		// route: report it once per start, not on every retry.
		conflict := errors.Is(err, errNameConflict)
		if warn != nil && !(conflict && warnedConflict) {
			warn(err)
		}
		warnedConflict = warnedConflict || conflict
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}

// defaultNameHolder names the non-dashboard route holding a name the
// default dashboard route needs, if any.
func defaultNameHolder(set docker.RouteSet) (docker.Hostname, bool) {
	route, err := BuildDashboardRoute(string(docker.DefaultDashboardName), docker.ExposureLocal)
	if err != nil {
		return "", false
	}
	_, err = withDashboardRoute(set, route)
	if !errors.Is(err, errNameConflict) {
		return "", false
	}
	for _, r := range set.Routes {
		for _, n := range r.Names() {
			for _, want := range route.Names() {
				if n == want {
					return n, true
				}
			}
		}
	}
	return "", true
}
