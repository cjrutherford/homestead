package docker

import (
	"strings"
	"testing"
)

// S498: a published app at a path redirects the local names to the public
// one; unpublished apps, craft apps and the sign-in path keep the LAN.
func lanRedirectSet() RouteSet {
	set := pathSet()
	set.Routes = append(set.Routes,
		Route{Host: "komga.lan", Target: Target{Host: "komga", Port: 25600}, Exposure: ExposureLocal, TLS: TLSInternal,
			Path: "/apps/komga/", Published: true},
		Route{Host: "figcraft.lan", Target: Target{Host: "figcraft", Port: 8080}, Exposure: ExposureLocal, TLS: TLSInternal,
			Path: "/apps/figcraft/", StripPrefix: true, CraftApp: "figcraft", Published: true})
	return set
}

func TestPublishedPathAppRedirectsLANToPublic(t *testing.T) {
	local, tunnel := renderDashboard(t, lanRedirectSet())
	for _, want := range []string{
		"redir /apps/komga https://homestead.example.com/apps/komga/ 307\n",
		"handle /apps/komga/* {\n\t\t\tredir https://homestead.example.com{uri} 307\n\t\t}\n",
		"redir /apps/nextcloud https://homestead.example.com/apps/nextcloud/ 307\n",
	} {
		if !strings.Contains(local, want) {
			t.Errorf("local lacks %q:\n%s", want, local)
		}
	}
	// Loop guard: the tunnel entry never redirects, it serves the app.
	if strings.Contains(tunnel, "redir https://") {
		t.Errorf("tunnel entry redirects:\n%s", tunnel)
	}
	if !strings.Contains(tunnel, "reverse_proxy komga:25600") {
		t.Errorf("tunnel entry does not serve komga:\n%s", tunnel)
	}
	// Craft apps and the sign-in path stay open on the LAN.
	if strings.Contains(local, "redir /apps/figcraft https://") || strings.Contains(local, "redir /apps/auth https://") {
		t.Errorf("craft or sign-in path redirected:\n%s", local)
	}
	if !strings.Contains(local, "reverse_proxy figcraft:8080") {
		t.Errorf("craft LAN not served:\n%s", local)
	}
}

func TestUnpublishedPathAppKeepsLAN(t *testing.T) {
	set := lanRedirectSet()
	set.Routes[len(set.Routes)-2].Published = false
	local, tunnel := renderDashboard(t, set)
	if strings.Contains(local, "komga.lan") || strings.Contains(local, "redir /apps/komga https://") ||
		strings.Contains(local, "redir https://homestead.example.com{uri}\n\t}\n\thandle /apps/nextcloud") {
		t.Errorf("unpublished komga redirects:\n%s", local)
	}
	if !strings.Contains(local, "reverse_proxy komga:25600") {
		t.Errorf("unpublished komga not served on the LAN:\n%s", local)
	}
	if strings.Contains(tunnel, "komga") {
		t.Errorf("unpublished komga on the tunnel:\n%s", tunnel)
	}
}

// S503: every redirect Homestead renders for the gate and for published path
// apps is temporary (307), so a browser that followed one does not keep
// jumping to a public name after the app is unpublished. No 308 is kept: the
// bare-path to trailing-slash redirect is 307 too, because it exists only
// while the route does.
func TestRenderedRedirectsAreTemporary(t *testing.T) {
	sets := []RouteSet{lanRedirectSet(), gatedPathSet(true), gatedPathSet(false)}
	for _, set := range sets {
		local, tunnel := renderDashboard(t, set)
		for _, out := range []string{local, tunnel} {
			if strings.Contains(out, " 308") {
				t.Errorf("rendered Caddy output has a permanent redirect:\n%s", out)
			}
		}
	}
}
