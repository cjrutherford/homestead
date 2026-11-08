package sso

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Proxy provider and embedded outpost (S418, ADR-055). A forward-auth
// app is one proxy provider in single-application mode, one application
// bound to the app's groups, and that provider's membership of the
// embedded outpost. Nothing here touches the OAuth2 code.

// EmbeddedOutpostManaged is the managed key Authentik gives its built-in
// outpost.
const EmbeddedOutpostManaged = "goauthentik.io/outposts/embedded"

// ProxySpec is the desired Authentik side of a gated app. It holds no
// secret.
type ProxySpec struct {
	ProviderName string `json:"provider_name"`
	AppName      string `json:"application_name"`
	AppSlug      string `json:"application_slug"`
	// ExternalHost is the app's canonical address, https://<name>. The
	// outpost matches a request to the provider by its host.
	ExternalHost      string `json:"external_host"`
	AuthorizationFlow string `json:"authorization_flow"`
	InvalidationFlow  string `json:"invalidation_flow"`
	// Bypass are the provider's "unauthenticated paths": regexes over the
	// request path.
	Bypass []string `json:"bypass"`
	// AuthentikHost is where the browser is sent to sign in: the public
	// Authentik address when it answers, otherwise the local one. It is
	// set on the embedded outpost and shared by every gated app (ADR-055
	// §8). Empty leaves the outpost's value alone.
	AuthentikHost string  `json:"authentik_host,omitempty"`
	Access        *Access `json:"access,omitempty"`
}

// remoteProxy is the proxy provider as Authentik returns it.
type remoteProxy struct {
	PK            int    `json:"pk"`
	Name          string `json:"name"`
	Mode          string `json:"mode"`
	ExternalHost  string `json:"external_host"`
	SkipPathRegex string `json:"skip_path_regex"`
	AuthFlow      string `json:"authorization_flow"`
	InvalidFlow   string `json:"invalidation_flow"`
	Intercept     bool   `json:"intercept_header_auth"`
	BasicAuth     bool   `json:"basic_auth_enabled"`
	CookieDomain  string `json:"cookie_domain"`
}

// RemoteOutpost is an outpost instance as Authentik returns it.
type RemoteOutpost struct {
	PK        string         `json:"pk"`
	Name      string         `json:"name"`
	Providers []int          `json:"providers"`
	Config    map[string]any `json:"config"`
}

func (a *API) findProxy(ctx context.Context, name string) (*remoteProxy, error) {
	var out listResult[remoteProxy]
	if err := a.do(ctx, http.MethodGet, "/api/v3/providers/proxy/?name="+url.QueryEscape(name), nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Results {
		if out.Results[i].Name == name {
			return &out.Results[i], nil
		}
	}
	return nil, nil
}

// EmbeddedOutpost returns the built-in outpost.
func (a *API) EmbeddedOutpost(ctx context.Context) (*RemoteOutpost, error) {
	var out listResult[RemoteOutpost]
	if err := a.do(ctx, http.MethodGet, "/api/v3/outposts/instances/?managed="+url.QueryEscape(EmbeddedOutpostManaged), nil, &out); err != nil {
		return nil, err
	}
	if len(out.Results) == 0 {
		return nil, fmt.Errorf("Authentik has no embedded outpost: forward-auth needs it")
	}
	return &out.Results[0], nil
}

func (r remoteProxy) matches(s ProxySpec, flow, invalid string) bool {
	return r.Mode == "forward_single" && r.ExternalHost == s.ExternalHost &&
		r.SkipPathRegex == strings.Join(s.Bypass, "\n") && r.AuthFlow == flow && r.InvalidFlow == invalid &&
		!r.Intercept && !r.BasicAuth && r.CookieDomain == ""
}

// ReconcileProxy creates or updates the proxy provider, the application,
// its group bindings and the embedded outpost's membership so they match
// spec. The outpost keeps every other provider it lists. Idempotent.
func (a *API) ReconcileProxy(ctx context.Context, spec ProxySpec, step func(string)) error {
	if spec.Access != nil {
		if err := spec.Access.Validate(); err != nil {
			return err
		}
	}
	flow, err := a.flowPK(ctx, spec.AuthorizationFlow)
	if err != nil {
		return err
	}
	invalid, err := a.flowPK(ctx, spec.InvalidationFlow)
	if err != nil {
		return err
	}
	outpost, err := a.EmbeddedOutpost(ctx)
	if err != nil {
		return err
	}
	body := map[string]any{
		"name": spec.ProviderName, "authorization_flow": flow, "invalidation_flow": invalid,
		"mode": "forward_single", "external_host": spec.ExternalHost,
		"skip_path_regex":       strings.Join(spec.Bypass, "\n"),
		"intercept_header_auth": false, "basic_auth_enabled": false, "cookie_domain": "",
	}
	prov, err := a.findProxy(ctx, spec.ProviderName)
	if err != nil {
		return err
	}
	var provPK int
	switch {
	case prov == nil:
		var created remoteProxy
		if err := a.do(ctx, http.MethodPost, "/api/v3/providers/proxy/", body, &created); err != nil {
			return err
		}
		provPK = created.PK
		step("created the proxy provider")
	case prov.matches(spec, flow, invalid):
		provPK = prov.PK
		step("proxy provider already matches")
	default:
		if err := a.do(ctx, http.MethodPatch, fmt.Sprintf("/api/v3/providers/proxy/%d/", prov.PK), body, nil); err != nil {
			return err
		}
		provPK = prov.PK
		step("updated the proxy provider")
	}
	app, err := a.FindApplication(ctx, spec.AppSlug)
	if err != nil {
		return err
	}
	appBody := map[string]any{"name": spec.AppName, "slug": spec.AppSlug, "provider": provPK,
		"meta_launch_url": spec.ExternalHost + "/", "open_in_new_tab": false}
	switch {
	case app == nil:
		if err := a.do(ctx, http.MethodPost, "/api/v3/core/applications/", appBody, nil); err != nil {
			return err
		}
		step("created the application")
	case app.Provider != nil && *app.Provider == provPK:
		step("application already matches")
	default:
		if err := a.do(ctx, http.MethodPatch, "/api/v3/core/applications/"+url.PathEscape(spec.AppSlug)+"/", appBody, nil); err != nil {
			return err
		}
		step("rebound the application")
	}
	if spec.Access != nil {
		if app == nil || app.PK == "" {
			if app, err = a.FindApplication(ctx, spec.AppSlug); err != nil {
				return err
			}
		}
		if app == nil {
			return fmt.Errorf("Authentik has no application %q after creating it", spec.AppSlug)
		}
		if err := a.reconcileAccess(ctx, *spec.Access, app.PK, step); err != nil {
			return err
		}
	}
	return a.joinOutpost(ctx, outpost, provPK, spec.AuthentikHost, step)
}

// joinOutpost adds the provider to the outpost's providers (merging, never
// replacing) and, when host is set, sets authentik_host in its config
// (merging, never replacing other config keys).
func (a *API) joinOutpost(ctx context.Context, o *RemoteOutpost, provPK int, host string, step func(string)) error {
	patch := map[string]any{}
	if !hasPK(o.Providers, provPK) {
		providers := append(append([]int(nil), o.Providers...), provPK)
		sort.Ints(providers)
		patch["providers"] = providers
	}
	if host != "" {
		if cur, _ := o.Config["authentik_host"].(string); cur != host {
			config := map[string]any{}
			for k, v := range o.Config {
				config[k] = v
			}
			config["authentik_host"] = host
			patch["config"] = config
		}
	}
	if len(patch) == 0 {
		step("outpost already includes the provider")
		return nil
	}
	if err := a.do(ctx, http.MethodPatch, "/api/v3/outposts/instances/"+url.PathEscape(o.PK)+"/", patch, nil); err != nil {
		return err
	}
	if _, ok := patch["providers"]; ok {
		step("added the provider to the embedded outpost")
	}
	if _, ok := patch["config"]; ok {
		step("set the outpost's Authentik address")
	}
	return nil
}

// RemoveProxy takes the provider out of the embedded outpost first, then
// deletes the application, the provider and the app's own groups. Objects
// already gone are fine; the outpost's other providers and its Authentik
// address are left alone.
func (a *API) RemoveProxy(ctx context.Context, spec ProxySpec, step func(string)) error {
	prov, err := a.findProxy(ctx, spec.ProviderName)
	if err != nil {
		return err
	}
	if prov != nil {
		outpost, err := a.EmbeddedOutpost(ctx)
		if err != nil {
			return err
		}
		if hasPK(outpost.Providers, prov.PK) {
			var keep []int
			for _, pk := range outpost.Providers {
				if pk != prov.PK {
					keep = append(keep, pk)
				}
			}
			if keep == nil {
				keep = []int{}
			}
			if err := a.do(ctx, http.MethodPatch, "/api/v3/outposts/instances/"+url.PathEscape(outpost.PK)+"/", map[string]any{"providers": keep}, nil); err != nil {
				return err
			}
			step("removed the provider from the embedded outpost")
		} else {
			step("provider was not in the embedded outpost")
		}
	}
	app, err := a.FindApplication(ctx, spec.AppSlug)
	if err != nil {
		return err
	}
	if app == nil {
		step("application already gone")
	} else {
		if err := a.do(ctx, http.MethodDelete, "/api/v3/core/applications/"+url.PathEscape(spec.AppSlug)+"/", nil, nil); err != nil {
			return err
		}
		step("deleted the application")
	}
	if prov == nil {
		step("proxy provider already gone")
	} else {
		if err := a.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v3/providers/proxy/%d/", prov.PK), nil, nil); err != nil {
			return err
		}
		step("deleted the proxy provider")
	}
	if spec.Access != nil {
		return a.removeAccess(ctx, *spec.Access, step)
	}
	return nil
}

// OutpostHost reads the embedded outpost's Authentik address, for the
// issuer-move re-plan and status.
func (a *API) OutpostHost(ctx context.Context) (string, error) {
	o, err := a.EmbeddedOutpost(ctx)
	if err != nil {
		return "", err
	}
	h, _ := o.Config["authentik_host"].(string)
	return h, nil
}

// RemoveAccess deletes an app's own groups and claim mapping, leaving the
// provider and application alone: one app leaves the shared gate while the
// others stay (ADR-062).
func (a *API) RemoveAccess(ctx context.Context, x Access, step func(string)) error {
	return a.removeAccess(ctx, x, step)
}
