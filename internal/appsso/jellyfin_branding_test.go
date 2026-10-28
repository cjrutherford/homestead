package appsso

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"
)

const ownerBranding = `<?xml version="1.0" encoding="utf-8"?>
<BrandingOptions>
  <LoginDisclaimer>Welcome &lt;b&gt;home&lt;/b&gt;</LoginDisclaimer>
  <CustomCss>body{color:red}</CustomCss>
  <SplashscreenEnabled>true</SplashscreenEnabled>
</BrandingOptions>
`

func TestJellyfinStartPathFollowsThePathBase(t *testing.T) {
	for addr, want := range map[string]string{
		"https://homestead.lan/apps/jellyfin":  "/apps/jellyfin/sso/OID/start/authentik",
		"https://homestead.lan/apps/jellyfin/": "/apps/jellyfin/sso/OID/start/authentik",
		"https://jellyfin.lan":                 "/sso/OID/start/authentik",
		"":                                     "/sso/OID/start/authentik",
	} {
		if got := jellyfinStartPath(addr); got != want {
			t.Errorf("%q: %q, want %q", addr, got, want)
		}
	}
}

func TestBrandingInsertsIntoEmptyAndKeepsOwnerText(t *testing.T) {
	start := jellyfinStartPath("https://homestead.lan/apps/jellyfin")
	out, changed, err := brandingWith("", start)
	if err != nil || !changed || !strings.Contains(parsed(t, out).LoginDisclaimer, `href="/apps/jellyfin/sso/OID/start/authentik"`) ||
		!strings.Contains(parsed(t, out).LoginDisclaimer, "Sign in with Authentik") || !strings.Contains(parsed(t, out).CustomCSS, "homestead:sso") {
		t.Fatalf("empty: %v %v\n%s", err, changed, out)
	}
	out, _, err = brandingWith(ownerBranding, start)
	if p := parsed(t, out); err != nil || !strings.HasPrefix(p.LoginDisclaimer, "Welcome <b>home</b>\n"+ssoHTMLOpen) || !strings.HasPrefix(p.CustomCSS, "body{color:red}\n"+ssoCSSOpen) ||
		!strings.Contains(out, "<SplashscreenEnabled>true</SplashscreenEnabled>") || !strings.Contains(p.LoginDisclaimer, "Sign in with Authentik") {
		t.Fatalf("owner: %v\n%s", err, out)
	}
	// Re-applying changes nothing, even for another base.
	again, changed, err := brandingWith(out, start)
	if err != nil || changed || again != out {
		t.Fatalf("idempotent: %v %v", err, changed)
	}
	moved, _, _ := brandingWith(out, "/other/sso/OID/start/authentik")
	if pm := parsed(t, moved); strings.Count(pm.LoginDisclaimer, "homestead:sso") != 2 || strings.Count(pm.CustomCSS, "homestead:sso") != 2 ||
		strings.Contains(moved, "/apps/jellyfin/") || !strings.Contains(pm.LoginDisclaimer, "/other/sso/") {
		t.Fatalf("replace:\n%s", moved)
	}
}

func TestBrandingRemovalLeavesTheOwnerText(t *testing.T) {
	on, _, _ := brandingWith(ownerBranding, "/sso/OID/start/authentik")
	off, changed, err := brandingWith(on, "")
	if err != nil || !changed || strings.Contains(off, "homestead") || strings.Contains(off, "Authentik") {
		t.Fatalf("off: %v\n%s", err, off)
	}
	var b, o brandingOptions
	if err := xmlUnmarshal(off, &b); err != nil || xmlUnmarshal(ownerBranding, &o) != nil ||
		b.LoginDisclaimer != o.LoginDisclaimer || b.CustomCSS != o.CustomCSS || len(b.Other) != 1 {
		t.Fatalf("owner text changed: %+v vs %+v", b, o)
	}
	// Nothing to remove, or no file: nothing is written.
	if _, changed, _ := brandingWith(ownerBranding, ""); changed {
		t.Fatal("removal rewrote an untouched file")
	}
	if _, changed, _ := brandingWith("", ""); changed {
		t.Fatal("removal created the file")
	}
	// With only Homestead's text, it ends up empty.
	only, _, _ := brandingWith("", "/sso/OID/start/authentik")
	if cleared, _, _ := brandingWith(only, ""); strings.Contains(cleared, "homestead") {
		t.Fatalf("not cleared:\n%s", cleared)
	}
}

func TestJellyfinAdapterWritesAndRemovesTheButton(t *testing.T) {
	f := stubDocker(t)
	disk := ownerBranding
	f.fail = func(c call) (string, error) {
		if c.args[len(c.args)-1] != jellyfinBranding {
			return "", nil
		}
		if c.stdin == "" {
			return disk, nil
		}
		disk = c.stdin
		return "", nil
	}
	a := jellyfin{}
	target := Target{Container: "jellyfin-jellyfin", Address: "https://homestead.lan/apps/jellyfin"}
	if err := a.writeBranding(context.Background(), target, jellyfinStartPath(target.Address)); err != nil ||
		!strings.Contains(parsed(t, disk).LoginDisclaimer, `href="/apps/jellyfin/sso/OID/start/authentik"`) || !strings.Contains(disk, "Welcome") {
		t.Fatalf("on: %v\n%s", err, disk)
	}
	if err := a.writeBranding(context.Background(), target, ""); err != nil || strings.Contains(disk, "Authentik") || !strings.Contains(disk, "body{color:red}") {
		t.Fatalf("off: %v\n%s", err, disk)
	}
}

func xmlUnmarshal(s string, v any) error { return xml.Unmarshal([]byte(s), v) }

func parsed(t *testing.T, doc string) brandingOptions {
	t.Helper()
	var b brandingOptions
	if err := xmlUnmarshal(doc, &b); err != nil {
		t.Fatal(err)
	}
	return b
}
