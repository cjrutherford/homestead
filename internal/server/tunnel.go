package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"homestead/internal/audit"
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
//	POST   /api/v1/tunnel/plan          {"name","hostname","service"} -> plan (read-only)
//	POST   /api/v1/tunnel/setup         {"name","hostname","service","digest"} -> operation
//	DELETE /api/v1/tunnel/tunnels/{id}  teardown operation
//
// The tunnel reuses the Domain step's Cloudflare token and derives the
// account from the hostname's zone: no route takes a token or an account
// ID, and neither is ever returned.
func (s *Server) MountTunnel(st *store.Store, secrets *secret.Store, port int) {
	s.mux.HandleFunc("GET /api/v1/tunnel/status", s.handleTunnelStatus(st, secrets))
	s.mux.HandleFunc("POST /api/v1/tunnel/plan", s.handleTunnelPlan(st, secrets, port))
	s.mux.HandleFunc("POST /api/v1/tunnel/setup", s.handleTunnelSetup(st, secrets, port))
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
	Discovery string               `json:"discovery_error,omitempty"`
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
		found := <-discovered
		response.Preferred, response.Gateway, response.Interface = found.Preferred, found.Gateway, found.Interface
		response.Protocols, response.Discovery = found.Protocols, found.Note
		writeJSON(w, response)
	}
}

// tunnelPlan is what the owner reviews before approving a tunnel
// (ADR-037): every Cloudflare change, and a digest that covers them and
// the hidden zone, account and record IDs they act on.
type tunnelPlan struct {
	Name     string             `json:"name"`
	Hostname string             `json:"hostname"`
	Service  string             `json:"service"`
	Zone     string             `json:"zone"`
	Ingress  []tunnelPlanRoute  `json:"ingress"`
	DNS      []tunnelPlanChange `json:"dns"`
	Warnings []string           `json:"warnings"`
	Digest   string             `json:"digest"`

	// Hidden: never serialized, but bound into the digest.
	zoneID    string
	accountID string
	replaces  []router.DNSRecord
}

type tunnelPlanRoute struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
}

type tunnelPlanChange struct {
	Name     string             `json:"name"`
	Type     string             `json:"type"`
	Content  string             `json:"content"`
	Proxied  bool               `json:"proxied"`
	Action   string             `json:"action"`
	Replaces []router.DNSRecord `json:"replaces"`
}

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
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
}

// buildTunnelPlan reads Cloudflare and the store and changes nothing.
func buildTunnelPlan(ctx context.Context, st *store.Store, secrets *secret.Store, port int, req tunnelRequest) (tunnelPlan, error) {
	if err := router.ValidateTunnelName(req.Name); err != nil {
		return tunnelPlan{}, refusePlan(http.StatusBadRequest, "%v", err)
	}
	route := router.TunnelRoute{Hostname: req.Hostname, TunnelID: req.Name, Service: req.Service}
	if err := route.Validate(); err != nil {
		return tunnelPlan{}, refusePlan(http.StatusBadRequest, "%v", err)
	}
	routes, err := router.ListRoutes(st)
	if err != nil {
		return tunnelPlan{}, err
	}
	for _, existing := range routes {
		if existing.Hostname == req.Hostname {
			return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity, "hostname is already set up; tear down first")
		}
	}
	token, err := tunnelToken(secrets)
	if err != nil {
		return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity, "%v", err)
	}
	client, err := tunnelClient()
	if err != nil {
		return tunnelPlan{}, err
	}
	zone, err := client.ZoneFor(ctx, token, req.Hostname)
	if errors.Is(err, router.ErrNoZone) {
		return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity,
			"no Cloudflare zone the token can see contains %s: add the domain to Cloudflare, or give the token access to its zone", req.Hostname)
	}
	if err != nil {
		return tunnelPlan{}, err
	}
	if zone.AccountID == "" {
		return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity, "Cloudflare did not say which account owns zone %s", zone.Name)
	}
	existing, err := client.RecordsAt(ctx, token, zone.ID, req.Hostname)
	if err != nil {
		return tunnelPlan{}, err
	}
	change := tunnelPlanChange{
		Name: req.Hostname, Type: "CNAME", Content: tunnelPendingTarget, Proxied: true,
		Action: "create", Replaces: []router.DNSRecord{},
	}
	for _, record := range existing {
		switch record.Type {
		case "A", "AAAA", "CNAME":
			change.Replaces = append(change.Replaces, record)
		default:
			return tunnelPlan{}, refusePlan(http.StatusUnprocessableEntity,
				"%s already has a %s record, which a tunnel CNAME cannot sit next to; remove it in Cloudflare or pick another hostname",
				req.Hostname, record.Type)
		}
	}
	sort.Slice(change.Replaces, func(i, j int) bool { return change.Replaces[i].ID < change.Replaces[j].ID })
	plan := tunnelPlan{
		Name: req.Name, Hostname: req.Hostname, Service: req.Service, Zone: zone.Name,
		Ingress:  []tunnelPlanRoute{{Hostname: req.Hostname, Service: req.Service}},
		Warnings: []string{},
		zoneID:   zone.ID, accountID: zone.AccountID, replaces: change.Replaces,
	}
	if len(change.Replaces) > 0 {
		change.Action = "replace"
		plan.Warnings = append(plan.Warnings,
			"Existing records for "+req.Hostname+" are deleted now and put back when the tunnel is torn down, unless something else is at that name by then.")
	}
	plan.DNS = []tunnelPlanChange{change}
	if serviceIsDaemon(req.Service, port) {
		plan.Warnings = append(plan.Warnings,
			"This service is the Homestead dashboard itself: the tunnel would publish it to the internet with password sign-in. Point the tunnel at an app instead.")
	}
	plan.Digest = plan.digest()
	return plan, nil
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
	return hex.EncodeToString(h.Sum(nil))
}

// serviceIsDaemon reports whether a tunnel service URL is the daemon's own
// listener on this host.
func serviceIsDaemon(service string, port int) bool {
	parsed, err := url.Parse(service)
	if err != nil || port == 0 {
		return false
	}
	servicePort := parsed.Port()
	if servicePort == "" {
		servicePort = map[string]string{"http": "80", "https": "443"}[parsed.Scheme]
	}
	if servicePort != strconv.Itoa(port) {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" || host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func decodeTunnelRequest(w http.ResponseWriter, r *http.Request, digest *string) (tunnelRequest, bool) {
	var body struct {
		tunnelRequest
		Digest    *string `json:"digest"`
		APIToken  *string `json:"api_token"`
		AccountID *string `json:"account_id"`
	}
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return tunnelRequest{}, false
	}
	if body.APIToken != nil || body.AccountID != nil {
		http.Error(w, "the tunnel no longer takes api_token or account_id: it uses the Cloudflare token from the setup Domain step and finds the account from the hostname's zone", http.StatusBadRequest)
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

func (s *Server) handleTunnelPlan(st *store.Store, secrets *secret.Store, port int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeTunnelRequest(w, r, nil)
		if !ok {
			return
		}
		plan, err := buildTunnelPlan(r.Context(), st, secrets, port, req)
		if err != nil {
			writePlanError(w, err)
			return
		}
		writeJSON(w, plan)
	}
}

func (s *Server) handleTunnelSetup(st *store.Store, secrets *secret.Store, port int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var digest string
		req, ok := decodeTunnelRequest(w, r, &digest)
		if !ok {
			return
		}
		plan, err := buildTunnelPlan(r.Context(), st, secrets, port, req)
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
		s.auditOperation(st, runner, op.ID, operatorActor(r), "tunnel.setup",
			fmt.Sprintf("operation %d: tunnel %s for %s -> %s", op.ID, plan.Name, plan.Hostname, plan.Service))
		writeJSON(w, map[string]any{"id": op.ID})
	}
}

// tunnelSetupDefinition builds the approved setup: create the tunnel,
// point its hostname at it (deleting the records the plan replaces, which
// are remembered for teardown), write and start the connector, and record
// the route. A DNS or connector failure undoes what this run created and
// puts the replaced records back, so a retry plans from scratch.
func tunnelSetupDefinition(st *store.Store, apiToken string, plan tunnelPlan) operation.Definition {
	var tunnelID, runToken, recordID string
	undoTunnel := func(ctx context.Context) {
		if client, err := tunnelClient(); err == nil && tunnelID != "" {
			_ = client.DeleteTunnel(ctx, apiToken, plan.accountID, tunnelID)
		}
	}
	undoRecord := func(ctx context.Context) {
		if client, err := tunnelClient(); err == nil && recordID != "" {
			if client.DeleteRecord(ctx, apiToken, plan.zoneID, recordID) == nil {
				_ = router.ForgetOwned(st, recordID)
			}
		}
	}
	undoReplaced := func(ctx context.Context) {
		if tunnelID != "" {
			_ = restoreReplacedRecords(ctx, st, apiToken, tunnelID)
		}
	}
	def := operation.Definition{Type: "tunnel.setup"}
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
					undoReplaced(ctx)
					undoTunnel(ctx)
					return err
				}
				if err := client.DeleteRecord(ctx, apiToken, plan.zoneID, record.ID); err != nil {
					_ = router.ForgetReplaced(st, tunnelID, record.ID)
					undoReplaced(ctx)
					undoTunnel(ctx)
					return fmt.Errorf("tunnel: delete %s record for %s: %w", record.Type, record.Name, err)
				}
			}
			created, err := client.CreateTunnelCNAME(ctx, apiToken, plan.zoneID, plan.Hostname, tunnelID)
			if err != nil {
				undoReplaced(ctx)
				undoTunnel(ctx)
				return fmt.Errorf("tunnel: create DNS record for %s: %w", plan.Hostname, err)
			}
			recordID = created.ID
			if err := router.RecordOwned(st, router.OwnedRecord{
				RecordID: created.ID, ZoneID: plan.zoneID, Hostname: plan.Hostname, TunnelID: tunnelID,
			}); err != nil {
				undoRecord(ctx)
				undoReplaced(ctx)
				undoTunnel(ctx)
				return err
			}
			return nil
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "connector",
		Run: func(ctx context.Context) error {
			fail := func(err error) error {
				undoRecord(ctx)
				undoReplaced(ctx)
				undoTunnel(ctx)
				return err
			}
			if _, err := tunnelLookPath("cloudflared"); err != nil {
				return fail(fmt.Errorf("tunnel: install cloudflared, then re-run setup"))
			}
			configPath := filepath.Join(tunnelConnectorDir, "config.yml")
			tokenPath := filepath.Join(tunnelConnectorDir, "token.env")
			if err := os.MkdirAll(tunnelConnectorDir, 0o755); err != nil {
				return fail(fmt.Errorf("tunnel: connector dir: %w", err))
			}
			if err := os.WriteFile(configPath,
				[]byte(router.RenderConnectorConfig(tunnelID, plan.Hostname, plan.Service)), 0o644); err != nil {
				return fail(fmt.Errorf("tunnel: connector config: %w", err))
			}
			if err := os.WriteFile(tokenPath,
				[]byte("TUNNEL_TOKEN="+runToken+"\n"), 0o600); err != nil {
				return fail(fmt.Errorf("tunnel: connector token: %w", err))
			}
			if err := tunnelSystemctl("enable", "--now", tunnelUnitName); err != nil {
				return fail(fmt.Errorf("tunnel: enable connector: %w (then run: sudo systemctl enable --now %s and re-run setup)", err, tunnelUnitName))
			}
			return nil
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "record-route",
		Run: func(ctx context.Context) error {
			if err := router.RecordRoute(st, router.TunnelRoute{
				Hostname: plan.Hostname, TunnelID: tunnelID, Service: plan.Service,
			}, plan.Name); err != nil {
				return err
			}
			return router.SetTunnelAccount(st, tunnelID, plan.accountID, plan.zoneID)
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
	owned, err := router.OwnedRecords(st, tunnelID)
	if err != nil {
		return err
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
	replaced, err := router.ReplacedRecords(st, tunnelID)
	if err != nil || len(replaced) == 0 {
		return err
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
