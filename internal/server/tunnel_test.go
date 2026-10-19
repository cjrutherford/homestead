package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/router"
	"homestead/internal/secret"
	"homestead/internal/store"
)

func tunnelTestServer(t *testing.T) (*Server, *store.Store, *secret.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	secrets, err := secret.New(s, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	// Connector writes stay in temp dirs: no test touches the host.
	tunnelConnectorDir = filepath.Join(dir, "tunnel")
	t.Cleanup(func() {
		tunnelConnectorDir = "/etc/homestead/tunnel"
	})
	srv := New("127.0.0.1:0")
	srv.MountTunnel(s, secrets)
	return srv, s, secrets
}

func TestTunnelStatus(t *testing.T) {
	srv, _, _ := tunnelTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/tunnel/status", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var status struct {
		Preferred string         `json:"preferred"`
		Protocols []string       `json:"protocols"`
		Tunnels   []any          `json:"tunnels"`
		Routes    []any          `json:"routes"`
		Connector map[string]any `json:"connector"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.Preferred == "" || status.Protocols == nil || status.Tunnels == nil || status.Routes == nil {
		t.Fatalf("status = %+v", status)
	}
	// Nothing recorded reads as empty, and no credential means no
	// tunnel health — never an error, never a leak.
	if len(status.Tunnels) != 0 || len(status.Routes) != 0 {
		t.Fatalf("status = %+v", status)
	}
	if strings.Contains(recorder.Body.String(), "api_token") {
		t.Fatal("status leaks credential material")
	}
}

func TestTunnelSetup(t *testing.T) {
	srv, st, _ := tunnelTestServer(t)
	// Malformed bodies refuse before sealing anything.
	for _, body := range []any{
		nil,
		map[string]any{},
		map[string]any{"name": "Bad Name", "hostname": "app.example.com",
			"service": "http://127.0.0.1:8080", "api_token": "token", "account_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		map[string]any{"name": "homestead", "hostname": "not a host",
			"service": "http://127.0.0.1:8080", "api_token": "token", "account_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		map[string]any{"name": "homestead", "hostname": "app.example.com",
			"service": "gopher://x", "api_token": "token", "account_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		map[string]any{"name": "homestead", "hostname": "app.example.com",
			"service": "http://127.0.0.1:8080", "account_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		map[string]any{"name": "homestead", "hostname": "app.example.com",
			"service": "http://127.0.0.1:8080", "api_token": "token"},
	} {
		recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %+v: code = %d, want 400", body, recorder.Code)
		}
	}
	// A valid body seals the token and opens an operation. The op
	// fails async here (no provider, no privileges); acceptance is
	// the contract, execution belongs to live proof.
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", map[string]any{
		"name": "homestead", "hostname": "app.example.com", "service": "http://127.0.0.1:8080",
		"api_token": "sealed-token", "account_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var started struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if started.ID == 0 {
		t.Fatal("no operation id")
	}
	if strings.Contains(recorder.Body.String(), "sealed-token") {
		t.Fatal("setup echoes the token")
	}
	// A recorded hostname refuses re-setup before creating anything.
	if err := router.RecordRoute(st, router.TunnelRoute{
		Hostname: "taken.example.com", TunnelID: "tunnel-id", Service: "http://127.0.0.1:8080",
	}, "homestead"); err != nil {
		t.Fatalf("RecordRoute: %v", err)
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", map[string]any{
		"name": "homestead", "hostname": "taken.example.com", "service": "http://127.0.0.1:8080",
		"api_token": "sealed-token", "account_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, want 422", recorder.Code)
	}
}

func TestTunnelTeardown(t *testing.T) {
	srv, _, secrets := tunnelTestServer(t)
	// No credential on file refuses before running anything.
	recorder := serveRequest(t, srv, http.MethodDelete, "/api/v1/tunnel/tunnels/tunnel-id?account_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, want 422", recorder.Code)
	}
	if err := secrets.Set("router_tunnel_api_token", "sealed-token"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Missing account refuses explicitly rather than guessing tenancy.
	recorder = serveRequest(t, srv, http.MethodDelete, "/api/v1/tunnel/tunnels/tunnel-id", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", recorder.Code)
	}
	// Credential plus account opens the operation; provider failure
	// lands async, acceptance stays synchronous.
	recorder = serveRequest(t, srv, http.MethodDelete, "/api/v1/tunnel/tunnels/tunnel-id?account_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
}

// Teardown stops the connector before it deletes the provider tunnel: the
// provider refuses to delete a tunnel with live connections (KVM, S287).
func TestTunnelTeardownStopsConnectorFirst(t *testing.T) {
	var calls []string
	prev := tunnelSystemctl
	tunnelSystemctl = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { tunnelSystemctl = prev })
	def := tunnelTeardownDefinition(nil, "tok", "acct", "tunnel-id")
	var names []string
	for _, step := range def.Steps {
		names = append(names, step.Name)
	}
	if got := strings.Join(names, ","); got != "stop-connector,delete-tunnel,remove-connector,forget-routes" {
		t.Fatalf("steps = %s", got)
	}
	if err := def.Steps[0].Run(context.Background()); err != nil {
		t.Fatalf("stop-connector: %v", err)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "stop ") {
		t.Fatalf("systemctl calls = %v, want one stop", calls)
	}
}
