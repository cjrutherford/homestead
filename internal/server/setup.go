package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"homestead/internal/audit"
	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/domain"
	"homestead/internal/router"
	"homestead/internal/secret"
	"homestead/internal/setup"
	"homestead/internal/sso"
	"homestead/internal/storage"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// Post-claim setup walkthrough (S287, ADR-034). Two session-class routes:
//
//	GET /api/v1/setup                 derived status of the five steps
//	PUT /api/v1/setup/steps/{name}    {"state"} and/or {"provider","access"}
//
// The walkthrough is orchestration. Nothing here changes the appliance;
// changes go through the existing plan, review and approve routes. The
// gate lives only in the dashboard router guard: no route reads it.

// starterApps is the short curated list for the last step, in order.
var starterApps = []string{"nextcloud", "immich", "jellyfin"}

// SetupConfig wires MountSetup.
type SetupConfig struct {
	Store      *store.Store
	Secrets    *secret.Store
	Wizards    *wizard.Service
	SSO        *sso.Manager // optional
	CatalogDir string
	// Port is the daemon's loopback port, for sign-in's default origins.
	Port int
}

// setupSources reads the real facts from the stores. It only counts and
// checks presence; no secret value is ever read.
func setupSources(c SetupConfig) setup.Sources {
	db := c.Store.DB()
	count := func(query string) (int, error) {
		var n int
		err := db.QueryRow(query).Scan(&n)
		return n, err
	}
	return setup.Sources{
		Storage: func() (bool, error) {
			_, err := storage.NewAssignments(c.Store).Get(storage.ClassApplication)
			if errors.Is(err, storage.ErrNoAssignment) {
				return false, nil
			}
			return err == nil, err
		},
		Recovery: func() (bool, bool, error) {
			rs := backup.NewRecipientStore(c.Store)
			_, exported, err := rs.LastExport()
			if err != nil {
				return false, false, err
			}
			_, acked, err := rs.LastAcknowledged()
			return exported, acked, err
		},
		SignIn: func() (setup.SSOFacts, error) {
			f := setup.SSOFacts{Installed: authentikInstalled(c.Store)}
			if c.SSO != nil {
				st, err := c.SSO.Status()
				if err != nil {
					return f, err
				}
				f.Configured, f.Linked, f.Dormant = st.Configured, st.Linked, st.Dormant
				if st.Configured {
					f.Replan = replanReason(st.Spec, ssoDefaults(c.Store, c.Port)) != ""
				}
			}
			return f, nil
		},
		Domain: func(chosen string) (setup.DomainFacts, error) {
			var f setup.DomainFacts
			if state, err := c.Wizards.Get("domain"); err == nil {
				f.Provider, _ = state.Payload["provider"].(string)
				f.Zone, _ = state.Payload["zone"].(string)
				f.Base, _ = state.Payload["base"].(string)
				f.Configured = f.Provider != "" && f.Zone != ""
			} else if !errors.Is(err, wizard.ErrNotFound) {
				return f, err
			}
			var provider string
			err := db.QueryRow(`SELECT provider FROM domain_onboarding WHERE id = 1`).Scan(&provider)
			switch {
			case err == nil:
				f.Onboarded = true
				if f.Provider == "" {
					f.Provider = provider
				}
				f.Configured = true
			case !errors.Is(err, sql.ErrNoRows):
				return f, err
			}
			// The setup step stores credentials for the provider chosen there,
			// before any domain wizard exists, so that provider is checked first.
			sealedFor := chosen
			if sealedFor == "" {
				sealedFor = f.Provider
			}
			if p, ok := setup.ProviderNamed(sealedFor); ok && c.Secrets != nil {
				infos, err := c.Secrets.List()
				if err != nil {
					return f, err
				}
				have := map[string]bool{}
				for _, i := range infos {
					have[i.Name] = true
				}
				f.CredentialsSealed = true
				for _, cf := range p.Credentials {
					if !have[domain.SecretName(sealedFor, cf.Name)] {
						f.CredentialsSealed = false
					}
				}
			}
			return f, nil
		},
		Tunnels: func() (int, error) {
			return count(`SELECT COUNT(*) FROM tunnel_routes`)
		},
		Exposures: func() (int, error) {
			return count(`SELECT COUNT(*) FROM exposure_approvals`)
		},
		Backups: func() (bool, bool, error) {
			_, ok, err := backup.NewPolicies(c.Store).Get()
			return ok, backupStorageAssigned(c.Store), err
		},
		Apps: func() ([]setup.StarterApp, int, error) {
			stacks, err := catalog.InstalledStacks(c.Store)
			if err != nil {
				return nil, 0, err
			}
			installed := 0
			for _, s := range stacks {
				if s != authentikPackage {
					installed++
				}
			}
			cat, err := catalog.Load(c.CatalogDir)
			if err != nil {
				return nil, installed, nil
			}
			var out []setup.StarterApp
			for _, name := range starterApps {
				m, err := cat.Get(name)
				if err != nil {
					continue
				}
				out = append(out, setup.StarterApp{
					Name: name, Title: m.Title, Blurb: m.Blurb,
					Installed: slices.Contains(stacks, name),
				})
			}
			return out, installed, nil
		},
	}
}

// MountSetup registers the setup routes and returns the service.
func (s *Server) MountSetup(c SetupConfig) *setup.Service {
	svc := setup.New(c.Wizards, setupSources(c))
	auditSvc := audit.New(c.Store)
	s.mux.HandleFunc("GET /api/v1/setup", func(w http.ResponseWriter, r *http.Request) {
		st, err := svc.Status()
		if err != nil {
			s.log.Warn("setup status failed", "error", err)
			http.Error(w, "failed to read setup status", http.StatusInternalServerError)
			return
		}
		writeJSON(w, st)
	})
	s.mux.HandleFunc("PUT /api/v1/setup/steps/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var body struct {
			State    *string `json:"state"`
			Provider *string `json:"provider"`
			Access   *string `json:"access"`
			Domain   *string `json:"domain"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, `invalid request body (want {"state"} and/or {"provider","access","domain"})`, http.StatusBadRequest)
			return
		}
		if body.Domain != nil {
			if err := checkDomainZone(r.Context(), svc, c.Secrets, *body.Domain, body.Provider); err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
		}
		st, err := svc.Update(name, setup.Update{State: body.State, Provider: body.Provider, Access: body.Access, Domain: body.Domain})
		if err != nil {
			switch {
			case errors.Is(err, setup.ErrUnknownStep):
				http.Error(w, err.Error(), http.StatusNotFound)
			case errors.Is(err, setup.ErrNotReady), errors.Is(err, setup.ErrRefused):
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			case errors.Is(err, setup.ErrInvalid):
				http.Error(w, err.Error(), http.StatusBadRequest)
			default:
				s.log.Warn("setup update failed", "error", err)
				http.Error(w, "failed to save setup", http.StatusInternalServerError)
			}
			return
		}
		// Audit names the step and the non-sensitive choice only.
		detail := "step=" + name
		if body.State != nil {
			detail += " state=" + *body.State
		}
		if body.Provider != nil {
			detail += " provider=" + *body.Provider
		}
		if body.Access != nil {
			detail += " access=" + *body.Access
		}
		if body.Domain != nil {
			detail += " domain=" + st.Choices.Domain
		}
		if _, err := auditSvc.Record(audit.Entry{
			Actor: operatorActor(r), Action: "setup.step", Outcome: audit.OutcomeOK, Detail: detail,
		}); err != nil {
			s.log.Warn("audit record failed", "action", "setup.step", "error", err)
		}
		writeJSON(w, st)
	})
	s.setupDomain = func() string {
		status, err := svc.Status()
		if err != nil {
			return ""
		}
		return status.Choices.Domain
	}
	s.domainZoneWarning = func(ctx context.Context) string {
		status, err := svc.Status()
		if err != nil || status.Choices.Domain == "" {
			return ""
		}
		cloudflare := "cloudflare"
		if err := checkDomainZone(ctx, svc, c.Secrets, status.Choices.Domain, &cloudflare); err != nil {
			return err.Error()
		}
		return ""
	}
	return svc
}

// checkDomainZone refuses a domain no Cloudflare zone the stored token can
// see contains, when Cloudflare is the provider and its token is stored.
// Without a token yet, or when Cloudflare cannot be reached, the domain is
// saved and the tunnel plan reports the zone later (S294).
func checkDomainZone(ctx context.Context, svc *setup.Service, secrets *secret.Store, raw string, provider *string) error {
	name, err := setup.NormalizeDomain(raw)
	if err != nil || name == "" || secrets == nil {
		return nil // Update refuses a bad name; empty clears the choice.
	}
	chosen := ""
	if provider != nil {
		chosen = *provider
	} else if status, err := svc.Status(); err == nil {
		chosen = status.Choices.Provider
	}
	if chosen != "cloudflare" {
		return nil
	}
	token, err := tunnelToken(secrets)
	if err != nil {
		return nil
	}
	client, err := tunnelClient()
	if err != nil {
		return nil
	}
	if _, err := client.ZoneFor(ctx, token, name); errors.Is(err, router.ErrNoZone) {
		return fmt.Errorf("no Cloudflare zone the stored token can see contains %s: add the domain to Cloudflare, or give the token Zone: Read on its zone", name)
	}
	return nil
}
