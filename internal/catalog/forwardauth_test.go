package catalog

import (
	"regexp"
	"strings"
	"testing"
)

func forwardManifest() Manifest {
	m := validManifest()
	m.SSO = SSODeclaration{Class: SSOUnsupported, ForwardAuth: &ForwardAuth{
		Service: "jellyfin", UserHeader: "X-Authentik-Username", CopyHeaders: []string{"X-Authentik-Username"},
		Bypass: []ForwardBypass{{Path: `^/rest/.*`, Reason: "api"}},
		Env:    map[string]string{"APP_USER_HEADER": "{{user_header}}", "APP_TRUSTED": "{{edge_source}}"},
		Admin:  "local",
	}}
	return m
}

func TestForwardAuthValidation(t *testing.T) {
	if err := forwardManifest().Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	cases := map[string]struct {
		mutate func(*Manifest)
		want   string
	}{
		"with oidc":           {func(m *Manifest) { m.SSO.OIDC = &OIDCAdapter{Service: "jellyfin", Adapter: AdapterImmich} }, "oidc protocol"},
		"wrong class":         {func(m *Manifest) { m.SSO.Class = SSOOptional }, "at least one protocol"},
		"unknown service":     {func(m *Manifest) { m.SSO.ForwardAuth.Service = "ghost" }, "must be a service"},
		"bad header":          {func(m *Manifest) { m.SSO.ForwardAuth.CopyHeaders = []string{"Remote-User"} }, "X-Authentik-*"},
		"header not copied":   {func(m *Manifest) { m.SSO.ForwardAuth.UserHeader = "X-Authentik-Email" }, "one of copyheaders"},
		"bypass whole site":   {func(m *Manifest) { m.SSO.ForwardAuth.Bypass[0].Path = `^/.*` }, "bypass"},
		"bypass outpost":      {func(m *Manifest) { m.SSO.ForwardAuth.Bypass[0].Path = `^/outpost\.goauthentik\.io/.*` }, "bypass"},
		"bypass no reason":    {func(m *Manifest) { m.SSO.ForwardAuth.Bypass[0].Reason = " " }, "needs a reason"},
		"secret env":          {func(m *Manifest) { m.SSO.ForwardAuth.Env["APP_TOKEN"] = "x" }, "not a secret"},
		"unknown placeholder": {func(m *Manifest) { m.SSO.ForwardAuth.Env["APP_X"] = "{{secret}}" }, "unknown placeholder"},
		"admin mapped":        {func(m *Manifest) { m.SSO.ForwardAuth.Admin = "group" }, "must be local"},
		"unknown access":      {func(m *Manifest) { m.SSO.ForwardAuth.Access = "everyone" }, "admin-only"},
		"unknown reader":      {func(m *Manifest) { m.SSO.ForwardAuth.AccountsFrom = "mongo" }, "unknown"},
	}
	for name, c := range cases {
		m := forwardManifest()
		c.mutate(&m)
		err := m.Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestNavidromeForwardAuthPin pins the gate Navidrome ships with (S418,
// ADR-055 §4): the bypass paths and why, the header it trusts, and the
// version the reader and the trust settings were checked against. A bump
// of either must re-check the header names, the share paths and the
// users table.
func TestNavidromeForwardAuthPin(t *testing.T) {
	m, err := repoCatalog(t).Get("navidrome")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "0.64.2" {
		t.Fatalf("navidrome %s: re-check the extauth settings, the share paths and the user table, then update this pin", m.Version)
	}
	fa := m.SSO.ForwardAuth
	if fa == nil || m.SSO.OIDC != nil || fa.Service != "navidrome" || fa.UserHeader != "X-Authentik-Username" ||
		len(fa.CopyHeaders) != 1 || fa.Admin != "local" || fa.AccountsFrom != AccountsNavidromeSQLite {
		t.Fatalf("forwardauth = %+v", fa)
	}
	var paths []string
	for _, b := range fa.Bypass {
		paths = append(paths, b.Path)
		if b.Reason == "" {
			t.Fatalf("bypass %s has no reason", b.Path)
		}
	}
	if got := strings.Join(paths, " "); got != `^/rest/.* ^/share/.* ^/app/(assets|static)/.*` {
		t.Fatalf("bypass = %s", got)
	}
	if fa.Env["ND_EXTAUTH_USERHEADER"] != "{{user_header}}" || fa.Env["ND_EXTAUTH_TRUSTEDSOURCES"] != "{{edge_source}}" || len(fa.Env) != 2 {
		t.Fatalf("env = %v", fa.Env)
	}
}

// TestKumaForwardAuthPin pins the gate Uptime Kuma ships with (S419, ADR-055
// §4): admins only, no header and no trust settings (Kuma keeps its own login),
// the exact open paths, and no host port published, because the gate and
// Kuma's login are the only way in. A version bump must re-check the status
// page paths and the metrics/socket paths that stay gated.
func TestKumaForwardAuthPin(t *testing.T) {
	m, err := repoCatalog(t).Get("uptime-kuma")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "2.5.7" {
		t.Fatalf("uptime-kuma %s: re-check the status page requests and the gated paths, then update this pin", m.Version)
	}
	fa := m.SSO.ForwardAuth
	if fa == nil || m.SSO.OIDC != nil || fa.Service != "uptime-kuma" || fa.Access != AccessAdminOnly ||
		fa.UserHeader != "" || len(fa.CopyHeaders) != 0 || len(fa.Env) != 0 || fa.AccountsFrom != "" {
		t.Fatalf("forwardauth = %+v", fa)
	}
	var paths []string
	for _, b := range fa.Bypass {
		paths = append(paths, b.Path)
		if b.Reason == "" {
			t.Fatalf("bypass %s has no reason", b.Path)
		}
	}
	if got := strings.Join(paths, " "); got != `^/api/push/.* ^/status/.* ^/api/status-page/.* ^/api/badge/.* ^/assets/.* ^/upload/.* ^/icon\.svg$` {
		t.Fatalf("bypass = %s", got)
	}
	for _, never := range []string{"/metrics", "/dashboard", "/socket.io/", "/status-page", "/manage-status-page", "/settings", "/"} {
		for _, re := range paths {
			if regexp.MustCompile(re).MatchString(never) {
				t.Errorf("bypass %s opens %s", re, never)
			}
		}
	}
	if len(m.Requirements.Ports) > 0 {
		t.Fatalf("Kuma publishes host ports %v: it must be reachable only through the edge (ADR-055 section 5)", m.Requirements.Ports)
	}
}

func TestForwardAuthRequiresHostBlockParses(t *testing.T) {
	m := forwardManifest()
	m.SSO.ForwardAuth.RequiresHostBlock = true
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}
