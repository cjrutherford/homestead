package sso

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func proxySpec() ProxySpec {
	return ProxySpec{
		ProviderName: "Homestead: Navidrome", AppName: "Navidrome", AppSlug: "homestead-navidrome",
		ExternalHost: "https://navidrome.lan", AuthorizationFlow: DefaultAuthorizationFlow, InvalidationFlow: DefaultInvalidationFlow,
		Bypass: []string{`^/rest/.*`, `^/share/.*`}, AuthentikHost: "https://homestead.lan/apps/auth/",
		Access: &Access{Groups: []string{"homestead-navidrome-users", "homestead-navidrome-admins"}, Bindings: []string{"homestead-navidrome-users", "homestead-navidrome-admins"}},
	}
}

func proxyAPI(f *fakeAuthentik) *API { return &API{Base: f.srv.URL, Token: apiToken, Client: f.client} }

func TestReconcileProxyCreatesAndMergesOutpost(t *testing.T) {
	f := newFakeAuthentik(t)
	var steps []string
	if err := proxyAPI(f).ReconcileProxy(context.Background(), proxySpec(), func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if f.proxy["mode"] != "forward_single" || f.proxy["external_host"] != "https://navidrome.lan" {
		t.Fatalf("provider: %v", f.proxy)
	}
	if f.proxy["skip_path_regex"] != "^/rest/.*\n^/share/.*" || f.proxy["intercept_header_auth"] != false || f.proxy["basic_auth_enabled"] != false {
		t.Fatalf("provider paths/flags: %v", f.proxy)
	}
	if f.app == nil || f.app["slug"] != "homestead-navidrome" {
		t.Fatalf("application: %v", f.app)
	}
	providers, _ := json.Marshal(f.outpost["providers"])
	if !strings.Contains(string(providers), "5") || !strings.Contains(string(providers), "42") {
		t.Fatalf("the outpost must keep provider 5 and add the new one: %s", providers)
	}
	cfg := f.outpost["config"].(map[string]any)
	if cfg["authentik_host"] != "https://homestead.lan/apps/auth/" || cfg["log_level"] != "info" {
		t.Fatalf("outpost config must set the host and keep other keys: %v", cfg)
	}
	if len(f.bindings) != 2 {
		t.Fatalf("bindings: %v", f.bindings)
	}
	// Idempotent.
	posts, patches := f.posts, f.patches
	steps = nil
	if err := proxyAPI(f).ReconcileProxy(context.Background(), proxySpec(), func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if f.posts != posts || f.patches != patches {
		t.Fatalf("second run wrote: posts %d->%d patches %d->%d (%v)", posts, f.posts, patches, f.patches, steps)
	}
}

func TestReconcileProxyNeedsEmbeddedOutpost(t *testing.T) {
	f := newFakeAuthentik(t)
	f.noOutpost = true
	if err := proxyAPI(f).ReconcileProxy(context.Background(), proxySpec(), func(string) {}); err == nil {
		t.Fatal("no outpost must refuse before creating anything")
	}
	if f.proxy != nil || f.app != nil {
		t.Fatal("objects created without an outpost")
	}
}

func TestRemoveProxyTakesOutpostFirstAndKeepsOthers(t *testing.T) {
	f := newFakeAuthentik(t)
	api := proxyAPI(f)
	if err := api.ReconcileProxy(context.Background(), proxySpec(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	f.requests = nil
	if err := api.RemoveProxy(context.Background(), proxySpec(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	patch, del := -1, -1
	for i, r := range f.requests {
		if strings.HasPrefix(r, "PATCH /api/v3/outposts/") && patch < 0 {
			patch = i
		}
		if strings.HasPrefix(r, "DELETE /api/v3/providers/proxy/") {
			del = i
		}
	}
	if patch < 0 || del < 0 || patch > del {
		t.Fatalf("outpost membership must go before the provider: %v", f.requests)
	}
	providers, _ := json.Marshal(f.outpost["providers"])
	if string(providers) != "[5]" {
		t.Fatalf("other providers must stay: %s", providers)
	}
	if f.proxy != nil || f.app != nil || f.groups["homestead-navidrome-users"] != nil {
		t.Fatal("objects left behind")
	}
	if f.outpost["config"].(map[string]any)["authentik_host"] != "https://homestead.lan/apps/auth/" {
		t.Fatal("the shared outpost address must be left alone")
	}
	// Removing again is fine.
	if err := api.RemoveProxy(context.Background(), proxySpec(), func(string) {}); err != nil {
		t.Fatal(err)
	}
}

func TestProxyErrorsNeverLeakTheToken(t *testing.T) {
	f := newFakeAuthentik(t)
	f.badToken = true
	err := proxyAPI(f).ReconcileProxy(context.Background(), proxySpec(), func(string) {})
	if err == nil || strings.Contains(err.Error(), apiToken) {
		t.Fatalf("err = %v", err)
	}
}
