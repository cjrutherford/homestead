package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"homestead/internal/audit"
	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// Local TLS (S280, ADR-030): the persisted internal CA, local leaf
// certificates for local-only edge hosts, and root distribution.

// EnvLocalLeafLifetime is an owner-set validation knob (a Go duration,
// at least one hour) in the root-owned service unit, never reachable over
// the API: it shortens leaf validity so KVM acceptance can watch a
// renewal. Unset means the 90-day default.
const EnvLocalLeafLifetime = "HOMESTEAD_LOCAL_LEAF_LIFETIME"

// LocalTLSJobID is the scheduler job that renews leaves and issues them
// for new local-only routes.
const LocalTLSJobID = "tls:local-leaves"

// NewLocalTLS wires the internal CA and leaf issuer to production
// collaborators. yamlPath is the public inspectable mirror.
func NewLocalTLS(st *store.Store, secrets *secret.Store, yamlPath string, getenv func(string) string) (*cert.LocalTLS, error) {
	record := func(actor, action, outcome, detail string) error {
		_, err := audit.New(st).Record(audit.Entry{Actor: actor, Action: action, Outcome: outcome, Detail: detail})
		return err
	}
	lifetime := time.Duration(0)
	if v := getenv(EnvLocalLeafLifetime); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Hour {
			return nil, fmt.Errorf("%s: want a duration of at least 1h, got %q", EnvLocalLeafLifetime, v)
		}
		lifetime = d
	}
	ca := &cert.LocalCA{Store: st, Secrets: secrets, YAMLPath: yamlPath, Audit: record}
	local := &cert.LocalTLS{
		CA: ca, Store: st, StackDir: docker.CaddyStack().Dir, LeafLifetime: lifetime, Audit: record,
		Routes: func() ([]docker.Route, error) { return servedCandidateRoutes(st) },
	}
	// A restored database may lack the inspectable mirror; recreate it.
	_ = ca.SyncYAML()
	return local, nil
}

// Root download headers: the response is the public certificate only.
func writeRoot(w http.ResponseWriter, contentType, filename string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

// MountLocalTLS serves the local-TLS surfaces. The two root downloads are
// the one deliberate unauthenticated read-only exception to ADR-020
// (ADR-030): a device fetching the root cannot have an operator session.
// They serve only the public root certificate; the authenticated status
// shows the fingerprint that verifies the download.
func (s *Server) MountLocalTLS(st *store.Store, local *cert.LocalTLS) {
	runner := operation.NewRunner(st)
	start := func(w http.ResponseWriter, r *http.Request, def operation.Definition) {
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
	s.mux.HandleFunc("GET /api/v1/tls/local-ca/root.pem", func(w http.ResponseWriter, r *http.Request) {
		body, err := local.CA.RootPEM()
		if err != nil {
			rootError(w, err)
			return
		}
		writeRoot(w, "application/x-pem-file", "homestead-local-ca.pem", body)
	})
	s.mux.HandleFunc("GET /api/v1/tls/local-ca/root.der", func(w http.ResponseWriter, r *http.Request) {
		body, err := local.CA.RootDER()
		if err != nil {
			rootError(w, err)
			return
		}
		writeRoot(w, "application/x-x509-ca-cert", "homestead-local-ca.crt", body)
	})
	s.mux.HandleFunc("GET /api/v1/tls/local-ca", func(w http.ResponseWriter, r *http.Request) {
		report, err := local.Report()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, report)
	})
	s.mux.HandleFunc("POST /api/v1/tls/local-ca", func(w http.ResponseWriter, r *http.Request) {
		info, created, err := local.CA.Ensure(operatorActor(r))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, cert.ErrCAKeyMissing) || errors.Is(err, cert.ErrCAKeyOrphaned) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, map[string]any{"ca": info, "created": created})
	})
	s.mux.HandleFunc("GET /api/v1/tls/local-ca/leaves", func(w http.ResponseWriter, r *http.Request) {
		leaves, err := local.Leaves()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"leaves": leaves})
	})
	s.mux.HandleFunc("POST /api/v1/tls/local-ca/leaves/reissue", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Host string `json:"host"`
		}
		if r.ContentLength != 0 {
			if err := decodeBody(r, &body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		actor := operatorActor(r)
		start(w, r, operation.Definition{Type: "tls.local-leaves", Steps: []operation.StepDef{{
			Name: "reissue", Run: func(ctx context.Context) error {
				_, err := local.Reconcile(ctx, actor, body.Host, true)
				return err
			}}}})
	})
	s.mux.HandleFunc("GET /api/v1/tls/local-ca/rotation", func(w http.ResponseWriter, r *http.Request) {
		plan, err := local.PlanRotation()
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, cert.ErrNoCA) {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, plan)
	})
	s.mux.HandleFunc("POST /api/v1/tls/local-ca/rotation", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Digest string `json:"digest"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		plan, err := local.PlanRotation()
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, cert.ErrNoCA) {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		if body.Digest != plan.Digest {
			http.Error(w, cert.ErrStalePlan.Error(), http.StatusConflict)
			return
		}
		actor := operatorActor(r)
		start(w, r, operation.Definition{Type: "tls.local-ca-rotate", Steps: []operation.StepDef{{
			Name: "rotate", Run: func(ctx context.Context) error {
				_, err := local.Rotate(ctx, actor, body.Digest)
				return err
			}}}})
	})
}

func rootError(w http.ResponseWriter, err error) {
	if errors.Is(err, cert.ErrNoCA) {
		http.Error(w, "the internal CA has not been created yet", http.StatusNotFound)
		return
	}
	http.Error(w, "the root certificate is unavailable", http.StatusInternalServerError)
}

// localTLSSource declares the leaf maintenance job (ADR-023, ADR-024).
// Its cadence is six hours, tightened to a sixth of the leaf lifetime
// when the validation knob shortens it, so renewal windows are never
// missed.
func localTLSSource(local *cert.LocalTLS, now func() time.Time) scheduler.Source {
	return scheduler.SourceFunc(func() ([]scheduler.Job, error) {
		interval := 6 * time.Hour
		if l := local.LeafLifetime; l > 0 && l/6 < interval {
			interval = l / 6
		}
		return []scheduler.Job{{
			ID: LocalTLSJobID, Interval: interval, Enabled: true, Since: now().Add(-interval),
			Build: func() (operation.Definition, error) {
				return operation.Definition{Type: "tls.local-leaves", Steps: []operation.StepDef{{
					Name: "renew", Run: func(ctx context.Context) error {
						_, err := local.Reconcile(ctx, scheduler.Actor, "", false)
						return err
					}}}}, nil
			},
		}}, nil
	})
}

// localTLSDoctorChecks reports the root and every leaf (S044 pattern).
func localTLSDoctorChecks(local *cert.LocalTLS, now func() time.Time) func() []doctor.Check {
	return func() []doctor.Check {
		report, err := local.Report()
		if err != nil {
			return []doctor.Check{{Name: "local-ca", Status: doctor.StatusWarn, Detail: "local TLS status unavailable: " + err.Error()}}
		}
		var checks []doctor.Check
		ca := report.CA
		switch {
		case report.OrphanedKey:
			checks = append(checks, doctor.Check{Name: "local-ca", Status: doctor.StatusFail,
				Detail: "a CA private key exists in the secret store but no CA is recorded (database restored without its CA row); it is never overwritten: restore the matching database or discard the key explicitly (see INTERNAL_CA_TRUST.md)"})
		case !ca.Present && report.Eligible > 0:
			checks = append(checks, doctor.Check{Name: "local-ca", Status: doctor.StatusWarn,
				Detail: "local-only HTTPS routes exist but the internal CA is not created; it is created on the next edge reconcile, or create it on the Trust page"})
		case !ca.Present:
			checks = append(checks, doctor.Check{Name: "local-ca", Status: doctor.StatusOK,
				Detail: "not created yet; it is created when the first local-only HTTPS route is applied"})
		case !ca.KeyPresent:
			checks = append(checks, doctor.Check{Name: "local-ca", Status: doctor.StatusFail,
				Detail: "the internal CA private key is missing from the secret store (restore the secret key file, or rotate the root); fingerprint " + ca.FingerprintSHA256})
		default:
			level, note := doctor.StatusOK, "valid"
			switch cert.ExpiringRoot(ca, now()) {
			case "fail":
				level, note = doctor.StatusFail, "expires within 30 days or already expired: rotate the root"
			case "warn":
				level, note = doctor.StatusWarn, "expires within a year: plan a root rotation"
			}
			checks = append(checks, doctor.Check{Name: "local-ca", Status: level,
				Detail: fmt.Sprintf("%s until %s; SHA-256 %s", note, ca.NotAfter.UTC().Format("2006-01-02"), ca.FingerprintSHA256)})
		}
		if len(report.LeftoverKeys) > 0 {
			checks = append(checks, doctor.Check{Name: "local-ca-old-key", Status: doctor.StatusWarn,
				Detail: "an old internal CA key was not destroyed after rotation: " + strings.Join(report.LeftoverKeys, ", ")})
		}
		for _, leaf := range report.Leaves {
			level := doctor.StatusOK
			switch leaf.Status {
			case cert.LeafExpired, cert.LeafMismatch:
				level = doctor.StatusFail
			case cert.LeafOK:
			default:
				level = doctor.StatusWarn
			}
			detail := leaf.Status
			if leaf.Detail != "" {
				detail += " - " + leaf.Detail
			} else if !leaf.NotAfter.IsZero() {
				detail += " - valid until " + leaf.NotAfter.UTC().Format("2006-01-02T15:04Z")
			}
			checks = append(checks, doctor.Check{Name: "local-leaf:" + leaf.Host, Status: level, Detail: detail})
		}
		return checks
	}
}
