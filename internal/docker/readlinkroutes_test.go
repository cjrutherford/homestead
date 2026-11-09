package docker

import (
	"regexp"
	"strings"
	"testing"
)

func readLinkSet(tasks, notes bool) RouteSet {
	set := pathSet()
	set.Routes = append(set.Routes,
		Route{Host: "tasks.lan", Target: Target{Host: "tasks", Port: 8081}, Exposure: ExposureLocal, TLS: TLSInternal,
			Path: "/apps/tasks/", ReadLinks: tasks},
		Route{Host: "notes.lan", Target: Target{Host: "notes", Port: 8081}, Exposure: ExposureLocal, TLS: TLSInternal,
			Path: "/apps/notes/", ReadLinks: notes},
	)
	return set
}

const token43 = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO-_"

// The public route set is exactly the two prefixes per approved app.
func TestReadLinkTunnelRoutesAreExactlyReadAndView(t *testing.T) {
	_, tunnel := renderDashboard(t, readLinkSet(true, true))
	for _, app := range []string{"tasks", "notes"} {
		prefix := "\thandle /apps/" + app + "/_homestead/* {\n"
		if strings.Count(tunnel, prefix) != 1 {
			t.Fatalf("%s: want one _homestead handle:\n%s", app, tunnel)
		}
		if n := strings.Count(tunnel, "reverse_proxy "+app+":8081"); n != 1 {
			t.Fatalf("%s: the app is proxied %d times, want only the read-link handle:\n%s", app, n, tunnel)
		}
		// No handle for the app itself, its events, data or source.
		if strings.Contains(tunnel, "handle /apps/"+app+"/*") {
			t.Fatalf("%s: the app path itself is routed:\n%s", app, tunnel)
		}
		re := regexp.MustCompile(`path_regexp (\^/apps/` + app + `/_homestead/[^\n]*)\n`)
		m := re.FindStringSubmatch(tunnel)
		if m == nil {
			t.Fatalf("%s: no path pattern:\n%s", app, tunnel)
		}
		pat := regexp.MustCompile(m[1])
		base := "/apps/" + app + "/_homestead/"
		for _, ok := range []string{base + "read/" + token43, base + "view/" + token43} {
			if !pat.MatchString(ok) {
				t.Errorf("%s: %q should be routed", app, ok)
			}
		}
		for _, bad := range []string{
			base + "read/", base + "view/", base + "read", base + "events", base + "data", base + "source",
			base + "callback", base + "login", base + "files/x", base + "read/../events", base + "read/%2e%2e",
			base + "read/" + token43 + "/x", base + "read/" + token43 + "/", base + "read//" + token43,
			base + "READ/" + token43, base + "read/a.b", base + "view/x?y", "/apps/" + app + "/",
			"/apps/" + app + "/index.html", "/apps/other/_homestead/read/" + token43,
			"/_homestead/read/" + token43, "/apps/" + app + "x/_homestead/read/" + token43,
			base + "read/" + token43 + "\n",
		} {
			if pat.MatchString(bad) {
				t.Errorf("%s: %q must not be routed", app, bad)
			}
		}
	}
	// Read only: GET and HEAD, the app answers nothing else; anything
	// else under _homestead/ and every other app path is 404 at the edge.
	if strings.Count(tunnel, "method GET HEAD") != 2 {
		t.Fatalf("methods are not limited to GET and HEAD:\n%s", tunnel)
	}
	if strings.Count(tunnel, "\t\thandle {\n\t\t\trespond 404\n") != 2 || !strings.Contains(tunnel, "handle /apps/* {\n\t\trespond 404") {
		t.Fatalf("misses are not 404:\n%s", tunnel)
	}
	// No gate, no cookie or header forwarding other than the edge's own.
	if strings.Contains(tunnel, "forward_auth") {
		t.Fatalf("read links are not gated:\n%s", tunnel)
	}
}

func TestReadLinksUnapprovedExposeNothing(t *testing.T) {
	local, tunnel := renderDashboard(t, readLinkSet(false, false))
	if strings.Contains(local, "_homestead") {
		t.Fatalf("local site:\n%s", local)
	}
	if strings.Contains(tunnel, "_homestead") || strings.Contains(tunnel, "tasks") || strings.Contains(tunnel, "notes") {
		t.Fatalf("an unapproved app is routed on the public name:\n%s", tunnel)
	}
	// One approved app does not open the other.
	_, tunnel = renderDashboard(t, readLinkSet(true, false))
	if strings.Contains(tunnel, "notes") || !strings.Contains(tunnel, "/apps/tasks/_homestead/*") {
		t.Fatalf("only tasks should be routed:\n%s", tunnel)
	}
}

func TestReadLinksRemovalUnexposes(t *testing.T) {
	set := readLinkSet(true, true)
	_, before := renderDashboard(t, set)
	set.Routes[len(set.Routes)-2].ReadLinks = false
	_, after := renderDashboard(t, set)
	if !strings.Contains(before, "tasks:8081") || strings.Contains(after, "tasks") || !strings.Contains(after, "notes:8081") {
		t.Fatalf("removal did not un-expose exactly tasks:\n%s", after)
	}
}

// The public names carry the tunnel entry only: a local name never gets a
// route from the plan, and without tunnel names nothing is rendered.
func TestReadLinksNeedTheTunnelEntry(t *testing.T) {
	set := readLinkSet(true, true)
	local, _ := renderDashboard(t, set)
	if strings.Contains(local, "_homestead") {
		t.Fatalf("the local site grew a read-link handle:\n%s", local)
	}
	set.Routes[0].Tunnel = nil
	out, tunnel := renderDashboard(t, set)
	if tunnel != "" || strings.Contains(out, "_homestead") {
		t.Fatalf("read links rendered without a public name:\n%s", out)
	}
}

// A published app already answers whole; the read-link handle is not added
// beside it.
func TestReadLinksPublishedAppHasNoDuplicate(t *testing.T) {
	set := readLinkSet(true, false)
	set.Routes[len(set.Routes)-2].Published = true
	_, tunnel := renderDashboard(t, set)
	if strings.Contains(tunnel, "@readlink") || !strings.Contains(tunnel, "handle /apps/tasks/* {") {
		t.Fatalf("published app:\n%s", tunnel)
	}
}

// Caddy replaces the forwarded address on the tunnel entry with what
// Cloudflare saw; the local site never copies a client value. Together with
// trusted_proxies staying unset this is what the bridge's -trusted-proxies
// default (the edge over the private network) expects.
func TestReadLinkForwardedAddressIsTheEdgesOwn(t *testing.T) {
	local, tunnel := renderDashboard(t, readLinkSet(true, true))
	i := strings.Index(tunnel, "handle /apps/tasks/_homestead/*")
	j := strings.Index(tunnel[i:], "handle /apps/notes/_homestead/*")
	if j < 0 {
		j = len(tunnel) - i
	}
	seg := tunnel[i : i+j]
	if !strings.Contains(seg, "header_up X-Forwarded-For {http.request.header.Cf-Connecting-Ip}") ||
		!strings.Contains(seg, "header_up X-Forwarded-Proto https") ||
		!strings.Contains(seg, "header_up X-Forwarded-Host {http.request.host}") {
		t.Fatalf("the read-link handle does not replace the forwarded headers:\n%s", seg)
	}
	for _, out := range []string{local, tunnel} {
		if strings.Contains(out, "trusted_proxies") {
			t.Fatalf("trusted_proxies set:\n%s", out)
		}
	}
	for _, line := range strings.Split(local, "\n") {
		if strings.Contains(line, "X-Forwarded-For") && !strings.Contains(line, "{http.request.remote.host}") {
			t.Fatalf("local site copies a forwarded address: %q", line)
		}
	}
}

func TestReadLinksValidation(t *testing.T) {
	good := Route{Host: "tasks.lan", Target: Target{Host: "tasks", Port: 8081}, Exposure: ExposureLocal, TLS: TLSInternal,
		Path: "/apps/tasks/", ReadLinks: true}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	host := good
	host.Path = ""
	craft := good
	craft.CraftApp, craft.StripPrefix = "tasks", true
	strip := good
	strip.StripPrefix = true
	for name, r := range map[string]Route{"host route": host, "craft": craft, "stripped": strip} {
		if r.Validate() == nil {
			t.Errorf("%s: read links accepted", name)
		}
	}
}
