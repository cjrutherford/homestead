package overlay

import (
	"net/netip"
	"strings"
	"testing"
)

func mustPrefix(t *testing.T, text string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		t.Fatalf("prefix %q: %v", text, err)
	}
	return prefix
}

func mustAddr(t *testing.T, text string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(text)
	if err != nil {
		t.Fatalf("addr %q: %v", text, err)
	}
	return addr
}

func validConfig(t *testing.T) Config {
	return Config{
		Provider: ProviderTailscale,
		Hostname: "homestead",
		AdvertiseRoutes: []SubnetRoute{
			{Destination: mustPrefix(t, "192.168.1.0/24"), Enabled: true},
			{Destination: mustPrefix(t, "fd00::/64")},
		},
		AcceptRoutes: true,
		DNS: DNSPreferences{
			Enabled:      true,
			SearchDomain: "tail.example",
			Nameservers:  []netip.Addr{mustAddr(t, "100.100.100.100")},
		},
	}
}

func TestConfigValid(t *testing.T) {
	if err := validConfig(t).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	off := Config{Provider: ProviderNone}
	if err := off.Validate(); err != nil {
		t.Fatalf("none Validate: %v", err)
	}
	headscale := validConfig(t)
	headscale.Provider = ProviderHeadscale
	headscale.Hostname = "appliance-01"
	if err := headscale.Validate(); err != nil {
		t.Fatalf("headscale Validate: %v", err)
	}
}

func TestConfigInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"bad provider", func(c *Config) { c.Provider = "zerotier" }, "provider must be"},
		{"stale hostname", func(c *Config) { c.Provider = ProviderNone; c.Hostname = "x" }, "takes no settings"},
		{"stale dns", func(c *Config) {
			c.Provider = ProviderNone
			c.DNS = DNSPreferences{Enabled: true}
		}, "takes no settings"},
		{"bad hostname", func(c *Config) { c.Hostname = "home stead" }, "invalid hostname"},
		{"empty hostname", func(c *Config) { c.Hostname = "" }, "1-63"},
		{"default route", func(c *Config) {
			c.AdvertiseRoutes = []SubnetRoute{{Destination: mustPrefixForTest("0.0.0.0/0"), Enabled: true}}
		}, "exit-node"},
		{"loopback route", func(c *Config) {
			c.AdvertiseRoutes = []SubnetRoute{{Destination: mustPrefixForTest("127.0.0.0/8")}}
		}, "routable"},
		{"dup routes", func(c *Config) {
			c.AdvertiseRoutes = append(c.AdvertiseRoutes, c.AdvertiseRoutes[0])
		}, "duplicate route"},
		{"bad search", func(c *Config) { c.DNS.SearchDomain = "BAD" }, "lowercase"},
		{"bad nameserver", func(c *Config) {
			c.DNS.Nameservers = []netip.Addr{mustAddrForTest("::")}
		}, "usable IP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig(t)
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatal("accepted invalid config")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func mustPrefixForTest(text string) netip.Prefix {
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		panic(err)
	}
	return prefix
}

func mustAddrForTest(text string) netip.Addr {
	addr, err := netip.ParseAddr(text)
	if err != nil {
		panic(err)
	}
	return addr
}

func TestNetworkValid(t *testing.T) {
	network := Network{
		Online:    true,
		Addresses: []netip.Addr{mustAddr(t, "100.64.0.1")},
		Nodes: []Node{
			{Name: "laptop", Addresses: []netip.Addr{mustAddr(t, "100.64.0.2")},
				Online: true, Offers: []netip.Prefix{mustPrefix(t, "192.168.7.0/24")}},
		},
	}
	if err := network.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	lonely := Network{Online: true}
	if err := lonely.Validate(); err != nil {
		t.Fatalf("lonely Validate: %v", err)
	}
	badNode := network
	badNode.Nodes = append(badNode.Nodes, badNode.Nodes[0])
	if err := badNode.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate node") {
		t.Fatalf("err = %v", err)
	}
	badAddr := network
	badAddr.Addresses = []netip.Addr{mustAddrForTest("::")}
	if err := badAddr.Validate(); err == nil {
		t.Fatal("accepted unspecified address")
	}
}

func TestProviderValues(t *testing.T) {
	for _, p := range []Provider{ProviderNone, ProviderHeadscale, ProviderTailscale} {
		if !p.Valid() {
			t.Fatalf("%q not valid", p)
		}
	}
	if (Provider("nebula")).Valid() {
		t.Fatal("nebula valid")
	}
}
