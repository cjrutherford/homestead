package server

import (
	"errors"
	"net"
	"net/http"

	"homestead/internal/config"
	"homestead/internal/discover"
	"homestead/internal/firewall"
)

// MountFirewall registers firewall setup endpoints (S056) for the daemon
// port, plus external-mode handling (S057): the mode comes from the same
// config service as everything else, so CLI, dashboard, and file agree.
// The optional resolver reader adds the local DNS rules while the owner
// has the appliance resolver on (S295).
func (s *Server) MountFirewall(daemonPort int, configSvc *config.Service, resolver ...func() (bool, error)) {
	extra := func() (bool, error) { return false, nil }
	if len(resolver) > 0 && resolver[0] != nil {
		extra = resolver[0]
	}
	mode := func() bool { m, err := firewallMode(configSvc); return err == nil && m == firewall.ModeExternal }
	firewallExternal.Store(&mode)
	s.mux.HandleFunc("GET /api/v1/firewall/status", handleFirewallStatus(daemonPort, configSvc))
	s.mux.HandleFunc("GET /api/v1/firewall/plan", handleFirewallPlan(daemonPort, configSvc, extra))
	s.mux.HandleFunc("POST /api/v1/firewall/apply", handleFirewallApply(daemonPort, configSvc, extra))
}

// firewallMode reads the management mode, defaulting to managed. A
// corrupt config file fails loudly instead of guessing.
func firewallMode(configSvc *config.Service) (string, error) {
	state, err := configSvc.Get()
	if err != nil {
		return "", err
	}
	return firewall.ModeOf(state.Settings), nil
}

type firewallStatus struct {
	NFTAvailable      bool     `json:"nft_available"`
	RulesetAccessible bool     `json:"ruleset_accessible"`
	Managed           bool     `json:"managed"`
	RuleCount         int      `json:"rule_count"`
	DaemonPort        int      `json:"daemon_port"`
	LocalSubnets      []string `json:"local_subnets"`
	Mode              string   `json:"mode"`
	ExternalManagers  []string `json:"external_managers"`
}

func handleFirewallStatus(daemonPort int, configSvc *config.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		mode, err := firewallMode(configSvc)
		if err != nil {
			http.Error(w, "failed to read configuration", http.StatusInternalServerError)
			return
		}
		detect := firewall.Detect()
		status := firewallStatus{
			NFTAvailable:      detect.NFTAvailable,
			RulesetAccessible: detect.RulesetAccessible,
			DaemonPort:        daemonPort,
			LocalSubnets:      []string{},
			Mode:              mode,
			ExternalManagers:  nonNil(detect.ExternalManagers),
		}
		network, err := discover.DiscoverNetwork()
		if err != nil {
			http.Error(w, "failed to discover network", http.StatusInternalServerError)
			return
		}
		status.LocalSubnets = firewall.LocalSubnets(network)
		if detect.RulesetAccessible {
			live, err := firewall.LiveTable()
			if err != nil && !errors.Is(err, firewall.ErrNoTable) {
				http.Error(w, "failed to read firewall table", http.StatusInternalServerError)
				return
			}
			status.Managed = live != nil
			status.RuleCount = len(live)
		}
		writeJSON(w, status)
	}
}

// nonNil keeps JSON arrays [] instead of null.
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func firewallDesired(daemonPort int, resolver func() (bool, error)) (firewall.RuleSet, discover.Network, error) {
	network, err := discover.DiscoverNetwork()
	if err != nil {
		return firewall.RuleSet{}, discover.Network{}, err
	}
	desired, err := firewall.ManagementBaseline(daemonPort, network)
	if err != nil {
		return firewall.RuleSet{}, discover.Network{}, err
	}
	on, err := resolver()
	if err != nil {
		return firewall.RuleSet{}, discover.Network{}, err
	}
	if on {
		if desired, err = firewall.WithLocalResolver(desired, network); err != nil {
			return firewall.RuleSet{}, discover.Network{}, err
		}
	}
	return desired, network, nil
}

func handleFirewallPlan(daemonPort int, configSvc *config.Service, resolver func() (bool, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		mode, err := firewallMode(configSvc)
		if err != nil {
			http.Error(w, "failed to read configuration", http.StatusInternalServerError)
			return
		}
		detect := firewall.Detect()
		if !detect.NFTAvailable {
			http.Error(w, "nftables unavailable on this host", http.StatusServiceUnavailable)
			return
		}
		desired, _, err := firewallDesired(daemonPort, resolver)
		if err != nil {
			http.Error(w, "failed to build desired rules", http.StatusInternalServerError)
			return
		}
		if mode == firewall.ModeExternal {
			script, err := firewall.Render(desired)
			if err != nil {
				http.Error(w, "failed to render rules", http.StatusInternalServerError)
				return
			}
			writeJSON(w, firewall.Plan{
				InSync: false,
				Changes: []firewall.Change{{
					Action: "external",
					Detail: "managed firewall disabled (firewall_mode=external): " +
						"implement the script below in your own firewall",
				}},
				Script: script,
			})
			return
		}
		live, err := firewall.LiveTable()
		if err != nil && !errors.Is(err, firewall.ErrNoTable) {
			http.Error(w, "firewall management needs privilege", http.StatusServiceUnavailable)
			return
		}
		plan, err := firewall.Build(desired, live)
		if err != nil {
			http.Error(w, "failed to plan firewall", http.StatusInternalServerError)
			return
		}
		if plan.Changes == nil {
			plan.Changes = []firewall.Change{}
		}
		writeJSON(w, plan)
	}
}

// edgeHTTPSPort is the port a LAN client reaches the dashboard on, through
// the Caddy edge (published 443:443).
const edgeHTTPSPort = 443

// requestConnection derives the guard connection from the caller: TCP
// remotes verify by source IP on the daemon port; recovery-socket callers are same-machine by
// construction and verify as loopback; edge callers use the forwarded address.
func requestConnection(r *http.Request, daemonPort int) firewall.Connection {
	// A request through the edge is not the machine itself: the dashboard
	// socket's front wrapper stored the validated client address (ADR-035).
	if a, ok := edgeClientFrom(r.Context()); ok {
		// A LAN client reaches the edge on HTTPS, not the daemon port.
		return firewall.Connection{SourceIP: a, Protocol: firewall.ProtocolTCP, Port: edgeHTTPSPort}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" || host == "@" {
		host = "127.0.0.1"
	}
	return firewall.Connection{SourceIP: host, Protocol: firewall.ProtocolTCP, Port: daemonPort}
}

func handleFirewallApply(daemonPort int, configSvc *config.Service, resolver func() (bool, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mode, err := firewallMode(configSvc)
		if err != nil {
			http.Error(w, "failed to read configuration", http.StatusInternalServerError)
			return
		}
		if mode == firewall.ModeExternal {
			http.Error(w, "managed firewall disabled (firewall_mode=external): implement the plan in your own firewall", http.StatusConflict)
			return
		}
		detect := firewall.Detect()
		if !detect.NFTAvailable {
			http.Error(w, "nftables unavailable on this host", http.StatusServiceUnavailable)
			return
		}
		desired, _, err := firewallDesired(daemonPort, resolver)
		if err != nil {
			http.Error(w, "failed to build desired rules", http.StatusInternalServerError)
			return
		}
		if err := firewall.Guard(desired, requestConnection(r, daemonPort)); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		snap, err := firewall.TakeSnapshot()
		if err != nil {
			http.Error(w, "failed to snapshot firewall", http.StatusInternalServerError)
			return
		}
		if err := firewall.ApplySet(desired); err != nil {
			if restoreErr := snap.Restore(); restoreErr != nil {
				http.Error(w, "apply failed and rollback failed: "+restoreErr.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, "apply failed, rolled back: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"applied": true})
	}
}
