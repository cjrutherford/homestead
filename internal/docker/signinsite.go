package docker

import (
	"fmt"
	"strings"
)

// SignInPath is Authentik's path under the appliance's names (ADR-044).
var SignInPath = AppPathFor("auth")

// signInPathRoute is the published Authentik path the dashboard route
// carries at render time, if any.
func (r Route) signInPathRoute() (Route, bool) {
	for _, p := range r.paths {
		if p.Path == SignInPath && p.Published {
			return p, true
		}
	}
	return Route{}, false
}

// signInSiteCert is the placed certificate for the dashboard's public
// name (ADR-047 §3): one from a public CA (never a local leaf, which
// the appliance root cannot issue for that name), exact name first.
func signInSiteCert(stackDir string, r Route) (PlacedCert, bool) {
	if !r.Dashboard || len(r.Tunnel) == 0 || stackDir == "" {
		return PlacedCert{}, false
	}
	return servingCertFor(stackDir, Route{Host: r.Tunnel[0], TLS: TLSAuto})
}

// renderSignInSite is the site block that answers the dashboard's public
// name on 443 on this box (ADR-047 §2), so an app's token, userinfo and
// key calls to Authentik's public address stay local. It carries only
// Authentik's path and needs a placed certificate: without one nothing
// is rendered, and the edge never asks a CA for the name itself.
func renderSignInSite(r Route, cert *PlacedCert) string {
	if cert == nil || len(r.Tunnel) == 0 {
		return ""
	}
	p, ok := r.signInPathRoute()
	if !ok {
		return ""
	}
	return fmt.Sprintf(`
# Sign-in back channel (ADR-047): Authentik at the dashboard's public name,
# answered on this box for the apps; only Authentik's path is served.
%s {
	tls %s/%s/%s/%s %s/%s/%s/%s
%s	handle {
		respond 404
	}
}
`, r.Tunnel[0], caddyRoutesDir, certsDirName, cert.Name, certChainFile,
		caddyRoutesDir, certsDirName, cert.Name, certKeyFile,
		strings.TrimSuffix(renderPathHandles([]Route{p}, false), "\n")+"\n")
}

// SignInSiteServed reports the dashboard's public name when the edge
// answers it on 443 for Authentik (ADR-047): Authentik's path is
// published, a public certificate covering the name is placed, and the
// dashboard's snippet on disk loads it.
func SignInSiteServed(stackDir string, set RouteSet) (Hostname, bool) {
	dash, ok := set.DashboardRoute()
	if !ok {
		return "", false
	}
	for _, r := range withPaths(set.Routes) {
		if r.Dashboard {
			dash = r
		}
	}
	if _, ok := dash.signInPathRoute(); !ok {
		return "", false
	}
	cert, ok := signInSiteCert(stackDir, dash)
	if !ok || !RouteServesCert(stackDir, dash.Host, cert.Name) {
		return "", false
	}
	return dash.Tunnel[0], true
}
