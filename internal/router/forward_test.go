package router

import (
	"net/netip"
	"strings"
	"testing"
)

func TestRequiredSet(t *testing.T) {
	web, err := RequiredForwards(PurposeWeb)
	if err != nil {
		t.Fatalf("web: %v", err)
	}
	if len(web) != 2 || web[0].Port != 80 || web[1].Port != 443 {
		t.Fatalf("web = %+v", web)
	}
	headscale, err := RequiredForwards(PurposeHeadscale)
	if err != nil {
		t.Fatalf("headscale: %v", err)
	}
	if len(headscale) != 1 || headscale[0].Port != 8080 {
		t.Fatalf("headscale = %+v", headscale)
	}
	if _, err := RequiredForwards("ftp"); err == nil {
		t.Fatal("served unknown purpose")
	}
	merged, err := RequiredSet(PurposeWeb, PurposeHeadscale, PurposeWeb)
	if err != nil {
		t.Fatalf("RequiredSet: %v", err)
	}
	if len(merged) != 3 || merged[0].Port != 80 || merged[1].Port != 443 || merged[2].Port != 8080 {
		t.Fatalf("merged = %+v", merged)
	}
	for _, forward := range merged {
		if err := forward.Validate(); err != nil {
			t.Fatalf("forward: %v", err)
		}
	}
}

func TestRenderInstructions(t *testing.T) {
	forwards, err := RequiredSet(PurposeWeb, PurposeHeadscale)
	if err != nil {
		t.Fatalf("RequiredSet: %v", err)
	}
	text, err := RenderInstructions(netip.MustParseAddr("192.168.1.10"), forwards)
	if err != nil {
		t.Fatalf("RenderInstructions: %v", err)
	}
	for _, want := range []string{
		"Port forwarding for Homestead at 192.168.1.10.",
		"web-http: external TCP 80 -> internal 192.168.1.10 80",
		"web-https: external TCP 443 -> internal 192.168.1.10 443",
		"headscale-control: external TCP 8080 -> internal 192.168.1.10 8080",
		"DHCP",
		"S133",
		"carrier-grade NAT",
		"management ports",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	for _, addr := range []string{"", "::", "127.0.0.1", "224.0.0.1"} {
		ip, _ := netip.ParseAddr(addr)
		if _, err := RenderInstructions(ip, forwards); err == nil {
			t.Fatalf("rendered for %q", addr)
		}
	}
	if _, err := RenderInstructions(netip.MustParseAddr("192.168.1.10"), nil); err == nil {
		t.Fatal("rendered empty set")
	}
	dup := append(append([]Forward{}, forwards...), forwards[0])
	if _, err := RenderInstructions(netip.MustParseAddr("192.168.1.10"), dup); err == nil {
		t.Fatal("rendered duplicates")
	}
	bad := []Forward{{Name: "Bad!", Protocol: ProtocolTCP, Port: 1}}
	if _, err := RenderInstructions(netip.MustParseAddr("192.168.1.10"), bad); err == nil {
		t.Fatal("rendered invalid forward")
	}
	udp := []Forward{{Name: "voip", Protocol: ProtocolUDP, Port: 3478}}
	text, err = RenderInstructions(netip.MustParseAddr("192.168.1.10"), udp)
	if err != nil {
		t.Fatalf("udp: %v", err)
	}
	if !strings.Contains(text, "external UDP 3478") {
		t.Fatalf("udp missing:\n%s", text)
	}
}
