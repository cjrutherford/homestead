package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserRequirementValidation(t *testing.T) {
	base := func(m Manifest) Manifest { return m }
	cat := repoCatalog(t)
	m, err := cat.Get("filmcraft")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		browser []BrowserRequirement
		want    string
	}{
		{"unknown capability", []BrowserRequirement{{Capability: "telepathy", Reason: "x"}}, "unknown capability"},
		{"duplicate", []BrowserRequirement{{Capability: BrowserWebGPU, Reason: "x"}, {Capability: BrowserWebGPU, Reason: "y"}}, "duplicate"},
		{"missing reason", []BrowserRequirement{{Capability: BrowserWebGPU}}, "needs a reason"},
		{"whitespace reason", []BrowserRequirement{{Capability: BrowserWebGPU, Reason: " \t\n "}}, "needs a reason"},
		{"long reason by runes", []BrowserRequirement{{Capability: BrowserWebGPU, Reason: strings.Repeat("é", 301)}}, "needs a reason"},
		{"long reason", []BrowserRequirement{{Capability: BrowserWebGPU, Reason: strings.Repeat("x", 301)}}, "needs a reason"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bad := base(m)
			bad.Browser = c.browser
			if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	ok := base(m)
	ok.Browser = nil
	if err := ok.Validate(); err != nil {
		t.Fatalf("no browser requirements must stay valid: %v", err)
	}
}

// The media (S252) and design and CAD (S253) apps carry advisories, and planning surfaces them
// without blocking: the plan still succeeds and says why it cannot check.
func TestCraftPackagesCarryBrowserAdvisories(t *testing.T) {
	cat := repoCatalog(t)
	want := map[string][]BrowserCapability{
		"filmcraft":   {BrowserWebCodecs, BrowserWebGPU, BrowserWebAudio, BrowserAudioOutput, BrowserSecureContext},
		"effectcraft": {BrowserWebCodecs, BrowserWebGPU, BrowserWebAudio, BrowserAudioOutput, BrowserSecureContext},
		"soundcraft":  {BrowserWebGL2, BrowserWebAudio, BrowserAudioOutput, BrowserSecureContext},
		"designcraft": {BrowserWebGPU, BrowserWebGL2, BrowserSecureContext, BrowserClipboard, BrowserStorage},
		"cadcraft":    {BrowserWebGPU, BrowserWebGL2, BrowserSecureContext, BrowserClipboard, BrowserStorage},
	}
	for name, caps := range want {
		m, err := cat.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		have := map[BrowserCapability]bool{}
		for _, b := range m.Browser {
			have[b.Capability] = true
		}
		for _, c := range caps {
			if !have[c] {
				t.Errorf("%s: missing browser capability %s", name, c)
			}
		}
		plan, err := PlanInstall(InstallRequest{
			Manifest: m, Host: HostFacts{Arch: "amd64", MemoryMB: 1024, CPUCount: 2}, Domain: "lan",
			ContainerPorts: map[string]int{name: craftContainerPort},
		})
		if err != nil {
			t.Fatalf("%s: advisories must never block planning: %v", name, err)
		}
		if len(plan.BrowserAdvisories) != len(m.Browser) {
			t.Errorf("%s: plan advisories = %d, manifest = %d", name, len(plan.BrowserAdvisories), len(m.Browser))
		}
		if !strings.Contains(strings.Join(plan.Notes, "\n"), "cannot see your browser") {
			t.Errorf("%s: plan must say the appliance cannot see browser capabilities: %v", name, plan.Notes)
		}
	}
	// A package without requirements adds neither advisories nor the note.
	m, _ := cat.Get("wordcraft")
	plan, err := PlanInstall(InstallRequest{
		Manifest: m, Host: HostFacts{Arch: "amd64", MemoryMB: 1024, CPUCount: 2}, Domain: "lan",
		ContainerPorts: map[string]int{"wordcraft": craftContainerPort},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.BrowserAdvisories) != 0 || strings.Contains(strings.Join(plan.Notes, "\n"), "browser") {
		t.Fatalf("wordcraft plan = %+v", plan)
	}
}

func TestBrowserReasonLimitCountsRunes(t *testing.T) {
	// 300 two-byte runes is 600 bytes but within the 300-character limit.
	if err := validateBrowser([]BrowserRequirement{{Capability: BrowserWebGPU, Reason: strings.Repeat("é", 300)}}); err != nil {
		t.Fatalf("300 characters must be accepted: %v", err)
	}
}

func TestBrowserRequirementJSONCarriesLabel(t *testing.T) {
	raw, err := json.Marshal(BrowserRequirement{Capability: BrowserWebGL2, Reason: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != `{"Capability":"webgl2","Label":"Graphics (WebGL2)","Reason":"r"}` {
		t.Fatalf("json = %s", got)
	}
	for _, c := range []BrowserCapability{BrowserWebCodecs, BrowserWebGPU, BrowserWebGL2, BrowserWebAudio, BrowserAudioOutput, BrowserSecureContext, BrowserClipboard, BrowserStorage} {
		if c.Label() == string(c) {
			t.Fatalf("capability %q has no label", c)
		}
	}
}
