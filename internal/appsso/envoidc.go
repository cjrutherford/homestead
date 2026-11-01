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
	lines = append(lines, fmt.Sprintf("The client secret is the app's generated secret %q, handed to the app as a file; it is never shown or logged.", a.cfg.Secret))
	if len(a.cfg.OnApply) > 0 {
		lines = append(lines, fmt.Sprintf("After the app restarts, this command runs inside it: %s.", strings.Join(a.cfg.OnApply, " ")))
	}
	return lines
}

func (a envOIDC) OffSettings() []string {
	lines := []string{fmt.Sprintf("Environment taken out of the app's override file: %s.", strings.Join(a.keys(), ", "))}
	if len(a.cfg.OnOff) > 0 {
		lines = append([]string{fmt.Sprintf("First, this command runs inside the app: %s.", strings.Join(a.cfg.OnOff, " "))}, lines...)
	}
	return lines
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
	if a.JavaStore() {
		return map[string]string{a.cfg.Trust: javaTrustOptions}
	}
	if a.combined() {
		return map[string]string{a.cfg.Trust: BundleFile}
	}
	return map[string]string{a.cfg.Trust: CAFile}
}

func (a envOIDC) combined() bool { return a.cfg.TrustBundle != "" }

// JavaStore reports that the trust variable carries JVM options naming a
// generated Java trust store instead of a PEM path (S411).
func (a envOIDC) JavaStore() bool { return a.cfg.TrustBundle == catalog.TrustBundleJava }

// javaTrustOptions is the value of the app's JDK_JAVA_OPTIONS (S411).
const javaTrustOptions = "-Djavax.net.ssl.trustStore=" + JavaStoreFile + " -Djavax.net.ssl.trustStorePassword=" + JavaStorePassword

// LegacyTrustEnv is the value an app set before it used the combined
// bundle (S409), so a re-apply can still remove it by value (S410).
func (a envOIDC) LegacyTrustEnv() map[string]string {
	if a.cfg.Trust == "" || !a.combined() || a.JavaStore() {
		return nil
	}
	return map[string]string{a.cfg.Trust: CAFile}
}

func privateIssuer(issuer string) bool {
	u, err := url.Parse(issuer)
	return err == nil && docker.IsPrivateName(docker.Hostname(u.Hostname()))
}

// CombinedBundleFor reports whether the app needs the combined trust
// bundle written for this issuer: it asks for one and the issuer is the
// appliance's own (S410).
func (a envOIDC) CombinedBundleFor(issuer string) bool {
	return a.combined() && privateIssuer(issuer)
}

// TrustEnvFor sets the trust variable only for the appliance's own
// issuer. Apps like Mealie use that file as their whole trust store, so
// pointing it at the appliance root breaks a public issuer, whose
// Let's Encrypt certificate chains to the image's default roots (S405).
func (a envOIDC) TrustEnvFor(issuer string) map[string]string {
	if !privateIssuer(issuer) {
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

// Enable runs the manifest's in-app command (S412), if it has one, once
// the stack is up with the sign-in environment.
func (a envOIDC) Enable(ctx context.Context, t Target, _ Settings) error {
	return a.run(ctx, t, a.cfg.OnApply)
}

// Disable runs the manifest's off command before the environment is
// taken away, so the app returns to its own login.
func (a envOIDC) Disable(ctx context.Context, t Target) error {
	return a.run(ctx, t, a.cfg.OnOff)
}

// run execs a static argv in the service's container as the manifest's
// user. The container was just recreated, so a failing exec is retried
// for the same wait the other adapters allow. Nothing secret travels.
func (a envOIDC) run(ctx context.Context, t Target, argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	var err error
	for i := 0; i < readyTries; i++ {
		if _, err = execIn(ctx, t.Container, a.cfg.ExecUser, "", argv...); err == nil {
			return nil
		}
		if i < readyTries-1 {
			if perr := pause(ctx); perr != nil {
				return perr
			}
		}
	}
	return fmt.Errorf("appsso: the in-app command %q failed in %s (finish the app's own setup first if it has not been done; the manual fallback is in its slice notes): %w",
		strings.Join(argv, " "), t.Container, err)
}

// Exclusive reports that sign-in replaces the app's own login (S412).
func (a envOIDC) Exclusive() bool { return a.cfg.Exclusive }

// PlanWarnings are the manifest's fixed warnings for the sign-in plan.
func (a envOIDC) PlanWarnings() []string { return append([]string(nil), a.cfg.Warnings...) }

// TrustMount is the read-only bind mount that lays the combined bundle
// over the image's own CA bundle (trustbundle: system, S412), for the
// appliance's own issuer only. TrustMountAny is the same mount whatever
// the issuer, so a stale one can be taken out.
func (a envOIDC) TrustMount(issuer string) string {
	if !privateIssuer(issuer) {
		return ""
	}
	return a.TrustMountAny()
}

func (a envOIDC) TrustMountAny() string {
	if a.cfg.TrustBundle != catalog.TrustBundleSystem {
		return ""
	}
	return "./" + CADir + "/" + bundleHostFn + ":" + a.cfg.TrustPath + ":ro"
}

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
