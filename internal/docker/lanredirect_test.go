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
		"redir /apps/komga https://homestead.example.com/apps/komga/ 308\n",
		"handle /apps/komga/* {\n\t\t\tredir https://homestead.example.com{uri} 308\n\t\t}\n",
		"redir /apps/nextcloud https://homestead.example.com/apps/nextcloud/ 308\n",
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
