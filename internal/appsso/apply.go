package appsso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"homestead/internal/catalog"
	"homestead/internal/operation"
	"homestead/internal/sso"
)

// Authentik is the slice of the admin API apply uses (sso.API).
type Authentik interface {
	Version(ctx context.Context) (string, error)
	Reconcile(ctx context.Context, spec sso.Spec, secret string, step func(string)) error
	Remove(ctx context.Context, spec sso.Spec, step func(string)) error
}

// Runtime is what apply touches on the appliance.
type Runtime struct {
	Records Records
	Secrets sso.SecretStore
	// Audit records names and outcomes, never a secret or token.
	Audit func(actor, action, outcome, detail string)
	// StackDir is the installed stack's directory.
	StackDir func(pkg string) string
	// RootPEM is the appliance root (ADR-030), public part only.
	RootPEM func() ([]byte, error)
	// Fingerprint re-baselines drift after the override file changes.
	Fingerprint func(pkg, dir string) error
}

func (rt Runtime) audit(actor, action, outcome, detail string) {
	if rt.Audit != nil {
		rt.Audit(actor, action, outcome, detail)
	}
}

// up brings the stack up after its override changed, stubbed in tests.
var up = catalog.UpStack

func newSecret() (string, error) {
	b := make([]byte, 36)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("appsso: entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// wiring is the override entries for the service that talks to Authentik.
func wiring(m catalog.Manifest, adapter Adapter, dashboard sso.Spec) catalog.SignInWiring {
	return catalog.SignInWiring{
		Service:    m.SSO.OIDC.Service,
		ExtraHosts: []string{hostOf(dashboard.AuthentikURL) + ":host-gateway"},
		Volumes:    []string{"./" + CADir + ":" + CAMount + ":ro"},
		Env:        adapter.TrustEnv(),
	}
}

// Definition is the app.sign-in operation for an approved plan: on
// creates or updates the Authentik objects, seals the client secret,
// wires the app to reach and trust Authentik, configures it through its
// adapter and records the result; off undoes each of those.
func Definition(rt Runtime, plan Plan, m catalog.Manifest, dashboard sso.Spec, api Authentik, actor string) (operation.Definition, error) {
	if m.SSO.OIDC == nil {
		return operation.Definition{}, unavailable("%s has no Authentik sign-in adapter", m.Title)
	}
	adapter, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		return operation.Definition{}, err
	}
	dir := rt.StackDir(m.Name)
	spec := plan.Spec(dashboard)
	target := Target{Manifest: m, Container: container(m, m.SSO.OIDC.Service)}
	if origins := originsOf(plan.RedirectURIs); len(origins) > 0 {
		target.Address = origins[0]
	}
	wire := wiring(m, adapter, dashboard)
	failed := func(what string, err error) error {
		rt.audit(actor, "app.sign-in", "error", fmt.Sprintf("%s: %s failed", m.Name, what))
		return err
	}
	rewire := func(ctx context.Context, on bool) error {
		caDir := filepath.Join(dir, CADir)
		if on {
			pem, err := rt.RootPEM()
			if err != nil || len(pem) == 0 {
				return fmt.Errorf("appsso: the appliance root is unavailable: %v", err)
			}
			if err := os.MkdirAll(caDir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(caDir, caHostFn), pem, 0o644); err != nil {
				return err
			}
		}
		if err := catalog.ApplySignInWiring(dir, wire, on); err != nil {
			return err
		}
		if !on {
			if err := os.RemoveAll(caDir); err != nil {
				return err
			}
		}
		if rt.Fingerprint != nil {
			if err := rt.Fingerprint(m.Name, dir); err != nil {
				return err
			}
		}
		return up(ctx, dir, m.Name)
	}
	reach := operation.StepDef{Name: "reach-authentik", Run: func(ctx context.Context) error {
		if _, err := api.Version(ctx); err != nil {
			return failed("reach Authentik", err)
		}
		return nil
	}}
	def := operation.Definition{Type: "app.sign-in"}
	if !plan.Enabled {
		def.Steps = []operation.StepDef{
			reach,
			{Name: "configure-app", Run: func(ctx context.Context) error {
				if err := adapter.Disable(ctx, target); err != nil {
					return failed("turn sign-in off in the app", err)
				}
				return nil
			}},
			{Name: "remove-from-authentik", Run: func(ctx context.Context) error {
				var steps []string
				if err := api.Remove(ctx, spec, func(s string) { steps = append(steps, s) }); err != nil {
					return failed("remove from Authentik", err)
				}
				rt.audit(actor, "app.sign-in.authentik", "ok", m.Name+": "+strings.Join(steps, "; "))
				return nil
			}},
			{Name: "unwire", Run: func(ctx context.Context) error { return rewire(ctx, false) }},
			{Name: "forget", Run: func(context.Context) error {
				if err := rt.Secrets.Delete(SecretName(m.Name)); err != nil {
					return fmt.Errorf("forget the client secret: %w", err)
				}
				if err := rt.Records.Delete(m.Name); err != nil {
					return err
				}
				rt.audit(actor, "app.sign-in.off", "ok", fmt.Sprintf("%s: sign-in with Authentik off; provider %q and application %s deleted", m.Name, plan.Provider, plan.ClientID))
				return nil
			}},
		}
		return def, nil
	}
	var secret string
	def.Steps = []operation.StepDef{reach}
	if waiter, ok := adapter.(SetUpWaiter); ok {
		// Before anything changes: an app still installing itself is
		// waited for, and one its owner must set up is refused with
		// guidance (S312).
		def.Steps = append(def.Steps, operation.StepDef{Name: "app-set-up", Run: func(ctx context.Context) error {
			if err := waiter.WaitSetUp(ctx, target); err != nil {
				return failed("wait for the app to be set up", err)
			}
			return nil
		}})
	}
	def.Steps = append(def.Steps, []operation.StepDef{
		{Name: "client-secret", Run: func(context.Context) error {
			s, err := rt.Secrets.Get(SecretName(m.Name))
			if err != nil || s == "" {
				if s, err = newSecret(); err != nil {
					return err
				}
				if err := rt.Secrets.SetSystem(SecretName(m.Name), s); err != nil {
					return fmt.Errorf("seal the client secret: %w", err)
				}
			}
			secret = s
			return nil
		}},
		{Name: "configure-authentik", Run: func(ctx context.Context) error {
			var steps []string
			if err := api.Reconcile(ctx, spec, secret, func(s string) { steps = append(steps, s) }); err != nil {
				return failed("configure Authentik", err)
			}
			rt.audit(actor, "app.sign-in.authentik", "ok", m.Name+": "+strings.Join(steps, "; "))
			return nil
		}},
		{Name: "wire", Run: func(ctx context.Context) error {
			if err := rewire(ctx, true); err != nil {
				return failed("wire the app to Authentik", err)
			}
			return nil
		}},
		{Name: "configure-app", Run: func(ctx context.Context) error {
			if err := adapter.Enable(ctx, target, Settings{Issuer: plan.Issuer, ClientID: plan.ClientID, ClientSecret: secret}); err != nil {
				return failed("configure the app", err)
			}
			return nil
		}},
		{Name: "record", Run: func(context.Context) error {
			rec := Record{Package: m.Name, Adapter: plan.Adapter, ClientID: plan.ClientID, Issuer: plan.Issuer,
				RedirectURIs: plan.RedirectURIs, PluginVersion: pluginVersion(plan.Plugin), Actor: actor}
			if err := rt.Records.Save(rec); err != nil {
				return err
			}
			rt.audit(actor, "app.sign-in.on", "ok", fmt.Sprintf("%s: sign-in with Authentik on through %s; client id %s; %d redirect URIs",
				m.Name, plan.Adapter, plan.ClientID, len(plan.RedirectURIs)))
			return nil
		}},
	}...)
	return def, nil
}
