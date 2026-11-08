package docker

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func pathSet() RouteSet {
	return RouteSet{Routes: []Route{
		{Host: "homestead.lan", Dashboard: true, Exposure: ExposureLocal, TLS: TLSInternal,
			Aliases: []Hostname{"homestead.local"}, Tunnel: []Hostname{"homestead.example.com"}},
		{Host: "nextcloud.lan", Target: Target{Host: "nextcloud", Port: 80}, Exposure: ExposureLocal, TLS: TLSInternal,
			Path: "/apps/nextcloud/", StripPrefix: true, Published: true,
			Redirects: []PathRedirect{{From: "/.well-known/caldav", To: "remote.php/dav/"}, {From: "/.well-known/carddav", To: "remote.php/dav/"}}},
		{Host: "authentik.lan", Target: Target{Host: "server", Port: 9000}, Exposure: ExposureLocal, TLS: TLSInternal,
			Path: "/apps/auth/", Published: true},
		{Host: "jellyfin.lan", Target: Target{Host: "jellyfin", Port: 8096}, Exposure: ExposureLocal, TLS: TLSInternal,
			Path: "/apps/jellyfin/"},
		{Host: "immich.lan", Target: Target{Host: "immich-server", Port: 2283}, Exposure: ExposureLocal, TLS: TLSInternal},
	}}
}

// indentTest adds one tab to each line.
func indentTest(s string) string {
	return indent(s)
}

func renderDashboard(t *testing.T, set RouteSet) (local, tunnel string) {
	t.Helper()
	if err := ValidateRenderable(set); err != nil {
		t.Fatalf("ValidateRenderable: %v", err)
	}
	for _, r := range withPaths(set.Routes) {
		if r.Dashboard {
			out, err := RenderRoute(r)
			if err != nil {
				t.Fatalf("RenderRoute: %v", err)
			}
			i := strings.Index(out, "# Tunnel entry")
			if i < 0 {
				return out, ""
			}
			return out[:i], out[i:]
		}
	}
	t.Fatal("no dashboard route")
	return "", ""
}

func TestPathRoutesRenderInsideDashboardSite(t *testing.T) {
	local, _ := renderDashboard(t, pathSet())
	for _, want := range []string{
		"homestead.lan, homestead.local {",
		"\t\tredir /apps/nextcloud /apps/nextcloud/ 308\n",
		"\t\thandle /apps/nextcloud/* {\n" + indentTest(sharedOriginHeaders(Route{Path: "/apps/nextcloud/"})) + "\t\t\turi strip_prefix /apps/nextcloud\n\t\t\treverse_proxy nextcloud:80 {",
		// Authentik keeps its own cookies (S470): no request_header strip.
		"\t\thandle /apps/auth/* {\n" + indentTest(sharedOriginHeaders(Route{Path: SignInPath})) + "\t\t\treverse_proxy server:9000 {",
		// Each handle uses the same directive, so Caddy sorts them by path.
		"\t\thandle /apps/jellyfin/* {\n" + indentTest(sharedOriginHeaders(Route{Path: "/apps/jellyfin/"})) + "\t\t\treverse_proxy jellyfin:8096 {",
		"\t\tredir /.well-known/caldav /apps/nextcloud/remote.php/dav/ 301\n",
		"\t\tredir /.well-known/carddav /apps/nextcloud/remote.php/dav/ 301\n",
		"\t\thandle /apps/* {\n\t\t\trespond 404\n\t\t}\n",
		"\t\thandle {\n\t\t\treverse_proxy unix/" + DashboardSocketPath,
	} {
		if !strings.Contains(local, want) {
			t.Fatalf("local site lacks %q:\n%s", want, local)
		}
	}
	// The app handles sit inside the dashboard's source restriction.
	if strings.Index(local, "@allowed remote_ip") > strings.Index(local, "handle /apps/") {
		t.Fatalf("app paths escape the dashboard policy:\n%s", local)
	}
	if strings.Contains(local, "handle_path") || strings.Contains(local, "strip_prefix /apps/auth") {
		t.Fatalf("mixed handle directives or Authentik stripped:\n%s", local)
	}
	if strings.Contains(local, "Cf-Connecting-Ip") {
		t.Fatalf("local site trusts a Cloudflare header:\n%s", local)
	}
}

func TestPathRoutesNeverShadowAPI(t *testing.T) {
	local, tunnel := renderDashboard(t, pathSet())
	for _, out := range []string{local, tunnel} {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "handle") && strings.Contains(line, "/") && !strings.Contains(line, " "+AppsPrefix) {
				t.Fatalf("handle outside %s: %q", AppsPrefix, line)
			}
		}
	}
	for _, bad := range []string{"/api/", "/apps/", "/apps/x", "/apps/x/y/", "/apps/X/", "/assets/", "/apps/-x/", "apps/x/"} {
		if err := ValidateAppPath(bad); err == nil {
			t.Fatalf("ValidateAppPath(%q) accepted", bad)
		}
	}
	set := pathSet()
	set.Routes[3].Path = "/api/"
	if err := set.Validate(); err == nil {
		t.Fatal("a path outside /apps/ validated")
	}
}

func TestPathRoutesPublicHostOnlyPublished(t *testing.T) {
	_, tunnel := renderDashboard(t, pathSet())
	if !strings.Contains(tunnel, "http://homestead.example.com:8480 {") {
		t.Fatalf("tunnel entry:\n%s", tunnel)
	}
	for _, want := range []string{
		"\thandle /apps/nextcloud/* {\n" + sharedOriginHeaders(Route{Path: "/apps/nextcloud/"}) + "\t\turi strip_prefix /apps/nextcloud\n",
		"\thandle /apps/auth/* {",
		"\t\t\theader_up X-Forwarded-For {http.request.header.Cf-Connecting-Ip}\n\t\t\theader_up X-Forwarded-Proto https\n",
		"\tredir /.well-known/caldav /apps/nextcloud/remote.php/dav/ 301\n",
		"\thandle /apps/* {\n\t\trespond 404\n\t}\n",
	} {
		if !strings.Contains(tunnel, want) {
			t.Fatalf("tunnel entry lacks %q:\n%s", want, tunnel)
		}
	}
	if strings.Contains(tunnel, "jellyfin") {
		t.Fatalf("unpublished app answers on the public name:\n%s", tunnel)
	}
	// Unpublishing Nextcloud drops its handle and its .well-known links
	// from the public names; the local names keep both.
	set := pathSet()
	set.Routes[1].Published = false
	local, tunnel := renderDashboard(t, set)
	if strings.Contains(tunnel, "nextcloud") || strings.Contains(tunnel, "well-known") {
		t.Fatalf("unpublished Nextcloud on the public name:\n%s", tunnel)
	}
	if !strings.Contains(local, "handle /apps/nextcloud/*") {
		t.Fatalf("local lost Nextcloud:\n%s", local)
	}
}

// A craft app under its path (ADR-045): the wrapper page and the file
// path go to the daemon's craft socket with the edge's own headers, on
// the local names and, once published, on the public name.
func TestCraftPathRoute(t *testing.T) {
	set := pathSet()
	set.Routes = append(set.Routes, Route{Host: "gridcraft.lan", Target: Target{Host: "gridcraft", Port: 8080},
		Exposure: ExposureLocal, TLS: TLSInternal, Path: "/apps/gridcraft/", StripPrefix: true, CraftApp: "gridcraft"})
	if !set.HasCraftRoutes() {
		t.Fatal("craft path route does not mount the craft socket")
	}
	craft := "\t\thandle /apps/gridcraft/* {\n" + indentTest(sharedOriginHeaders(Route{Path: "/apps/gridcraft/"})) + "\t\t\turi strip_prefix /apps/gridcraft\n" +
		"\t\t\thandle /_homestead/* {\n\t\t\t\treverse_proxy unix/" + CraftSocketPath + " {\n" +
		"\t\t\t\t\theader_up X-Homestead-Craft-App gridcraft\n" +
		"\t\t\t\t\theader_up X-Homestead-Craft-Host {http.request.host}\n" +
		"\t\t\t\t\theader_up X-Homestead-Craft-Prefix /apps/gridcraft\n" +
		"\t\t\t\t\theader_up X-Forwarded-Proto https\n\t\t\t\t}\n\t\t\t}\n" +
		"\t\t\thandle {\n\t\t\t\treverse_proxy gridcraft:8080 {\n"
	local, tunnel := renderDashboard(t, set)
	if !strings.Contains(local, craft) {
		t.Fatalf("local site lacks the craft handle:\n%s", local)
	}
	if strings.Contains(tunnel, "gridcraft") {
		t.Fatalf("unpublished craft app on the public name:\n%s", tunnel)
	}
	set.Routes[len(set.Routes)-1].Published = true
	_, tunnel = renderDashboard(t, set)
	if !strings.Contains(tunnel, strings.ReplaceAll(craft, "\n\t\t", "\n\t")[1:]) ||
		!strings.Contains(tunnel, "\t\t\t\theader_up X-Forwarded-For {http.request.header.Cf-Connecting-Ip}") {
		t.Fatalf("tunnel entry lacks the craft handle:\n%s", tunnel)
	}
	// A craft route on a name of its own still takes no tunnel names.
	host := Route{Host: "gridcraft.lan", Target: Target{Host: "gridcraft", Port: 8080}, Exposure: ExposureLocal,
		TLS: TLSInternal, CraftApp: "gridcraft", Tunnel: []Hostname{"gridcraft.example.com"}}
	if err := host.Validate(); err == nil || !strings.Contains(err.Error(), "apps move") {
		t.Fatalf("craft host route with tunnel names: %v", err)
	}
}

func TestPathRoutesValidation(t *testing.T) {
	cases := map[string]func(*RouteSet){
		"duplicate path":   func(s *RouteSet) { s.Routes[3].Path = "/apps/auth/" },
		"craft unstripped": func(s *RouteSet) { s.Routes[3].CraftApp = "gridcraft" },
		"tunnel names":     func(s *RouteSet) { s.Routes[3].Tunnel = []Hostname{"jellyfin.example.com"} },
		"public exposure":  func(s *RouteSet) { s.Routes[3].Exposure = ExposurePublic },
		"strip on host":    func(s *RouteSet) { s.Routes[4].StripPrefix = true },
		"bad redirect":     func(s *RouteSet) { s.Routes[1].Redirects[0].To = "/elsewhere" },
		"no dashboard":     func(s *RouteSet) { s.Routes = s.Routes[1:] },
		"duplicate redirect": func(s *RouteSet) {
			s.Routes[3].Redirects = []PathRedirect{{From: "/.well-known/caldav", To: "x/"}}
		},
	}
	for name, mutate := range cases {
		set := pathSet()
		mutate(&set)
		if err := set.Validate(); err == nil {
			t.Fatalf("%s: validated", name)
		}
	}
	if _, err := RenderRoute(pathSet().Routes[1]); err == nil {
		t.Fatal("a path route rendered a site of its own")
	}
}

func TestReconcilePathRoutesWritesNoAppSite(t *testing.T) {
	dir := t.TempDir()
	set := pathSet()
	// The old host route of Nextcloud is replaced by its path route.
	prior := RouteSet{Routes: []Route{{Host: "nextcloud.lan", Target: Target{Host: "nextcloud", Port: 80}, Exposure: ExposureLocal, TLS: TLSInternal}}}
	if err := os.MkdirAll(filepath.Join(dir, "routes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "routes", "nextcloud.lan.caddy"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileRoutes(dir, set, prior); err != nil {
		t.Fatalf("ReconcileRoutes: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "routes"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	got := strings.Join(names, ",")
	if got != "homestead.lan.caddy,immich.lan.caddy" {
		t.Fatalf("snippets = %s", got)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "routes", "homestead.lan.caddy"))
	if !strings.Contains(string(body), "handle /apps/nextcloud/*") {
		t.Fatalf("dashboard snippet lacks the app path:\n%s", body)
	}
	if lan := set.LANNames(); strings.Join(lan, ",") != "homestead.lan,immich.lan" {
		t.Fatalf("LANNames = %v", lan)
	}
	if labels := set.MDNSLabels(); strings.Join(labels, ",") != "immich" {
		t.Fatalf("MDNSLabels = %v", labels)
	}
}

// S431: the Everything bridge rate-limits read links on the address in
// X-Forwarded-For, which it believes only from the edge. That is sound only
// while the edge never forwards a client-sent value: Caddy replaces
// X-Forwarded-For with the address it saw unless trusted_proxies is set, so
// the generated config must never set it, and the tunnel entry must set the
// header itself from Cloudflare's header (loopback-only listener).
func TestEdgeNeverForwardsAClientSentAddress(t *testing.T) {
	set := pathSet()
	set.Routes = append(set.Routes, Route{Host: "tasks.lan", Target: Target{Host: "tasks", Port: 8081}, Exposure: ExposureLocal, TLS: TLSInternal})
	if err := ValidateRenderable(set); err != nil {
		t.Fatal(err)
	}
	local, tunnel := renderDashboard(t, set)
	for _, r := range withPaths(set.Routes) {
		if r.Path != "" {
			continue // rendered inside the dashboard site, checked below
		}
		out, err := RenderRoute(r)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "X-Forwarded-For") && !r.Dashboard {
			t.Fatalf("%s sets X-Forwarded-For:\n%s", r.Host, out)
		}
		if strings.Contains(out, "trusted_proxies") {
			t.Fatalf("%s: trusted_proxies set:\n%s", r.Host, out)
		}
	}
	if strings.Contains(local+tunnel, "trusted_proxies") {
		t.Fatal("trusted_proxies set")
	}
	app := local[strings.Index(local, "handle /apps/jellyfin/*"):]
	app = app[:strings.Index(app, "handle /apps/*")]
	if strings.Contains(app, "X-Forwarded-For") {
		t.Fatalf("local app handle sets X-Forwarded-For from something else:\n%s", app)
	}
	if n := strings.Count(tunnel, "header_up X-Forwarded-For {http.request.header.Cf-Connecting-Ip}"); n < 2 {
		t.Fatalf("tunnel app handles must set X-Forwarded-For themselves (%d):\n%s", n, tunnel)
	}
	if strings.Contains(tunnel, "{http.request.header.X-Forwarded-For}") || strings.Contains(local, "{http.request.header.X-Forwarded-For}") {
		t.Fatal("an edge route copies a client-sent X-Forwarded-For")
	}
}

// S470 (ADR-061 §4): every app path on the shared origin loses the
// dashboard and Authentik cookies on the way in and cannot set them on the
// way out, and gets framing and referrer defaults; Authentik at its own
// path keeps its cookies; the read-link handle strips too.
func TestSharedOriginHardening(t *testing.T) {
	local, tunnel := renderDashboard(t, withPublished(pathSet()))
	for name, out := range map[string]string{"local": local, "tunnel": tunnel} {
		for _, want := range []string{
			"header ?Content-Security-Policy \"frame-ancestors 'self'\"",
			"header ?Referrer-Policy same-origin",
			"request_header Cookie \"(?i)(^|;)[ ]*(__Host-homestead_session|__Host-homestead_csrf|homestead_session|homestead_csrf|authentik_session|authentik_csrf)=[^;]*\" \"\"",
			"header_down Set-Cookie \"(?i)^(__Host-homestead_session|__Host-homestead_csrf|homestead_session|homestead_csrf|authentik_session|authentik_csrf)=.*\" \"\"",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s entry lacks %q:\n%s", name, want, out)
			}
		}
		// Authentik's handle is the only one without the strip.
		i := strings.Index(out, "handle /apps/auth/* {")
		j := strings.Index(out[i:], "reverse_proxy")
		if strings.Contains(out[i:i+j], "request_header Cookie") {
			t.Fatalf("%s: Authentik loses its own cookies:\n%s", name, out[i:i+j])
		}
		if k := strings.Index(out[i:], "}\n"); strings.Contains(out[i:i+k], "header_down Set-Cookie") {
			t.Fatalf("%s: Authentik's Set-Cookie is blanked", name)
		}
	}
	// The pattern removes one cookie and keeps the others.
	re := regexp.MustCompile(`(?i)(^|;)[ ]*(` + reservedCookieAlt + `)=[^;]*`)
	got := re.ReplaceAllString("a=1; __Host-homestead_session=x; b=2; authentik_session=y", "")
	if got != "a=1; b=2" {
		t.Fatalf("cookie strip = %q", got)
	}
	if re.MatchString("homestead_sessionx=1; my_authentik_session=2") {
		t.Fatalf("pattern matches look-alike names")
	}
}

func TestReadLinkHandleStripsReservedCookies(t *testing.T) {
	out := renderReadLinkHandle(Route{Host: "x.lan", Target: Target{Host: "nextcloud", Port: 80}, Path: "/apps/nextcloud/"})
	if !strings.Contains(out, "request_header Cookie") || !strings.Contains(out, "header_down Set-Cookie") {
		t.Fatalf("read-link handle:\n%s", out)
	}
}

func withPublished(set RouteSet) RouteSet {
	for i := range set.Routes {
		if set.Routes[i].Path != "" {
			set.Routes[i].Published = true
		}
	}
	return set
}
