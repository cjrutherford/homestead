package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

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

// MountTunnel serves tunnel remote access (S246) over the API:
//
//	GET    /api/v1/tunnel/status      discovery, tunnels, connector
//	POST   /api/v1/tunnel/setup       {"name","hostname","service","api_token","account_id"} -> operation
//	DELETE /api/v1/tunnel/tunnels/{id}  teardown operation
//
// The API token seals into the secret store on setup and never reads
// back out: status reports health, never credentials.
func (s *Server) MountTunnel(st *store.Store, secrets *secret.Store) {
	s.mux.HandleFunc("GET /api/v1/tunnel/status", s.handleTunnelStatus(st, secrets))
	s.mux.HandleFunc("POST /api/v1/tunnel/setup", s.handleTunnelSetup(st, secrets))
	s.mux.HandleFunc("DELETE /api/v1/tunnel/tunnels/{id}", s.handleTunnelTeardown(st, secrets))
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

func (s *Server) handleTunnelStatus(st *store.Store, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := tunnelStatusResponse{Preferred: "tunnel", Protocols: []string{}}
		iface, gateway, err := router.DefaultRoute()
		if err != nil {
			response.Discovery = err.Error()
		} else {
			response.Gateway = gateway
			response.Interface = iface
			info, err := router.DiscoverGateway(r.Context(), iface, gateway)
			if err != nil {
				response.Discovery = err.Error()
			} else {
				for _, protocol := range info.Protocols {
					response.Protocols = append(response.Protocols, string(protocol))
				}
				response.Preferred = router.PreferredProvider(info.Protocols)
			}
		}
		routes, err := router.ListRoutes(st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		response.Routes = routes
		token, tokenErr := secrets.Get(router.TunnelSecretName)
		accountID := ""
		if tokenErr == nil {
			if client, err := router.NewTunnelClient(router.CloudflareAPIBase); err == nil {
				if accounts, err := client.ListAccounts(r.Context(), token); err == nil {
					response.Accounts = len(accounts)
					if len(accounts) == 1 {
						accountID = accounts[0]
					}
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
			if tokenErr == nil && accountID != "" {
				client, err := router.NewTunnelClient(router.CloudflareAPIBase)
				if err == nil {
					if status, err := client.GetTunnel(r.Context(), token, accountID, route.TunnelID); err == nil {
						entry.Name = status.Name
						entry.Status = status.Status
						entry.Connections = status.Connections
					} else {
						entry.Status = "unreachable"
					}
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
		writeJSON(w, response)
	}
}

func (s *Server) handleTunnelSetup(st *store.Store, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name      string `json:"name"`
			Hostname  string `json:"hostname"`
			Service   string `json:"service"`
			APIToken  string `json:"api_token"`
			AccountID string `json:"account_id"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		route := router.TunnelRoute{Hostname: body.Hostname, TunnelID: body.Name, Service: body.Service}
		if err := route.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := router.ValidateTunnelName(body.Name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.APIToken == "" {
			http.Error(w, "api_token must not be empty", http.StatusBadRequest)
			return
		}
		if body.AccountID == "" {
			http.Error(w, "account_id must not be empty", http.StatusBadRequest)
			return
		}
		if routes, err := router.ListRoutes(st); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else {
			for _, route := range routes {
				if route.Hostname == body.Hostname {
					http.Error(w, "hostname is already set up; tear down first", http.StatusUnprocessableEntity)
					return
				}
			}
		}
		if err := secrets.Set(router.TunnelSecretName, body.APIToken); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		def := tunnelSetupDefinition(st, body.Name, body.Hostname, body.Service, body.APIToken, body.AccountID)
		runner := operation.NewRunner(st)
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
}

// tunnelSetupDefinition builds the setup operation: create the tunnel,
// fetch its run token, write connector files, enable the connector,
// and record the route. Steps share locals; a later failure leaves
// earlier effects for teardown to converge away.
func tunnelSetupDefinition(st *store.Store, name, hostname, service, apiToken, accountID string) operation.Definition {
	var tunnelID, runToken string
	def := operation.Definition{Type: "tunnel.setup"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "create-tunnel",
		Run: func(ctx context.Context) error {
			client, err := router.NewTunnelClient(router.CloudflareAPIBase)
			if err != nil {
				return err
			}
			tunnel, err := client.CreateTunnel(ctx, apiToken, accountID, name)
			if err != nil {
				return err
			}
			tunnelID = tunnel.ID
			runToken = tunnel.Token
			return nil
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "connector",
		Run: func(ctx context.Context) error {
			if _, err := tunnelLookPath("cloudflared"); err != nil {
				return fmt.Errorf("tunnel: install cloudflared, then re-run setup")
			}
			configPath := filepath.Join(tunnelConnectorDir, "config.yml")
			tokenPath := filepath.Join(tunnelConnectorDir, "token.env")
			if err := os.MkdirAll(tunnelConnectorDir, 0o755); err != nil {
				return fmt.Errorf("tunnel: connector dir: %w", err)
			}
			if err := os.WriteFile(configPath,
				[]byte(router.RenderConnectorConfig(tunnelID, hostname, service)), 0o644); err != nil {
				return fmt.Errorf("tunnel: connector config: %w", err)
			}
			if err := os.WriteFile(tokenPath,
				[]byte("TUNNEL_TOKEN="+runToken+"\n"), 0o600); err != nil {
				return fmt.Errorf("tunnel: connector token: %w", err)
			}
			if err := tunnelSystemctl("enable", "--now", tunnelUnitName); err != nil {
				client, clientErr := router.NewTunnelClient(router.CloudflareAPIBase)
				if clientErr == nil {
					_ = client.DeleteTunnel(ctx, apiToken, accountID, tunnelID)
				}
				return fmt.Errorf("tunnel: enable connector: %w (then run: sudo systemctl enable --now %s and re-run setup)", err, tunnelUnitName)
			}
			return nil
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "record-route",
		Run: func(ctx context.Context) error {
			return router.RecordRoute(st, router.TunnelRoute{
				Hostname: hostname, TunnelID: tunnelID, Service: service,
			}, name)
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
		token, err := secrets.Get(router.TunnelSecretName)
		if err != nil {
			http.Error(w, "no tunnel API token on file; set up first", http.StatusUnprocessableEntity)
			return
		}
		accountID := r.URL.Query().Get("account_id")
		if accountID == "" {
			http.Error(w, "account_id query is required", http.StatusBadRequest)
			return
		}
		def := tunnelTeardownDefinition(st, token, accountID, id)
		runner := operation.NewRunner(st)
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
}

// tunnelTeardownDefinition builds teardown: delete the provider
// tunnel (missing reads as already gone), disable the connector and
// remove setup-written files (the static unit file stays: it belongs
// to provisioning, not setup), then forget recorded routes. Files go
// before rows so a failure never leaves a live connector with no
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

// deleteProviderTunnel deletes the provider tunnel; a tunnel the provider
// no longer knows reads as already gone.
func deleteProviderTunnel(ctx context.Context, apiToken, accountID, tunnelID string) error {
	client, err := router.NewTunnelClient(router.CloudflareAPIBase)
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
