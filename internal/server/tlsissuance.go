package server

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"homestead/internal/audit"
	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/exposure"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// NewCertIssuing wires the first-issuance flow (S278) to the production
// collaborators: the saved TLS strategy and domain setup, the secret
// store, the production DNS providers, and the Caddy stack.
func NewCertIssuing(st *store.Store, secrets *secret.Store) (*cert.Issuing, error) {
	providers, err := cert.ProvidersFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	stackDir := docker.CaddyStack().Dir
	// Interrupted placements (staging directories, stale temp files) are
	// cleaned, or rolled back, before anything else touches them.
	if err := docker.CleanCertTemps(stackDir); err != nil {
		return nil, err
	}
	return &cert.Issuing{
		Store: st, Wizards: wizard.New(st), Secrets: secrets, Providers: providers,
		StackDir: stackDir, CloudflareAPI: os.Getenv(cert.EnvCloudflareAPI),
		Probe:  cert.ProbeFromEnv(os.Getenv),
		Routes: func() ([]docker.Route, error) { return servedCandidateRoutes(st) },
		// Renewal attempts and account re-registrations are audited, never
		// with key material (S272).
		Audit: func(actor, action, outcome, detail string) error {
			_, err := audit.New(st).Record(audit.Entry{Actor: actor, Action: action, Outcome: outcome, Detail: detail})
			return err
		},
	}, nil
}

// servedCandidateRoutes is every route that may serve an issued
// certificate: the edge reconciler's table plus approved exposure routes.
// The refresh only rewrites snippets that already exist, so an approved
// but unapplied exposure is never created by issuing a certificate.
func servedCandidateRoutes(st *store.Store) ([]docker.Route, error) {
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return nil, err
	}
	routes := append([]docker.Route(nil), set.Routes...)
	seen := map[docker.Hostname]bool{}
	for _, r := range routes {
		seen[r.Host] = true
	}
	approvals, err := exposure.Approvals(st)
	if err != nil {
		return nil, err
	}
	for _, a := range approvals {
		if !seen[a.Plan.Edge.Host] {
			routes = append(routes, a.Plan.Edge)
		}
	}
	return routes, nil
}

// MountTLSIssuance serves owner-approved first issuance (S278, ADR-006,
// ADR-010): POST credentials (DNS provider secrets into the secret store,
// write-only), GET plans (a pure preview, no CA contact), GET and POST
// approvals (the owner approves one plan digest, audited), and POST
// apply (starts the issuance operation). Nothing is requested from the CA
// before an approval of the current plan exists.
func (s *Server) MountTLSIssuance(st *store.Store, issuing *cert.Issuing) {
	runner := operation.NewRunner(st)
	// The approval is audited before it is persisted: if the audit entry
	// cannot be written, nothing is approved.
	issuing.OnApprove = func(approval cert.Approval) error {
		_, err := audit.New(st).Record(audit.Entry{Actor: approval.Actor, Action: "cert.issuance.approve",
			Outcome: audit.OutcomeOK,
			Detail: approval.Certificate + " digest " + approval.Digest + " directory " + approval.Plan.Directory +
				" (" + approval.Plan.DirectoryClass + ")"})
		return err
	}
	s.mux.HandleFunc("GET /api/v1/tls/issuance/plans", func(w http.ResponseWriter, r *http.Request) {
		plans, err := issuing.Plans()
		if err != nil {
			writeIssuanceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"plans": plans})
	})
	s.mux.HandleFunc("GET /api/v1/tls/issuance/approvals", func(w http.ResponseWriter, r *http.Request) {
		approvals, err := issuing.Approvals()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"approvals": approvals})
	})
	s.mux.HandleFunc("POST /api/v1/tls/issuance/approvals", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Certificate       string `json:"certificate"`
			Digest            string `json:"digest"`
			AgreeTerms        bool   `json:"agree_terms"`
			ConfirmProduction bool   `json:"confirm_production"`
			// Renewal is "auto" (default) or "manual" (S272).
			Renewal string `json:"renewal"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		actor := operatorActor(r)
		approval, err := issuing.ApproveWith(body.Certificate, body.Digest, actor, body.AgreeTerms, body.ConfirmProduction, body.Renewal)
		if err != nil {
			_, _ = audit.New(st).Record(audit.Entry{Actor: actor, Action: "cert.issuance.approve",
				Outcome: audit.OutcomeError, Detail: body.Certificate + ": " + err.Error()})
			writeIssuanceError(w, err)
			return
		}
		writeJSON(w, approval)
	})
	s.mux.HandleFunc("POST /api/v1/tls/issuance/credentials", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Provider string            `json:"provider"`
			Values   map[string]string `json:"values"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		actor := operatorActor(r)
		fields, err := issuing.StoreCredentials(body.Provider, body.Values)
		if err != nil {
			_, _ = audit.New(st).Record(audit.Entry{Actor: actor, Action: "cert.issuance.credentials",
				Outcome: audit.OutcomeError, Detail: body.Provider + ": " + err.Error()})
			writeIssuanceError(w, err)
			return
		}
		// Names only: values are never audited, logged, or returned.
		if _, err := audit.New(st).Record(audit.Entry{Actor: actor, Action: "cert.issuance.credentials",
			Outcome: audit.OutcomeOK, Detail: body.Provider + " fields " + strings.Join(fields, ",")}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := map[string]any{"provider": body.Provider, "stored": fields}
		// A Cloudflare token that cannot see the setup domain's zone is
		// stored anyway; the owner is told now, not at the tunnel plan.
		if body.Provider == "cloudflare" && s.domainZoneWarning != nil {
			if warning := s.domainZoneWarning(r.Context()); warning != "" {
				out["warning"] = warning
			}
		}
		writeJSON(w, out)
	})
	// Owner action for a CA that forgot the ACME account (S272): required
	// for production directories, available for any class. Audited by the
	// issuance runner (success and failure).
	// The terms the owner is asked to agree to before re-registering
	// (one directory GET; no account is touched).
	s.mux.HandleFunc("GET /api/v1/tls/issuance/account/terms", func(w http.ResponseWriter, r *http.Request) {
		terms, err := issuing.AccountTerms(r.Context(), r.URL.Query().Get("certificate"))
		if err != nil {
			writeIssuanceError(w, err)
			return
		}
		writeJSON(w, terms)
	})
	s.mux.HandleFunc("POST /api/v1/tls/issuance/account/reregister", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Certificate string `json:"certificate"`
			// AgreeTerms is the fresh terms acknowledgement a production
			// directory requires.
			AgreeTerms bool `json:"agree_terms"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := issuing.ReregisterAccount(r.Context(), body.Certificate, operatorActor(r), body.AgreeTerms); err != nil {
			writeIssuanceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"certificate": body.Certificate, "reregistered": true})
	})
	s.mux.HandleFunc("POST /api/v1/tls/issuance/apply", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Certificate string `json:"certificate"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		def, err := issuing.Definition(body.Certificate)
		if err != nil {
			writeIssuanceError(w, err)
			return
		}
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.auditOperation(st, runner, op.ID, operatorActor(r), "cert.issuance", body.Certificate)
		writeJSON(w, map[string]any{"id": op.ID})
	})
}

func writeIssuanceError(w http.ResponseWriter, err error) {
	status := http.StatusUnprocessableEntity
	switch {
	case errors.Is(err, cert.ErrNoStrategy):
		status = http.StatusNotFound
	case errors.Is(err, cert.ErrNotApproved), errors.Is(err, cert.ErrPlanChanged):
		status = http.StatusConflict
	}
	http.Error(w, err.Error(), status)
}

// certOverrideDoctorCheck warns while a validation-only ACME override is
// set in the daemon's environment: the Cloudflare API origin or the
// propagation resolver. They exist for local test CAs and should not stay
// on an appliance issuing real certificates.
func certOverrideDoctorCheck(getenv func(string) string) func() []doctor.Check {
	return func() []doctor.Check {
		var set []string
		for _, name := range []string{cert.EnvCloudflareAPI, cert.EnvDNSResolver} {
			if getenv(name) != "" {
				set = append(set, name)
			}
		}
		if len(set) == 0 {
			return []doctor.Check{{Name: "acme-overrides", Status: doctor.StatusOK, Detail: "no ACME validation overrides set"}}
		}
		return []doctor.Check{{Name: "acme-overrides", Status: doctor.StatusWarn,
			Detail: strings.Join(set, ", ") + " set: validation-only overrides redirect DNS-01 and propagation checks; remove them from the service unit for real issuance"}}
	}
}
