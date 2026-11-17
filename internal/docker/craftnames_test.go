package docker

import (
	"strings"
	"testing"
)

// S382: the local names the appliance resolver and the mDNS responder
// answer are derived from the route table (ADR-039), so the thirteen
// craft apps, served by path (S320), add no names of their own and a
// public route never leaks into the local answers.
var craftApps = []string{"wordcraft", "gridcraft", "pdfcraft", "deckcraft", "vectorcraft", "photocraft",
	"lightcraft", "figcraft", "filmcraft", "effectcraft", "soundcraft", "designcraft", "cadcraft"}

func craftSet(apps []string, exposure Exposure) RouteSet {
	set := RouteSet{Routes: []Route{{Host: "homestead.lan", Dashboard: true, Exposure: ExposureLocal, TLS: TLSInternal,
		Aliases: []Hostname{"homestead.local"}}}}
	for _, a := range apps {
		set.Routes = append(set.Routes, Route{Host: Hostname(a + ".lan"), CraftApp: a,
			Target: Target{Host: a, Port: 8080}, Exposure: exposure, TLS: TLSInternal,
			Path: "/apps/" + a + "/", StripPrefix: true})
	}
	return set
}

func TestCraftPathAppsAddNoLocalNames(t *testing.T) {
	set := craftSet(craftApps, ExposureLocal)
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := strings.Join(set.LANNames(), ","); got != "homestead.lan" {
		t.Fatalf("LANNames = %q, want only homestead.lan", got)
	}
	if got := set.MDNSLabels(); len(got) != 0 {
		t.Fatalf("MDNSLabels = %v, craft path apps announce no names of their own", got)
	}
}

func TestCraftRemovalDropsLocalNames(t *testing.T) {
	// A craft app served on a name of its own answers on it; removing the
	// route removes the answer, and the dashboard's name stays.
	host := RouteSet{Routes: []Route{
		{Host: "homestead.lan", Dashboard: true, Exposure: ExposureLocal, TLS: TLSInternal},
		{Host: "wordcraft.lan", Target: Target{Host: "wordcraft", Port: 8080}, Exposure: ExposureLocal, TLS: TLSInternal},
	}}
	if got := strings.Join(host.LANNames(), ","); got != "homestead.lan,wordcraft.lan" {
		t.Fatalf("LANNames = %q", got)
	}
	host.Routes = host.Routes[:1]
	if got := strings.Join(host.LANNames(), ","); got != "homestead.lan" {
		t.Fatalf("after removal LANNames = %q", got)
	}
	if got := host.MDNSLabels(); len(got) != 0 {
		t.Fatalf("after removal MDNSLabels = %v", got)
	}
}

func TestPublicRoutesNeverEnterLocalNames(t *testing.T) {
	host := RouteSet{Routes: []Route{
		{Host: "homestead.lan", Dashboard: true, Exposure: ExposureLocal, TLS: TLSInternal},
		{Host: "figcraft.lan", Target: Target{Host: "figcraft", Port: 8080}, Exposure: ExposurePublic, TLS: TLSInternal},
	}}
	if got := strings.Join(host.LANNames(), ","); got != "homestead.lan" {
		t.Fatalf("LANNames = %q, a public route must not be answered locally", got)
	}
	for _, l := range host.MDNSLabels() {
		if l == "figcraft" {
			t.Fatalf("a public route is announced over mDNS: %v", host.MDNSLabels())
		}
	}
}
