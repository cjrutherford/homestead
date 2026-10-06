package server

import (
	"errors"
	"net"
	"net/http"

	"homestead/internal/discover"
	"homestead/internal/firewall"
)

// MountFirewall registers firewall setup endpoints (S056) for the daemon
// port: status (detection plus LAN identification), plan (reviewable
// diff), and apply (guarded, snapshot-backed). The wizard and CLI drive
// all three; nothing here enforces authentication yet, consistent with
// the rest of the local API.
func (s *Server) MountFirewall(daemonPort int) {
	s.mux.HandleFunc("GET /api/v1/firewall/status", handleFirewallStatus(daemonPort))
	s.mux.HandleFunc("GET /api/v1/firewall/plan", handleFirewallPlan(daemonPort))
	s.mux.HandleFunc("POST /api/v1/firewall/apply", handleFirewallApply(daemonPort))
}

type firewallStatus struct {
	NFTAvailable      bool     `json:"nft_available"`
	RulesetAccessible bool     `json:"ruleset_accessible"`
	Managed           bool     `json:"managed"`
	RuleCount         int      `json:"rule_count"`
	DaemonPort        int      `json:"daemon_port"`
	LocalSubnets      []string `json:"local_subnets"`
}

func handleFirewallStatus(daemonPort int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		detect := firewall.Detect()
		status := firewallStatus{
			NFTAvailable:      detect.NFTAvailable,
			RulesetAccessible: detect.RulesetAccessible,
			DaemonPort:        daemonPort,
			LocalSubnets:      []string{},
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

func firewallDesired(daemonPort int) (firewall.RuleSet, discover.Network, error) {
	network, err := discover.DiscoverNetwork()
	if err != nil {
		return firewall.RuleSet{}, discover.Network{}, err
	}
	desired, err := firewall.ManagementBaseline(daemonPort, network)
	if err != nil {
		return firewall.RuleSet{}, discover.Network{}, err
	}
	return desired, network, nil
}

func handleFirewallPlan(daemonPort int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		detect := firewall.Detect()
		if !detect.NFTAvailable {
			http.Error(w, "nftables unavailable on this host", http.StatusServiceUnavailable)
			return
		}
		desired, _, err := firewallDesired(daemonPort)
		if err != nil {
			http.Error(w, "failed to build desired rules", http.StatusInternalServerError)
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

// requestConnection derives the guard connection from the caller: TCP
// remotes verify by source IP; Unix-socket callers are same-machine by
// construction and verify as loopback.
func requestConnection(r *http.Request, daemonPort int) firewall.Connection {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" || host == "@" {
		host = "127.0.0.1"
	}
	return firewall.Connection{SourceIP: host, Protocol: firewall.ProtocolTCP, Port: daemonPort}
}

func handleFirewallApply(daemonPort int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		detect := firewall.Detect()
		if !detect.NFTAvailable {
			http.Error(w, "nftables unavailable on this host", http.StatusServiceUnavailable)
			return
		}
		desired, _, err := firewallDesired(daemonPort)
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
