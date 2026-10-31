package appsso

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

// EnvSetter is an adapter that configures the app through plain
// environment variables (S402). Definition merges the result into the
// service's override wiring on enable and removes exactly those keys on
// disable. Values never carry the client secret.
type EnvSetter interface {
	Env(s Settings) map[string]string
}

// envOIDC is the generic adapter for apps configured by environment
// alone. The manifest's env block is the whole configuration: the app
// has nothing to run in the container, the rewire step writes the keys.
type envOIDC struct{ cfg catalog.EnvOIDC }

func (a envOIDC) RedirectURIs(origins []string) []string {
	var out []string
	for _, o := range origins {
		for _, p := range a.cfg.RedirectPaths {
			out = append(out, o+p)
		}
	}
	return out
}

func (a envOIDC) SignInURL(origin string) string { return origin + a.cfg.SignInPath }

func (a envOIDC) keys() []string {
	keys := make([]string, 0, len(a.cfg.Set))
	for k := range a.cfg.Set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (a envOIDC) OnSettings(issuer, clientID string) []string {
	lines := []string{fmt.Sprintf("Environment set in the app's override file: %s (issuer %s, client id %s).", strings.Join(a.keys(), ", "), issuer, clientID)}
	return append(lines, fmt.Sprintf("The client secret is the app's generated secret %q, handed to the app as a file; it is never shown or logged.", a.cfg.Secret))
}

func (a envOIDC) OffSettings() []string {
	return []string{fmt.Sprintf("Environment taken out of the app's override file: %s.", strings.Join(a.keys(), ", "))}
}

func (a envOIDC) uses(name string) bool {
	for _, t := range a.cfg.Set {
		if catalog.EnvTemplateUses(t, name) {
			return true
		}
	}
	return false
}

func (a envOIDC) Roles(g GroupNames) RoleMapping {
	if !a.uses("admin_group") {
		return RoleMapping{Note: "its sign-in setting names no admin group."}
	}
	return RoleMapping{Mapped: true,
		Note: fmt.Sprintf("Members of %s become administrators in the app through its admin group setting, everyone else in %s a user.", g.Admins, g.Users)}
}

func (a envOIDC) TrustEnv() map[string]string {
	if a.cfg.Trust == "" {
		return nil
	}
	return map[string]string{a.cfg.Trust: CAFile}
}

// TrustEnvFor sets the trust variable only for the appliance's own
// issuer. Apps like Mealie use that file as their whole trust store, so
// pointing it at the appliance root breaks a public issuer, whose
// Let's Encrypt certificate chains to the image's default roots (S405).
func (a envOIDC) TrustEnvFor(issuer string) map[string]string {
	if u, err := url.Parse(issuer); err != nil || !docker.IsPrivateName(docker.Hostname(u.Hostname())) {
		return nil
	}
	return a.TrustEnv()
}

// Env renders the manifest's templates.
func (a envOIDC) Env(s Settings) map[string]string {
	vals := map[string]string{"issuer": s.Issuer, "authentik_base": authentikBase(s.Issuer), "client_id": s.ClientID,
		"admin_group": s.Groups.Admins, "user_group": s.Groups.Users}
	out := make(map[string]string, len(a.cfg.Set))
	for k, t := range a.cfg.Set {
		out[k] = catalog.RenderEnvTemplate(t, vals)
	}
	return out
}

func (envOIDC) Enable(context.Context, Target, Settings) error { return nil }
func (envOIDC) Disable(context.Context, Target) error          { return nil }

// authentikBase is Authentik's address without the application path: the
// issuer https://auth.example/application/o/<slug>/ gives
// https://auth.example. An issuer without that path is returned without
// a trailing slash (S409).
func authentikBase(issuer string) string {
	if i := strings.Index(issuer, "/application/o/"); i >= 0 {
		return issuer[:i]
	}
	return strings.TrimSuffix(issuer, "/")
}
