package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"homestead/internal/audit"
	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/firewall"
	"homestead/internal/localdns"
	"homestead/internal/mdns"
	"homestead/internal/store"
)

// App names (S295, ADR-039). Every app route named <label>.lan is also
// <label>.local over mDNS; the optional appliance resolver answers the
// `.lan` names on port 53 once the owner approves its plan.
//
//	GET  /api/v1/names                   -> names status
//	POST /api/v1/names/resolver/plan     {"enabled"} -> plan (read-only)
//	POST /api/v1/names/resolver/apply    {"enabled","digest"} -> status

// namesCacheTTL bounds how often per-query readers (mDNS, resolver) read
// the route table.
const namesCacheTTL = 10 * time.Second

// routerHint is how the owner sends `.lan` lookups to the appliance.
const routerHint = "On your router, add a conditional forward (also called a domain forward or DNS override) for the domain \"lan\" to this appliance's address. Or set one device's DNS server to it to try it first."

// LocalNames owns the appliance's local names and the resolver listener.
type LocalNames struct {
	st *store.Store

	// links lists the LAN addresses to answer on; start binds them.
	// Tests replace both so nothing binds port 53.
	links func() ([]localdns.Link, error)
	start func(localdns.Config) (*localdns.Server, error)

	mu       sync.Mutex
	cachedAt time.Time
	routes   docker.RouteSet
	running  *localdns.Server
	startErr string
}

// NewLocalNames builds the service over the edge route table.
func NewLocalNames(st *store.Store) *LocalNames {
	return &LocalNames{st: st, links: lanLinks, start: localdns.Start}
}

// lanLinks lists the LAN IPv4 addresses the resolver answers on: the
// same set the mDNS responder announces.
func lanLinks() ([]localdns.Link, error) {
	network, err := discover.DiscoverNetwork()
	if err != nil {
		return nil, err
	}
	var out []localdns.Link
	for _, l := range mdns.LANIPv4(network) {
		out = append(out, localdns.Link{Addr: l.Addr, Prefix: l.Prefix})
	}
	return out, nil
}

// routeSet returns the edge routes, cached for namesCacheTTL. A read
// error keeps the last good table.
func (n *LocalNames) routeSet() docker.RouteSet {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.cachedAt.IsZero() && time.Since(n.cachedAt) < namesCacheTTL {
		return n.routes
	}
	if set, err := loadEdgeRoutes(n.st); err == nil {
		n.routes = set
	}
	n.cachedAt = time.Now()
	return n.routes
}

// forget drops the cache, so a status read after a change is current.
func (n *LocalNames) forget() {
	n.mu.Lock()
	n.cachedAt = time.Time{}
	n.mu.Unlock()
}

// MDNSLabels are the app labels announced as <label>.local.
func (n *LocalNames) MDNSLabels() []string { return n.routeSet().MDNSLabels() }

// LANNames are the names the resolver answers.
func (n *LocalNames) LANNames() []string { return n.routeSet().LANNames() }

// Enabled reports whether the owner turned the resolver on.
func (n *LocalNames) Enabled() (bool, error) {
	var on bool
	err := n.st.DB().QueryRow(`SELECT enabled FROM local_resolver WHERE id = 1`).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("names: read resolver: %w", err)
	}
	return on, nil
}

func (n *LocalNames) setEnabled(on bool, actor string) error {
	_, err := n.st.DB().Exec(`INSERT INTO local_resolver (id, enabled, actor, updated_at) VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled, actor = excluded.actor, updated_at = excluded.updated_at`,
		on, actor, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("names: save resolver: %w", err)
	}
	return nil
}

// sync starts or stops the listener to match the stored switch. A start
// failure (port 53 taken) is kept for status and the doctor.
func (n *LocalNames) sync() error {
	on, err := n.Enabled()
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if !on {
		if n.running != nil {
			n.running.Close()
			n.running = nil
		}
		n.startErr = ""
		return nil
	}
	if n.running != nil {
		return nil
	}
	links, err := n.links()
	if err == nil {
		n.running, err = n.start(localdns.Config{Names: n.LANNames, Links: links})
	}
	if err != nil {
		n.startErr = err.Error()
		return err
	}
	n.startErr = ""
	return nil
}

// Run starts the resolver when it is on and stops it with ctx. Called
// once by the daemon; a failure only warns, since names are convenience.
func (n *LocalNames) Run(ctx context.Context, warn func(error)) {
	if err := n.sync(); err != nil && warn != nil {
		warn(err)
	}
	<-ctx.Done()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.running != nil {
		n.running.Close()
		n.running = nil
	}
}

// appNames is one route's local names.
type appNames struct {
	Host  string   `json:"host"`
	Names []string `json:"names"`
}

type namesStatus struct {
	Resolver resolverStatus `json:"resolver"`
	Apps     []appNames     `json:"apps"`
}

type resolverStatus struct {
	Enabled    bool     `json:"enabled"`
	Running    bool     `json:"running"`
	Listening  []string `json:"listening"`
	Error      string   `json:"error,omitempty"`
	Names      []string `json:"names"`
	RouterHint string   `json:"router_hint"`
}

// Status reads the names and the resolver state.
func (n *LocalNames) Status() (namesStatus, error) {
	on, err := n.Enabled()
	if err != nil {
		return namesStatus{}, err
	}
	set := n.routeSet()
	out := namesStatus{
		Resolver: resolverStatus{Enabled: on, Listening: []string{}, Names: nonNil(set.LANNames()), RouterHint: routerHint},
		Apps:     appNamesOf(set),
	}
	n.mu.Lock()
	if n.running != nil {
		out.Resolver.Running = true
		out.Resolver.Listening = append(out.Resolver.Listening, n.running.Listening...)
	}
	out.Resolver.Error = n.startErr
	n.mu.Unlock()
	return out, nil
}

// appNamesOf lists each non-public route's local names, by host.
func appNamesOf(set docker.RouteSet) []appNames {
	out := []appNames{}
	for _, r := range set.Routes {
		if r.Exposure == docker.ExposurePublic {
			continue
		}
		var names []string
		for _, h := range r.Names() {
			names = append(names, strings.ToLower(string(h)))
		}
		out = append(out, appNames{Host: string(r.Host), Names: names})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// resolverPlan is what turning the resolver on or off changes.
type resolverPlan struct {
	Enabled   bool           `json:"enabled"`
	Changes   []string       `json:"changes"`
	Listen    []string       `json:"listen"`
	Names     []string       `json:"names"`
	Firewall  []firewallLine `json:"firewall"`
	Warnings  []string       `json:"warnings"`
	RouterTip string         `json:"router_hint"`
	Digest    string         `json:"digest"`
}

// firewallLine is one rule the plan adds or removes, for display.
type firewallLine struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

func (n *LocalNames) plan(enable bool) (resolverPlan, error) {
	on, err := n.Enabled()
	if err != nil {
		return resolverPlan{}, err
	}
	network, err := discover.DiscoverNetwork()
	if err != nil {
		return resolverPlan{}, fmt.Errorf("names: discover network: %w", err)
	}
	links, err := n.links()
	if err != nil {
		return resolverPlan{}, err
	}
	p := resolverPlan{
		Enabled: enable, Changes: []string{}, Listen: []string{}, Names: nonNil(n.LANNames()),
		Firewall: []firewallLine{}, Warnings: []string{}, RouterTip: routerHint,
	}
	for _, l := range links {
		p.Listen = append(p.Listen, netip.AddrPortFrom(l.Addr, localdns.Port).String())
	}
	rules, err := firewall.WithLocalResolver(firewall.RuleSet{}, network)
	if err != nil {
		return resolverPlan{}, err
	}
	for _, r := range rules.Rules {
		from := "everywhere else"
		if r.Source.CIDR != "" {
			from = r.Source.CIDR
		}
		p.Firewall = append(p.Firewall, firewallLine{Name: r.Name, Detail: fmt.Sprintf("%s %s/%d from %s", r.Action, r.Service.Protocol, r.Service.Port, from)})
	}
	switch {
	case enable == on:
		p.Changes = append(p.Changes, "Nothing changes: the resolver is already "+onOff(on)+".")
	case enable:
		p.Changes = append(p.Changes,
			"Answer DNS for this appliance's .lan names on port 53 (UDP and TCP) at the addresses below.",
			"Other .lan names get \"no such name\"; every other lookup is refused. Nothing is forwarded.",
			"Add the firewall rules below the next time the firewall plan is applied.")
		if len(links) == 0 {
			p.Warnings = append(p.Warnings, "No LAN address was found, so the resolver has nowhere to listen.")
		} else if s, err := n.start(localdns.Config{Names: func() []string { return nil }, Links: links}); err != nil {
			p.Warnings = append(p.Warnings, "Port 53 is not free: "+err.Error()+". Another DNS server on this machine must stop listening on these addresses first.")
		} else {
			s.Close()
		}
	default:
		p.Changes = append(p.Changes,
			"Stop answering DNS on port 53.",
			"Remove the firewall rules below the next time the firewall plan is applied.",
			"Devices that use the router's `lan` forward stop resolving app names; <app>.local keeps working.")
	}
	p.Digest = p.digest()
	return p, nil
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func (p resolverPlan) digest() string {
	visible := p
	visible.Digest = ""
	data, _ := json.Marshal(visible)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// MountNames registers the names routes.
func (s *Server) MountNames(names *LocalNames) {
	s.mux.HandleFunc("GET /api/v1/names", func(w http.ResponseWriter, _ *http.Request) {
		status, err := names.Status()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, status)
	})
	s.mux.HandleFunc("POST /api/v1/names/resolver/plan", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if !decodeResolverRequest(w, r, &req) || !requireEnabled(w, req.Enabled) {
			return
		}
		plan, err := names.plan(*req.Enabled)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, plan)
	})
	s.mux.HandleFunc("POST /api/v1/names/resolver/apply", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Enabled *bool  `json:"enabled"`
			Digest  string `json:"digest"`
		}
		if !decodeResolverRequest(w, r, &req) || !requireEnabled(w, req.Enabled) {
			return
		}
		plan, err := names.plan(*req.Enabled)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if plan.Digest != req.Digest {
			http.Error(w, "the plan changed since it was reviewed; review it again", http.StatusConflict)
			return
		}
		actor := operatorActor(r)
		action := "names.resolver.disable"
		if plan.Enabled {
			action = "names.resolver.enable"
		}
		if err := names.setEnabled(plan.Enabled, actor); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		names.forget()
		entry := audit.Entry{Actor: actor, Action: action, Outcome: audit.OutcomeOK, Detail: strings.Join(plan.Listen, ", ")}
		if err := names.sync(); err != nil {
			entry.Outcome, entry.Detail = audit.OutcomeError, err.Error()
		}
		_, _ = audit.New(names.st).Record(entry)
		status, err := names.Status()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, status)
	})
}

func decodeResolverRequest(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

func requireEnabled(w http.ResponseWriter, enabled *bool) bool {
	if enabled == nil {
		http.Error(w, "enabled is required", http.StatusBadRequest)
		return false
	}
	return true
}

// resolverDoctorCheck warns when the owner turned the resolver on but it
// is not answering (port 53 taken, no LAN address).
func resolverDoctorCheck(names *LocalNames) func() []doctor.Check {
	return func() []doctor.Check {
		status, err := names.Status()
		if err != nil || !status.Resolver.Enabled || status.Resolver.Running {
			return nil
		}
		detail := status.Resolver.Error
		if detail == "" {
			detail = "it is not listening"
		}
		return []doctor.Check{{
			Name:   "local-resolver",
			Status: doctor.StatusWarn,
			Detail: "The .lan resolver is on but not answering: " + detail + ". Free port 53 on the LAN addresses, then turn it off and on again in Names.",
		}}
	}
}
