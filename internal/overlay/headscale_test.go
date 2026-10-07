package overlay

import (
	"context"
	"strings"
	"testing"
)

func TestHeadscaleServerValidation(t *testing.T) {
	for _, raw := range []string{
		"https://headscale.example.com",
		"https://headscale.example.com:8443/prefix",
		"http://192.168.1.5:8080",
	} {
		if err := (HeadscaleServer{URL: raw}).Validate(); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{"", "tailscale.com", "ftp://x/", "https://"} {
		if err := (HeadscaleServer{URL: raw}).Validate(); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if HeadscalePreauthSecret == "" || HeadscaleAPIKeySecret == "" ||
		HeadscalePreauthSecret == HeadscaleAPIKeySecret {
		t.Fatal("secret names indistinct")
	}
}

func TestHeadscaleUp(t *testing.T) {
	calls := stubTailscale(t, func(args []string) (string, error) {
		return "", nil
	})
	server := HeadscaleServer{URL: "https://headscale.example.com/"}
	def, err := HeadscaleUp(server, UpOptions{Hostname: "homestead", AuthKey: "preauth"})
	if err != nil {
		t.Fatalf("HeadscaleUp: %v", err)
	}
	if def.Type != "tailscale.up" {
		t.Fatalf("def = %+v", def)
	}
	if err := def.Steps[0].Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	joined := strings.Join(*calls, " ")
	if !strings.Contains(joined, "--login-server=https://headscale.example.com") {
		t.Fatalf("calls = %v", *calls)
	}
	if strings.Contains(joined, "//") && strings.Contains(joined, "example.com//") {
		t.Fatalf("trailing slash doubled: %v", *calls)
	}
	if _, err := HeadscaleUp(HeadscaleServer{URL: "bogus"}, UpOptions{Hostname: "ok"}); err == nil {
		t.Fatal("enrolled against bad server")
	}
	if _, err := UpDefinition(UpOptions{Hostname: "ok", LoginServer: "bogus"}); err == nil {
		t.Fatal("accepted bad login server")
	}
}

func TestHeadscaleStatusReusesMapping(t *testing.T) {
	stubTailscale(t, func(args []string) (string, error) {
		return `{"Version": "1.80.0", "BackendState": "Running",
			"Self": {"HostName": "homestead", "DNSName": "homestead.users.hs.example.",
				"TailscaleIPs": ["100.64.0.1"]},
			"Peer": {"k": {"HostName": "laptop", "TailscaleIPs": ["100.64.0.2"],
				"AllowedIPs": ["100.64.0.2/32"], "Online": true}}}`, nil
	})
	network, err := HeadscaleStatus(context.Background())
	if err != nil {
		t.Fatalf("HeadscaleStatus: %v", err)
	}
	if !network.Online || len(network.Nodes) != 1 || network.Nodes[0].Name != "laptop" {
		t.Fatalf("network = %+v", network)
	}
	if err := network.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
