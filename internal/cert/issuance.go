package cert

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"homestead/internal/dns"
	"homestead/internal/docker"
	"homestead/internal/domain"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// First certificate issuance (S278): plan, preview, approve, issue.
//
// The plan is computed from the saved TLS strategy (S106/S226) and the
// saved domain setup (S097) with no network access: nothing reaches a CA
// before the owner approves the exact plan digest. Approving records the
// owner, the terms agreement, and (for production CAs) an explicit
// production confirmation. The approved plan then runs as an operation
// through DNS01.IssueFor: issue, place chain and key for Caddy, refresh
// the route snippets that now serve it, and reload the edge. Key
// material only ever lives in memory, the 0600 key file, and the secret
// store (ACME account key); it is never logged, audited, or returned.

// Directory classes (ADR-010): what kind of CA a directory URL names.
const (
	// DirectoryStaging is Let's Encrypt staging: certificates untrusted,
	// generous rate limits, the place to test.
	DirectoryStaging = "staging"
	// DirectoryTest is a private or loopback CA (Pebble and friends).
	DirectoryTest = "test"
	// DirectoryProduction is any other CA. It needs the owner's explicit
	// production confirmation at approval; nothing defaults to it.
	DirectoryProduction = "production"
)

// LetsEncryptStagingDirectory is the staging directory URL.
const LetsEncryptStagingDirectory = "https://acme-staging-v02.api.letsencrypt.org/directory"

// LetsEncryptDirectory is Let's Encrypt's production directory, the CA
// tunnel setup issues the public dashboard name's certificate from
// (ADR-048 §5).
const LetsEncryptDirectory = "https://acme-v02.api.letsencrypt.org/directory"

// LetsEncryptTermsURL is where Let's Encrypt publishes the subscriber
// agreement an owner agrees to when approving issuance from it.
const LetsEncryptTermsURL = "https://letsencrypt.org/repository/"

// testSuffixes are the private or reserved suffixes of test CAs.
var testSuffixes = []string{"lan", "local", "localhost", "internal", "home.arpa", "test", "example", "invalid"}

// ClassifyDirectory grades a directory URL. "test" is only for hosts that
// cannot be a public CA: loopback, RFC 1918, unique-local and link-local
// addresses, and private or reserved suffixes. Public IP literals and
// single-label or ordinary names (which may resolve publicly) count as
// production, so confirming is the safe default.
func ClassifyDirectory(directory string) string {
	if directory == LetsEncryptStagingDirectory {
		return DirectoryStaging
	}
	u, err := url.Parse(directory)
	if err != nil || u.Hostname() == "" {
		return DirectoryProduction
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() {
			return DirectoryTest
		}
		return DirectoryProduction
	}
	for _, suffix := range testSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return DirectoryTest
		}
	}
	return DirectoryProduction
}

// Issuance flow errors the surfaces map to statuses.
var (
	// ErrNotEligible reports a certificate this flow cannot issue.
	ErrNotEligible = errors.New("cert: certificate is not an ACME dns-01 certificate")
	// ErrNoStrategy reports issuance planning without a saved TLS strategy.
	ErrNoStrategy = errors.New("cert: no TLS strategy saved")
	// ErrNotApproved reports apply without a current approval.
	ErrNotApproved = errors.New("cert: issuance not approved")
	// ErrPlanChanged reports a plan that differs from what was approved
	// or previewed: the owner must review it again.
	ErrPlanChanged = errors.New("cert: issuance plan changed since it was reviewed")
)

// CredentialStatus is one DNS credential field and whether it is stored.
type CredentialStatus struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Present bool   `json:"present"`
}

// AccountPlan states whether an ACME account exists for the directory.
type AccountPlan struct {
	// State is "existing" (key held, no registration needed) or "new"
	// (a key will be generated and an account registered).
	State  string `json:"state"`
	Detail string `json:"detail"`
	// Secret names the secret-store entry (never its value).
	Secret string `json:"secret"`
}

// PlacementPlan says where chain and key will be placed.
type PlacementPlan struct {
	Directory string `json:"directory"`
	ChainFile string `json:"chain_file"`
	KeyFile   string `json:"key_file"`
	KeyMode   string `json:"key_mode"`
}

// IssuancePlan is the previewed first issuance of one certificate.
type IssuancePlan struct {
	Certificate    string        `json:"certificate"`
	Names          []string      `json:"names"`
	Provider       string        `json:"provider"`
	Zone           string        `json:"zone"`
	Challenge      string        `json:"challenge"`
	Directory      string        `json:"directory"`
	DirectoryClass string        `json:"directory_class"`
	Account        AccountPlan   `json:"account"`
	Terms          string        `json:"terms"`
	Placement      PlacementPlan `json:"placement"`
	// ServedRoutes are the edge routes (automatic TLS, snippet present)
	// that will start serving this certificate after the reload.
	ServedRoutes []string `json:"served_routes"`
	// Credentials lists the DNS provider's credential fields and whether
	// each is stored (presence only, never values). Not part of the digest.
	Credentials []CredentialStatus `json:"credentials"`
	Ready       bool               `json:"ready"`
	Blockers    []string           `json:"blockers"`
	// IssuerMoves lists the apps whose sign-in moves to Authentik's
	// public address once this certificate is served (ADR-048 §6). Part
	// of the digest; absent when none move.
	IssuerMoves *MovePlan `json:"issuer_moves,omitempty"`
	// Digest identifies exactly this plan; approval and apply bind to it.
	Digest string `json:"digest"`
	// Approved reports a current approval of this digest.
	Approved bool `json:"approved"`
}

// IssuerMove is one applied app whose sign-in issuer moves to
// Authentik's public address once the certificate is served (ADR-048
// §6, ADR-047 §1): the issuer change, the host entry the app's
// override gains (and the one it drops), and the app's sign-in plan
// digest as it will stand, which the re-apply is checked against.
type IssuerMove struct {
	App               string `json:"app"`
	Title             string `json:"title"`
	From              string `json:"from"`
	To                string `json:"to"`
	HostEntry         string `json:"host_entry"`
	ReplacedHostEntry string `json:"replaced_host_entry,omitempty"`
	Digest            string `json:"digest"`
}

// MovePlan is the issuer-move list a certificate plan carries. With
// TokenKept the plan's last step re-applies each app's sign-in with the
// kept Authentik API token; without it nothing changes in Authentik and
// Note says to re-plan each app.
type MovePlan struct {
	Apps      []IssuerMove `json:"apps"`
	TokenKept bool         `json:"token_kept"`
	Note      string       `json:"note"`
}

// Reapplies reports whether the plan's last step re-applies sign-in.
func (m *MovePlan) Reapplies() bool { return m != nil && m.TokenKept && len(m.Apps) > 0 }

// digestInput is what the digest covers: the facts the owner approves.
// Account state and readiness are excluded: they change without the
// issuance changing.
type digestInput struct {
	Certificate  string        `json:"certificate"`
	Names        []string      `json:"names"`
	Provider     string        `json:"provider"`
	Zone         string        `json:"zone"`
	Challenge    string        `json:"challenge"`
	Directory    string        `json:"directory"`
	Placement    PlacementPlan `json:"placement"`
	ServedRoutes []string      `json:"served_routes"`
	IssuerMoves  *MovePlan     `json:"issuer_moves,omitempty"`
}

func (p IssuancePlan) computeDigest() string {
	raw, _ := json.Marshal(digestInput{
		Certificate: p.Certificate, Names: p.Names, Provider: p.Provider, Zone: p.Zone,
		Challenge: p.Challenge, Directory: p.Directory, Placement: p.Placement,
		ServedRoutes: p.ServedRoutes, IssuerMoves: p.IssuerMoves,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Approval is the owner's recorded approval of one plan digest.
type Approval struct {
	Certificate         string `json:"certificate"`
	Digest              string `json:"digest"`
	Actor               string `json:"actor"`
	AgreedTerms         bool   `json:"agreed_terms"`
	ConfirmedProduction bool   `json:"confirmed_production"`
	// RenewalMode is the owner's choice at approval: RenewalAuto (the
	// default) lets Homestead re-issue an unchanged certificate unattended
	// (ADR-024); RenewalManual leaves renewal to the owner.
	RenewalMode string       `json:"renewal_mode"`
	ApprovedAt  time.Time    `json:"approved_at"`
	Plan        IssuancePlan `json:"plan"`
	// Via names the plan the owner approved this through, when it was
	// not the TLS page's own (the tunnel plan, ADR-048 §5). Audited, not
	// stored.
	Via string `json:"via,omitempty"`
}

// Renewal modes an approval can record.
const (
	RenewalAuto   = "auto"
	RenewalManual = "manual"
)

// Issuing runs the first-issuance flow. All collaborators are explicit
// so tests substitute a fake CA, fake DNS provider, and a temp stack.
type Issuing struct {
	Store     *store.Store
	Wizards   *wizard.Service
	Secrets   *secret.Store
	Providers *dns.Registry
	// StackDir is the Caddy stack directory (docker.CaddyStack().Dir).
	StackDir string
	// NewClient builds the ACME client for a directory; nil means NewClient.
	NewClient func(directory string) (*Client, error)
	Probe     dns.ProbeFunc
	Interval  time.Duration
	// Routes lists the routes that may serve the certificate: the edge
	// table plus approved exposure routes. Only automatic-TLS routes
	// with an existing snippet are ever touched.
	Routes func() ([]docker.Route, error)
	// Reload asks the edge to adopt the snippets; nil means docker.ReloadProxy.
	Reload func(context.Context) error
	// CloudflareAPI is the Cloudflare API origin override in effect
	// (EnvCloudflareAPI); empty means the production API.
	CloudflareAPI string
	// OnApprove runs before an approval is persisted; its failure aborts
	// the approval (S278: the audit entry is written first).
	OnApprove func(Approval) error
	// Audit records an owner-visible event (account re-registration,
	// renewal attempts); nil means none. Never given key material.
	Audit func(actor, action, outcome, detail string) error
	// Timeout bounds one issuance (DNS propagation can otherwise wait
	// forever); zero means DefaultIssueTimeout.
	Timeout time.Duration
	// Now stamps records; nil means time.Now.
	Now func() time.Time
	// PlanMoves lists the applied apps whose sign-in moves to
	// Authentik's public address once a certificate covering names is
	// served (ADR-048 §6); nil, or a nil result, means none.
	PlanMoves func(names []string) (*MovePlan, error)
	// MoveIssuers re-applies the listed apps' sign-in with the kept
	// Authentik API token, as the issuance's last step.
	MoveIssuers func(ctx context.Context, actor string, moves MovePlan) error

	accountMu sync.Mutex
}

// DefaultIssueTimeout bounds one issuance run.
const DefaultIssueTimeout = 15 * time.Minute

// Validation-harness environment (docs/DEVELOPMENT.md, S278): owner-set
// in the root-owned service unit, never reachable over the API. They let
// KVM validation run against a local ACME CA with no public DNS.
const (
	// EnvCloudflareAPI overrides the Cloudflare API origin.
	EnvCloudflareAPI = "HOMESTEAD_ACME_CLOUDFLARE_API"
	// EnvDNSResolver overrides the resolver (host:port) that propagation
	// checks query; the default is a public resolver.
	EnvDNSResolver = "HOMESTEAD_ACME_DNS_RESOLVER"
	// DefaultDNSResolver is the public resolver propagation checks use.
	DefaultDNSResolver = "1.1.1.1:53"
)

// ProvidersFromEnv is DefaultProviders honouring EnvCloudflareAPI.
func ProvidersFromEnv(getenv func(string) string) (*dns.Registry, error) {
	api := getenv(EnvCloudflareAPI)
	if api == "" {
		api = dns.CloudflareAPI
	}
	return providersWith(api)
}

// ProbeFromEnv builds the propagation probe honouring EnvDNSResolver.
func ProbeFromEnv(getenv func(string) string) dns.ProbeFunc {
	if addr := getenv(EnvDNSResolver); addr != "" {
		return dns.ProbeVia(addr)
	}
	return dns.ProbeVia(DefaultDNSResolver)
}

func (i *Issuing) now() time.Time {
	if i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

func (i *Issuing) client(directory string) (*Client, error) {
	if i.NewClient != nil {
		return i.NewClient(directory)
	}
	return NewClient(directory)
}

// AccountSecretName names the ACME account secret for a directory:
// sys_ so neither owner routes nor the catalog can touch it (ADR-021),
// per directory so staging and production never share an account.
func AccountSecretName(directory string) string {
	sum := sha256.Sum256([]byte(directory))
	return secret.SystemSecretPrefix + "acme_account_" + hex.EncodeToString(sum[:8])
}

// storedAccount is the secret value: the account key and, once
// registered, its location. Never leaves the secret store.
type storedAccount struct {
	KeyPEM string `json:"key_pem"`
	KID    string `json:"kid"`
	// Terms is the terms-of-service URL agreed at registration.
	Terms string `json:"terms,omitempty"`
}

func (i *Issuing) accountExists(directory string) bool {
	if i.Secrets == nil {
		return false
	}
	_, err := i.Secrets.Get(AccountSecretName(directory))
	return err == nil
}

// loadAccount returns the directory's account, generating and storing a
// key on first use. The key is stored before registration so a crash
// between the two never orphans a registered account.
func (i *Issuing) loadAccount(directory string) (*Account, error) {
	// Serialized: two concurrent applies must share one generated key, or
	// one of them would register an account whose key is then overwritten.
	i.accountMu.Lock()
	defer i.accountMu.Unlock()
	name := AccountSecretName(directory)
	raw, err := i.Secrets.Get(name)
	if err == nil {
		var stored storedAccount
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			return nil, fmt.Errorf("cert: stored ACME account is unreadable")
		}
		key, err := ParseAccountKey([]byte(stored.KeyPEM))
		if err != nil {
			return nil, fmt.Errorf("cert: stored ACME account key is unreadable")
		}
		return &Account{Key: key, KID: stored.KID, Terms: stored.Terms}, nil
	}
	if !errors.Is(err, secret.ErrNotFound) {
		return nil, err
	}
	key, err := GenerateAccountKey()
	if err != nil {
		return nil, err
	}
	acct := &Account{Key: key}
	if err := i.saveAccountLocked(directory, acct); err != nil {
		return nil, err
	}
	return acct, nil
}

func (i *Issuing) saveAccount(directory string, acct *Account) error {
	i.accountMu.Lock()
	defer i.accountMu.Unlock()
	return i.saveAccountLocked(directory, acct)
}

func (i *Issuing) saveAccountLocked(directory string, acct *Account) error {
	pemKey, err := EncodeAccountKey(acct.Key)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(storedAccount{KeyPEM: string(pemKey), KID: acct.KID, Terms: acct.Terms})
	if err != nil {
		return err
	}
	return i.Secrets.SetSystem(AccountSecretName(directory), string(encoded))
}

// strategy loads the saved TLS strategy; none is ErrNoStrategy.
func (i *Issuing) strategy() (Set, error) {
	set, err := LoadStrategy(i.Wizards)
	if errors.Is(err, wizard.ErrNotFound) {
		return Set{}, ErrNoStrategy
	}
	return set, err
}

func isDNS01ACME(c Certificate) bool {
	return c.Issuer.Type == IssuerACME && c.Issuer.Challenge == ChallengeDNS01
}

// domainZone reads the saved domain setup's provider and zone.
func (i *Issuing) domainZone() (provider, zone string) {
	state, err := i.Wizards.Get(domain.WizardID)
	if err != nil {
		return "", ""
	}
	provider, _ = state.Payload["provider"].(string)
	zone, _ = state.Payload["zone"].(string)
	return provider, zone
}

// Plans previews every certificate this flow can issue.
func (i *Issuing) Plans() ([]IssuancePlan, error) {
	set, err := i.strategy()
	if err != nil {
		return nil, err
	}
	approvals, err := i.approvalMap()
	if err != nil {
		return nil, err
	}
	routes, err := i.routes()
	if err != nil {
		return nil, err
	}
	plans := []IssuancePlan{}
	for _, c := range set.Certificates {
		if !isDNS01ACME(c) {
			continue
		}
		plan := i.buildPlan(c, routes, nil, true)
		if a, ok := approvals[c.Name]; ok && a.Digest == plan.Digest {
			plan.Approved = true
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// Plan previews one certificate's issuance.
func (i *Issuing) Plan(name string) (IssuancePlan, error) {
	plans, err := i.Plans()
	if err != nil {
		return IssuancePlan{}, err
	}
	for _, plan := range plans {
		if plan.Certificate == name {
			return plan, nil
		}
	}
	return IssuancePlan{}, fmt.Errorf("%w: %q", ErrNotEligible, name)
}

func (i *Issuing) routes() ([]docker.Route, error) {
	if i.Routes == nil {
		return nil, nil
	}
	return i.Routes()
}

// Preview plans issuing c, which need not be in the saved strategy yet,
// as if routes were the edge's and moves the apps moving with it (the
// tunnel plan's projection, ADR-048 §5). It contacts no one and records
// nothing; the plan issued later must come out with the same digest.
func (i *Issuing) Preview(c Certificate, routes []docker.Route, moves *MovePlan) IssuancePlan {
	return i.buildPlan(c, routes, moves, false)
}

// buildPlan previews c's issuance. With useHook the issuer moves come
// from PlanMoves; otherwise moves is taken as given.
func (i *Issuing) buildPlan(c Certificate, routes []docker.Route, moves *MovePlan, useHook bool) IssuancePlan {
	fp := NewFingerprint(c.DNSNames, c.Issuer.DNSProvider, string(c.Issuer.Challenge))
	chain, key := docker.CertPaths(i.StackDir, c.Name)
	plan := IssuancePlan{
		Certificate:    c.Name,
		Names:          fp.Names,
		Provider:       c.Issuer.DNSProvider,
		Challenge:      string(c.Issuer.Challenge),
		Directory:      c.Issuer.Directory,
		DirectoryClass: ClassifyDirectory(c.Issuer.Directory),
		Terms:          "Approving records your agreement to the certificate authority's terms of service; the CA publishes them when the account is registered.",
		Placement: PlacementPlan{
			Directory: docker.CertDir(i.StackDir, c.Name), ChainFile: chain, KeyFile: key, KeyMode: "0600",
		},
		ServedRoutes: []string{},
		Credentials:  []CredentialStatus{},
		Blockers:     []string{},
	}
	block := func(format string, args ...any) {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf(format, args...))
	}
	plan.Account = AccountPlan{Secret: AccountSecretName(c.Issuer.Directory)}
	if i.accountExists(c.Issuer.Directory) {
		plan.Account.State = "existing"
		plan.Account.Detail = "An ACME account key for this directory is already held in the secret store; no new registration."
	} else {
		plan.Account.State = "new"
		plan.Account.Detail = "A new ACME account key will be generated, held in the secret store, and registered with this directory."
	}
	if bad := fp.InvalidNames(); len(bad) > 0 {
		block("invalid names: %s", strings.Join(bad, ", "))
	}
	// DNS-01 readiness: the saved domain setup names the zone and the
	// provider that can write it; the provider credentials must exist.
	domainProvider, zone := i.domainZone()
	plan.Zone = zone
	switch {
	case zone == "":
		block("no domain setup saved: DNS-01 needs the DNS provider and zone (set it up first)")
	case domainProvider != c.Issuer.DNSProvider:
		block("the saved domain setup uses provider %q, this certificate uses %q", domainProvider, c.Issuer.DNSProvider)
	default:
		for _, name := range fp.Names {
			base := strings.TrimPrefix(name, "*.")
			if base != zone && !strings.HasSuffix(base, "."+zone) {
				block("%q is outside the zone %q", name, zone)
			}
		}
	}
	if i.Providers == nil {
		block("no DNS providers are registered")
	} else if provider, err := i.Providers.Get(c.Issuer.DNSProvider); err != nil {
		block("unknown DNS provider %q", c.Issuer.DNSProvider)
	} else if i.Secrets != nil {
		for _, field := range provider.RequiredCredentials() {
			_, err := i.Secrets.Get(domain.SecretName(provider.Name(), field.Name))
			plan.Credentials = append(plan.Credentials, CredentialStatus{Name: field.Name, Label: field.Label, Present: err == nil})
			if err != nil {
				block("missing DNS credential %q", field.Name)
			}
		}
	}
	if c.Issuer.DNSProvider == "cloudflare" && i.CloudflareAPI != "" &&
		!strings.HasPrefix(i.CloudflareAPI, "https://") && plan.DirectoryClass != DirectoryTest {
		block("the Cloudflare API override %q is not https; plain http is only allowed against a test CA", i.CloudflareAPI)
	}
	for _, route := range routes {
		if docker.WouldServe(fp.Names, route) && docker.RouteSnippetExists(i.StackDir, route.Host) {
			plan.ServedRoutes = append(plan.ServedRoutes, string(route.Host))
		}
	}
	if useHook && i.PlanMoves != nil {
		got, err := i.PlanMoves(fp.Names)
		if err != nil {
			block("the apps moving to Authentik's public address could not be listed: %v", err)
		}
		moves = got
	}
	if moves != nil && len(moves.Apps) > 0 {
		plan.IssuerMoves = moves
	}
	plan.Ready = len(plan.Blockers) == 0
	plan.Digest = plan.computeDigest()
	return plan
}

// Approve records the owner's approval of the reviewed plan digest. It
// refuses plans that are not ready, a digest other than the current
// one, missing terms agreement, and production CAs without an explicit
// production confirmation. It contacts no one.
func (i *Issuing) Approve(name, digest, actor string, agreeTerms, confirmProduction bool) (Approval, error) {
	return i.ApproveWith(name, digest, actor, agreeTerms, confirmProduction, "")
}

// ApproveWith is Approve with an explicit renewal mode: "auto" (the
// default when empty) or "manual", the owner's opt-out of unattended
// re-issue.
func (i *Issuing) ApproveWith(name, digest, actor string, agreeTerms, confirmProduction bool, renewalMode string) (Approval, error) {
	return i.ApproveVia(name, digest, actor, agreeTerms, confirmProduction, renewalMode, "")
}

// ApproveVia is ApproveWith for an approval given through another plan
// (via names it, for the audit): the tunnel plan approves the public
// dashboard certificate (ADR-048 §5).
func (i *Issuing) ApproveVia(name, digest, actor string, agreeTerms, confirmProduction bool, renewalMode, via string) (Approval, error) {
	switch renewalMode {
	case "":
		renewalMode = RenewalAuto
	case RenewalAuto, RenewalManual:
	default:
		return Approval{}, fmt.Errorf("cert: renewal mode must be %q or %q", RenewalAuto, RenewalManual)
	}
	if actor == "" {
		return Approval{}, fmt.Errorf("cert: approval needs an actor")
	}
	plan, err := i.Plan(name)
	if err != nil {
		return Approval{}, err
	}
	if !plan.Ready {
		return Approval{}, fmt.Errorf("cert: plan is not ready: %s", strings.Join(plan.Blockers, "; "))
	}
	if digest != plan.Digest {
		return Approval{}, fmt.Errorf("%w: review it again", ErrPlanChanged)
	}
	if !agreeTerms {
		return Approval{}, fmt.Errorf("cert: approval needs agreement to the CA's terms of service")
	}
	if plan.DirectoryClass == DirectoryProduction && !confirmProduction {
		return Approval{}, fmt.Errorf("cert: %s is a production CA: confirm production issuance explicitly (staging is the place to test)", plan.Directory)
	}
	approval := Approval{
		Certificate: name, Digest: plan.Digest, Actor: actor, AgreedTerms: true,
		ConfirmedProduction: confirmProduction, RenewalMode: renewalMode, ApprovedAt: i.now().UTC(), Plan: plan, Via: via,
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return Approval{}, err
	}
	if i.OnApprove != nil {
		if err := i.OnApprove(approval); err != nil {
			return Approval{}, fmt.Errorf("cert: approval not recorded: %w", err)
		}
	}
	// A fresh approval is a new owner decision: failed unattended retries
	// of the previous one no longer apply.
	if err := NewRenewalStates(i.Store).Clear(name); err != nil {
		return Approval{}, err
	}
	if _, err := i.Store.DB().Exec(`INSERT INTO cert_issuance_approvals
(certificate, digest, plan, actor, agreed_terms, confirmed_production, approved_at, renewal_mode)
VALUES (?, ?, ?, ?, 1, ?, ?, ?)
ON CONFLICT(certificate) DO UPDATE SET digest = excluded.digest, plan = excluded.plan,
	actor = excluded.actor, agreed_terms = 1, confirmed_production = excluded.confirmed_production,
	approved_at = excluded.approved_at, renewal_mode = excluded.renewal_mode`,
		name, plan.Digest, string(encoded), actor, boolInt(confirmProduction),
		approval.ApprovedAt.Format(time.RFC3339Nano), renewalMode); err != nil {
		return Approval{}, fmt.Errorf("cert: record approval: %w", err)
	}
	return approval, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Approvals lists recorded approvals ordered by certificate.
func (i *Issuing) Approvals() ([]Approval, error) {
	rows, err := i.Store.DB().Query(`SELECT certificate, digest, plan, actor, agreed_terms,
confirmed_production, approved_at, renewal_mode FROM cert_issuance_approvals ORDER BY certificate`)
	if err != nil {
		return nil, fmt.Errorf("cert: list approvals: %w", err)
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		var a Approval
		var plan, at string
		var agreed, confirmed int
		if err := rows.Scan(&a.Certificate, &a.Digest, &plan, &a.Actor, &agreed, &confirmed, &at, &a.RenewalMode); err != nil {
			return nil, err
		}
		a.AgreedTerms, a.ConfirmedProduction = agreed != 0, confirmed != 0
		a.ApprovedAt, _ = time.Parse(time.RFC3339Nano, at)
		if err := json.Unmarshal([]byte(plan), &a.Plan); err != nil {
			return nil, fmt.Errorf("cert: approval %q plan: %w", a.Certificate, err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (i *Issuing) approvalMap() (map[string]Approval, error) {
	list, err := i.Approvals()
	if err != nil {
		return nil, err
	}
	out := map[string]Approval{}
	for _, a := range list {
		out[a.Certificate] = a
	}
	return out, nil
}

// approvalFor is the current approval of name, or an error saying why
// there is none.
func (i *Issuing) approvalFor(name string, plan IssuancePlan) (Approval, error) {
	var a Approval
	var digest, actor, at string
	var agreed, confirmed int
	err := i.Store.DB().QueryRow(`SELECT digest, actor, agreed_terms, confirmed_production, approved_at
FROM cert_issuance_approvals WHERE certificate = ?`, name).Scan(&digest, &actor, &agreed, &confirmed, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Approval{}, fmt.Errorf("%w: review and approve the plan for %q first", ErrNotApproved, name)
	}
	if err != nil {
		return Approval{}, err
	}
	if digest != plan.Digest {
		return Approval{}, fmt.Errorf("%w: the plan for %q differs from the approved one; review and approve it again", ErrPlanChanged, name)
	}
	a = Approval{Certificate: name, Digest: digest, Actor: actor, AgreedTerms: agreed != 0,
		ConfirmedProduction: confirmed != 0}
	a.ApprovedAt, _ = time.Parse(time.RFC3339Nano, at)
	if plan.DirectoryClass == DirectoryProduction && !a.ConfirmedProduction {
		return Approval{}, fmt.Errorf("%w: production issuance was not confirmed", ErrNotApproved)
	}
	return a, nil
}

// OperationType names the issuance operation.
const OperationType = "cert.issue"

// Definition plans the issuance operation for an approved plan: nothing
// is requested from the CA unless the current plan matches an approval.
// Steps: issue-and-place (ACME order, chain and key placed 0600, S263
// fingerprint recorded), refresh-routes (snippets of automatic-TLS routes
// now covered), reload-edge (Caddy adopts them; a refused reload rolls
// the snippets back and leaves the placed files in place).
//
// When the plan lists apps moving to Authentik's public address and the
// API token is kept, a last app-sign-in step re-applies their sign-in
// (ADR-048 §6) once the certificate is served.
func (i *Issuing) Definition(name string) (operation.Definition, error) {
	def, run, err := i.definition(name)
	if err != nil {
		return def, err
	}
	if moves := run.plan.IssuerMoves; moves.Reapplies() && i.MoveIssuers != nil {
		def.Steps = append(def.Steps, operation.StepDef{Name: MoveStep, Run: func(ctx context.Context) error {
			return i.MoveIssuers(ctx, run.approval.Actor, *moves)
		}})
	}
	return def, nil
}

// MoveStep names the step that moves apps to the public issuer.
const MoveStep = "app-sign-in"

// IssueDefinition is Definition without the issuer-move step: the
// tunnel operation runs the moves as a step of its own (ADR-048 §6).
func (i *Issuing) IssueDefinition(name string) (operation.Definition, error) {
	def, _, err := i.definition(name)
	return def, err
}

func (i *Issuing) definition(name string) (operation.Definition, *issueRun, error) {
	plan, err := i.Plan(name)
	if err != nil {
		return operation.Definition{}, nil, err
	}
	if !plan.Ready {
		return operation.Definition{}, nil, fmt.Errorf("cert: plan is not ready: %s", strings.Join(plan.Blockers, "; "))
	}
	approval, err := i.approvalFor(name, plan)
	if err != nil {
		return operation.Definition{}, nil, err
	}
	certificate, err := i.certificateNamed(name)
	if err != nil {
		return operation.Definition{}, nil, err
	}
	run := &issueRun{plan: plan, certificate: certificate, approval: approval, served: plan.ServedRoutes}
	def := i.steps(run)
	// A manual issuance settles any failed unattended renewal (ADR-024):
	// the stale retry state must not outlive the certificate it was about.
	last := def.Steps[len(def.Steps)-1].Run
	def.Steps[len(def.Steps)-1].Run = func(ctx context.Context) error {
		err := last(ctx)
		if err == nil {
			_ = NewRenewalStates(i.Store).Clear(name)
		}
		return err
	}
	return def, run, nil
}

func (i *Issuing) certificateNamed(name string) (Certificate, error) {
	set, err := i.strategy()
	if err != nil {
		return Certificate{}, err
	}
	for _, c := range set.Certificates {
		if c.Name == name {
			return c, nil
		}
	}
	return Certificate{}, fmt.Errorf("%w: %q", ErrNotEligible, name)
}

// issueRun is one execution of the issuance steps: first issuance and
// unattended re-issue (S272) share them, so renewal places, serves, and
// reloads exactly as the owner-approved first issuance does.
type issueRun struct {
	plan        IssuancePlan
	certificate Certificate
	approval    Approval
	// served are the hosts whose snippets are refreshed: the plan's for a
	// first issuance, the approved plan's for a renewal (never wider).
	served []string
	// force skips reuse of the placed certificate: a renewal always
	// orders a new one.
	force bool

	delivered    bool
	reloadFailed bool
}

func (i *Issuing) steps(run *issueRun) operation.Definition {
	var (
		undo    func() error
		warning error
	)
	def := operation.Definition{Type: OperationType}
	def.Steps = []operation.StepDef{
		{Name: "issue-and-place", Run: func(ctx context.Context) error {
			delivered, err := i.issue(ctx, run.plan, run.certificate, run.approval, run.force)
			if delivered {
				run.delivered = true
				// Issued and placed: later steps must still run so the
				// edge serves it; the leftover problem is reported last.
				warning = err
				return nil
			}
			return err
		}},
		{Name: "refresh-routes", Run: func(context.Context) error {
			routes, err := i.routes()
			if err != nil {
				return err
			}
			undo, err = docker.RefreshCertRoutes(i.StackDir, routes, run.served)
			return err
		}},
		{Name: "reload-edge", Run: func(ctx context.Context) error {
			reload := i.Reload
			if reload == nil {
				reload = docker.ReloadProxy
			}
			if err := reload(ctx); err != nil {
				run.reloadFailed = true
				if undo != nil {
					if rbErr := undo(); rbErr != nil {
						err = fmt.Errorf("%w (rollback also failed: %v)", err, rbErr)
					}
				}
				return fmt.Errorf("certificate issued and placed, but the edge reload was refused: %w", err)
			}
			if warning != nil {
				return fmt.Errorf("certificate issued, placed and served, but: %w", warning)
			}
			return nil
		}},
	}
	return def
}

// issue runs the ACME order through DNS01.IssueFor. delivered reports
// that chain and key were placed (the err may still carry a cleanup or
// recording problem). The account is persisted even when issuance
// fails, so a registered account is never lost. force skips reuse of an
// already-placed certificate (renewal). When the CA has forgotten the
// account (accountDoesNotExist) and the directory is a test or staging CA,
// a new account key is registered and the order retried once; production
// directories need the owner's explicit re-registration.
func (i *Issuing) issue(ctx context.Context, plan IssuancePlan, certificate Certificate, approval Approval, force bool) (delivered bool, err error) {
	if !force && i.reusable(plan, approval) {
		// A retry of an approval that already produced a certificate (for
		// example after a refused reload): the placed, valid, recorded
		// certificate is kept instead of ordering a new one.
		return true, nil
	}
	timeout := i.Timeout
	if timeout <= 0 {
		timeout = DefaultIssueTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := i.client(plan.Directory)
	if err != nil {
		return false, err
	}
	var recoveredFrom *string // terms URL of the forgotten account, once recovering
	for attempt := 0; ; attempt++ {
		acct, err := i.loadAccount(plan.Directory)
		if err != nil {
			return false, err
		}
		startKID := acct.KID
		d := &DNS01{
			Providers: i.Providers, Secrets: i.Secrets, Client: client,
			Probe: i.Probe, Interval: i.Interval,
			Issuances: NewIssuances(i.Store), Now: i.now,
			Deliver: func(name string, issued Issued) error {
				if err := docker.PlaceCert(i.StackDir, docker.PlacedCert{Name: name, Names: plan.Names},
					issued.ChainPEM, issued.KeyPEM); err != nil {
					return err
				}
				delivered = true
				return nil
			},
		}
		_, issueErr := d.IssueFor(ctx, plan.Certificate, acct, certificate.Issuer, plan.Zone, plan.Names, approval.AgreedTerms)
		if acct.KID != "" && acct.KID != startKID {
			if saveErr := i.saveAccount(plan.Directory, acct); saveErr != nil {
				issueErr = errors.Join(issueErr, fmt.Errorf("cert: saving ACME account location: %w", saveErr))
			}
		}
		if recoveredFrom != nil && acct.KID != startKID {
			// The new registration is audited with the terms URL it agreed
			// to, flagging a change since the forgotten account (S272).
			detail := fmt.Sprintf("%s: the %s CA %s forgot the ACME account; a new account key was registered automatically for %s directories; terms %q",
				plan.Certificate, plan.DirectoryClass, plan.Directory, plan.DirectoryClass, acct.Terms)
			if *recoveredFrom != acct.Terms {
				detail += fmt.Sprintf(" (changed from %q)", *recoveredFrom)
			}
			if auditErr := i.audit("system", "cert.account.reregister", "ok", detail); auditErr != nil {
				issueErr = errors.Join(issueErr, auditErr)
			}
		}
		if recoveredFrom != nil && acct.KID == "" {
			if auditErr := i.audit("system", "cert.account.reregister", "error",
				fmt.Sprintf("%s: automatic ACME account re-registration with %s failed: %v", plan.Certificate, plan.Directory, issueErr)); auditErr != nil {
				issueErr = errors.Join(issueErr, auditErr)
			}
		}
		var problem *ProblemError
		if attempt == 0 && !delivered && errors.As(issueErr, &problem) &&
			problem.Type == ProblemAccountDoesNotExist && plan.DirectoryClass != DirectoryProduction {
			if resetErr := i.replaceAccount(plan.Directory); resetErr != nil {
				return false, errors.Join(issueErr, resetErr)
			}
			terms := acct.Terms
			recoveredFrom = &terms
			continue
		}
		return delivered, issueErr
	}
}

func (i *Issuing) audit(actor, action, outcome, detail string) error {
	if i.Audit == nil {
		return nil
	}
	if err := i.Audit(actor, action, outcome, detail); err != nil {
		return fmt.Errorf("cert: audit entry not recorded: %w", err)
	}
	return nil
}

// replaceAccount discards the stored account for a directory and stores
// a fresh unregistered key, which the next issuance registers.
func (i *Issuing) replaceAccount(directory string) error {
	key, err := GenerateAccountKey()
	if err != nil {
		return err
	}
	i.accountMu.Lock()
	defer i.accountMu.Unlock()
	return i.saveAccountLocked(directory, &Account{Key: key})
}

// StoreCredentials saves a DNS provider's credential values under the
// conventional secret names the DNS-01 flow reads (S096). The provider
// must be registered, every declared field must be present and
// non-empty, and extras are refused. Values go to the secret store only;
// no surface ever reads them back.
func (i *Issuing) StoreCredentials(provider string, values map[string]string) ([]string, error) {
	if i.Providers == nil || i.Secrets == nil {
		return nil, fmt.Errorf("cert: credential storage is not available")
	}
	p, err := i.Providers.Get(provider)
	if err != nil {
		return nil, err
	}
	fields := p.RequiredCredentials()
	if len(values) != len(fields) {
		return nil, fmt.Errorf("cert: %q takes %d credential field(s)", provider, len(fields))
	}
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		value, ok := values[field.Name]
		if !ok || value == "" {
			return nil, fmt.Errorf("cert: missing credential %q", field.Name)
		}
		names = append(names, field.Name)
	}
	for _, field := range fields {
		if err := i.Secrets.Set(domain.SecretName(provider, field.Name), values[field.Name]); err != nil {
			return nil, fmt.Errorf("cert: store credential %q: %w", field.Name, err)
		}
	}
	return names, nil
}

// reusable reports that this approval was already consumed by an
// issuance still in place: a recorded issuance made at or after the
// approval whose chain is exactly the placed chain, valid, paired with
// its key, and covering the approved names. A fresh approval always
// orders again, which is how the owner replaces a certificate.
func (i *Issuing) reusable(plan IssuancePlan, approval Approval) bool {
	issuances, err := NewIssuances(i.Store).List()
	if err != nil {
		return false
	}
	for _, rec := range issuances {
		if rec.Certificate != plan.Certificate {
			continue
		}
		if rec.IssuedAt.Before(approval.ApprovedAt) {
			return false
		}
		want := NewFingerprint(plan.Names, plan.Provider, plan.Challenge)
		if !rec.Fingerprint.Equal(want) {
			return false
		}
		placed, ok := docker.ReadPlacedChain(i.StackDir, plan.Certificate, plan.Names)
		return ok && string(placed) == string(rec.ChainPEM)
	}
	return false
}
