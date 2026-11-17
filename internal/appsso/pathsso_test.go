package appsso

import (
	"fmt"
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/sso"
)

// ADR-044 §4: with Authentik at <server>/apps/auth/ and an app at
// <server>/apps/<name>/, the issuer and every redirect URI sit under the
// paths, on each appliance name and the public name once published, and
// the back channel resolves the appliance's own host.
func TestPathAppsSignInUnderTheirPaths(t *testing.T) {
	dash := docker.Route{Host: "homestead.lan", Dashboard: true, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal,
		Aliases: []docker.Hostname{"homestead.local"}, Tunnel: []docker.Hostname{"homestead.example.com"}}
	spec := dashboard
	spec.AuthentikURL = "https://homestead.lan/apps/auth"
	for _, c := range []struct {
		name, service, path string
		published           bool
		want                []string
	}{
		{"nextcloud", "nextcloud", "/apps/nextcloud/", true, []string{
			"https://homestead.lan/apps/nextcloud/apps/user_oidc/code",
			"https://homestead.local/apps/nextcloud/index.php/apps/user_oidc/code",
			"https://homestead.example.com/apps/nextcloud/apps/user_oidc/code"}},
		{"jellyfin", "jellyfin", "/apps/jellyfin/", false, []string{
			"https://homestead.lan/apps/jellyfin/sso/OID/redirect/" + ProviderKey,
			"https://homestead.local/apps/jellyfin/sso/OID/redirect/" + ProviderKey}},
	} {
		m := manifest(t, c.name)
		app := route(c.name+".lan", c.service)
		app.Path, app.Published = c.path, c.published
		table := []docker.Route{dash, app, route("authentik.lan", "server")}
		in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: spec, DashboardOK: true,
			Routes: docker.ServedRoutes(table, AppRoutes(m, table))}
		p, err := BuildPlan(in, true)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.Issuer != "https://homestead.lan/apps/auth/application/o/"+ClientID(c.name)+"/" || p.Issuer != sso.IssuerFor(spec.AuthentikURL, ClientID(c.name)) {
			t.Fatalf("%s: issuer = %s", c.name, p.Issuer)
		}
		got := strings.Join(p.RedirectURIs, " ")
		for _, want := range c.want {
			if !strings.Contains(got, want) {
				t.Fatalf("%s: redirect URIs %v lack %s", c.name, p.RedirectURIs, want)
			}
		}
		if !c.published && strings.Contains(got, "example.com") {
			t.Fatalf("%s: unpublished app has a public redirect: %v", c.name, p.RedirectURIs)
		}
		if strings.Contains(got, c.name+".lan") {
			t.Fatalf("%s: the record key leaked into the redirects: %v", c.name, p.RedirectURIs)
		}
		if !strings.HasPrefix(p.SignInURL, "https://homestead.lan"+strings.TrimSuffix(c.path, "/")+"/") {
			t.Fatalf("%s: sign-in URL = %s", c.name, p.SignInURL)
		}
		adapter, err := AdapterFor(*m.SSO.OIDC)
		if err != nil {
			t.Fatal(err)
		}
		if w := wiring(m, adapter, p.Issuer, Settings{}); fmt.Sprint(w.ExtraHosts) != "[homestead.lan:host-gateway]" {
			t.Fatalf("%s: back channel = %v", c.name, w.ExtraHosts)
		}
	}
}

// ADR-047 §1-2: a published app signs in at Authentik's public address
// once the edge answers it on this box, and its host entry follows; until
// then the plan says why. An unpublished app served by path signs in at
// the public address too (S408).
func TestPublishedAppsSignInAtPublicAddress(t *testing.T) {
	dash := docker.Route{Host: "homestead.lan", Dashboard: true, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal,
		Tunnel: []docker.Hostname{"homestead.example.com"}}
	spec := dashboard
	spec.AuthentikURL = "https://homestead.lan/apps/auth"
	m := manifest(t, "nextcloud")
	adapter, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	inputs := func(publishedApp bool, public, waiting string, applied *Record) Inputs {
		app := route("nextcloud.lan", "nextcloud")
		app.Path, app.Published = "/apps/nextcloud/", publishedApp
		table := []docker.Route{dash, app}
		return Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: spec, DashboardOK: true,
			Routes: docker.ServedRoutes(table, AppRoutes(m, table)), Applied: applied,
			PublicAuthentikURL: public, PublicWaiting: waiting}
	}
	local := "https://homestead.lan/apps/auth/application/o/homestead-nextcloud/"
	public := "https://homestead.example.com/apps/auth/application/o/homestead-nextcloud/"

	p, err := BuildPlan(inputs(true, "https://homestead.example.com/apps/auth/", "", nil), true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Issuer != public || p.ReplacedIssuer != "" {
		t.Fatalf("published issuer = %s (replaces %q)", p.Issuer, p.ReplacedIssuer)
	}
	if w := wiring(m, adapter, p.Issuer, Settings{}); fmt.Sprint(w.ExtraHosts) != "[homestead.example.com:host-gateway]" ||
		!strings.Contains(strings.Join(p.AppSettings, " "), "resolves homestead.example.com to the appliance edge") {
		t.Fatalf("back channel = %v, settings %v", w.ExtraHosts, p.AppSettings)
	}

	// S408: an app served by path shares the dashboard's origin, so it
	// signs in at the public address even when it is not published.
	if p, _ = BuildPlan(inputs(false, "https://homestead.example.com/apps/auth", "", nil), true); p.Issuer != public {
		t.Fatalf("unpublished path app issuer = %s", p.Issuer)
	}
	if p, _ = BuildPlan(inputs(false, "", "add a certificate.", nil), true); p.Issuer != local || strings.Contains(strings.Join(p.Warnings, " "), "cannot sign in") {
		t.Fatalf("unpublished path app, no public address: %s %v", p.Issuer, p.Warnings)
	}

	waiting := "add a certificate for homestead.example.com."
	if p, _ = BuildPlan(inputs(true, "", waiting, nil), true); p.Issuer != local ||
		!strings.Contains(strings.Join(p.Warnings, " "), "cannot sign in to Nextcloud with Authentik yet: "+waiting) {
		t.Fatalf("waiting plan = %s %v", p.Issuer, p.Warnings)
	}

	applied := &Record{Package: "nextcloud", Issuer: local, RedirectURIs: p.RedirectURIs}
	p, _ = BuildPlan(inputs(true, "https://homestead.example.com/apps/auth", "", applied), true)
	if p.ReplacedIssuer != local || !strings.Contains(strings.Join(p.Changes, " "), "the issuer "+public+" replaces "+local) {
		t.Fatalf("re-plan = %+v", p)
	}
	if st := BuildStatus(inputs(true, "https://homestead.example.com/apps/auth", "", applied), true); !st.Replan {
		t.Fatal("status does not ask for a re-plan after publishing")
	}
}
