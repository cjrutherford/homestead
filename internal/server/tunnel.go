package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"homestead/internal/audit"
	"homestead/internal/docker"
	"homestead/internal/domain"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// Tunnel file locations, overridden in tests with temp directories.
// The connector config and token live inside the daemon's own state
// directory (/etc/homestead), which the sandboxed unit may already
// write: setup never touches paths outside it. The connector unit
// itself ships static via scripts/install-systemd.sh.
var (
	tunnelConnectorDir = "/etc/homestead/tunnel"
	tunnelUnitName     = "homestead-tunnel"
	tunnelLookPath     = exec.LookPath
	tunnelSystemctl    = runSystemctl
)

func runSystemctl(args ...string) error {
	command := exec.Command("systemctl", args...)
	command.Stdout = nil
	command.Stderr = nil
	return command.Run()
}

// MountTunnel serves tunnel remote access (S246, planned in S294 per
// ADR-037) over the API:
//
//	GET    /api/v1/tunnel/status        discovery, tunnels, connector
//	POST   /api/v1/tunnel/plan          {"name"|"adopt","dashboard_hostname","auth_hostname"} -> plan (read-only)
//	POST   /api/v1/tunnel/setup         the same with "digest" -> operation
//	DELETE /api/v1/tunnel/tunnels/{id}  teardown operation
//
// The tunnel reuses the Domain step's Cloudflare token and derives the
// account from the hostname's zone: no route takes a token or an account
// ID, and neither is ever returned.
func (s *Server) MountTunnel(st *store.Store, secrets *secret.Store, port int) {
	s.mux.HandleFunc("GET /api/v1/tunnel/status", s.handleTunnelStatus(st, secrets))
	s.mux.HandleFunc("POST /api/v1/tunnel/plan", s.handleTunnelPlan(st, secrets))
	s.mux.HandleFunc("POST /api/v1/tunnel/setup", s.handleTunnelSetup(st, secrets))
	s.mux.HandleFunc("DELETE /api/v1/tunnel/tunnels/{id}", s.handleTunnelTeardown(st, secrets))
}

// tunnelAPIBase is the Cloudflare API root, swapped for a double in tests.
var tunnelAPIBase = router.CloudflareAPIBase

func tunnelClient() (*router.TunnelClient, error) {
	return router.NewTunnelClient(tunnelAPIBase)
}

// tunnelToken returns the one Cloudflare token: the Domain step's, or the
// pre-S294 tunnel token on appliances that still have only that.
func tunnelToken(secrets *secret.Store) (string, error) {
	if token, err := secrets.Get(domain.SecretName("cloudflare", "api_token")); err == nil && token != "" {
		return token, nil
	}
	if token, err := secrets.Get(router.TunnelSecretName); err == nil && token != "" {
		return token, nil
	}
	return "", errors.New("no Cloudflare API token on file: store one in the setup Domain step (Cloudflare)")
}

type tunnelStatusResponse struct {
	Preferred string               `json:"preferred"`
	Gateway   string               `json:"gateway"`
	Interface string               `json:"interface"`
	Protocols []string             `json:"protocols"`
	Tunnels   []tunnelEntry        `json:"tunnels"`
	Routes    []router.TunnelRoute `json:"routes"`
	Connector connectorStatus      `json:"connector"`
	Accounts  int                  `json:"accounts"`
	// Proposed is the default plan request: public names from the setup
	// domain and whether the dashboard would be published (ADR-037 §4).
	Proposed  tunnelProposal `json:"proposed"`
	Discovery string         `json:"discovery_error,omitempty"`
	// Coverage is the edge certificate lamp for each served name (S312).
	Coverage []publicNameCoverage `json:"coverage"`
}

type tunnelProposal struct {
	DashboardHostname string `json:"dashboard_hostname"`
	AuthHostname      string `json:"auth_hostname"`
	SignInLinked      bool   `json:"sign_in_linked"`
}

type tunnelEntry struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Status      string                    `json:"status"`
	Connections []router.TunnelConnection `json:"connections"`
}

type connectorStatus struct {
	Installed bool `json:"installed"`
	Active    bool `json:"active"`
}

// tunnelDiscovery is what the status read shows about the router.
type tunnelDiscovery struct {
	Preferred, Gateway, Interface, Note string
	Protocols                           []string
}

// discoverGateway probes the router (UPnP and NAT-PMP/PCP, which wait for
// answers that often never come); overridden in tests.
var discoverGateway = func(ctx context.Context) tunnelDiscovery {
	found := tunnelDiscovery{Preferred: "tunnel", Protocols: []string{}}
	iface, gateway, err := router.DefaultRoute()
	if err != nil {
		found.Note = err.Error()
		return found
	}
	found.Gateway, found.Interface = gateway, iface
	info, err := router.DiscoverGateway(ctx, iface, gateway)
	if err != nil {
		found.Note = err.Error()
		return found
	}
	for _, protocol := range info.Protocols {
		found.Protocols = append(found.Protocols, string(protocol))
	}
	found.Preferred = router.PreferredProvider(info.Protocols)
	return found
}

// tunnelDiscoveryTTL is how long a router discovery is reused: the router
// rarely changes, and probing it costs seconds on every status read.
const tunnelDiscoveryTTL = 5 * time.Minute

var discoveryCache struct {
	sync.Mutex
	at    time.Time
	found tunnelDiscovery
}

// WarmTunnelDiscovery probes the router once so the first tunnel status
// read after start is served from the cache instead of waiting for UPnP
// and NAT-PMP/PCP. The daemon runs it in the background at start.
func WarmTunnelDiscovery(ctx context.Context) {
	cachedDiscovery(ctx)
}

func cachedDiscovery(ctx context.Context) tunnelDiscovery {
	discoveryCache.Lock()
	defer discoveryCache.Unlock()
	if !discoveryCache.at.IsZero() && time.Since(discoveryCache.at) < tunnelDiscoveryTTL {
		return discoveryCache.found
	}
	found := discoverGateway(ctx)
	if ctx.Err() == nil {
		discoveryCache.at, discoveryCache.found = time.Now(), found
	}
	return found
}

func (s *Server) handleTunnelStatus(st *store.Store, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := tunnelStatusResponse{}
		facts := s.tunnelFacts()
		response.Proposed.DashboardHostname, response.Proposed.AuthHostname = tunnelDefaults(facts.Domain)
		response.Proposed.SignInLinked = facts.SignInLinked
		// The router probe runs alongside the Cloudflare reads below. The
		// channel is buffered, so an early return never strands it.
		discovered := make(chan tunnelDiscovery, 1)
		go func() { discovered <- cachedDiscovery(r.Context()) }()
		routes, err := router.ListRoutes(st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		response.Routes = routes
		token, tokenErr := tunnelToken(secrets)
		client, clientErr := tunnelClient()
		// The single visible account is the fallback for tunnels set up
		// before S294 stored theirs.
		fallbackAccount := ""
		if tokenErr == nil && clientErr == nil {
			if accounts, err := client.ListAccounts(r.Context(), token); err == nil {
				response.Accounts = len(accounts)
				if len(accounts) == 1 {
					fallbackAccount = accounts[0]
				}
			}
		}
		seen := map[string]bool{}
		for _, route := range routes {
			if seen[route.TunnelID] {
				continue
			}
			seen[route.TunnelID] = true
			entry := tunnelEntry{ID: route.TunnelID, Status: "unknown"}
			accountID, err := router.TunnelAccount(st, route.TunnelID)
			if err != nil || accountID == "" {
				accountID = fallbackAccount
			}
			if tokenErr == nil && clientErr == nil && accountID != "" {
				if status, err := client.GetTunnel(r.Context(), token, accountID, route.TunnelID); err == nil {
					entry.Name = status.Name
					entry.Status = status.Status
					entry.Connections = status.Connections
				} else {
					entry.Status = "unreachable"
				}
			}
			response.Tunnels = append(response.Tunnels, entry)
		}
		if response.Tunnels == nil {
			response.Tunnels = []tunnelEntry{}
		}
		if _, err := tunnelLookPath("cloudflared"); err == nil {
			response.Connector.Installed = true
			if err := tunnelSystemctl("is-active", "--quiet", tunnelUnitName); err == nil {
				response.Connector.Active = true
			}
		}
		names := []string{}
		for _, route := range routes {
			names = append(names, route.Hostname)
		}
		response.Coverage = statusCoverage(r.Context(), secrets, names)
		found := <-discovered
		response.Preferred, response.Gateway, response.Interface = found.Preferred, found.Gateway, found.Interface
		response.Protocols, response.Discovery = found.Protocols, found.Note
		writeJSON(w, response)
	}
}

// tunnelPlan is what the owner reviews before approving a tunnel
// (ADR-037 §4): the dashboard and Authentik public names, every
// Cloudflare change, what is left out and why, and a digest that covers
// them and the hidden zone, account and record IDs they act on.
type tunnelPlan struct {
	Name              string             `json:"name"`
	DashboardHostname string             `json:"dashboard_hostname"`
	AuthHostname      string             `json:"auth_hostname"`
	Zone              string             `json:"zone"`
	Ingress           []tunnelPlanRoute  `json:"ingress"`
	DNS               []tunnelPlanChange `json:"dns"`
	Omitted           []tunnelOmission   `json:"omitted"`
	SignIn            string             `json:"sign_in,omitempty"`
	// Adopt is set when the plan takes over an existing tunnel instead of
	// creating one (S290, ADR-041 §3).
	Adopt *tunnelAdoption `json:"adopt,omitempty"`
	// Coverage says whether Cloudflare has a certificate for each public
	// name (S312); an uncovered name is also a warning.
	Coverage []publicNameCoverage `json:"coverage"`
	Warnings []string             `json:"warnings"`
	Digest   string               `json:"digest"`

	// Hidden: never serialized, but bound into the digest.
	zoneID    string
	accountID string
	replaces  []router.DNSRecord
	// keeps are existing CNAMEs already pointing at an adopted tunnel,
	// recorded as Homestead's at approval.
	keeps []router.DNSRecord
}

// tunnelAdoption is what adopting shows: the tunnel Cloudflare has, its
// live connections, and what happens to each entry of the connector
// config already on this appliance.
type tunnelAdoption struct {
	ID          string              `json:"id"`
	Status      string              `json:"status"`
	Connections int                 `json:"connections"`
	Existing    []tunnelAdoptedName `json:"existing"`
}

// tunnelAdoptedName is one ingress entry of the config being adopted.
type tunnelAdoptedName struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	Outcome  string `json:"outcome"`
}

// tunnelPlanRoute is one public name: the connector delivers it to the
// edge's tunnel entry, which serves it from the local route Route.
type tunnelPlanRoute struct {
	Hostname string `json:"hostname"`
	Role     string `json:"role"`
	Route    string `json:"route"`
	Service  string `json:"service"`
}

// tunnelOmission is a proposed public name the plan leaves out.
type tunnelOmission struct {
	Hostname string `json:"hostname"`
	Role     string `json:"role"`
	Reason   string `json:"reason"`
}

type tunnelPlanChange struct {
	Name     string             `json:"name"`
	Type     string             `json:"type"`
	Content  string             `json:"content"`
	Proxied  bool               `json:"proxied"`
	Action   string             `json:"action"`
	Replaces []router.DNSRecord `json:"replaces"`
}

// Public name roles (ADR-037 §4).
const (
	tunnelRoleDashboard = "dashboard"
	tunnelRoleAuth      = "authentik"
)

// tunnelEntryService is where the connector delivers every public name:
// the edge's loopback-only tunnel entry, which routes by name (ADR-037).
var tunnelEntryService = "http://127.0.0.1:" + strconv.Itoa(docker.TunnelEntryPort)

// tunnelPendingTarget stands for the tunnel ID Cloudflare assigns at setup.
const tunnelPendingTarget = "<new tunnel id>.cfargotunnel.com"

// planRefusal is a plan that cannot be offered, with its HTTP status.
type planRefusal struct {
	status int
	msg    string
}

func (e *planRefusal) Error() string { return e.msg }

func refusePlan(status int, format string, args ...any) error {
	return &planRefusal{status: status, msg: fmt.Sprintf(format, args...)}
}

func writePlanError(w http.ResponseWriter, err error) {
	var refusal *planRefusal
	if errors.As(err, &refusal) {
		http.Error(w, refusal.msg, refusal.status)
		return
	}
	var provider *router.TunnelError
	if errors.As(err, &provider) {
		http.Error(w, "Cloudflare refused: "+provider.Error(), http.StatusBadGateway)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

type tunnelRequest struct {
	Name              string `json:"name"`
	DashboardHostname string `json:"dashboard_hostname"`
	AuthHostname      string `json:"auth_hostname"`
	// Adopt names an existing tunnel to take over instead of creating one.
	Adopt string `json:"adopt,omitempty"`
}

// validTunnelID accepts the shape of a Cloudflare tunnel ID (a UUID).
func validTunnelID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if c != '-' && (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// tunnelFacts is what the plan reads outside Cloudflare and the store:
// the setup domain and whether sign-in with Authentik is linked.
type tunnelFacts struct {
	Domain       string
	SignInLinked bool
}

func (s *Server) tunnelFacts() tunnelFacts {
	var f tunnelFacts
	if s.setupDomain != nil {
		f.Domain = s.setupDomain()
	}
	if s.signInOffered != nil {
		f.SignInLinked = s.signInOffered()
	}
	return f
}

// tunnelDefaults proposes the public names from the setup domain.
func tunnelDefaults(domain string) (dashboard, auth string) {
	if domain == "" {
		return "", ""
	}
	return "homestead." + domain, "auth." + domain
}

// authentikRoute finds the Authentik server route among edge routes.
func authentikRoute(routes []docker.Route) int {
	for i, r := range routes {
		host := strings.ToLower(string(r.Host))
		if r.Target.Host == "server" &&
			(strings.HasPrefix(host, authentikPackage+".") || strings.Contains(host, "-"+authentikPackage+".")) {
			return i
		}
	}
	return -1
}

func dashboardRoute(routes []docker.Route) int {
	for i, r := range routes {
		if r.Dashboard {
			return i
		}
	}
	return -1
}

// buildTunnelPlan reads Cloudflare and the store and changes nothing.
func buildTunnelPlan(ctx context.Context, st *store.Store, secrets *secret.Store, facts tunnelFacts, req tunnelRequest) (tunnelPlan, error) {
	req.Adopt = strings.ToLower(strings.TrimSpace(req.Adopt))
	if req.Adopt != "" {
		if !validTunnelID(req.Adopt) {
			return tunnelPlan{}, refusePlan(http.StatusBadRequest, "adopt takes a Cloudflare tunnel ID (a UUID)")
		}
	} else if err := router.ValidateTunnelName(req.Name); err != nil {
		return tunnelPlan{}, refusePlan(http.StatusBadRequest, "%v", err)
	}
	dashName, authName := tunnelDefaults(facts.Domain)
	if req.DashboardHostname != "" {
		dashName = req.DashboardHostname
	}
	if req.AuthHostname != "" {
		authName = req.AuthHostname
	}
	dashName, authName = strings.ToLower(strings.TrimSpace(dashName)), strings.ToLower(strings.TrimSpace(authName))
	if dashName == "" || authName == "" {
		return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity,
			"no domain is chosen in setup: choose one on the Domain step, or name dashboard_hostname and auth_hostname")
	}
	if dashName == authName {
		return tunnelPlan{}, refusePlan(http.StatusBadRequest, "the dashboard and Authentik need different public names")
	}
	ref := req.Name
	if req.Adopt != "" {
		ref = req.Adopt
	}
	for _, name := range []string{dashName, authName} {
		if err := (router.TunnelRoute{Hostname: name, TunnelID: ref, Service: tunnelEntryService}).Validate(); err != nil {
			return tunnelPlan{}, refusePlan(http.StatusBadRequest, "%v", err)
		}
		if docker.IsPrivateName(docker.Hostname(name)) {
			return tunnelPlan{}, refusePlan(http.StatusBadRequest, "%s is a local name; the tunnel carries public names", name)
		}
	}
	routes, err := router.ListRoutes(st)
	if err != nil {
		return tunnelPlan{}, err
	}
	if len(routes) > 0 {
		if req.Adopt != "" {
			return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity, "Homestead already manages a tunnel; tear it down before adopting another")
		}
		return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity, "a tunnel is already set up; tear it down first")
	}
	var adopted router.ConnectorConfig
	if req.Adopt != "" {
		data, err := os.ReadFile(connectorConfigPath())
		if err != nil {
			return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity,
				"this appliance has no connector config at %s, so its connector does not serve tunnel %s", connectorConfigPath(), req.Adopt)
		}
		if adopted, err = router.ParseConnectorConfig(data); err != nil {
			return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity, "%v", err)
		}
		if !strings.EqualFold(adopted.Tunnel, req.Adopt) {
			return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity,
				"this appliance's connector serves a different tunnel, not %s", req.Adopt)
		}
	}
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return tunnelPlan{}, err
	}
	for _, r := range set.Routes {
		for _, n := range r.AllNames() {
			if string(n) == dashName || string(n) == authName {
				return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity, "%s is already a name of the edge route %s", n, r.Host)
			}
		}
	}
	dash, ak := dashboardRoute(set.Routes), authentikRoute(set.Routes)
	plan := tunnelPlan{
		Name: req.Name, DashboardHostname: dashName, AuthHostname: authName,
		Ingress: []tunnelPlanRoute{}, DNS: []tunnelPlanChange{}, Omitted: []tunnelOmission{}, Warnings: []string{},
		Coverage: []publicNameCoverage{},
	}
	switch {
	case ak < 0:
		plan.Omitted = append(plan.Omitted, tunnelOmission{Hostname: authName, Role: tunnelRoleAuth,
			Reason: "Authentik is not installed. Install it from the catalog: remote sign-in needs it."})
	default:
		plan.Ingress = append(plan.Ingress, tunnelPlanRoute{Hostname: authName, Role: tunnelRoleAuth,
			Route: string(set.Routes[ak].Host), Service: tunnelEntryService})
	}
	// The dashboard is published only behind Authentik (ADR-037 §4).
	switch {
	case dash < 0:
		plan.Omitted = append(plan.Omitted, tunnelOmission{Hostname: dashName, Role: tunnelRoleDashboard,
			Reason: "The dashboard has no edge address yet."})
	case !facts.SignInLinked:
		plan.Omitted = append(plan.Omitted, tunnelOmission{Hostname: dashName, Role: tunnelRoleDashboard,
			Reason: "Sign-in with Authentik is not linked yet. The dashboard is published only behind Authentik: link it on the Sign-in page, then plan the tunnel again."})
	case ak < 0:
		plan.Omitted = append(plan.Omitted, tunnelOmission{Hostname: dashName, Role: tunnelRoleDashboard,
			Reason: "Authentik is not published, so remote sign-in could not finish."})
	default:
		plan.Ingress = append([]tunnelPlanRoute{{Hostname: dashName, Role: tunnelRoleDashboard,
			Route: string(set.Routes[dash].Host), Service: tunnelEntryService}}, plan.Ingress...)
	}
	if len(plan.Ingress) == 0 {
		reasons := make([]string, 0, len(plan.Omitted))
		for _, o := range plan.Omitted {
			reasons = append(reasons, o.Hostname+": "+o.Reason)
		}
		return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity, "nothing to publish yet. %s", strings.Join(reasons, " "))
	}
	token, err := tunnelToken(secrets)
	if err != nil {
		return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity, "%v", err)
	}
	client, err := tunnelClient()
	if err != nil {
		return tunnelPlan{}, err
	}
	for _, in := range plan.Ingress {
		zone, err := client.ZoneFor(ctx, token, in.Hostname)
		if errors.Is(err, router.ErrNoZone) {
			return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity,
				"no Cloudflare zone the token can see contains %s: add the domain to Cloudflare, or give the token access to its zone", in.Hostname)
		}
		if err != nil {
			return tunnelPlan{}, err
		}
		if zone.AccountID == "" {
			return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity, "Cloudflare did not say which account owns zone %s", zone.Name)
		}
		if plan.zoneID == "" {
			plan.Zone, plan.zoneID, plan.accountID = zone.Name, zone.ID, zone.AccountID
		} else if zone.ID != plan.zoneID {
			return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity,
				"%s and %s are in different Cloudflare zones; pick names in one zone", plan.Ingress[0].Hostname, in.Hostname)
		}
		existing, err := client.RecordsAt(ctx, token, zone.ID, in.Hostname)
		if err != nil {
			return tunnelPlan{}, err
		}
		change := tunnelPlanChange{
			Name: in.Hostname, Type: "CNAME", Content: tunnelPendingTarget, Proxied: true,
			Action: "create", Replaces: []router.DNSRecord{},
		}
		if req.Adopt != "" {
			change.Content = router.TunnelTarget(req.Adopt)
		}
		for _, record := range existing {
			if req.Adopt != "" && record.Type == "CNAME" && strings.EqualFold(record.Content, change.Content) {
				change.Action = "keep"
				plan.keeps = append(plan.keeps, record)
				continue
			}
			switch record.Type {
			case "A", "AAAA", "CNAME":
				change.Replaces = append(change.Replaces, record)
			default:
				return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity,
					"%s already has a %s record, which a tunnel CNAME cannot sit next to; remove it in Cloudflare or pick another hostname",
					in.Hostname, record.Type)
			}
		}
		sort.Slice(change.Replaces, func(i, j int) bool { return change.Replaces[i].ID < change.Replaces[j].ID })
		if len(change.Replaces) > 0 {
			change.Action = "replace"
			plan.Warnings = append(plan.Warnings,
				"Existing records for "+in.Hostname+" are deleted now and put back when the tunnel is torn down, unless something else is at that name by then.")
		}
		plan.replaces = append(plan.replaces, change.Replaces...)
		plan.DNS = append(plan.DNS, change)
	}
	names := make([]string, 0, len(plan.Ingress))
	for _, in := range plan.Ingress {
		names = append(names, in.Hostname)
	}
	if len(names) > 0 {
		coverage, warnings, err := certificateCoverage(ctx, client, token,
			router.Zone{ID: plan.zoneID, Name: plan.Zone, AccountID: plan.accountID}, names)
		if err != nil {
			return tunnelPlan{}, err
		}
		plan.Coverage = coverage
		plan.Warnings = append(plan.Warnings, warnings...)
	}
	if req.Adopt != "" {
		if err := planAdoption(ctx, client, token, &plan, req.Adopt, adopted); err != nil {
			return tunnelPlan{}, err
		}
	}
	for _, in := range plan.Ingress {
		if in.Role == tunnelRoleDashboard {
			plan.SignIn = "After setup, re-plan Sign-in and approve it: it adds https://" + dashName +
				" and its redirect address, and sends browsers there to https://" + authName + ". Password sign-in, claim, recovery and linking stay on the local and overlay addresses."
		}
	}
	plan.Digest = plan.digest()
	return plan, nil
}

// planAdoption checks the tunnel being adopted with Cloudflare and lists
// what happens to each entry of the connector config already here.
func planAdoption(ctx context.Context, client *router.TunnelClient, token string, plan *tunnelPlan, id string, adopted router.ConnectorConfig) error {
	details, err := client.GetTunnelDetails(ctx, token, plan.accountID, id)
	var refusal *router.TunnelError
	if errors.As(err, &refusal) || (err == nil && details.Deleted) {
		return refusePlan(http.StatusUnprocessableEntity,
			"Cloudflare has no tunnel %s in the account that holds %s", id, plan.Zone)
	}
	if err != nil {
		return err
	}
	if details.RemoteConfig {
		return refusePlan(http.StatusUnprocessableEntity,
			"tunnel %s is configured from the Cloudflare dashboard; Homestead adopts only tunnels configured by the connector's local file", id)
	}
	plan.Name = details.Name
	plan.Adopt = &tunnelAdoption{ID: id, Status: details.Status, Connections: len(details.Connections), Existing: []tunnelAdoptedName{}}
	planned := map[string]bool{}
	for _, in := range plan.Ingress {
		planned[in.Hostname] = true
	}
	for _, in := range adopted.Ingress {
		if in.Hostname == "" {
			continue
		}
		entry := tunnelAdoptedName{Hostname: in.Hostname, Service: in.Service,
			Outcome: "Kept, now through the edge to " + in.Hostname + "'s Homestead route."}
		if !planned[in.Hostname] {
			entry.Outcome = "Dropped from the connector. Its DNS record is left as it is and answers 404 until you remove it in Cloudflare or publish the name again."
		}
		plan.Adopt.Existing = append(plan.Adopt.Existing, entry)
	}
	if len(details.Connections) == 0 {
		plan.Warnings = append(plan.Warnings, "The tunnel has no live connections right now; approving restarts this appliance's connector.")
	}
	return nil
}

// edgeNames maps each local edge route to the public names it gains.
func (p tunnelPlan) edgeNames() map[docker.Hostname][]docker.Hostname {
	names := map[docker.Hostname][]docker.Hostname{}
	for _, in := range p.Ingress {
		names[docker.Hostname(in.Route)] = append(names[docker.Hostname(in.Route)], docker.Hostname(in.Hostname))
	}
	return names
}

// applyTunnelNames gives each edge route exactly the tunnel names in
// names, keyed by the route's host (every other route loses its tunnel
// names), deploys the edge stack so it publishes the loopback tunnel
// entry, and reconciles the routes. A var so tests skip Docker.
var applyTunnelNames = func(ctx context.Context, st *store.Store, names map[docker.Hostname][]docker.Hostname) error {
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return err
	}
	for i := range set.Routes {
		set.Routes[i].Tunnel = names[set.Routes[i].Host]
	}
	deploy, err := docker.DeployDefinition(docker.CaddyStack())
	if err != nil {
		return err
	}
	reconcile, err := docker.ReconcileDefinition(docker.CaddyStack(), set, docker.RouteState{
		Load: func() (docker.RouteSet, error) { return loadEdgeRoutes(st) },
		Save: func(set docker.RouteSet) error { return saveEdgeRoutes(st, set) },
	})
	if err != nil {
		return err
	}
	for _, def := range []operation.Definition{deploy, reconcile} {
		for _, step := range def.Steps {
			if err := step.Run(ctx); err != nil {
				return fmt.Errorf("edge %s: %w", step.Name, err)
			}
		}
	}
	return nil
}

// digest binds the reviewed plan to the hidden IDs it acts on, so a
// changed zone, account or replaced record invalidates the approval.
func (p tunnelPlan) digest() string {
	h := sha256.New()
	visible := p
	visible.Digest = ""
	data, _ := json.Marshal(visible)
	h.Write(data)
	fmt.Fprintf(h, "\nzone=%s\naccount=%s", p.zoneID, p.accountID)
	for _, record := range p.replaces {
		fmt.Fprintf(h, "\nreplace=%s", record.ID)
	}
	for _, record := range p.keeps {
		fmt.Fprintf(h, "\nkeep=%s", record.ID)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func decodeTunnelRequest(w http.ResponseWriter, r *http.Request, digest *string) (tunnelRequest, bool) {
	var body struct {
		tunnelRequest
		Digest    *string `json:"digest"`
		APIToken  *string `json:"api_token"`
		AccountID *string `json:"account_id"`
		Hostname  *string `json:"hostname"`
		Service   *string `json:"service"`
	}
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return tunnelRequest{}, false
	}
	if body.APIToken != nil || body.AccountID != nil {
		http.Error(w, "the tunnel no longer takes api_token or account_id: it uses the Cloudflare token from the setup Domain step and finds the account from the hostname's zone", http.StatusBadRequest)
		return tunnelRequest{}, false
	}
	if body.Hostname != nil || body.Service != nil {
		http.Error(w, "the tunnel no longer takes hostname or service: it publishes the dashboard and Authentik through the edge, named by dashboard_hostname and auth_hostname (default homestead.<domain> and auth.<domain>)", http.StatusBadRequest)
		return tunnelRequest{}, false
	}
	if digest == nil && body.Digest != nil {
		http.Error(w, "a plan takes no digest", http.StatusBadRequest)
		return tunnelRequest{}, false
	}
	if digest != nil {
		if body.Digest == nil || *body.Digest == "" {
			http.Error(w, "digest is required: review the plan, then approve it with its digest", http.StatusBadRequest)
			return tunnelRequest{}, false
		}
		*digest = *body.Digest
	}
	return body.tunnelRequest, true
}

func (s *Server) handleTunnelPlan(st *store.Store, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeTunnelRequest(w, r, nil)
		if !ok {
			return
		}
		plan, err := buildTunnelPlan(r.Context(), st, secrets, s.tunnelFacts(), req)
		if err != nil {
			writePlanError(w, err)
			return
		}
		writeJSON(w, plan)
	}
}

func (s *Server) handleTunnelSetup(st *store.Store, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var digest string
		req, ok := decodeTunnelRequest(w, r, &digest)
		if !ok {
			return
		}
		plan, err := buildTunnelPlan(r.Context(), st, secrets, s.tunnelFacts(), req)
		if err != nil {
			writePlanError(w, err)
			return
		}
		if plan.Digest != digest {
			http.Error(w, "the plan changed since it was reviewed; review it again", http.StatusConflict)
			return
		}
		token, err := tunnelToken(secrets)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		runner := operation.NewRunner(st)
		op, err := runner.Start(r.Context(), tunnelSetupDefinition(st, token, plan), "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		published := make([]string, 0, len(plan.Ingress))
		for _, in := range plan.Ingress {
			published = append(published, in.Hostname+" -> "+in.Route)
		}
		action, verb := "tunnel.setup", "tunnel"
		if plan.Adopt != nil {
			action, verb = "tunnel.adopt", "adopt tunnel"
		}
		s.auditOperation(st, runner, op.ID, operatorActor(r), action,
			fmt.Sprintf("operation %d: %s %s for %s", op.ID, verb, plan.Name, strings.Join(published, ", ")))
		writeJSON(w, map[string]any{"id": op.ID})
	}
}

// tunnelSetupDefinition builds the approved setup: create the tunnel,
// point each public name at it (deleting the records the plan replaces,
// which are remembered for teardown), give the edge routes their public
// names, write and start the connector, and record the routes. A failure
// undoes what this run created and puts the replaced records back, so a
// retry plans from scratch.
func tunnelSetupDefinition(st *store.Store, apiToken string, plan tunnelPlan) operation.Definition {
	var tunnelID, runToken string
	var recordIDs []string
	adopting := plan.Adopt != nil
	undoTunnel := func(ctx context.Context) {
		// An adopted tunnel is the owner's: a failed adoption never deletes it.
		if client, err := tunnelClient(); err == nil && tunnelID != "" && !adopting {
			_ = client.DeleteTunnel(ctx, apiToken, plan.accountID, tunnelID)
		}
	}
	undoRecords := func(ctx context.Context) {
		client, err := tunnelClient()
		if err != nil {
			return
		}
		for _, id := range recordIDs {
			if client.DeleteRecord(ctx, apiToken, plan.zoneID, id) == nil {
				_ = router.ForgetOwned(st, id)
			}
		}
	}
	undoReplaced := func(ctx context.Context) {
		if tunnelID != "" {
			_ = restoreReplacedRecords(ctx, st, apiToken, tunnelID)
		}
	}
	undoKeeps := func() {
		for _, record := range plan.keeps {
			_ = router.ForgetOwned(st, record.ID)
		}
	}
	undoCloudflare := func(ctx context.Context) {
		undoRecords(ctx)
		undoKeeps()
		undoReplaced(ctx)
		undoTunnel(ctx)
	}
	def := operation.Definition{Type: "tunnel.setup"}
	if adopting {
		def.Type = "tunnel.adopt"
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "adopt-tunnel",
			Run: func(ctx context.Context) error {
				client, err := tunnelClient()
				if err != nil {
					return err
				}
				token, err := client.TunnelRunToken(ctx, apiToken, plan.accountID, plan.Adopt.ID)
				if err != nil {
					return fmt.Errorf("tunnel: fetch the run token for %s: %w", plan.Adopt.ID, err)
				}
				tunnelID, runToken = plan.Adopt.ID, token
				return nil
			},
		})
	} else {
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "create-tunnel",
			Run: func(ctx context.Context) error {
				client, err := tunnelClient()
				if err != nil {
					return err
				}
				tunnel, err := client.CreateTunnel(ctx, apiToken, plan.accountID, plan.Name)
				if err != nil {
					return err
				}
				tunnelID = tunnel.ID
				runToken = tunnel.Token
				return nil
			},
		})
	}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "dns",
		Run: func(ctx context.Context) error {
			client, err := tunnelClient()
			if err != nil {
				undoTunnel(ctx)
				return err
			}
			for _, record := range plan.replaces {
				// Remembered first, so a crash mid-step still leaves it restorable.
				if err := router.RecordReplaced(st, router.ReplacedRecord{TunnelID: tunnelID, ZoneID: plan.zoneID, Record: record}); err != nil {
					undoCloudflare(ctx)
					return err
				}
				if err := client.DeleteRecord(ctx, apiToken, plan.zoneID, record.ID); err != nil {
					_ = router.ForgetReplaced(st, tunnelID, record.ID)
					undoCloudflare(ctx)
					return fmt.Errorf("tunnel: delete %s record for %s: %w", record.Type, record.Name, err)
				}
			}
			for _, record := range plan.keeps {
				if err := router.RecordOwned(st, router.OwnedRecord{
					RecordID: record.ID, ZoneID: plan.zoneID, Hostname: record.Name, TunnelID: tunnelID,
				}); err != nil {
					undoCloudflare(ctx)
					return err
				}
			}
			for _, in := range plan.Ingress {
				if kept(plan, in.Hostname) {
					continue
				}
				created, err := client.CreateTunnelCNAME(ctx, apiToken, plan.zoneID, in.Hostname, tunnelID)
				if err != nil {
					undoCloudflare(ctx)
					return fmt.Errorf("tunnel: create DNS record for %s: %w", in.Hostname, err)
				}
				recordIDs = append(recordIDs, created.ID)
				if err := router.RecordOwned(st, router.OwnedRecord{
					RecordID: created.ID, ZoneID: plan.zoneID, Hostname: in.Hostname, TunnelID: tunnelID,
				}); err != nil {
					undoCloudflare(ctx)
					return err
				}
			}
			return nil
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "edge",
		Run: func(ctx context.Context) error {
			if err := applyTunnelNames(ctx, st, plan.edgeNames()); err != nil {
				_ = applyTunnelNames(ctx, st, nil)
				undoCloudflare(ctx)
				return err
			}
			return nil
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "connector",
		Run: func(ctx context.Context) error {
			fail := func(err error) error {
				_ = applyTunnelNames(ctx, st, nil)
				undoCloudflare(ctx)
				return err
			}
			if _, err := tunnelLookPath("cloudflared"); err != nil {
				return fail(fmt.Errorf("tunnel: install cloudflared, then re-run setup"))
			}
			// An adopted connector's own files come back if this fails, so
			// it keeps serving what it served before.
			previousConfig, configErr := os.ReadFile(connectorConfigPath())
			previousToken, tokenErr := os.ReadFile(connectorTokenPath())
			restore := func() {
				if !adopting {
					return
				}
				if configErr == nil {
					_, _ = writeIfChanged(connectorConfigPath(), previousConfig, 0o644)
				}
				if tokenErr == nil {
					_, _ = writeIfChanged(connectorTokenPath(), previousToken, 0o600)
				}
				_ = tunnelSystemctl("restart", tunnelUnitName)
			}
			ingress := make([]router.Ingress, 0, len(plan.Ingress))
			for _, in := range plan.Ingress {
				ingress = append(ingress, router.Ingress{Hostname: in.Hostname, Service: in.Service})
			}
			sort.Slice(ingress, func(i, j int) bool { return ingress[i].Hostname < ingress[j].Hostname })
			changed, err := writeConnector(tunnelID, ingress, runToken)
			if err != nil {
				restore()
				return fail(err)
			}
			if err := startConnector(changed); err != nil {
				restore()
				return fail(err)
			}
			return nil
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "record-route",
		Run: func(ctx context.Context) error {
			for _, in := range plan.Ingress {
				if err := router.RecordRoute(st, router.TunnelRoute{
					Hostname: in.Hostname, TunnelID: tunnelID, Service: in.Service,
				}, plan.Name); err != nil {
					return err
				}
			}
			return router.SetTunnelAccount(st, tunnelID, plan.accountID, plan.zoneID, plan.Zone)
		},
	})
	return def
}

func (s *Server) handleTunnelTeardown(st *store.Store, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			http.Error(w, "tunnel id must not be empty", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Has("account_id") {
			http.Error(w, "teardown no longer takes account_id: the account is the one the tunnel was set up under", http.StatusBadRequest)
			return
		}
		token, err := tunnelToken(secrets)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		accountID, err := tunnelTeardownAccount(r.Context(), st, token, id)
		if err != nil {
			writePlanError(w, err)
			return
		}
		runner := operation.NewRunner(st)
		op, err := runner.Start(r.Context(), tunnelTeardownDefinition(st, token, accountID, id), "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.auditOperation(st, runner, op.ID, operatorActor(r), "tunnel.teardown",
			fmt.Sprintf("operation %d: tunnel %s", op.ID, id))
		writeJSON(w, map[string]any{"id": op.ID})
	}
}

// tunnelTeardownAccount finds the account a tunnel lives in: the one
// stored at setup, else the account of a recorded hostname's zone, else
// the token's only account (tunnels set up before S294).
func tunnelTeardownAccount(ctx context.Context, st *store.Store, token, tunnelID string) (string, error) {
	if account, err := router.TunnelAccount(st, tunnelID); err != nil {
		return "", err
	} else if account != "" {
		return account, nil
	}
	client, err := tunnelClient()
	if err != nil {
		return "", err
	}
	routes, err := router.ListRoutes(st)
	if err != nil {
		return "", err
	}
	for _, route := range routes {
		if route.TunnelID != tunnelID {
			continue
		}
		if zone, err := client.ZoneFor(ctx, token, route.Hostname); err == nil && zone.AccountID != "" {
			return zone.AccountID, nil
		}
	}
	accounts, err := client.ListAccounts(ctx, token)
	if err != nil {
		return "", err
	}
	if len(accounts) != 1 {
		return "", refusePlan(http.StatusUnprocessableEntity,
			"cannot tell which Cloudflare account holds this tunnel: the token sees %d accounts and no recorded hostname is in a visible zone", len(accounts))
	}
	return accounts[0], nil
}

// tunnelTeardownDefinition builds teardown: stop the connector, delete
// the provider tunnel (missing reads as already gone), delete the DNS
// records Homestead created that still point at it, put back the records
// setup replaced (when their name is free), disable the
// connector and remove setup-written files (the static unit file stays:
// it belongs to provisioning, not setup), then forget recorded routes.
// Files go before rows so a failure never leaves a live connector with no
// record of it.
func tunnelTeardownDefinition(st *store.Store, apiToken, accountID, tunnelID string) operation.Definition {
	def := operation.Definition{Type: "tunnel.teardown"}
	// The connector stops first: the provider refuses to delete a tunnel
	// that still has live connections (error 1022). If the delete then
	// fails the connector is started again so the tunnel keeps serving.
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "stop-connector",
		Run: func(ctx context.Context) error {
			_ = tunnelSystemctl("stop", tunnelUnitName)
			return nil
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "delete-tunnel",
		Run: func(ctx context.Context) error {
			err := deleteProviderTunnel(ctx, apiToken, accountID, tunnelID)
			if err != nil {
				_ = tunnelSystemctl("start", tunnelUnitName)
			}
			return err
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "delete-dns",
		Run: func(ctx context.Context) error {
			return deleteOwnedRecords(ctx, st, apiToken, tunnelID)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "restore-dns",
		Run: func(ctx context.Context) error {
			return restoreReplacedRecords(ctx, st, apiToken, tunnelID)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "remove-connector",
		Run: func(ctx context.Context) error {
			_ = tunnelSystemctl("disable", "--now", tunnelUnitName)
			_ = os.Remove(filepath.Join(tunnelConnectorDir, "config.yml"))
			_ = os.Remove(filepath.Join(tunnelConnectorDir, "token.env"))
			return nil
		},
	})
	// The edge drops the public names, so its loopback tunnel entry
	// serves nothing.
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "edge",
		Run: func(ctx context.Context) error {
			return applyTunnelNames(ctx, st, nil)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "forget-routes",
		Run: func(ctx context.Context) error {
			routes, err := router.ListRoutes(st)
			if err != nil {
				return err
			}
			for _, route := range routes {
				if route.TunnelID == tunnelID {
					if err := router.DeleteRoute(st, route.Hostname); err != nil {
						return err
					}
				}
			}
			return nil
		},
	})
	return def
}

// deleteOwnedRecords deletes the records Homestead created for a tunnel,
// only while they still point at it (ADR-037). A record the owner changed
// is left alone and audited; a record already gone is forgotten.
func deleteOwnedRecords(ctx context.Context, st *store.Store, apiToken, tunnelID string) error {
	return deleteOwnedRecordsAt(ctx, st, apiToken, tunnelID, "")
}

// deleteOwnedRecordsAt is deleteOwnedRecords for one public name, or for
// every name when hostname is empty.
func deleteOwnedRecordsAt(ctx context.Context, st *store.Store, apiToken, tunnelID, hostname string) error {
	all, err := router.OwnedRecords(st, tunnelID)
	if err != nil {
		return err
	}
	owned := all[:0:0]
	for _, record := range all {
		if hostname == "" || record.Hostname == hostname {
			owned = append(owned, record)
		}
	}
	if len(owned) == 0 {
		return nil
	}
	client, err := tunnelClient()
	if err != nil {
		return err
	}
	for _, record := range owned {
		current, err := client.GetRecord(ctx, apiToken, record.ZoneID, record.RecordID)
		var refusal *router.TunnelError
		switch {
		case errors.As(err, &refusal):
			// Gone already.
		case err != nil:
			return err
		case current.Type == "CNAME" && current.Content == router.TunnelTarget(tunnelID):
			if err := client.DeleteRecord(ctx, apiToken, record.ZoneID, record.RecordID); err != nil {
				return fmt.Errorf("tunnel: delete DNS record for %s: %w", record.Hostname, err)
			}
		default:
			_, _ = audit.New(st).Record(audit.Entry{
				Actor: "homestead", Action: "tunnel.teardown.dns-kept", Outcome: audit.OutcomeOK,
				Detail: fmt.Sprintf("%s no longer points at tunnel %s, so it was left in place", record.Hostname, tunnelID),
			})
		}
		if err := router.ForgetOwned(st, record.RecordID); err != nil {
			return err
		}
	}
	return nil
}

// restoreReplacedRecords puts back the records a tunnel's setup deleted.
// A name that holds an address or CNAME record again (the owner's, or a
// Homestead record kept because it was changed) is left alone and the
// skip is audited; nothing is overwritten.
func restoreReplacedRecords(ctx context.Context, st *store.Store, apiToken, tunnelID string) error {
	return restoreReplacedRecordsAt(ctx, st, apiToken, tunnelID, "")
}

// restoreReplacedRecordsAt is restoreReplacedRecords for one public name,
// or for every name when hostname is empty.
func restoreReplacedRecordsAt(ctx context.Context, st *store.Store, apiToken, tunnelID, hostname string) error {
	all, err := router.ReplacedRecords(st, tunnelID)
	if err != nil {
		return err
	}
	replaced := all[:0:0]
	for _, r := range all {
		if hostname == "" || r.Record.Name == hostname {
			replaced = append(replaced, r)
		}
	}
	if len(replaced) == 0 {
		return nil
	}
	client, err := tunnelClient()
	if err != nil {
		return err
	}
	// Checked once per name before restoring any of it, so an A and an
	// AAAA that were replaced together both come back.
	occupied := map[string]bool{}
	for _, r := range replaced {
		key := r.ZoneID + "/" + r.Record.Name
		if _, checked := occupied[key]; checked {
			continue
		}
		current, err := client.RecordsAt(ctx, apiToken, r.ZoneID, r.Record.Name)
		if err != nil {
			return err
		}
		occupied[key] = false
		for _, record := range current {
			switch record.Type {
			case "A", "AAAA", "CNAME":
				occupied[key] = true
			}
		}
	}
	for _, r := range replaced {
		if occupied[r.ZoneID+"/"+r.Record.Name] {
			_, _ = audit.New(st).Record(audit.Entry{
				Actor: "homestead", Action: "tunnel.teardown.dns-not-restored", Outcome: audit.OutcomeOK,
				Detail: fmt.Sprintf("%s %s -> %s was not put back: the name has a record again", r.Record.Type, r.Record.Name, r.Record.Content),
			})
		} else if _, err := client.RestoreRecord(ctx, apiToken, r.ZoneID, r.Record); err != nil {
			return fmt.Errorf("tunnel: restore %s record for %s: %w", r.Record.Type, r.Record.Name, err)
		}
		if err := router.ForgetReplaced(st, tunnelID, r.Record.ID); err != nil {
			return err
		}
	}
	return nil
}

// deleteProviderTunnel deletes the provider tunnel; a tunnel the provider
// no longer knows reads as already gone.
func deleteProviderTunnel(ctx context.Context, apiToken, accountID, tunnelID string) error {
	client, err := tunnelClient()
	if err != nil {
		return err
	}
	if _, err := client.GetTunnel(ctx, apiToken, accountID, tunnelID); err != nil {
		var refusal *router.TunnelError
		if errors.As(err, &refusal) {
			return nil
		}
		return err
	}
	return client.DeleteTunnel(ctx, apiToken, accountID, tunnelID)
}

// kept says an adopted CNAME already points the name at the tunnel.
func kept(plan tunnelPlan, hostname string) bool {
	for _, record := range plan.keeps {
		if record.Name == hostname {
			return true
		}
	}
	return false
}
