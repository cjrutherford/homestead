package catalog

import (
	"strings"
	"testing"
)

func TestIntegrationLevelsAreDerived(t *testing.T) {
	cases := map[IntegrationKind]IntegrationLevel{
		IntegrationCompanion: LevelCoexist,
		IntegrationIdentity:  LevelIdentity,
		IntegrationLibrary:   LevelLibrary,
		IntegrationHandoff:   LevelHandoff,
	}
	for kind, want := range cases {
		if got := (Integration{Kind: kind}).Level(); got != want {
			t.Fatalf("%s level = %q, want %q", kind, got, want)
		}
	}
	if (Integration{Kind: "mystery"}).Level() != LevelCoexist {
		t.Fatal("an unknown kind must not resolve to a deeper level")
	}
	for _, kind := range []IntegrationKind{IntegrationIdentity, IntegrationLibrary, IntegrationHandoff, IntegrationCompanion} {
		if !kind.Valid() {
			t.Fatalf("%q must be valid", kind)
		}
	}
	if IntegrationKind("").Valid() {
		t.Fatal("empty kind must not be valid")
	}
}

func TestIntegrationValidate(t *testing.T) {
	cases := []struct {
		name string
		decl Integration
		want string
	}{
		{"unknown kind", Integration{Kind: "peer", Target: "jellyfin", Note: "note"}, "kind"},
		{"bad target", Integration{Kind: IntegrationCompanion, Target: "../etc", Note: "note"}, "target"},
		{"self", Integration{Kind: IntegrationCompanion, Target: "nextcloud", Note: "note"}, "itself"},
		{"blank note", Integration{Kind: IntegrationCompanion, Target: "jellyfin", Note: "  "}, "note"},
		{"long note", Integration{Kind: IntegrationCompanion, Target: "jellyfin", Note: strings.Repeat("x", integrationNoteMax+1)}, "note"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := test.decl.Validate("nextcloud")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
	good := Integration{Kind: IntegrationIdentity, Target: "authentik", Note: "Sign in through Authentik."}
	if err := good.Validate("nextcloud"); err != nil {
		t.Fatalf("valid declaration refused: %v", err)
	}
}

func TestManifestIntegrationGates(t *testing.T) {
	base, err := repoCatalog(t).Get("jellyfin")
	if err != nil {
		t.Fatal(err)
	}

	duplicate := base
	duplicate.Integrations = append(duplicate.Integrations,
		Integration{Kind: IntegrationIdentity, Target: "authentik", Note: "Again."})
	if err := duplicate.validateIntegrations(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate err = %v", err)
	}

	unsupported := base
	unsupported.SSO.Class = SSOUnsupported
	unsupported.SSO.Protocols = nil
	unsupported.SSO.OIDC = nil
	if err := unsupported.validateIntegrations(); err == nil || !strings.Contains(err.Error(), "SSO class") {
		t.Fatalf("unsupported SSO err = %v", err)
	}

	unstated := base
	unstated.Integrations = nil
	if err := unstated.validateIntegrations(); err == nil || !strings.Contains(err.Error(), "no identity provider") {
		t.Fatalf("unstated provider err = %v", err)
	}

	noBulk := base
	for i := range noBulk.Services {
		for j := range noBulk.Services[i].Data {
			noBulk.Services[i].Data[j].Class = "application"
		}
	}
	if err := noBulk.validateIntegrations(); err == nil || !strings.Contains(err.Error(), "bulk storage") {
		t.Fatalf("library without bulk err = %v", err)
	}
}

// cloneCatalog copies the entry map and each manifest's integration slice:
// gate cases mutate declarations and must not leak into one another.
func cloneCatalog(in Catalog) Catalog {
	out := Catalog{entries: make(map[string]Manifest, len(in.entries))}
	for name, manifest := range in.entries {
		manifest.Integrations = append([]Integration(nil), manifest.Integrations...)
		out.entries[name] = manifest
	}
	return out
}

func TestCatalogIntegrationGates(t *testing.T) {
	base := repoCatalog(t)

	dangling := cloneCatalog(base)
	jellyfin, err := dangling.Get("jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	jellyfin.Integrations[0].Target = "ghost"
	dangling.entries["jellyfin"] = jellyfin
	if err := dangling.validateIntegrations(); err == nil || !strings.Contains(err.Error(), "does not ship") {
		t.Fatalf("dangling target err = %v", err)
	}

	notProvider := cloneCatalog(base)
	jellyfin, err = notProvider.Get("jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	jellyfin.Integrations[0].Target = "nextcloud"
	notProvider.entries["jellyfin"] = jellyfin
	if err := notProvider.validateIntegrations(); err == nil || !strings.Contains(err.Error(), "not a registered provider") {
		t.Fatalf("non-provider identity err = %v", err)
	}

	noBulkTarget := cloneCatalog(base)
	jellyfin, err = noBulkTarget.Get("jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	for i := range jellyfin.Integrations {
		if jellyfin.Integrations[i].Kind == IntegrationLibrary {
			jellyfin.Integrations[i].Target = "authentik"
		}
	}
	noBulkTarget.entries["jellyfin"] = jellyfin
	if err := noBulkTarget.validateIntegrations(); err == nil || !strings.Contains(err.Error(), "bulk storage") {
		t.Fatalf("bulk-less library target err = %v", err)
	}
}

func TestLoadRunsIntegrationGates(t *testing.T) {
	dir := t.TempDir()
	entry := loaderFixture + `sso:
  class: optional
  protocols: [oidc]
integrations:
- kind: identity
  target: ghost
  note: Sign in through a provider that does not exist.
`
	writeEntry(t, dir, "jellyfin.yaml", entry)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "does not ship") {
		t.Fatalf("Load err = %v", err)
	}
}

func TestShippedIntegrations(t *testing.T) {
	cat := repoCatalog(t)
	want := map[string][]IntegrationKind{
		"nextcloud": {IntegrationIdentity, IntegrationLibrary},
		"jellyfin":  {IntegrationIdentity, IntegrationLibrary},
		"immich":    {IntegrationIdentity},
		"authentik": {},
	}
	for name, kinds := range want {
		manifest, err := cat.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(manifest.Integrations) != len(kinds) {
			t.Fatalf("%s declarations = %+v, want %v", name, manifest.Integrations, kinds)
		}
		for i, kind := range kinds {
			if manifest.Integrations[i].Kind != kind {
				t.Fatalf("%s declaration %d = %q, want %q", name, i, manifest.Integrations[i].Kind, kind)
			}
		}
	}
	authentik, err := cat.Get("authentik")
	if err != nil {
		t.Fatal(err)
	}
	if resolved := cat.IntegrationsFor(authentik); resolved == nil || len(resolved) != 0 {
		t.Fatalf("provider resolved = %+v, want an empty non-nil list", resolved)
	}
}

func TestIntegrationsForResolvesLevelsAndTitles(t *testing.T) {
	cat := repoCatalog(t)
	jellyfin, err := cat.Get("jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	resolved := cat.IntegrationsFor(jellyfin)
	if len(resolved) != 2 {
		t.Fatalf("resolved = %+v", resolved)
	}
	if resolved[0].Kind != IntegrationIdentity || resolved[0].Level != LevelIdentity ||
		resolved[0].Target != "authentik" || resolved[0].Title != "Authentik" {
		t.Fatalf("identity = %+v", resolved[0])
	}
	if resolved[1].Kind != IntegrationLibrary || resolved[1].Level != LevelLibrary ||
		resolved[1].Target != "nextcloud" || resolved[1].Title != "Nextcloud" {
		t.Fatalf("library = %+v", resolved[1])
	}
	if resolved[0].Note == "" {
		t.Fatal("notes carry through resolution")
	}
}

func TestHandoffNotesAreAdvisoryAndOnceEach(t *testing.T) {
	manifest := Manifest{
		Title: "Paperless",
		Integrations: []Integration{
			{Kind: IntegrationHandoff, Target: "nextcloud", Note: "Reads scans from Nextcloud."},
			{Kind: IntegrationHandoff, Target: "nextcloud", Note: "Duplicate target."},
			{Kind: IntegrationIdentity, Target: "authentik", Note: "Sign in."},
		},
	}
	notes := HandoffNotes(manifest, nil)
	if len(notes) != 1 || !strings.Contains(notes[0], "nextcloud") {
		t.Fatalf("notes = %v", notes)
	}
	if notes := HandoffNotes(manifest, func(string) bool { return true }); len(notes) != 0 {
		t.Fatalf("installed handoff still noted: %v", notes)
	}
}
