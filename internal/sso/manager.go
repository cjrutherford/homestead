package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"homestead/internal/operation"

	"gopkg.in/yaml.v3"
)

// Sealed secret names (ADR-021, reserved sys_ namespace: neither the owner
// secret routes nor configuration export carry them).
const (
	ClientSecretName = "sys_sso_client_secret"
	APITokenName     = "sys_sso_authentik_token"
)

// Lifetimes (ADR-033).
const (
	StateTTL    = 10 * time.Minute
	PlanTTL     = 30 * time.Minute
	flowTimeout = 10 * time.Second
	// maxOpenStates bounds unauthenticated GET /sso/login records.
	maxOpenStates = 200
)

// Plan actions.
const (
	ActionCreate    = "create"
	ActionUpdate    = "update"
	ActionUnchanged = "unchanged"
	// ActionUnknown means no API token was given, so Authentik was not read.
	ActionUnknown = "inspected at apply"
)

// Flow kinds.
const (
	kindLogin = "login"
	kindLink  = "link"
)

// Fixed browser-facing failure codes (the login page maps each to a
// sentence; the codes carry no detail).
const (
	CodeUnavailable = "unavailable"
	CodeState       = "state"
	CodeDenied      = "denied"
	CodeToken       = "token"
	CodeUnlinked    = "unlinked"
	CodeRate        = "rate-limited"
	CodeOff         = "not-configured"
	CodeConflict    = "link-conflict"
	CodeOrigin      = "origin"
)

// Errors returned to API handlers.
var (
	ErrNotConfigured = errors.New("sign-in with Authentik is not configured")
	ErrNotLinked     = errors.New("no Authentik identity is linked")
	ErrAlreadyLinked = errors.New("an Authentik identity is already linked: unlink it first")
	ErrNoPlan        = errors.New("no current plan: review a plan first")
	ErrDigest        = errors.New("the plan changed since you reviewed it: review it again")
	ErrNotApproved   = errors.New("the plan is not approved")
	ErrInput         = errors.New("invalid request")
	ErrOrigin        = errors.New("this address is not one of the sign-in addresses in the plan")
)

// FlowError is a sign-in failure with its browser-facing code.
type FlowError struct {
	Code string
	Err  error
}

func (e *FlowError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *FlowError) Unwrap() error { return e.Err }

func flowErr(code string, err error) *FlowError { return &FlowError{Code: code, Err: err} }

// SecretStore is the slice of the secret store SSO uses.
type SecretStore interface {
	SetSystem(name, value string) error
	Get(name string) (string, error)
	Delete(name string) error
}

// Defaults are the starting points for a plan, derived from the appliance's
// own state (the edge route table, the tunnel hostnames and the daemon
// port), never from a request.
type Defaults struct {
	AuthentikURL string   `json:"authentik_url"`
	Origins      []string `json:"origins"`
	// PublicOrigins are the dashboard origins the tunnel delivers
	// (ADR-037); PublicAuthentikURL is Authentik's tunnel address, where a
	// browser signing in at a public origin is sent.
	PublicOrigins      []string `json:"public_origins,omitempty"`
	PublicAuthentikURL string   `json:"public_authentik_url,omitempty"`
}

// Manager owns the SSO state.
type Manager struct {
	DB       *sql.DB
	Secrets  SecretStore
	Client   func() *http.Client
	YAMLPath string
	Now      func() time.Time
	// Defaults supplies the plan's starting address and origins.
	Defaults func() Defaults
	// Installed reports whether Authentik is installed; an uninstalled
	// Authentik makes the link dormant.
	Installed func() bool
	// Version is the Authentik version the catalog pins.
	Version func() string
	// Audit records an event; values are names and ids, never tokens.
	Audit func(actor, action, outcome, detail string)
	// RevokeOIDC ends an identity's oidc sessions (unlink, disable).
	RevokeOIDC func(identityID int64) error

	mu        sync.Mutex
	providers map[string]*Provider
	wins      map[string]*rateWindow
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) audit(actor, action, outcome, detail string) {
	if m.Audit != nil {
		m.Audit(actor, action, outcome, detail)
	}
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("sso: entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ---- plan ----

// Object is one Authentik object a plan creates, changes or leaves alone.
type Object struct {
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Action  string   `json:"action"`
	Changes []string `json:"changes,omitempty"`
}

// PlanRequest asks for a plan. Every field is optional: the defaults come
// from the appliance's own state, and the owner edits them here. APIToken
// is used in memory only, to read what already exists in Authentik; it is
// never stored by planning.
type PlanRequest struct {
	AuthentikURL      string   `json:"authentik_url"`
	Origins           []string `json:"origins"`
	SigningKey        string   `json:"signing_key"`
	AuthorizationFlow string   `json:"authorization_flow"`
	APIToken          string   `json:"api_token"`
}

// Plan is a reviewable change to Authentik and to Homestead's sign-in
// configuration. Digest binds the approval to exactly this spec.
type Plan struct {
	Digest    string    `json:"digest"`
	Spec      Spec      `json:"spec"`
	Objects   []Object  `json:"objects"`
	Untouched []string  `json:"untouched"`
	Warnings  []string  `json:"warnings"`
	Inspected bool      `json:"inspected"`
	ExpiresAt time.Time `json:"expires_at"`
}

func digestOf(spec Spec) string {
	raw, _ := json.Marshal(spec)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ValidateOrigin accepts scheme://host[:port] with no path. https is
// required unless the host is a loopback address (this machine only).
func ValidateOrigin(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", fmt.Errorf("%w: %q is not an origin", ErrInput, origin)
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: %q must be scheme://host[:port] with no path", ErrInput, origin)
	}
	if !validHost.MatchString(u.Hostname()) {
		return "", fmt.Errorf("%w: %q is not a valid host name", ErrInput, origin)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	if scheme != "https" && !(scheme == "http" && loopbackHost(u.Hostname())) {
		return "", fmt.Errorf("%w: %q must be https (http is only allowed for this machine)", ErrInput, origin)
	}
	return scheme + "://" + host, nil
}

var validHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$|^[0-9a-fA-F:.]+$`)

func loopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func cleanOrigins(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, o := range in {
		c, err := ValidateOrigin(strings.TrimSpace(o))
		if err != nil {
			return nil, err
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: at least one dashboard address is needed", ErrInput)
	}
	if len(out) > 8 {
		return nil, fmt.Errorf("%w: at most 8 dashboard addresses", ErrInput)
	}
	return out, nil
}

// BuildSpec turns a request and the appliance defaults into a Spec.
func (m *Manager) BuildSpec(req PlanRequest) (Spec, error) {
	var def Defaults
	if m.Defaults != nil {
		def = m.Defaults()
	}
	base := strings.TrimSpace(req.AuthentikURL)
	if base == "" {
		base = def.AuthentikURL
	}
	if base == "" {
		return Spec{}, fmt.Errorf("%w: Authentik has no address yet: give it one in the install wizard, or enter its address here", ErrInput)
	}
	if u, err := url.Parse(base); err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return Spec{}, fmt.Errorf("%w: the Authentik address must be scheme://host[:port]", ErrInput)
	}
	cb, err := ValidateOrigin(base)
	if err != nil {
		return Spec{}, err
	}
	origins := req.Origins
	if len(origins) == 0 {
		origins = def.Origins
	}
	origins, err = cleanOrigins(origins)
	if err != nil {
		return Spec{}, err
	}
	public := map[string]bool{}
	for _, o := range def.PublicOrigins {
		if c, err := ValidateOrigin(o); err == nil {
			public[c] = true
		}
	}
	var publicOrigins []string
	for _, o := range origins {
		if public[o] {
			publicOrigins = append(publicOrigins, o)
		}
	}
	publicAuthentik := ""
	if len(publicOrigins) > 0 && def.PublicAuthentikURL != "" {
		if publicAuthentik, err = ValidateOrigin(def.PublicAuthentikURL); err != nil {
			return Spec{}, err
		}
	}
	spec := Spec{
		PublicOrigins: publicOrigins, PublicAuthentikURL: publicAuthentik,
		AuthentikURL: cb, Issuer: IssuerFor(cb, AppSlug), ClientID: ClientID,
		Origins: origins, AuthorizationFlow: orDefault(req.AuthorizationFlow, DefaultAuthorizationFlow),
		InvalidationFlow: DefaultInvalidationFlow, SigningKey: orDefault(req.SigningKey, DefaultSigningKey),
		Scopes: append([]string(nil), Scopes...), SubMode: "hashed_user_id",
		ProviderName: ProviderName, AppName: AppName, AppSlug: AppSlug,
	}
	for _, o := range origins {
		spec.RedirectURIs = append(spec.RedirectURIs, o+CallbackPath)
	}
	if m.Version != nil {
		spec.AuthentikVersion = m.Version()
	}
	return spec, nil
}

func orDefault(v, d string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return d
}

// Plan builds, stores and returns the current plan. With an API token it
// also reads Authentik to show what already exists; the token is not kept.
func (m *Manager) Plan(ctx context.Context, req PlanRequest, actor string) (Plan, error) {
	spec, err := m.BuildSpec(req)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Spec: spec, Digest: digestOf(spec), ExpiresAt: m.now().Add(PlanTTL),
		Untouched: []string{
			"users and groups", "flows and stages (the two stock flows named here are only referenced)",
			"policies and bindings", "every other application and provider", "Authentik's own settings",
		},
	}
	plan.Warnings = planWarnings(spec)
	plan.Objects = []Object{
		{Kind: "OAuth2/OpenID provider", Name: spec.ProviderName, Action: ActionUnknown},
		{Kind: "Application", Name: spec.AppName + " (" + spec.AppSlug + ")", Action: ActionUnknown},
	}
	if t := strings.TrimSpace(req.APIToken); t != "" {
		secret, _ := m.Secrets.Get(ClientSecretName)
		objs, err := (&API{Base: spec.AuthentikURL, Token: t, Client: m.client}).Inspect(ctx, spec, secret)
		if err != nil {
			return Plan{}, inspectError(err)
		}
		plan.Objects, plan.Inspected = objs, true
	}
	raw, _ := json.Marshal(spec)
	if _, err := m.DB.Exec(
		`INSERT INTO sso_plan (id, digest, spec, created_at) VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET digest = excluded.digest, spec = excluded.spec, created_at = excluded.created_at,
		 approved_digest = '', approved_by = '', approved_at = '', keep_token = 0`,
		plan.Digest, string(raw), stamp(m.now()),
	); err != nil {
		return Plan{}, fmt.Errorf("sso: store plan: %w", err)
	}
	m.audit(actor, "sso.plan", "ok", "digest "+plan.Digest[:12])
	return plan, nil
}

func inspectError(err error) error {
	var ae *APIError
	if asAPIError(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
		return fmt.Errorf("%w: Authentik refused the API token (check it is an API token for an administrator)", ErrInput)
	}
	if errors.Is(err, ErrUnreachable) {
		return fmt.Errorf("%w: Authentik did not answer at the planned address: is it running?", ErrInput)
	}
	return fmt.Errorf("%w: %v", ErrInput, err)
}

func planWarnings(spec Spec) []string {
	w := []string{
		"The API token you give Homestead can do anything an Authentik administrator can; Authentik tokens are not narrowly scoped. Homestead uses it once to create the provider and the application, then forgets it unless you choose to keep it.",
		"Anyone who controls Authentik (an administrator there, a stolen token, a bad upgrade) can sign in to this dashboard as the linked owner. The local owner password always works, and you can unlink or turn sign-in off from it at any time.",
		"Homestead generates the client secret and keeps it sealed; it never appears in this plan, in the audit trail or in an export.",
	}
	for _, o := range spec.Origins {
		if strings.HasPrefix(o, "http://") {
			w = append(w, o+" is not encrypted: sign-in there only works from the machine that runs Homestead.")
		}
	}
	for _, o := range spec.PublicOrigins {
		if spec.PublicAuthentikURL == "" {
			w = append(w, o+" is reached through the tunnel, but Authentik has no tunnel address: sign-in there fails until the tunnel carries Authentik too.")
			continue
		}
		w = append(w, o+" is reached through the tunnel: only Authentik sign-in works there, and the browser signs in at "+spec.PublicAuthentikURL+". Homestead itself still talks to Authentik at "+spec.AuthentikURL+".")
	}
	return w
}

func (m *Manager) client() *http.Client { return m.Client() }

type planRow struct {
	digest, spec, createdAt, approvedDigest, approvedBy string
	keep                                                bool
}

func (m *Manager) readPlan() (planRow, bool, error) {
	var r planRow
	var keep int
	err := m.DB.QueryRow(`SELECT digest, spec, created_at, approved_digest, approved_by, keep_token FROM sso_plan WHERE id = 1`).
		Scan(&r.digest, &r.spec, &r.createdAt, &r.approvedDigest, &r.approvedBy, &keep)
	if errors.Is(err, sql.ErrNoRows) {
		return planRow{}, false, nil
	}
	if err != nil {
		return planRow{}, false, fmt.Errorf("sso: read plan: %w", err)
	}
	r.keep = keep != 0
	return r, true, nil
}

func (m *Manager) livePlan() (planRow, Spec, error) {
	r, ok, err := m.readPlan()
	if err != nil {
		return planRow{}, Spec{}, err
	}
	if !ok {
		return planRow{}, Spec{}, ErrNoPlan
	}
	created, _ := time.Parse(time.RFC3339Nano, r.createdAt)
	if m.now().Sub(created) > PlanTTL {
		return planRow{}, Spec{}, ErrNoPlan
	}
	var spec Spec
	if err := json.Unmarshal([]byte(r.spec), &spec); err != nil {
		return planRow{}, Spec{}, fmt.Errorf("sso: stored plan unreadable: %w", err)
	}
	return r, spec, nil
}

// ApproveRequest approves the reviewed plan by its digest. APIToken is
// sealed under ADR-021; KeepToken is the explicit opt-in to keep it after
// apply.
type ApproveRequest struct {
	Digest    string `json:"digest"`
	APIToken  string `json:"api_token"`
	KeepToken bool   `json:"keep_token"`
}

// Approve binds an approval to the plan's digest and seals the token.
func (m *Manager) Approve(req ApproveRequest, actor string) error {
	row, _, err := m.livePlan()
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(req.Digest), []byte(row.digest)) != 1 {
		return ErrDigest
	}
	token := strings.TrimSpace(req.APIToken)
	if token == "" {
		if _, err := m.Secrets.Get(APITokenName); err != nil {
			return fmt.Errorf("%w: the Authentik API token is needed to approve", ErrInput)
		}
	} else if err := m.Secrets.SetSystem(APITokenName, token); err != nil {
		return fmt.Errorf("sso: seal API token: %w", err)
	}
	keep := 0
	if req.KeepToken {
		keep = 1
	}
	if _, err := m.DB.Exec(`UPDATE sso_plan SET approved_digest = ?, approved_by = ?, approved_at = ?, keep_token = ? WHERE id = 1`,
		row.digest, actor, stamp(m.now()), keep); err != nil {
		return fmt.Errorf("sso: record approval: %w", err)
	}
	m.audit(actor, "sso.approve", "ok", fmt.Sprintf("digest %s keep_token=%t", row.digest[:12], req.KeepToken))
	return nil
}

// ApplyDefinition returns the operation that configures Authentik. It
// refuses unless the stored plan is approved at its current digest.
func (m *Manager) ApplyDefinition(actor string) (operation.Definition, error) {
	row, spec, err := m.livePlan()
	if err != nil {
		return operation.Definition{}, err
	}
	if row.approvedDigest == "" || row.approvedDigest != row.digest || digestOf(spec) != row.digest {
		return operation.Definition{}, ErrNotApproved
	}
	token, err := m.Secrets.Get(APITokenName)
	if err != nil {
		return operation.Definition{}, fmt.Errorf("%w: the sealed API token is missing: approve the plan again", ErrNotApproved)
	}
	api := &API{Base: spec.AuthentikURL, Token: token, Client: m.client}
	keep := row.keep
	var secret string
	return operation.Definition{
		Type: "sso.apply",
		Steps: []operation.StepDef{
			{Name: "reach-authentik", Run: func(ctx context.Context) error {
				v, err := api.Version(ctx)
				if err != nil {
					return m.failed(actor, "reach Authentik", inspectError(err))
				}
				if want := spec.AuthentikVersion; want != "" && v != "" && majorMinor(v) != majorMinor(want) {
					m.audit(actor, "sso.apply", "ok", fmt.Sprintf("note: Authentik reports %s, the catalog pins %s", v, want))
				}
				return nil
			}},
			{Name: "client-secret", Run: func(context.Context) error {
				s, err := m.Secrets.Get(ClientSecretName)
				if err != nil {
					if s, err = randomString(36); err != nil {
						return err
					}
					if err := m.Secrets.SetSystem(ClientSecretName, s); err != nil {
						return fmt.Errorf("seal client secret: %w", err)
					}
				}
				secret = s
				return nil
			}},
			{Name: "configure-authentik", Run: func(ctx context.Context) error {
				var steps []string
				if err := api.Reconcile(ctx, spec, secret, func(s string) { steps = append(steps, s) }); err != nil {
					return m.failed(actor, "configure Authentik", err)
				}
				m.audit(actor, "sso.apply.authentik", "ok", strings.Join(steps, "; "))
				return nil
			}},
			{Name: "verify-issuer", Run: func(ctx context.Context) error {
				p := &Provider{Issuer: spec.Issuer, Client: m.client, Now: m.Now}
				if _, err := p.Discover(ctx); err != nil {
					return m.failed(actor, "check the issuer", err)
				}
				return nil
			}},
			{Name: "record-configuration", Run: func(context.Context) error {
				return m.recordConfig(spec, actor)
			}},
			{Name: "forget-token", Run: func(context.Context) error {
				if keep {
					m.audit(actor, "sso.token", "ok", "Authentik API token kept (owner opted in)")
					return nil
				}
				if err := m.Secrets.Delete(APITokenName); err != nil {
					return fmt.Errorf("forget the API token: %w", err)
				}
				m.audit(actor, "sso.token", "ok", "Authentik API token forgotten")
				return nil
			}},
		},
	}, nil
}

func (m *Manager) failed(actor, what string, err error) error {
	m.audit(actor, "sso.apply", "error", what+" failed")
	return err
}

// dropLink ends the identity's oidc sessions and only then deletes the
// link, so a failed revocation leaves the link in place and a retry revokes
// again.
func (m *Manager) dropLink(link linkRow) error {
	if m.RevokeOIDC != nil {
		if err := m.RevokeOIDC(link.IdentityID); err != nil {
			return err
		}
	}
	if _, err := m.DB.Exec(`DELETE FROM sso_link`); err != nil {
		return fmt.Errorf("sso: remove link: %w", err)
	}
	return nil
}

func (m *Manager) recordConfig(spec Spec, actor string) error {
	raw, _ := json.Marshal(spec)
	now := stamp(m.now())
	if _, err := m.DB.Exec(
		`INSERT INTO sso_config (id, spec, applied_at, updated_at) VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET spec = excluded.spec, applied_at = excluded.applied_at, updated_at = excluded.updated_at`,
		string(raw), now, now); err != nil {
		return fmt.Errorf("sso: record configuration: %w", err)
	}
	// A new issuer or client invalidates any link made against another.
	if link, ok, _ := m.readLink(); ok && link.Issuer != spec.Issuer {
		if err := m.dropLink(link); err != nil {
			return err
		}
	}
	_, _ = m.DB.Exec(`DELETE FROM sso_plan`)
	m.mu.Lock()
	m.providers = nil
	m.mu.Unlock()
	m.audit(actor, "sso.apply", "ok", "issuer "+spec.Issuer+" origins "+strings.Join(spec.Origins, ","))
	return m.syncYAML(spec)
}

type yamlDoc struct {
	Version int  `yaml:"version"`
	SSO     Spec `yaml:"sso"`
}

// syncYAML mirrors the non-secret configuration to an inspectable file
// (ADR-003). The link and every secret stay out of it.
func (m *Manager) syncYAML(spec Spec) error {
	if m.YAMLPath == "" {
		return nil
	}
	data, err := yaml.Marshal(yamlDoc{Version: 1, SSO: spec})
	if err != nil {
		return err
	}
	tmp := m.YAMLPath + ".tmp"
	if err := os.WriteFile(tmp, append([]byte("# Dashboard sign-in with Authentik (S286). Managed by Homestead; no secrets.\n"), data...), 0o644); err != nil {
		return fmt.Errorf("sso: write %s: %w", filepath.Base(m.YAMLPath), err)
	}
	return os.Rename(tmp, m.YAMLPath)
}

// ---- state ----

type linkRow struct {
	Issuer     string
	Sub        string
	IdentityID int64
	LinkedAt   time.Time
}

func (m *Manager) readLink() (linkRow, bool, error) {
	var r linkRow
	var at string
	err := m.DB.QueryRow(`SELECT issuer, sub, identity_id, linked_at FROM sso_link WHERE id = 1`).Scan(&r.Issuer, &r.Sub, &r.IdentityID, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return linkRow{}, false, nil
	}
	if err != nil {
		return linkRow{}, false, fmt.Errorf("sso: read link: %w", err)
	}
	r.LinkedAt, _ = time.Parse(time.RFC3339Nano, at)
	return r, true, nil
}

func (m *Manager) readConfig() (Spec, bool, error) {
	var raw string
	err := m.DB.QueryRow(`SELECT spec FROM sso_config WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Spec{}, false, nil
	}
	if err != nil {
		return Spec{}, false, fmt.Errorf("sso: read configuration: %w", err)
	}
	var spec Spec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return Spec{}, false, fmt.Errorf("sso: configuration unreadable: %w", err)
	}
	return spec, true, nil
}

// Status is the owner-facing state. It carries no secret and no token.
type Status struct {
	// State is none, planned, approved or configured.
	State        string     `json:"state"`
	Configured   bool       `json:"configured"`
	Linked       bool       `json:"linked"`
	Dormant      bool       `json:"dormant"`
	Offered      bool       `json:"offered"`
	Spec         *Spec      `json:"spec,omitempty"`
	Subject      string     `json:"subject,omitempty"`
	LinkedAt     *time.Time `json:"linked_at,omitempty"`
	SecretSealed bool       `json:"client_secret_sealed"`
	TokenKept    bool       `json:"api_token_kept"`
	Plan         *PlanState `json:"plan,omitempty"`
}

// PlanState summarises the pending plan.
type PlanState struct {
	Digest    string    `json:"digest"`
	Approved  bool      `json:"approved"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Status reads local state only; it never contacts Authentik.
func (m *Manager) Status() (Status, error) {
	st := Status{State: "none"}
	spec, configured, err := m.readConfig()
	if err != nil {
		return st, err
	}
	if configured {
		st.Configured, st.Spec, st.State = true, &spec, "configured"
	}
	link, linked, err := m.readLink()
	if err != nil {
		return st, err
	}
	if linked {
		st.Linked, st.Subject = true, link.Sub
		at := link.LinkedAt
		st.LinkedAt = &at
	}
	if m.Installed != nil && configured && !m.Installed() {
		st.Dormant = true
	}
	st.Offered = st.Configured && st.Linked && !st.Dormant && link.Issuer == spec.Issuer
	_, errSecret := m.Secrets.Get(ClientSecretName)
	st.SecretSealed = errSecret == nil
	_, errTok := m.Secrets.Get(APITokenName)
	st.TokenKept = errTok == nil && configured
	if row, ok, err := m.readPlan(); err == nil && ok {
		created, _ := time.Parse(time.RFC3339Nano, row.createdAt)
		if exp := created.Add(PlanTTL); exp.After(m.now()) {
			st.Plan = &PlanState{Digest: row.digest, Approved: row.approvedDigest == row.digest, ExpiresAt: exp}
			if !configured {
				st.State = "planned"
				if st.Plan.Approved {
					st.State = "approved"
				}
			}
		}
	}
	return st, nil
}

// Offered is the public login-page answer, from local state only.
func (m *Manager) Offered() bool {
	st, err := m.Status()
	return err == nil && st.Offered
}

// Disable turns sign-in with Authentik off: configuration, link, plan,
// flow state and both sealed secrets are removed, and oidc sessions end.
// The local owner is untouched. Objects in Authentik are left as they are.
func (m *Manager) Disable(actor string) error {
	link, linked, err := m.readLink()
	if err != nil {
		return err
	}
	if linked {
		if err := m.dropLink(link); err != nil {
			return err
		}
	}
	for _, q := range []string{`DELETE FROM sso_config`, `DELETE FROM sso_plan`, `DELETE FROM sso_state`} {
		if _, err := m.DB.Exec(q); err != nil {
			return fmt.Errorf("sso: disable: %w", err)
		}
	}
	// Secret deletion is idempotent: an absent secret is success.
	for _, name := range []string{ClientSecretName, APITokenName} {
		if err := m.Secrets.Delete(name); err != nil {
			return fmt.Errorf("sso: disable: remove sealed secret %s: %w", name, err)
		}
	}
	m.mu.Lock()
	m.providers = nil
	m.mu.Unlock()
	if m.YAMLPath != "" {
		_ = os.Remove(m.YAMLPath)
	}
	m.audit(actor, "sso.disable", "ok", "sign-in with Authentik turned off")
	return nil
}

// Unlink removes the single link and ends the owner's oidc sessions.
// Sessions are revoked first, so a failure can be retried.
func (m *Manager) Unlink(actor string) error {
	link, ok, err := m.readLink()
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotLinked
	}
	if err := m.dropLink(link); err != nil {
		return err
	}
	_, _ = m.DB.Exec(`DELETE FROM sso_state`)
	m.audit(actor, "sso.unlink", "ok", "method=password subject="+link.Sub)
	return nil
}

// ---- flows ----

// rebase moves an endpoint URL onto another origin, keeping its path and
// query.
func rebase(endpoint, origin string) string {
	u, err := url.Parse(endpoint)
	o, err2 := url.Parse(origin)
	if err != nil || err2 != nil {
		return endpoint
	}
	u.Scheme, u.Host = o.Scheme, o.Host
	return u.String()
}

// OriginFor selects the redirect origin for a request by exact match of
// its Host against the planned origins. The scheme comes from the planned
// origin, never from the request or X-Forwarded-*; an unplanned Host is
// refused.
func OriginFor(spec Spec, host string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", false
	}
	for _, o := range spec.Origins {
		if u, err := url.Parse(o); err == nil && u.Host == host {
			return o, true
		}
	}
	return "", false
}

func (m *Manager) provider(spec Spec) *Provider {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.providers[spec.Issuer]; p != nil {
		return p
	}
	if m.providers == nil {
		m.providers = map[string]*Provider{}
	}
	p := &Provider{Issuer: spec.Issuer, Client: m.client, Now: m.Now}
	m.providers[spec.Issuer] = p
	return p
}

// FlowStart is where to send the browser.
type FlowStart struct {
	URL       string
	State     string
	Secure    bool // the redirect origin is https
	ExpiresAt time.Time
}

func (m *Manager) begin(ctx context.Context, kind, origin string, identityID int64, actor string) (FlowStart, error) {
	spec, ok, err := m.readConfig()
	if err != nil {
		return FlowStart{}, err
	}
	if !ok {
		return FlowStart{}, ErrNotConfigured
	}
	redirect := ""
	for _, uri := range spec.RedirectURIs {
		if uri == origin+CallbackPath {
			redirect = uri
		}
	}
	if redirect == "" {
		return FlowStart{}, ErrOrigin
	}
	ctx, cancel := context.WithTimeout(ctx, flowTimeout)
	defer cancel()
	d, err := m.provider(spec).DiscoverFresh(ctx)
	if err != nil {
		return FlowStart{}, flowErr(CodeUnavailable, err)
	}
	state, err1 := randomString(32)
	nonce, err2 := randomString(32)
	verifier, err3 := randomString(48)
	if err := errors.Join(err1, err2, err3); err != nil {
		return FlowStart{}, err
	}
	now := m.now()
	_, _ = m.DB.Exec(`DELETE FROM sso_state WHERE expires_at <= ?`, stamp(now))
	if kind == kindLogin {
		var open int
		_ = m.DB.QueryRow(`SELECT COUNT(*) FROM sso_state WHERE kind = ?`, kindLogin).Scan(&open)
		if open >= maxOpenStates {
			return FlowStart{}, flowErr(CodeRate, errors.New("too many sign-ins in flight"))
		}
	}
	exp := now.Add(StateTTL)
	if _, err := m.DB.Exec(
		`INSERT INTO sso_state (state_hash, kind, nonce, verifier, redirect_uri, identity_id, actor, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		hashState(state), kind, nonce, verifier, redirect, identityID, actor, stamp(exp)); err != nil {
		return FlowStart{}, fmt.Errorf("sso: store state: %w", err)
	}
	if spec.PublicAuthentikURL != "" && slices.Contains(spec.PublicOrigins, origin) {
		// A browser on the tunnel cannot reach Authentik's local address:
		// send it to Authentik's tunnel address. Discovery, the token
		// exchange and the issuer stay local (ADR-037).
		d.AuthorizationEndpoint = rebase(d.AuthorizationEndpoint, spec.PublicAuthentikURL)
	}
	return FlowStart{
		URL: AuthURL(d, AuthURLParams{ClientID: spec.ClientID, RedirectURI: redirect, State: state, Nonce: nonce,
			Challenge: Challenge(verifier), Fresh: kind == kindLink}),
		State: state, Secure: strings.HasPrefix(origin, "https://"), ExpiresAt: exp,
	}, nil
}

func hashState(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// StartLogin begins a sign-in from the planned origin matching host. It is
// refused unless sign-in is offered.
func (m *Manager) StartLogin(ctx context.Context, host string) (FlowStart, error) {
	st, err := m.Status()
	if err != nil {
		return FlowStart{}, err
	}
	if !st.Offered {
		return FlowStart{}, ErrNotConfigured
	}
	origin, ok := OriginFor(*st.Spec, host)
	if !ok {
		return FlowStart{}, ErrOrigin
	}
	return m.begin(ctx, kindLogin, origin, 0, "")
}

// CreateLinkIntent mints the single-use, ten minute link intent for the
// authenticated and re-authenticated owner and returns the URL that runs
// the flow with max_age=30. origin must be a planned origin; when empty
// the request's host selects one.
func (m *Manager) CreateLinkIntent(ctx context.Context, identityID int64, actor, host, origin string) (FlowStart, error) {
	spec, ok, err := m.readConfig()
	if err != nil {
		return FlowStart{}, err
	}
	if !ok {
		return FlowStart{}, ErrNotConfigured
	}
	if _, linked, err := m.readLink(); err != nil {
		return FlowStart{}, err
	} else if linked {
		return FlowStart{}, ErrAlreadyLinked
	}
	if origin == "" {
		if origin, ok = OriginFor(spec, host); !ok {
			return FlowStart{}, ErrOrigin
		}
	}
	// A previous intent for the owner is replaced, so one is ever live.
	_, _ = m.DB.Exec(`DELETE FROM sso_state WHERE kind = ? AND identity_id = ?`, kindLink, identityID)
	start, err := m.begin(ctx, kindLink, origin, identityID, actor)
	if err != nil {
		return FlowStart{}, err
	}
	return start, nil
}

// Outcome is a finished flow.
type Outcome struct {
	Kind       string
	IdentityID int64
	Subject    string
	Actor      string
	Secure     bool
}

// Kinds exposed for handlers.
const (
	KindLogin = kindLogin
	KindLink  = kindLink
)

type stateRow struct {
	kind, nonce, verifier, redirect, actor string
	identityID                             int64
	expires                                time.Time
}

func (m *Manager) consume(state string) (stateRow, error) {
	var r stateRow
	var exp string
	err := m.DB.QueryRow(
		`DELETE FROM sso_state WHERE state_hash = ? RETURNING kind, nonce, verifier, redirect_uri, identity_id, actor, expires_at`,
		hashState(state)).Scan(&r.kind, &r.nonce, &r.verifier, &r.redirect, &r.identityID, &r.actor, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return r, flowErr(CodeState, errors.New("unknown or already used state"))
	}
	if err != nil {
		return r, fmt.Errorf("sso: read state: %w", err)
	}
	r.expires, _ = time.Parse(time.RFC3339Nano, exp)
	if !r.expires.After(m.now()) {
		return r, flowErr(CodeState, errors.New("state expired"))
	}
	return r, nil
}

// Callback finishes a flow: it consumes the state (single use), redeems the
// code on the back channel, verifies the ID token and applies the link or
// login rule. cookieState is the value of the browser's state cookie; the
// login flow requires it to equal state, the link flow is bound by state
// alone (the Strict session cookie is not sent on the return).
func (m *Manager) Callback(ctx context.Context, state, code, providerError, cookieState string) (Outcome, error) {
	if state == "" {
		return Outcome{}, flowErr(CodeState, errors.New("missing state"))
	}
	// A login state is bound to the browser that started it: compare the
	// cookie before consuming, so a third party who knows a pending state
	// cannot burn it.
	var pending string
	if err := m.DB.QueryRow(`SELECT kind FROM sso_state WHERE state_hash = ?`, hashState(state)).Scan(&pending); err == nil &&
		pending == kindLogin && subtle.ConstantTimeCompare([]byte(cookieState), []byte(state)) != 1 {
		return Outcome{Kind: pending}, flowErr(CodeState, errors.New("state cookie does not match"))
	}
	row, err := m.consume(state)
	if err != nil {
		return Outcome{Kind: row.kind, Actor: row.actor}, err
	}
	if row.kind == kindLogin && subtle.ConstantTimeCompare([]byte(cookieState), []byte(state)) != 1 {
		return Outcome{Kind: row.kind, Actor: row.actor}, flowErr(CodeState, errors.New("state cookie does not match"))
	}
	spec, ok, err := m.readConfig()
	if err != nil {
		return Outcome{Kind: row.kind, Actor: row.actor}, err
	}
	if !ok {
		return Outcome{Kind: row.kind, Actor: row.actor}, flowErr(CodeOff, ErrNotConfigured)
	}
	if providerError != "" {
		return Outcome{Kind: row.kind, Actor: row.actor}, flowErr(CodeDenied, errors.New("Authentik reported "+safeToken(providerError)))
	}
	if code == "" {
		return Outcome{Kind: row.kind, Actor: row.actor}, flowErr(CodeDenied, errors.New("no authorization code"))
	}
	secret, err := m.Secrets.Get(ClientSecretName)
	if err != nil {
		return Outcome{Kind: row.kind, Actor: row.actor}, flowErr(CodeOff, errors.New("client secret is not sealed"))
	}
	ctx, cancel := context.WithTimeout(ctx, flowTimeout)
	defer cancel()
	p := m.provider(spec)
	raw, err := p.ExchangeCode(ctx, spec.ClientID, secret, code, row.verifier, row.redirect)
	if err != nil {
		return Outcome{Kind: row.kind, Actor: row.actor}, classify(err)
	}
	claims, err := p.Verify(ctx, raw, spec.ClientID, row.nonce)
	if err != nil {
		return Outcome{Kind: row.kind, Actor: row.actor}, classify(err)
	}
	out := Outcome{Kind: row.kind, Subject: claims.Subject, Actor: row.actor, Secure: strings.HasPrefix(row.redirect, "https://")}
	switch row.kind {
	case kindLink:
		now := stamp(m.now())
		if _, err := m.DB.Exec(`INSERT INTO sso_link (id, issuer, sub, identity_id, linked_at) VALUES (1, ?, ?, ?, ?)`,
			claims.Issuer, claims.Subject, row.identityID, now); err != nil {
			return out, flowErr(CodeConflict, errors.New("a link already exists"))
		}
		out.IdentityID = row.identityID
	default:
		link, linked, err := m.readLink()
		if err != nil {
			return out, err
		}
		if !linked || link.Issuer != claims.Issuer || link.Sub != claims.Subject {
			return out, flowErr(CodeUnlinked, errors.New("subject is not the linked identity"))
		}
		if m.Installed != nil && !m.Installed() {
			return out, flowErr(CodeOff, errors.New("Authentik is not installed"))
		}
		out.IdentityID = link.IdentityID
	}
	return out, nil
}

// PendingLink reports whether state names a live link intent. The first
// link is completed by a callback that arrives while nothing is linked yet,
// so that callback is reachable on the strength of its intent alone.
func (m *Manager) PendingLink(state string) bool {
	if state == "" {
		return false
	}
	var exp string
	err := m.DB.QueryRow(`SELECT expires_at FROM sso_state WHERE state_hash = ? AND kind = ?`, hashState(state), kindLink).Scan(&exp)
	if err != nil {
		return false
	}
	t, _ := time.Parse(time.RFC3339Nano, exp)
	return t.After(m.now())
}

func safeToken(s string) string {
	if len(s) > 32 {
		s = s[:32]
	}
	var b strings.Builder
	for _, r := range s {
		if r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func classify(err error) error {
	switch {
	case errors.Is(err, ErrUnreachable), errors.Is(err, ErrDiscovery):
		return flowErr(CodeUnavailable, err)
	default:
		return flowErr(CodeToken, err)
	}
}

// ---- rate limit ----

const (
	rateWindowLen = time.Minute
	rateMax       = 30
	// rateMaxKeys bounds the per-address map; addresses beyond it share
	// one overflow bucket.
	rateMaxKeys  = 1024
	rateOverflow = "*overflow*"
)

type rateWindow struct {
	start    time.Time
	n        int
	rejected int
}

// Allow counts one flow request from addr (the host part of the request's
// RemoteAddr as the daemon sees it; never a forwarded header) against that
// address's per-minute budget and reports whether it may proceed. The first
// rejection of a window and the final count when the window ends are
// audited, so a flood is one entry, not thousands.
func (m *Manager) Allow(addr string) bool {
	m.mu.Lock()
	now := m.now()
	if m.wins == nil {
		m.wins = map[string]*rateWindow{}
	}
	type note struct {
		addr string
		n    int
	}
	var flushes []note
	if len(m.wins) >= rateMaxKeys {
		for k, w := range m.wins {
			if now.Sub(w.start) >= rateWindowLen {
				if w.rejected > 0 {
					flushes = append(flushes, note{k, w.rejected})
				}
				delete(m.wins, k)
			}
		}
	}
	key := addr
	w := m.wins[key]
	if w == nil && len(m.wins) >= rateMaxKeys {
		key = rateOverflow
		w = m.wins[key]
	}
	if w != nil && now.Sub(w.start) >= rateWindowLen {
		if w.rejected > 0 {
			flushes = append(flushes, note{key, w.rejected})
		}
		w = nil
	}
	if w == nil {
		w = &rateWindow{start: now}
		m.wins[key] = w
	}
	w.n++
	allowed := w.n <= rateMax
	first := false
	if !allowed {
		w.rejected++
		first = w.rejected == 1
	}
	m.mu.Unlock()
	for _, f := range flushes {
		m.audit("anonymous", "sso.rate-limited", "error", fmt.Sprintf("%d sign-in requests from %s refused in the previous minute", f.n, f.addr))
	}
	if first {
		m.audit("anonymous", "sso.rate-limited", "error", "sign-in requests from "+key+" are being refused: more than 30 in a minute")
	}
	return allowed
}

// ---- helpers for the server ----

func majorMinor(v string) string {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}
