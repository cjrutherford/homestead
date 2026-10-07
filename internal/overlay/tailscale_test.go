package overlay

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
	"homestead/internal/store"
)

func stubTailscale(t *testing.T, handler func(args []string) (string, error)) *[]string {
	t.Helper()
	var calls []string
	old := tailscaleExec
	tailscaleExec = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, "tailscale "+strings.Join(args, " "))
		return handler(args)
	}
	t.Cleanup(func() { tailscaleExec = old })
	return &calls
}

const tailscaleStatusFixture = `{
	"Version": "1.80.0",
	"BackendState": "Running",
	"Self": {"HostName": "homestead", "DNSName": "homestead.tail.example.",
		"TailscaleIPs": ["100.64.0.1", "fd7a::1"]},
	"Peer": {
		"node1": {"HostName": "laptop", "TailscaleIPs": ["100.64.0.2"],
			"AllowedIPs": ["100.64.0.2/32", "192.168.7.0/24", "0.0.0.0/0"], "Online": true},
		"node2": {"HostName": "phone", "TailscaleIPs": ["100.64.0.3"],
			"AllowedIPs": ["100.64.0.3/32"], "Online": false},
		"node3": {"HostName": "Bad Name!", "TailscaleIPs": ["100.64.0.4"],
			"AllowedIPs": ["100.64.0.4/32"]}
	}
}`

func TestStatusMaps(t *testing.T) {
	stubTailscale(t, func(args []string) (string, error) {
		if len(args) == 1 && args[0] == "version" {
			return "1.80.0", nil
		}
		return tailscaleStatusFixture, nil
	})
	if !Present(context.Background()) {
		t.Fatal("not present")
	}
	network, err := Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !network.Online || len(network.Addresses) != 2 {
		t.Fatalf("network = %+v", network)
	}
	if err := network.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// Invalid peer skipped; survivors sorted.
	if len(network.Nodes) != 2 || network.Nodes[0].Name != "laptop" || network.Nodes[1].Name != "phone" {
		t.Fatalf("nodes = %+v", network.Nodes)
	}
	laptop := network.Nodes[0]
	if !laptop.Online || len(laptop.Addresses) != 1 {
		t.Fatalf("laptop = %+v", laptop)
	}
	// Own /32 excluded from offers; subnet and exit offers kept.
	var offers []string
	for _, offer := range laptop.Offers {
		offers = append(offers, offer.String())
	}
	if len(offers) != 2 || offers[0] != "192.168.7.0/24" || offers[1] != "0.0.0.0/0" {
		t.Fatalf("offers = %v", offers)
	}
	if network.Nodes[1].Online {
		t.Fatal("phone online")
	}
	// Stopped backend reports offline without erroring.
	stubTailscale(t, func(args []string) (string, error) {
		return `{"BackendState": "Stopped", "Self": {}, "Peer": {}}`, nil
	})
	down, err := Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if down.Online {
		t.Fatal("stopped online")
	}
}

func TestStatusAbsent(t *testing.T) {
	stubTailscale(t, func(args []string) (string, error) {
		return "", ErrTailscaleAbsent
	})
	if Present(context.Background()) {
		t.Fatal("present without binary")
	}
	if _, err := Status(context.Background()); !errors.Is(err, ErrTailscaleAbsent) {
		t.Fatalf("err = %v", err)
	}
	stubTailscale(t, func(args []string) (string, error) {
		return "not json", nil
	})
	if _, err := Status(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "parse status") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpDefinition(t *testing.T) {
	calls := stubTailscale(t, func(args []string) (string, error) {
		return "", nil
	})
	options := UpOptions{
		AuthKey:  "secret",
		Hostname: "homestead",
		Routes: []SubnetRoute{
			{Destination: netip.MustParsePrefix("192.168.1.0/24"), Enabled: true},
			{Destination: netip.MustParsePrefix("192.168.2.0/24")},
		},
		AcceptRoutes: true,
		AcceptDNS:    true,
	}
	def, err := UpDefinition(options)
	if err != nil {
		t.Fatalf("UpDefinition: %v", err)
	}
	if def.Type != "tailscale.up" || len(def.Steps) != 1 {
		t.Fatalf("def = %+v", def)
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	runner := operation.NewRunner(s)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q", done.Status)
	}
	joined := strings.Join(*calls, " ")
	for _, want := range []string{"--authkey=secret", "--hostname=homestead",
		"--advertise-routes=192.168.1.0/24", "--accept-routes", "--accept-dns"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("calls = %v, want %q", *calls, want)
		}
	}
	if strings.Contains(joined, "192.168.2.0/24") {
		t.Fatalf("disabled route advertised: %v", *calls)
	}
	// Re-apply without key omits authkey.
	calls2 := stubTailscale(t, func(args []string) (string, error) { return "", nil })
	plain, err := UpDefinition(UpOptions{Hostname: "homestead"})
	if err != nil {
		t.Fatalf("UpDefinition: %v", err)
	}
	op, err = runner.Start(context.Background(), plain, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if done, err := runner.Wait(context.Background(), op.ID); err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("done = %+v %v", done, err)
	}
	if strings.Contains(strings.Join(*calls2, " "), "--authkey=") {
		t.Fatalf("authkey without key: %v", *calls2)
	}
	// Logout converges through the runner too.
	calls3 := stubTailscale(t, func(args []string) (string, error) { return "", nil })
	def = LogoutDefinition()
	if def.Type != "tailscale.logout" {
		t.Fatalf("def = %+v", def)
	}
	op, err = runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if done, err := runner.Wait(context.Background(), op.ID); err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("done = %+v %v", done, err)
	}
	if !strings.Contains(strings.Join(*calls3, " "), "tailscale logout") {
		t.Fatalf("calls = %v", *calls3)
	}
	// Invalid intent refuses before running.
	if _, err := UpDefinition(UpOptions{Hostname: "Bad Name"}); err == nil {
		t.Fatal("accepted bad hostname")
	}
	badRoutes := UpOptions{Hostname: "ok", Routes: []SubnetRoute{{Destination: netip.MustParsePrefix("0.0.0.0/0"), Enabled: true}}}
	if _, err := UpDefinition(badRoutes); err == nil {
		t.Fatal("accepted exit route")
	}
}

func TestDemoProfile(t *testing.T) {
	config := DemoConfig()
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	network := DemoNetwork()
	if err := network.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !strings.Contains(config.Hostname, "demo") || !strings.Contains(network.Nodes[0].Name, "demo") {
		t.Fatalf("not labeled demo: %+v %+v", config, network)
	}
}
