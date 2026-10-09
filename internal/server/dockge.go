package server

import (
	"fmt"
	"net/http"

	"homestead/internal/docker"
)

// MountDockge serves the expert-interface link (S211): the assessed
// local-only route for a base domain plus whether the stack runs.
// Read-only; reconciling the route into the proxy belongs to later
// slices, so this endpoint plans nothing and writes nothing.
func (s *Server) MountDockge(client *docker.Client) {
	s.mux.HandleFunc("GET /api/v1/dockge", handleDockgeLink(client))
}

// dockgeLink is the advanced-UI row: where the interface lives, the
// access policy the engine assesses, and whether anything answers.
type dockgeLink struct {
	Host      string   `json:"host"`
	URL       string   `json:"url"`
	Exposure  string   `json:"exposure"`
	TLS       string   `json:"tls"`
	Aligned   bool     `json:"aligned"`
	Findings  []string `json:"findings"`
	Installed bool     `json:"installed"`
}

func handleDockgeLink(client *docker.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.URL.Query().Get("domain")
		if domain == "" {
			domain = "lan"
		}
		host, err := docker.DockgeDefaultHostname(domain)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		route, err := docker.DockgeRouteFor(host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		assessment, err := docker.Assess(route)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		health, err := client.Health(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("engine unavailable: %v", err), http.StatusBadGateway)
			return
		}
		if !health.Reachable {
			http.Error(w, "engine unavailable: Docker engine unreachable", http.StatusBadGateway)
			return
		}
		findings := []string{}
		for _, finding := range assessment.Findings {
			findings = append(findings, fmt.Sprintf("%s: %s", finding.Severity, finding.Message))
		}
		writeJSON(w, dockgeLink{
			Host:      string(host),
			URL:       "https://" + string(host),
			Exposure:  string(route.Exposure),
			TLS:       string(route.TLS),
			Aligned:   assessment.Aligned,
			Findings:  findings,
			Installed: docker.DockgeInstalled(health),
		})
	}
}
