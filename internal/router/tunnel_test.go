package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tunnelStub(t *testing.T) (*TunnelClient, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1000,"message":"bad token"}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/accounts":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/tunnels":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"tunnel-id","name":"homestead","token":"run-token"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/accounts/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/tunnels/tunnel-id":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"tunnel-id","name":"homestead","status":"healthy","connections":[{"colo_name":"atl12","opened_at":"2026-10-11T15:24:52Z"}]}}`))
		case r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1000,"message":"not found"}]}`))
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewTunnelClient(server.URL)
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	return client, server.Close
}

func TestTunnelClientFlow(t *testing.T) {
	client, _ := tunnelStub(t)
	ctx := context.Background()
	const account = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	tunnel, err := client.CreateTunnel(ctx, "test-token", account, "homestead")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if tunnel.ID != "tunnel-id" || tunnel.Name != "homestead" || tunnel.Token != "run-token" {
		t.Fatalf("tunnel = %+v", tunnel)
	}
	status, err := client.GetTunnel(ctx, "test-token", account, "tunnel-id")
	if err != nil {
		t.Fatalf("GetTunnel: %v", err)
	}
	if status.Status != "healthy" || len(status.Connections) != 1 || status.Connections[0].Colo != "atl12" {
		t.Fatalf("status = %+v", status)
	}
	if err := client.DeleteTunnel(ctx, "test-token", account, "tunnel-id"); err != nil {
		t.Fatalf("DeleteTunnel: %v", err)
	}
	accounts, err := client.ListAccounts(ctx, "test-token")
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 1 || accounts[0] != account {
		t.Fatalf("accounts = %+v", accounts)
	}
	// Refusals surface with codes, never empty results.
	if _, err := client.CreateTunnel(ctx, "bad-token", account, "homestead"); err == nil {
		t.Fatal("bad token accepted")
	}
}

func TestTunnelClientRejects(t *testing.T) {
	client, _ := tunnelStub(t)
	ctx := context.Background()
	for _, base := range []string{"", "ftp://x", "https://"} {
		if _, err := NewTunnelClient(base); err == nil {
			t.Fatalf("base %q accepted", base)
		}
	}
	for _, account := range []string{"", "short", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"} {
		if _, err := client.CreateTunnel(ctx, "test-token", account, "homestead"); err == nil {
			t.Fatalf("account %q accepted", account)
		}
	}
	for _, name := range []string{"", "Upper", "-lead", "trail-", strings.Repeat("a", 65)} {
		if _, err := client.CreateTunnel(ctx, "test-token", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", name); err == nil {
			t.Fatalf("name %q accepted", name)
		}
	}
	if _, err := client.GetTunnel(ctx, "", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "tunnel-id"); err == nil {
		t.Fatal("empty token accepted")
	}
}

func TestTunnelRouteValidate(t *testing.T) {
	good := TunnelRoute{Hostname: "app.example.com", TunnelID: "tunnel-id", Service: "http://127.0.0.1:8080"}
	if err := good.Validate(); err != nil {
		t.Fatalf("good route: %v", err)
	}
	bad := []TunnelRoute{
		{},
		{Hostname: "UPPER.example.com", TunnelID: "t", Service: "http://127.0.0.1:8080"},
		{Hostname: "app.example.com.", TunnelID: "t", Service: "http://127.0.0.1:8080"},
		{Hostname: "app.example.com", Service: "http://127.0.0.1:8080"},
		{Hostname: "app.example.com", TunnelID: "t", Service: "gopher://x"},
		{Hostname: "app.example.com", TunnelID: "t", Service: "http://"},
	}
	for i, route := range bad {
		if err := route.Validate(); err == nil {
			t.Fatalf("route #%d accepted", i)
		}
	}
}

func TestPreferredProvider(t *testing.T) {
	if got := PreferredProvider(nil); got != "tunnel" {
		t.Fatalf("silent router = %q, want tunnel", got)
	}
	if got := PreferredProvider([]MappingProtocol{ProtocolPCP, ProtocolUPnP}); got != "upnp" {
		t.Fatalf("got %q, want upnp first", got)
	}
	if got := PreferredProvider([]MappingProtocol{ProtocolNATPMP}); got != "nat-pmp" {
		t.Fatalf("got %q", got)
	}
	if got := PreferredProvider([]MappingProtocol{ProtocolPCP}); got != "pcp" {
		t.Fatalf("got %q", got)
	}
	if got := PreferredProvider([]MappingProtocol{"unknown"}); got != "manual" {
		t.Fatalf("got %q, want manual", got)
	}
}

func TestRenderConnector(t *testing.T) {
	config := RenderConnectorConfig("tunnel-id", "app.example.com", "http://127.0.0.1:8080")
	for _, want := range []string{"tunnel: tunnel-id", "hostname: app.example.com",
		"service: http://127.0.0.1:8080", "http_status:404"} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
}

func TestRenderConnectorIngress(t *testing.T) {
	got := RenderConnectorIngress("tid", []Ingress{
		{Hostname: "homestead.example.com", Service: "http://127.0.0.1:8480"},
		{Hostname: "auth.example.com", Service: "http://127.0.0.1:8480"},
	})
	want := "tunnel: tid\ningress:\n" +
		"  - hostname: homestead.example.com\n    service: http://127.0.0.1:8480\n" +
		"  - hostname: auth.example.com\n    service: http://127.0.0.1:8480\n" +
		"  - service: http_status:404\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
