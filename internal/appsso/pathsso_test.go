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
		if w := wiring(m, adapter, spec); fmt.Sprint(w.ExtraHosts) != "[homestead.lan:host-gateway]" {
			t.Fatalf("%s: back channel = %v", c.name, w.ExtraHosts)
		}
	}
}
