package catalog

import (
	"testing"

	"homestead/internal/exposure"
)

// adr061Facts is the fact table of ADR-061 (checked 2026-11-08): package
// to its public form and, for host packages, the reason. It is a fixture:
// the manifests must match it, so neither can drift alone. S472 to S477
// change a row together with the package that moves.
var adr061Facts = func() map[string][2]string {
	facts := map[string][2]string{
		"authentik": {PublishPath, ""}, "nextcloud": {PublishPath, ""}, "jellyfin": {PublishPath, ""},
		"immich": {PublishHost, ReasonOwnNameADR044}, "gitea": {PublishHost, ReasonSingleOrigin},
		"actual-budget": {PublishHost, ReasonSingleOrigin}, "mealie": {PublishHost, ReasonSingleOrigin},
		"calibre-web-automated": {PublishHost, ReasonSingleOrigin}, "vaultwarden": {PublishHost, ReasonOwnerChoice},
		"memos": {PublishHost, ReasonNoSubpath}, "bookstack": {PublishHost, ReasonNoSubpath},
		"vikunja": {PublishHost, ReasonNoSubpath}, "uptime-kuma": {PublishNever, ""},
		// Subpath candidates: host until their own slice verifies them.
		"navidrome": {PublishPath, ""}, "audiobookshelf": {PublishHost, ReasonNoSubpath},
		"komga": {PublishPath, ""}, "paperless-ngx": {PublishPath, ""},
		"linkding": {PublishHost, ReasonNoSubpath}, "freshrss": {PublishHost, ReasonNoSubpath},
	}
	for _, name := range []string{"api-client", "circuits", "contacts", "expense", "inventory", "notes", "parametric",
		"projects", "records", "solitaire", "sudoku", "tasks", "three-d", "time-tracking"} {
		facts[name] = [2]string{PublishPath, ""}
	}
	for _, name := range []string{"cadcraft", "deckcraft", "designcraft", "effectcraft", "figcraft", "filmcraft",
		"gridcraft", "lightcraft", "pdfcraft", "photocraft", "soundcraft", "vectorcraft", "wordcraft"} {
		facts[name] = [2]string{PublishNever, ""}
	}
	return facts
}()

func TestPublishMatchesADR061FactTable(t *testing.T) {
	cat := repoCatalog(t)
	var manifests []Manifest
	for _, name := range cat.Names() {
		m, _ := cat.Get(name)
		manifests = append(manifests, m)
	}
	if len(manifests) != 46 || len(adr061Facts) != 46 {
		t.Fatalf("%d manifests, %d facts; want 46 each", len(manifests), len(adr061Facts))
	}
	for _, m := range manifests {
		want, ok := adr061Facts[m.Name]
		if !ok {
			t.Errorf("%s: not in the fact table", m.Name)
			continue
		}
		if m.Publish != want[0] || m.PublishReason != want[1] {
			t.Errorf("%s: publish %q/%q, table %q/%q", m.Name, m.Publish, m.PublishReason, want[0], want[1])
		}
		if m.DerivedPublish() != m.Publish {
			t.Errorf("%s: declared %q, derived %q", m.Name, m.Publish, m.DerivedPublish())
		}
	}
}

func TestPublishValidation(t *testing.T) {
	cat := repoCatalog(t)
	get := func(name string) Manifest {
		m, err := cat.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	cases := map[string]struct {
		base   string
		mutate func(*Manifest)
	}{
		"unknown form":              {"nextcloud", func(m *Manifest) { m.Publish = "both" }},
		"path on a host package":    {"memos", func(m *Manifest) { m.Publish, m.PublishReason = PublishPath, "" }},
		"host on a path package":    {"nextcloud", func(m *Manifest) { m.Publish, m.PublishReason = PublishHost, ReasonNoSubpath }},
		"host without reason":       {"memos", func(m *Manifest) { m.PublishReason = "" }},
		"unknown reason":            {"memos", func(m *Manifest) { m.PublishReason = "because" }},
		"reason on path":            {"nextcloud", func(m *Manifest) { m.PublishReason = ReasonNoSubpath }},
		"single origin as never":    {"gitea", func(m *Manifest) { m.Publish, m.PublishReason = PublishNever, "" }},
		"single origin wrong cause": {"gitea", func(m *Manifest) { m.PublishReason = ReasonNoSubpath }},
		"reason without flag":       {"memos", func(m *Manifest) { m.PublishReason = ReasonSingleOrigin }},
		"allowwider false not never": {"memos", func(m *Manifest) {
			m.Exposure.Endpoints = append([]exposure.EndpointExposure(nil), m.Exposure.Endpoints...)
			m.Exposure.Endpoints[0].AllowWider = false
		}},
		"never with allowwider": {"memos", func(m *Manifest) { m.Publish, m.PublishReason = PublishNever, "" }},
	}
	for name, c := range cases {
		m := get(c.base)
		c.mutate(&m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s: validated", name)
		}
	}
	for _, name := range []string{"nextcloud", "memos", "gitea", "cadcraft", "uptime-kuma"} {
		m := get(name)
		if err := m.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	m := get("memos")
	m.Publish, m.PublishReason = "", ""
	if m.Validate() != nil || m.PublishForm() != PublishHost {
		t.Errorf("undeclared publish should derive host")
	}
}
