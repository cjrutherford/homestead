package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"homestead/internal/appsso"
	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// The public dashboard name's certificate and the apps that move to
// Authentik's public address with it (ADR-048 §5-§6). The tunnel plan
// plans the certificate and the moves; the TLS issuance plan carries the
// same moves for a certificate issued from TLS.

// publicCertName is the TLS strategy entry tunnel setup adds for the
// public dashboard name when the strategy has none covering it.
const publicCertName = "public-dashboard"

// tunnelCertificate is the certificate section of a tunnel plan: the
// public dashboard name, and either the certificate already placed for
// it, or the Let's Encrypt DNS-01 issuance approving the plan runs.
type tunnelCertificate struct {
	Name string `json:"name"`
	// Issue says the tunnel operation has a certificate step; approving
	// then needs agree_terms (and confirm_production for a production CA).
	Issue bool `json:"issue"`
	// Placed names the public certificate already placed for Name.
	Placed         string   `json:"placed,omitempty"`
	Certificate    string   `json:"certificate,omitempty"`
	AddsToStrategy bool     `json:"adds_to_strategy,omitempty"`
	Directory      string   `json:"directory,omitempty"`
	DirectoryClass string   `json:"directory_class,omitempty"`
	Challenge      string   `json:"challenge,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	Zone           string   `json:"zone,omitempty"`
	Renewal        string   `json:"renewal,omitempty"`
	TermsURL       string   `json:"terms_url,omitempty"`
	Note           string   `json:"note"`
	Blockers       []string `json:"blockers,omitempty"`
	// Digest is the certificate's issuance plan digest as it will stand
	// once the edge carries the public name; the step issues only if the
	// plan then still has it.
	Digest string `json:"digest,omitempty"`
}

// publicCertIssuer is the issuer tunnel setup adds: Let's Encrypt
// production, DNS-01 through the Cloudflare token stored in setup.
func publicCertIssuer() cert.Issuer {
	return cert.Issuer{Name: "letsencrypt", Type: cert.IssuerACME, Directory: cert.LetsEncryptDirectory,
		Challenge: cert.ChallengeDNS01, DNSProvider: "cloudflare"}
}

// coversName reports a certificate whose names cover host (exactly or
// by a one-label wildcard).
func coversName(names []string, host string) bool {
	return docker.WouldServe(names, docker.Route{Host: docker.Hostname(host), TLS: docker.TLSAuto})
}

// placedPublicCert is the placed certificate from a public CA covering
// name, if any (a local leaf never serves a public name).
func placedPublicCert(stackDir, name string) (docker.PlacedCert, bool) {
	placed, ok := docker.ServingCert(stackDir, docker.Hostname(name))
	if !ok || strings.HasPrefix(placed.Name, docker.LocalCertPrefix) {
		return docker.PlacedCert{}, false
	}
	return placed, true
}

// planTunnelCertificate plans the certificate for the public dashboard
// name dashName on the edge table projected (the tunnel plan's names
// applied), and the apps that move with it. add is the strategy entry
// the certificate step adds, when the strategy has none.
func (s *Server) planTunnelCertificate(st *store.Store, dashName string, projected docker.RouteSet) (*tunnelCertificate, *cert.MovePlan, *cert.Certificate, error) {
	issuing := s.certIssuing
	out := &tunnelCertificate{Name: dashName}
	if placed, ok := placedPublicCert(issuing.StackDir, dashName); ok {
		out.Placed = placed.Name
		out.Note = "A certificate from a public CA for " + dashName + " is already placed (" + placed.Name + "), so this plan issues none."
		moves, err := s.issuerMoves(projected, []string{dashName})
		return out, moves, nil, err
	}
	set, err := cert.LoadStrategy(issuing.Wizards)
	if err != nil && !errors.Is(err, wizard.ErrNotFound) {
		out.Blockers = append(out.Blockers, "the TLS strategy could not be read: "+err.Error())
	}
	var chosen cert.Certificate
	var add *cert.Certificate
	for _, c := range set.Certificates {
		if coversName(c.DNSNames, dashName) {
			chosen = c
			break
		}
	}
	switch {
	case len(out.Blockers) > 0:
	case chosen.Name != "" && !(chosen.Issuer.Type == cert.IssuerACME && chosen.Issuer.Challenge == cert.ChallengeDNS01):
		out.Certificate = chosen.Name
		out.Blockers = append(out.Blockers, fmt.Sprintf("the TLS strategy issues %s through %s (%s), which tunnel setup does not run: issue it from TLS", chosen.Name, chosen.Issuer.Type, chosen.Issuer.Challenge))
	case chosen.Name == "":
		chosen = cert.Certificate{Name: publicCertName, DNSNames: []string{dashName}, Issuer: publicCertIssuer(),
			State: cert.StatePending, Renewal: cert.Renewal{Auto: true}}
		add = &chosen
		out.AddsToStrategy = true
		next := cert.Set{Certificates: append(append([]cert.Certificate(nil), set.Certificates...), chosen)}
		if err := next.Validate(); err != nil {
			out.Blockers = append(out.Blockers, "the certificate cannot join the TLS strategy: "+err.Error())
		}
	}
	if len(out.Blockers) > 0 {
		out.Note = "The certificate for " + dashName + " is not issued by this plan: " + strings.Join(out.Blockers, "; ") + "."
		return out, nil, nil, nil
	}
	moves, err := s.issuerMoves(projected, chosen.DNSNames)
	if err != nil {
		return nil, nil, nil, err
	}
	routes, err := servedCandidateRoutesFrom(st, projected)
	if err != nil {
		return nil, nil, nil, err
	}
	plan := issuing.Preview(chosen, routes, moves)
	out.Certificate, out.Directory, out.DirectoryClass = plan.Certificate, plan.Directory, plan.DirectoryClass
	out.Challenge, out.Provider, out.Zone = plan.Challenge, plan.Provider, plan.Zone
	out.Renewal = cert.RenewalManual
	if chosen.Renewal.Auto {
		out.Renewal = cert.RenewalAuto
	}
	if plan.Directory == cert.LetsEncryptDirectory {
		out.TermsURL = cert.LetsEncryptTermsURL
	}
	if !plan.Ready {
		out.Blockers = append(out.Blockers, plan.Blockers...)
		out.Note = "The certificate for " + dashName + " is not issued by this plan: " + strings.Join(plan.Blockers, "; ") + ". Fix it, then plan again, or issue it later from TLS."
		return out, nil, nil, nil
	}
	out.Issue, out.Digest = true, plan.Digest
	out.Note = fmt.Sprintf("Approving also approves a certificate for %s from %s by DNS-01 through the Cloudflare token stored in setup, renewed automatically. "+
		"It is issued once the tunnel is up, so the appliance answers Authentik's public address itself and published apps can sign in through the tunnel.", dashName, plan.Directory)
	if add != nil {
		out.Note += " It is added to TLS as " + add.Name + "."
	}
	return out, moves, add, nil
}

// issueTunnelCertificate is the tunnel operation's certificate step
// (ADR-048 §5): add the strategy entry if planned, check the issuance
// plan is the one in the tunnel plan, record the owner's approval
// through the tunnel plan, and issue and place the certificate through
// the TLS issuance code, which refreshes the edge so it serves it.
func (s *Server) issueTunnelCertificate(ctx context.Context, actor, tunnelDigest string, want tunnelCertificate, add *cert.Certificate, confirmProduction bool) error {
	issuing := s.certIssuing
	if issuing == nil {
		return errors.New("certificate issuance is not available")
	}
	if add != nil {
		if err := cert.AddToStrategy(issuing.Wizards, *add); err != nil {
			return err
		}
	}
	plan, err := issuing.Plan(want.Certificate)
	if err != nil {
		return err
	}
	if plan.Digest != want.Digest {
		return errors.New("its plan changed since the tunnel plan was approved")
	}
	if _, err := issuing.ApproveVia(want.Certificate, plan.Digest, actor, true, confirmProduction, want.Renewal,
		"tunnel plan "+shortDigest(tunnelDigest)); err != nil {
		return err
	}
	return runCertIssuance(ctx, issuing, want.Certificate)
}

// runCertIssuance runs the approved issuance's steps in place: issue and
// place, refresh the routes that serve it, reload the edge. A var so
// tests never reach a CA.
var runCertIssuance = func(ctx context.Context, issuing *cert.Issuing, name string) error {
	def, err := issuing.IssueDefinition(name)
	if err != nil {
		return err
	}
	for _, step := range def.Steps {
		if err := step.Run(ctx); err != nil {
			return err
		}
	}
	return nil
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// issuerHost is an issuer URL's host.
func issuerHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// issuerMoves lists the applied apps whose sign-in issuer moves to
// Authentik's public address once a certificate covering names is served
// on the edge table set (ADR-048 §6): an app whose plan, with the public
// address answered, names a different issuer than the applied one. Nil
// when none move, or the certificate does not cover the public
// dashboard name, or Authentik's path is not published.
func (s *Server) issuerMoves(set docker.RouteSet, names []string) (*cert.MovePlan, error) {
	a := s.appSignIn
	if a == nil {
		return nil, nil
	}
	name := docker.PublicServerName(set.Routes)
	if name == "" || !coversName(names, string(name)) {
		return nil, nil
	}
	public := ""
	for _, r := range set.Routes {
		if isAuthentikRoute(r) && r.IsPath() {
			_, public = authentikPathURLs(set, r)
		}
	}
	if public == "" {
		return nil, nil
	}
	pkgs, err := (appsso.Records{DB: a.cfg.Store.DB()}).Packages()
	if err != nil || len(pkgs) == 0 {
		return nil, err
	}
	entries, err := catalog.Load(a.cfg.CatalogDir)
	if err != nil {
		return nil, fmt.Errorf("catalog unavailable: %w", err)
	}
	moves := &cert.MovePlan{Apps: []cert.IssuerMove{}, TokenKept: a.tokenKept()}
	for _, pkg := range pkgs {
		manifest, err := entries.Get(pkg)
		if err != nil {
			continue
		}
		in, err := appSignInInputsFrom(a.cfg.Store, a.m, manifest, set)
		if err != nil {
			return nil, err
		}
		in.PublicAuthentikURL, in.PublicWaiting = public, ""
		if in.Applied == nil {
			continue
		}
		plan, err := appsso.BuildPlan(in, true)
		if err != nil || plan.Issuer == in.Applied.Issuer {
			// An app that cannot be planned now (not installed, no
			// address) keeps its sign-in status's own re-plan.
			continue
		}
		move := cert.IssuerMove{App: pkg, Title: manifest.Title, From: in.Applied.Issuer, To: plan.Issuer,
			HostEntry: issuerHost(plan.Issuer) + ":host-gateway", Digest: plan.Digest}
		if old := issuerHost(in.Applied.Issuer); old != issuerHost(plan.Issuer) {
			move.ReplacedHostEntry = old + ":host-gateway"
		}
		moves.Apps = append(moves.Apps, move)
	}
	if len(moves.Apps) == 0 {
		return nil, nil
	}
	if moves.TokenKept {
		moves.Note = "Once the certificate is placed, each app's sign-in is applied again with the Authentik API token you kept, so it signs in at " + public + "."
	} else {
		moves.Note = "Homestead did not keep the Authentik API token, so nothing changes in Authentik. Once the certificate is placed, re-plan each app's sign-in from its card and approve it."
	}
	return moves, nil
}

// reapplyAppSignIn re-applies one app's sign-in by the digest approved
// with the move, through the app sign-in apply path (audited as
// app.sign-in), and waits for it. A var so tests skip Authentik.
var reapplyAppSignIn = func(ctx context.Context, a *appSignIn, move cert.IssuerMove, actor string) error {
	id, err := a.start(ctx, move.App, true, move.Digest, "", actor)
	if errors.Is(err, errPlanChanged) {
		return errors.New("its sign-in plan changed since it was approved")
	}
	if err != nil {
		return err
	}
	done, err := a.runner.Wait(ctx, id)
	if err != nil {
		return err
	}
	if done.Status != operation.StatusSucceeded {
		return errors.New(done.Error)
	}
	return nil
}

// moveIssuers is the app-sign-in step (ADR-048 §6): each listed app's
// sign-in is re-planned now that the certificate is served, checked
// against the approved move and applied with the kept token. One app
// failing does not stop the others; the error names every failed app,
// whose sign-in status then shows the re-plan.
func (s *Server) moveIssuers(ctx context.Context, actor string, moves cert.MovePlan) error {
	a := s.appSignIn
	if a == nil {
		return errors.New("app sign-in is not available on this appliance")
	}
	var failed []string
	for _, move := range moves.Apps {
		if err := reapplyAppSignIn(ctx, a, move, actor); err != nil {
			if a.audit != nil {
				a.audit(actor, "app.sign-in", audit.OutcomeError, move.App+": moving to "+move.To+" failed: "+err.Error())
			}
			failed = append(failed, fmt.Sprintf("%s (%v)", move.Title, err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("the certificate is placed, but sign-in did not move to Authentik's public address for %s. Re-plan each one's sign-in from its card and approve it",
			strings.Join(failed, "; "))
	}
	return nil
}

// servedCandidateRoutesFrom is servedCandidateRoutes over the edge table
// set instead of the stored one.
func servedCandidateRoutesFrom(st *store.Store, set docker.RouteSet) ([]docker.Route, error) {
	return candidateRoutes(st, set)
}
