package docker

import (
	"fmt"
	"regexp"
	"strings"
)

// Forward-auth gate (S418, ADR-055). A gated route sends every request
// that is not on its bypass list through Authentik's embedded outpost
// before the app sees it. The gate is set and cleared only by the app
// sign-in operation; reconciling the table never invents one.
const (
	// OutpostUpstream is the embedded outpost, reached over the edge
	// network, not through the public or path address. It answers at the
	// root path, not under /apps/auth/ (ADR-055 §1, verified on VM 2).
	OutpostUpstream = "authentik-server:9000"
	// OutpostPrefix is where the outpost answers on every gated host, so
	// the browser's sign-in redirect and callback land on the app's name.
	OutpostPrefix = "/outpost.goauthentik.io/"
	// OutpostAuthURI is the forward-auth endpoint of the outpost.
	OutpostAuthURI = OutpostPrefix + "auth/caddy"
)

// stripHeaders are the identity headers a client can never send through
// a gated host (ADR-055 §5). Wildcards are Caddy's delete form.
var stripHeaders = []string{"X-Authentik-*", "Remote-User", "X-Forwarded-User"}

// Gate puts an app route behind Authentik forward-auth.
type Gate struct {
	// Canonical is the one name that answers while the gate is on: the
	// public name when published, otherwise the .lan name. Every other
	// name of the route answers 308 to it (ADR-055 §2), because Authentik
	// matches a request to a provider by one external host.
	Canonical Hostname
	// CopyHeaders are the only headers copied from the outpost's answer
	// to the request the app receives. Empty for an app that trusts none.
	CopyHeaders []string `json:",omitempty"`
	// Bypass are anchored path regexes that skip the gate and keep the
	// app's own authentication. The identity headers are stripped there
	// too, so a bypass can only authenticate with the app's credentials.
	Bypass []string `json:",omitempty"`
	// Groups are the Authentik groups allowed through an app at a path
	// behind the shared gate (ADR-062). Authentik only says who the person
	// is; the edge then lets a request through only when the outpost's
	// X-Authentik-Groups names one of these exactly, and answers 403 to
	// everyone else. Empty means the gate admits every person Authentik
	// accepts (a gate of an app's own, ADR-055).
	Groups []string `json:",omitempty"`
}

// GroupsHeader is the outpost's answer header that lists the person's
// groups, separated by "|". It is copied from the outpost's answer only,
// checked at the edge, and removed before the app is reached.
const GroupsHeader = "X-Authentik-Groups"

var validGateGroup = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// groupMatch is the regex that matches the groups header only when one of
// groups is a whole entry of it: the header is "a|b|c", so an entry is
// bounded by the start, "|" and the end. A group that merely contains or
// extends another ("homestead-x-users-old") never matches.
func groupMatch(groups []string) string {
	alts := make([]string, 0, len(groups))
	for _, g := range groups {
		alts = append(alts, regexp.QuoteMeta(g))
	}
	return `(^|\|)(` + strings.Join(alts, "|") + `)(\||$)`
}

var (
	validGateHeader = regexp.MustCompile(`^X-Authentik-[A-Za-z0-9-]+$`)
	// bypassChars keeps a regex quotable in a Caddyfile and valid in both
	// Go and Python (Authentik) regex syntax: no braces, quotes, spaces,
	// comment marks or lookarounds.
	bypassChars = regexp.MustCompile(`^[A-Za-z0-9/._\-()|?*+\[\]\\^$:]+$`)
)

// ValidateBypass checks one bypass regex: anchored, a narrow path, and
// never the whole site.
func ValidateBypass(re string) error {
	switch {
	case !strings.HasPrefix(re, "^/"):
		return fmt.Errorf("gate.bypass: %q must be anchored with ^/", re)
	case !bypassChars.MatchString(re):
		return fmt.Errorf("gate.bypass: %q uses characters a bypass path may not", re)
	case strings.Contains(re, "(?"):
		return fmt.Errorf("gate.bypass: %q: lookarounds and flags are not allowed", re)
	}
	switch re {
	case "^/.*", "^/.+", "^/", "^/(.*)", "^/(.*)$", "^/.*$", "^/.+$":
		return fmt.Errorf("gate.bypass: %q would open the whole site", re)
	}
	if _, err := regexp.Compile(re); err != nil {
		return fmt.Errorf("gate.bypass: %q: %v", re, err)
	}
	for _, probe := range []string{OutpostPrefix, OutpostAuthURI, "/"} {
		if regexp.MustCompile(re).MatchString(probe) {
			return fmt.Errorf("gate.bypass: %q matches %s; the outpost's paths are handled by the gate itself", re, probe)
		}
	}
	return nil
}

// validateGate: a gated route is an app route on names of its own.
func (r Route) validateGate() error {
	g := r.Gate
	if g == nil {
		return nil
	}
	switch {
	case r.Dashboard:
		return fmt.Errorf("route.gate: the dashboard keeps its own sign-in and is never gated")
	case r.CraftApp != "":
		return fmt.Errorf("route.gate: a craft app route is not gated")
	case r.IsPath() && r.StripPrefix:
		return fmt.Errorf("route.gate: an app at a path that the edge strips is not gated; it must serve under its path itself")
	}
	if r.IsPath() {
		// A path app is gated inside the dashboard site (ADR-061); the name
		// it answers on is worked out from the dashboard route when it is
		// rendered, so Canonical is only a record of it. Bypass paths carry
		// the app's prefix.
		if g.Canonical != "" {
			if err := g.Canonical.Validate(); err != nil {
				return fmt.Errorf("route.gate.canonical: %w", err)
			}
		}
		for _, b := range g.Bypass {
			if !strings.HasPrefix(b, "^"+r.Path) {
				return fmt.Errorf("route.gate.bypass: %q must start with ^%s for an app at a path", b, r.Path)
			}
		}
	} else {
		if err := g.Canonical.Validate(); err != nil {
			return fmt.Errorf("route.gate.canonical: %w", err)
		}
		found := false
		for _, n := range r.AllNames() {
			found = found || n == g.Canonical
		}
		if !found {
			return fmt.Errorf("route.gate.canonical: %q is not one of the route's names", g.Canonical)
		}
	}
	if len(g.Groups) > 0 && !r.IsPath() {
		return fmt.Errorf("route.gate.groups: only an app at a path shares the gate (ADR-062)")
	}
	for _, grp := range g.Groups {
		if !validGateGroup.MatchString(grp) {
			return fmt.Errorf("route.gate.groups: %q is not a plain group name", grp)
		}
	}
	for _, h := range g.CopyHeaders {
		if !validGateHeader.MatchString(h) {
			return fmt.Errorf("route.gate.copyheaders: %q must be an X-Authentik-* header", h)
		}
	}
	for _, b := range g.Bypass {
		if err := ValidateBypass(b); err != nil {
			return err
		}
	}
	return nil
}

// stripped is the identity headers removed from every request, the
// gate's copied headers included.
func (g Gate) stripped() []string {
	out := append([]string(nil), stripHeaders...)
	for _, h := range g.CopyHeaders {
		out = append(out, h)
	}
	return out
}

// renderGatedBody is the serving body of one gated site, inside one
// route so the order holds (ADR-055 §1): strip identity headers, bypass
// handles, the outpost's own paths, then forward-auth in front of the
// app. proxy is the app's reverse_proxy block. The app is only reached
// inside the last handle, after forward-auth succeeds, or in a bypass
// handle; a missing or failing outpost answers 5xx and never the app
// (fail closed, §9). On the tunnel entry the scheme the outpost builds
// its redirects from is https, because Cloudflare terminated TLS.
func (g Gate) renderGatedBody(proxy string, tunnel bool) string {
	var b strings.Builder
	b.WriteString("\t# Authentik forward-auth gate (ADR-055). Identity headers come only from the outpost.\n")
	b.WriteString("\troute {\n")
	for _, h := range g.stripped() {
		fmt.Fprintf(&b, "\t\trequest_header -%s\n", h)
	}
	for i, re := range g.Bypass {
		fmt.Fprintf(&b, "\t\t@gate_bypass%d path_regexp %s\n\t\thandle @gate_bypass%d {\n", i, re, i)
		b.WriteString(indentBy(proxy, 3))
		b.WriteString("\t\t}\n")
	}
	proto := ""
	if tunnel {
		proto = "\t\t\t\theader_up X-Forwarded-Proto https\n"
	}
	fmt.Fprintf(&b, "\t\thandle %s* {\n", OutpostPrefix)
	if tunnel {
		fmt.Fprintf(&b, "\t\t\treverse_proxy %s {\n%s\t\t\t}\n", OutpostUpstream, proto)
	} else {
		fmt.Fprintf(&b, "\t\t\treverse_proxy %s\n", OutpostUpstream)
	}
	b.WriteString("\t\t}\n\t\thandle {\n")
	fmt.Fprintf(&b, "\t\t\tforward_auth %s {\n\t\t\t\turi %s\n", OutpostUpstream, OutpostAuthURI)
	if len(g.CopyHeaders) > 0 {
		fmt.Fprintf(&b, "\t\t\t\tcopy_headers %s\n", strings.Join(g.CopyHeaders, " "))
	}
	b.WriteString(proto)
	b.WriteString("\t\t\t}\n")
	b.WriteString(indentBy(proxy, 3))
	b.WriteString("\t\t}\n\t}\n")
	return b.String()
}

// renderRedirect answers every request on the other names of a gated
// route with a 308 to the same path on the canonical name.
func (g Gate) renderRedirect() string {
	return fmt.Sprintf("\tredir https://%s{uri} 308\n", g.Canonical)
}

// indentBy puts every line of body n tabs deeper than it is written
// (proxy blocks are written one tab deep).
func indentBy(body string, n int) string {
	pad := strings.Repeat("\t", n-1)
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		b.WriteString(pad + line + "\n")
	}
	return b.String()
}

// siteList is a Caddy site address list for names.
func siteList(names []Hostname, plain bool) string {
	parts := make([]string, 0, len(names))
	for _, n := range names {
		if plain {
			parts = append(parts, "http://"+string(n))
		} else {
			parts = append(parts, string(n))
		}
	}
	return strings.Join(parts, ", ")
}

func without(names []Hostname, skip Hostname) []Hostname {
	var out []Hostname
	for _, n := range names {
		if n != skip {
			out = append(out, n)
		}
	}
	return out
}

// renderGatedSites renders the local and tunnel sites of a gated route:
// the canonical name serves the gate, every other name redirects to it.
func renderGatedSites(r Route, tlsLine, proxy, tunnelBody string) string {
	g := r.Gate
	local := r.Names()
	plain := r.TLS == TLSOff
	var out strings.Builder
	canonicalLocal := false
	for _, n := range local {
		canonicalLocal = canonicalLocal || n == g.Canonical
	}
	writeGate := func(site, tls string, body string) {
		fmt.Fprintf(&out, "%s {\n%s%s}\n", site, tls, body)
	}
	writeRedirect := func(site, tls string) {
		fmt.Fprintf(&out, "\n# Redirect to the canonical name (ADR-055 §2).\n%s {\n%s%s}\n", site, tls, g.renderRedirect())
	}
	if canonicalLocal {
		writeGate(siteList([]Hostname{g.Canonical}, plain), tlsLine,
			renderUpstream(r.Policy, g.renderGatedBody(proxy, false)))
		if others := without(local, g.Canonical); len(others) > 0 {
			writeRedirect(siteList(others, plain), tlsLine)
		}
	} else {
		writeRedirect(siteList(local, plain), tlsLine)
	}
	if len(r.Tunnel) > 0 {
		var sites []string
		for _, n := range r.Tunnel {
			if n != g.Canonical {
				sites = append(sites, fmt.Sprintf("http://%s:%d", n, TunnelEntryPort))
			}
		}
		fmt.Fprintf(&out, "\n# Tunnel entry (ADR-037), gated (ADR-055).\n")
		if !canonicalLocal {
			writeGate(fmt.Sprintf("http://%s:%d", g.Canonical, TunnelEntryPort), "",
				g.renderGatedBody(tunnelBody, true))
		}
		if len(sites) > 0 {
			writeRedirect(strings.Join(sites, ", "), "")
		}
	}
	return out.String()
}

// HealGates keeps every gate valid when a route's names change under it:
// unpublishing removes the public name a gate answers on. A gate whose
// canonical name is gone moves to the route's public name if it has one,
// else its host. The gate stays on (fail closed); its Authentik provider
// is out of date until sign-in is planned again, which the app's status
// shows (S418).
func HealGates(routes []Route) {
	for i, r := range routes {
		if r.Gate == nil || r.IsPath() {
			continue
		}
		present := false
		for _, n := range r.AllNames() {
			present = present || n == r.Gate.Canonical
		}
		if present {
			continue
		}
		g := *r.Gate
		g.Canonical = r.Host
		if len(r.Tunnel) > 0 {
			g.Canonical = r.Tunnel[0]
		}
		routes[i].Gate = &g
	}
}

// PathBypass is a bypass regex of a package, written for the app's own
// root (^/rest/.*), as it must be matched at the app's path (ADR-061):
// ^/apps/navidrome/rest/.*. Authentik matches the same regex against the
// request path, so the plan and the edge use this one form.
func PathBypass(path, re string) string {
	return "^" + strings.TrimSuffix(path, "/") + strings.TrimPrefix(re, "^")
}

// PathGateCanonical is the one name a gated app at a path answers on: the
// dashboard's public name once the path is published, else the dashboard's
// own name. Authentik matches a request to the app's provider by one
// external host, so every other name redirects to it.
func PathGateCanonical(p, dash Route) Hostname {
	if p.Published && len(dash.Tunnel) > 0 {
		return dash.Tunnel[0]
	}
	return dash.Host
}

// SharedGateCanonical is the one name every app behind the shared gate
// answers on (ADR-062): Authentik's proxy provider matches one external
// host, so all gated apps at a path share the dashboard's. It is the
// dashboard's public name once any gated app at a path is published, else
// the dashboard's own name. routes is the whole edge table.
func SharedGateCanonical(routes []Route, dash Route) Hostname {
	if len(dash.Tunnel) > 0 {
		for _, r := range routes {
			if r.IsPath() && r.Gate != nil && r.Published {
				return dash.Tunnel[0]
			}
		}
	}
	return dash.Host
}

// renderOutpostHandle is the dashboard site's handle for Authentik's
// embedded outpost, which the gate's sign-in redirect and callback need on
// the name the app answers on (ADR-061; public too, because the gate runs
// for public visitors). On the tunnel entry the scheme the outpost builds
// its redirects from is https, because Cloudflare terminated TLS.
func renderOutpostHandle(tunnel bool) string {
	if tunnel {
		return fmt.Sprintf("\thandle %s* {\n\t\treverse_proxy %s {\n\t\t\theader_up X-Forwarded-Proto https\n\t\t}\n\t}\n", OutpostPrefix, OutpostUpstream)
	}
	return fmt.Sprintf("\thandle %s* {\n\t\treverse_proxy %s\n\t}\n", OutpostPrefix, OutpostUpstream)
}

// renderGatedPath renders one gated app's handle inside the dashboard
// site, and reports whether this site must carry the outpost handle (it
// does where the app answers; the other names redirect to the canonical
// one). Order inside the route holds as for a site of its own: identity
// headers stripped, bypass handles, then forward-auth in front of the app.
// The reserved cookies are removed from the request only after the
// forward-auth subrequest, so the outpost still sees Authentik's cookie
// (ADR-061 section 3).
func renderGatedPath(p, dash Route, canonical Hostname, tunnel bool) (string, bool) {
	g := p.Gate
	bare := strings.TrimSuffix(p.Path, "/")
	label := strings.ReplaceAll(strings.Trim(bare, "/"), "/", "_")
	var b strings.Builder
	b.WriteString("\t# Authentik forward-auth gate at the app's path (ADR-061, ADR-055). Identity headers come only from the outpost.\n")
	fmt.Fprintf(&b, "\thandle %s* {\n", p.Path)
	if !tunnel && !isHostIn(canonical, dash.Names()) {
		// Published: the local names send the visitor to the public one.
		fmt.Fprintf(&b, "\t\tredir https://%s{uri} 308\n\t}\n", canonical)
		return b.String(), false
	}
	if !tunnel && len(dash.Names()) > 1 {
		fmt.Fprintf(&b, "\t\t@gate_%s_other {\n\t\t\tnot host %s\n\t\t}\n\t\tredir @gate_%s_other https://%s{uri} 308\n", label, canonical, label, canonical)
	}
	b.WriteString(sharedOriginResponseHeaders())
	cookie := ""
	if p.Path != SignInPath {
		cookie = "\t\t\t\trequest_header Cookie \"(?i)(^|;)[ ]*(" + reservedCookieAlt + ")=[^;]*\" \"\"\n"
	}
	proxy := pathUpstream(p, tunnel)
	b.WriteString("\t\troute {\n")
	for _, h := range g.stripped() {
		fmt.Fprintf(&b, "\t\t\trequest_header -%s\n", h)
	}
	for i, re := range g.Bypass {
		fmt.Fprintf(&b, "\t\t\t@gate_%s_bypass%d path_regexp %s\n\t\t\thandle @gate_%s_bypass%d {\n", label, i, re, label, i)
		b.WriteString(cookie)
		b.WriteString(indentBy(proxy, 4))
		b.WriteString("\t\t\t}\n")
	}
	// The last handle is a route of its own, so the order is the one
	// written: forward-auth first, then the group check, then the headers
	// and cookies the app must not see, then the app.
	b.WriteString("\t\t\thandle {\n\t\t\t\troute {\n")
	fmt.Fprintf(&b, "\t\t\t\t\tforward_auth %s {\n\t\t\t\t\t\turi %s\n", OutpostUpstream, OutpostAuthURI)
	copied := append([]string(nil), g.CopyHeaders...)
	if len(g.Groups) > 0 && !hasFold(copied, GroupsHeader) {
		copied = append(copied, GroupsHeader)
	}
	if len(copied) > 0 {
		fmt.Fprintf(&b, "\t\t\t\t\t\tcopy_headers %s\n", strings.Join(copied, " "))
	}
	if tunnel {
		b.WriteString("\t\t\t\t\t\theader_up X-Forwarded-Proto https\n")
	}
	b.WriteString("\t\t\t\t\t}\n")
	if len(g.Groups) > 0 {
		// ADR-062: Authentik said who this is; only members of the app's
		// groups pass. An absent header matches nothing, so it fails closed.
		fmt.Fprintf(&b, "\t\t\t\t\t@gate_%s_denied {\n\t\t\t\t\t\tnot header_regexp %s %s\n\t\t\t\t\t}\n", label, GroupsHeader, groupMatch(g.Groups))
		fmt.Fprintf(&b, "\t\t\t\t\trespond @gate_%s_denied \"You do not have access to this app.\" 403\n", label)
		if !hasFold(g.CopyHeaders, GroupsHeader) {
			fmt.Fprintf(&b, "\t\t\t\t\trequest_header -%s\n", GroupsHeader)
		}
	}
	b.WriteString(strings.Replace(cookie, "\t\t\t\t", "\t\t\t\t\t", 1))
	b.WriteString(indentBy(proxy, 5))
	b.WriteString("\t\t\t\t}\n\t\t\t}\n\t\t}\n\t}\n")
	return b.String(), true
}

func hasFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func isHostIn(h Hostname, names []Hostname) bool {
	for _, n := range names {
		if n == h {
			return true
		}
	}
	return false
}
