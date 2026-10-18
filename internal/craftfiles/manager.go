package craftfiles

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SecretName is the sealed app password (ADR-021, reserved sys_ namespace:
// neither the owner secret routes nor configuration export carry it).
const SecretName = "sys_craftfiles_nextcloud"

// Link actions.
const (
	ActionLink   = "link"
	ActionRotate = "rotate"
	ActionUnlink = "unlink"
)

// Manager errors.
var (
	ErrNotLinked     = errors.New("Nextcloud is not linked")
	ErrAlreadyLinked = errors.New("Nextcloud is already linked: rotate or unlink first")
	ErrLocalOnly     = errors.New("this app is set to keep documents browser-local")
	ErrNoRoute       = errors.New("no edge route is recorded for this craft app: install it and reconcile the edge routes")
	ErrNoHook        = errors.New("this craft app has no file hook")
	ErrNoPlan        = errors.New("plan not found or expired")
	ErrInput         = errors.New("invalid request")
	ErrUpstream      = errors.New("Nextcloud check failed")
)

// SecretStore is the slice of the secret store the link needs.
type SecretStore interface {
	SetSystem(name, value string) error
	Get(name string) (string, error)
	Delete(name string) error
}

// CraftRoute is a recorded edge route that serves a craft app.
type CraftRoute struct {
	App  string
	Host string
	TLS  bool
}

// Manager owns the link state: non-secret fields in SQLite and an
// inspectable YAML mirror, the app password in the secret store.
type Manager struct {
	DB       *sql.DB
	Secrets  SecretStore
	Client   func() *http.Client
	YAMLPath string
	Now      func() time.Time
	// Routes lists the recorded craft routes (edge route table).
	Routes func() ([]CraftRoute, error)
	// Audit records an event; values are names and ids only.
	Audit func(actor, action, outcome, detail string)

	mu    sync.Mutex
	plans map[string]*Plan
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

// PlanRequest asks for a link change. It carries no secret.
type PlanRequest struct {
	Action  string `json:"action"`
	BaseURL string `json:"base_url"`
	Account string `json:"account"`
	Root    string `json:"root"`
	// CreateAppPassword approves the daemon creating the app password
	// through Nextcloud's app-password endpoint.
	CreateAppPassword bool `json:"create_app_password"`
	// AllowInsecureHTTP is the owner's explicit opt-in to a plain http://
	// Nextcloud address that is not on this machine: the app password
	// then crosses the network unencrypted. Loopback needs no opt-in.
	AllowInsecureHTTP bool `json:"allow_insecure_http"`
}

// Plan is a reviewable, approvable link change.
type Plan struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	BaseURL   string    `json:"base_url"`
	Account   string    `json:"account"`
	Root      string    `json:"root"`
	Create    bool      `json:"create_app_password"`
	Insecure  bool      `json:"allow_insecure_http,omitempty"`
	Summary   []string  `json:"summary"`
	Warnings  []string  `json:"warnings"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ApproveRequest approves a plan. AppPassword (or AccountPassword when the
// plan asked the daemon to create one) is used once and never stored in
// the plan, returned, logged or audited.
type ApproveRequest struct {
	PlanID          string `json:"plan_id"`
	AppPassword     string `json:"app_password"`
	AccountPassword string `json:"account_password"`
}

const planTTL = 15 * time.Minute

type linkRow struct {
	Link
	DaemonCreated bool
	UpdatedAt     time.Time
}

func (m *Manager) readLink() (linkRow, bool, error) {
	var r linkRow
	var created int
	var updated string
	err := m.DB.QueryRow(`SELECT base_url, account, root, daemon_created, updated_at FROM craft_file_link WHERE id = 1`).
		Scan(&r.BaseURL, &r.Account, &r.Root, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return linkRow{}, false, nil
	}
	if err != nil {
		return linkRow{}, false, fmt.Errorf("craftfiles: read link: %w", err)
	}
	r.DaemonCreated = created != 0
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return r, true, nil
}

// CurrentLink is the proxy's Link provider.
func (m *Manager) CurrentLink() (Link, bool, error) {
	r, ok, err := m.readLink()
	return r.Link, ok, err
}

// Password is the proxy's credential provider.
func (m *Manager) Password() (string, error) { return m.Secrets.Get(SecretName) }

func (m *Manager) localOnly() (map[string]bool, error) {
	rows, err := m.DB.Query(`SELECT app FROM craft_file_local_only`)
	if err != nil {
		return nil, fmt.Errorf("craftfiles: read local-only: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var app string
		if err := rows.Scan(&app); err != nil {
			return nil, err
		}
		out[app] = true
	}
	return out, rows.Err()
}

// Info is the handler's per-app view of the link.
func (m *Manager) Info(app string) (SessionInfo, error) {
	r, ok, err := m.readLink()
	if err != nil {
		return SessionInfo{}, err
	}
	lo, err := m.localOnly()
	if err != nil {
		return SessionInfo{}, err
	}
	return SessionInfo{Linked: ok, LocalOnly: lo[app], Root: r.Root, MaxUpload: 0}, nil
}

// AppStatus is one craft app's file-storage state.
type AppStatus struct {
	App
	LocalOnly bool   `json:"local_only"`
	Host      string `json:"host,omitempty"`
	Ready     bool   `json:"ready"` // linked, not local-only, and a route is recorded
}

// Status is the link state. It never carries the credential.
type Status struct {
	Linked           bool        `json:"linked"`
	BaseURL          string      `json:"base_url,omitempty"`
	Account          string      `json:"account,omitempty"`
	Root             string      `json:"root,omitempty"`
	CredentialStored bool        `json:"credential_stored"`
	DaemonCreated    bool        `json:"daemon_created"`
	UpdatedAt        *time.Time  `json:"updated_at,omitempty"`
	Apps             []AppStatus `json:"apps"`
}

// Status reports the link and per-app state.
func (m *Manager) Status() (Status, error) {
	r, linked, err := m.readLink()
	if err != nil {
		return Status{}, err
	}
	lo, err := m.localOnly()
	if err != nil {
		return Status{}, err
	}
	hosts := map[string]CraftRoute{}
	if m.Routes != nil {
		routes, err := m.Routes()
		if err != nil {
			return Status{}, err
		}
		for _, cr := range routes {
			hosts[cr.App] = cr
		}
	}
	st := Status{Linked: linked, Apps: []AppStatus{}}
	if linked {
		st.BaseURL, st.Account, st.Root, st.DaemonCreated = r.BaseURL, r.Account, r.Root, r.DaemonCreated
		t := r.UpdatedAt
		st.UpdatedAt = &t
		if pw, err := m.Secrets.Get(SecretName); err == nil && pw != "" {
			st.CredentialStored = true
		}
	}
	for _, a := range Apps() {
		cr, has := hosts[a.Name]
		st.Apps = append(st.Apps, AppStatus{
			App: a, LocalOnly: lo[a.Name], Host: cr.Host,
			Ready: linked && st.CredentialStored && !lo[a.Name] && has,
		})
	}
	return st, nil
}

// isLoopbackHost reports hosts that name this machine itself, where plain
// http never leaves it.
func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// validateBaseURL requires https, except loopback hosts, or any http host
// when the owner opted in (insecure reports that the opt-in was needed).
func validateBaseURL(raw string, allowHTTP bool) (base string, insecure bool, err error) {
	u, perr := url.Parse(strings.TrimSpace(raw))
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false, fmt.Errorf("%w: base_url must be an http(s) URL of your Nextcloud without credentials, query or fragment", ErrInput)
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		if !allowHTTP {
			return "", false, fmt.Errorf("%w: base_url must use https: the app password would cross the network unencrypted (an http address is accepted only on this machine, or with allow_insecure_http)", ErrInput)
		}
		insecure = true
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String(), insecure, nil
}

const insecureWarning = "INSECURE: this Nextcloud address uses plain http, so the app password and every document cross your network unencrypted and can be read or changed by anything on that path. Use https unless you accept this."

func validateAccount(a string) error {
	if a == "" || a == "." || a == ".." || len(a) > 128 || strings.ContainsAny(a, "/\\:") {
		return fmt.Errorf("%w: account must be a Nextcloud account name", ErrInput)
	}
	for _, r := range a {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: account must be a Nextcloud account name", ErrInput)
		}
	}
	return nil
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Plan validates a link change and returns it for approval. Nothing
// changes until Approve.
func (m *Manager) Plan(req PlanRequest, actor string) (Plan, error) {
	cur, linked, err := m.readLink()
	if err != nil {
		return Plan{}, err
	}
	p := Plan{ID: newID(), Action: req.Action, ExpiresAt: m.now().Add(planTTL)}
	switch req.Action {
	case ActionLink:
		if linked {
			return Plan{}, ErrAlreadyLinked
		}
		base, insecure, err := validateBaseURL(req.BaseURL, req.AllowInsecureHTTP)
		if err != nil {
			return Plan{}, err
		}
		if err := validateAccount(req.Account); err != nil {
			return Plan{}, err
		}
		root, err := CleanRel(req.Root)
		if err != nil || root == "" {
			return Plan{}, fmt.Errorf("%w: root must be a folder in the account, such as Homestead", ErrInput)
		}
		p.BaseURL, p.Account, p.Root, p.Create = base, req.Account, root, req.CreateAppPassword
		p.Summary = []string{
			fmt.Sprintf("Link %s as account %q; documents live in the folder %q.", base, req.Account, root),
			"The app password is sealed in the Homestead secret store and never shown again.",
		}
		if req.CreateAppPassword {
			p.Summary = append(p.Summary, "Homestead will create the app password with your account password, used once and not kept.")
		}
		p.Warnings = []string{"Homestead enters the file path: it reads and writes your documents on your behalf, within that folder only."}
		if insecure {
			p.Insecure = true
			p.Warnings = append([]string{insecureWarning}, p.Warnings...)
		}
	case ActionRotate:
		if !linked {
			return Plan{}, ErrNotLinked
		}
		if req.BaseURL != "" && strings.TrimSuffix(req.BaseURL, "/") != cur.BaseURL {
			return Plan{}, fmt.Errorf("%w: rotation keeps the linked Nextcloud; unlink and link to change it", ErrInput)
		}
		if req.Account != "" && req.Account != cur.Account {
			return Plan{}, fmt.Errorf("%w: rotation keeps the linked account; unlink and link to change it", ErrInput)
		}
		root := cur.Root
		if req.Root != "" {
			if root, err = CleanRel(req.Root); err != nil || root == "" {
				return Plan{}, fmt.Errorf("%w: invalid root folder", ErrInput)
			}
		}
		if _, insecure, err := validateBaseURL(cur.BaseURL, req.AllowInsecureHTTP); err != nil {
			return Plan{}, fmt.Errorf("%w: the linked address is plain http; confirm allow_insecure_http to rotate, or unlink and link over https", ErrInput)
		} else if insecure {
			p.Insecure = true
			p.Warnings = append(p.Warnings, insecureWarning)
		}
		p.BaseURL, p.Account, p.Root, p.Create = cur.BaseURL, cur.Account, root, req.CreateAppPassword
		p.Summary = []string{fmt.Sprintf("Replace the sealed app password for %q at %s.", cur.Account, cur.BaseURL)}
		if cur.DaemonCreated {
			p.Summary = append(p.Summary, "The old app password Homestead created will be revoked in Nextcloud.")
		} else {
			p.Warnings = append(p.Warnings, "Revoke the old app password in Nextcloud yourself.")
		}
	case ActionUnlink:
		if !linked {
			return Plan{}, ErrNotLinked
		}
		p.BaseURL, p.Account, p.Root = cur.BaseURL, cur.Account, cur.Root
		p.Summary = []string{"Forget the link and delete the sealed app password. Craft apps return to browser-local documents; files already in Nextcloud stay there."}
		if !cur.DaemonCreated {
			p.Warnings = append(p.Warnings, "Revoke the app password in Nextcloud yourself.")
		}
	default:
		return Plan{}, fmt.Errorf("%w: action must be link, rotate or unlink", ErrInput)
	}
	m.mu.Lock()
	if m.plans == nil {
		m.plans = map[string]*Plan{}
	}
	now := m.now()
	for id, old := range m.plans {
		if !old.ExpiresAt.After(now) {
			delete(m.plans, id)
		}
	}
	cp := p
	m.plans[p.ID] = &cp
	m.mu.Unlock()
	m.audit(actor, "craftfiles.plan", "ok", fmt.Sprintf("action=%s account=%s", p.Action, p.Account))
	return p, nil
}

// Approve applies a plan once.
func (m *Manager) Approve(ctx context.Context, req ApproveRequest, actor string) (Status, error) {
	m.mu.Lock()
	p, ok := m.plans[req.PlanID]
	if ok {
		delete(m.plans, req.PlanID)
	}
	m.mu.Unlock()
	if !ok || !p.ExpiresAt.After(m.now()) {
		return Status{}, ErrNoPlan
	}
	var err error
	switch p.Action {
	case ActionUnlink:
		err = m.unlink(ctx)
	default:
		err = m.apply(ctx, p, req)
	}
	if err != nil {
		m.audit(actor, "craftfiles."+p.Action, "error", fmt.Sprintf("account=%s: %v", p.Account, err))
		return Status{}, err
	}
	m.audit(actor, "craftfiles."+p.Action, "ok", fmt.Sprintf("account=%s root=%s", p.Account, p.Root))
	return m.Status()
}

func (m *Manager) apply(ctx context.Context, p *Plan, req ApproveRequest) error {
	link := Link{BaseURL: p.BaseURL, Account: p.Account, Root: p.Root}
	nc := &nextcloud{client: m.Client(), link: link}
	password := req.AppPassword
	created := false
	if p.Create {
		if req.AccountPassword == "" {
			return fmt.Errorf("%w: the account password is required to create an app password", ErrInput)
		}
		var err error
		if password, err = nc.createAppPassword(ctx, req.AccountPassword); err != nil {
			return err
		}
		created = true
	} else if password == "" {
		return fmt.Errorf("%w: an app password is required", ErrInput)
	}
	nc.password = password
	if err := nc.verify(ctx); err != nil {
		if created {
			_ = nc.revoke(ctx)
		}
		return err
	}
	if err := nc.ensureRoot(ctx); err != nil {
		if created {
			_ = nc.revoke(ctx)
		}
		return err
	}
	old, hadLink, err := m.readLink()
	if err != nil {
		return err
	}
	oldPassword, _ := m.Secrets.Get(SecretName)
	if err := m.Secrets.SetSystem(SecretName, password); err != nil {
		return fmt.Errorf("craftfiles: store credential: %w", err)
	}
	now := m.now().UTC().Format(time.RFC3339Nano)
	_, err = m.DB.Exec(`INSERT INTO craft_file_link (id, base_url, account, root, daemon_created, created_at, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET base_url = excluded.base_url, account = excluded.account, root = excluded.root,
			daemon_created = excluded.daemon_created, updated_at = excluded.updated_at`,
		link.BaseURL, link.Account, link.Root, boolInt(created), now, now)
	if err != nil {
		if oldPassword != "" {
			_ = m.Secrets.SetSystem(SecretName, oldPassword)
		} else {
			_ = m.Secrets.Delete(SecretName)
		}
		return fmt.Errorf("craftfiles: record link: %w", err)
	}
	if hadLink && old.DaemonCreated && oldPassword != "" {
		_ = (&nextcloud{client: m.Client(), link: old.Link, password: oldPassword}).revoke(ctx)
	}
	return m.renderYAML()
}

func (m *Manager) unlink(ctx context.Context) error {
	old, ok, err := m.readLink()
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotLinked
	}
	// Order matters so a failure never orphans the credential: revoke the
	// daemon-created app password in Nextcloud (best effort), delete the
	// sealed secret, and only then forget the row. Any failure leaves the
	// row in place, so unlinking again retries the remaining steps.
	pw, _ := m.Secrets.Get(SecretName)
	if old.DaemonCreated && pw != "" {
		_ = (&nextcloud{client: m.Client(), link: old.Link, password: pw}).revoke(ctx)
	}
	if err := m.Secrets.Delete(SecretName); err != nil && !strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("craftfiles: delete credential: %w", err)
	}
	if _, err := m.DB.Exec(`DELETE FROM craft_file_link WHERE id = 1`); err != nil {
		return fmt.Errorf("craftfiles: remove link: %w", err)
	}
	return m.renderYAML()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SetLocalOnly records the per-app opt-out as desired configuration.
func (m *Manager) SetLocalOnly(app string, localOnly bool, actor string) error {
	if _, ok := HookedApp(app); !ok {
		return ErrNoHook
	}
	var err error
	if localOnly {
		_, err = m.DB.Exec(`INSERT OR IGNORE INTO craft_file_local_only (app, created_at) VALUES (?, ?)`, app, m.now().UTC().Format(time.RFC3339Nano))
	} else {
		_, err = m.DB.Exec(`DELETE FROM craft_file_local_only WHERE app = ?`, app)
	}
	if err != nil {
		return fmt.Errorf("craftfiles: set local-only: %w", err)
	}
	m.audit(actor, "craftfiles.local-only", "ok", fmt.Sprintf("app=%s local_only=%t", app, localOnly))
	return m.renderYAML()
}

// LaunchResult is a minted launch: the URL carries the one-time code in its
// fragment, which a browser never sends to any server.
type LaunchResult struct {
	URL       string    `json:"url"`
	App       string    `json:"app"`
	ExpiresAt time.Time `json:"expires_at"`
}

// OpenPagePath is the wrapper page the craft image serves.
const OpenPagePath = "/_homestead/open.html"

// Launch mints a one-time launch code for app, bound to the calling
// operator session.
func (m *Manager) Launch(sessions *Sessions, opToken, username string, opExpires time.Time, app string) (LaunchResult, error) {
	if _, ok := HookedApp(app); !ok {
		return LaunchResult{}, ErrNoHook
	}
	st, err := m.Status()
	if err != nil {
		return LaunchResult{}, err
	}
	if !st.Linked || !st.CredentialStored {
		return LaunchResult{}, ErrNotLinked
	}
	var as AppStatus
	for _, a := range st.Apps {
		if a.Name == app {
			as = a
		}
	}
	if as.LocalOnly {
		return LaunchResult{}, ErrLocalOnly
	}
	routes, err := m.Routes()
	if err != nil {
		return LaunchResult{}, err
	}
	var route *CraftRoute
	for i := range routes {
		if routes[i].App == app {
			route = &routes[i]
		}
	}
	if route == nil {
		return LaunchResult{}, ErrNoRoute
	}
	code, exp, err := sessions.Mint(opToken, username, opExpires, app, route.Host)
	if err != nil {
		return LaunchResult{}, err
	}
	scheme := "https"
	if !route.TLS {
		scheme = "http"
	}
	m.audit(username, "craftfiles.launch", "ok", fmt.Sprintf("app=%s host=%s", app, route.Host))
	return LaunchResult{URL: scheme + "://" + route.Host + OpenPagePath + "#code=" + code, App: app, ExpiresAt: exp}, nil
}

func (m *Manager) renderYAML() error {
	if m.YAMLPath == "" {
		return nil
	}
	r, linked, err := m.readLink()
	if err != nil {
		return err
	}
	lo, err := m.localOnly()
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Generated by Homestead (S254). Craft file storage in your Nextcloud.\n")
	b.WriteString("# Inspectable desired configuration. The app password is never written\n# here; it is sealed in the Homestead secret store.\n")
	fmt.Fprintf(&b, "linked: %t\n", linked)
	if linked {
		j := func(s string) string { x, _ := json.Marshal(s); return string(x) }
		fmt.Fprintf(&b, "base_url: %s\naccount: %s\nroot: %s\ncredential: sealed\n", j(r.BaseURL), j(r.Account), j(r.Root))
	}
	apps := make([]string, 0, len(lo))
	for a := range lo {
		apps = append(apps, a)
	}
	sort.Strings(apps)
	if len(apps) == 0 {
		b.WriteString("local_only: []\n")
	} else {
		b.WriteString("local_only:\n")
		for _, a := range apps {
			fmt.Fprintf(&b, "  - %s\n", a)
		}
	}
	tmp := m.YAMLPath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(m.YAMLPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.YAMLPath)
}

// SyncYAML recreates the mirror at startup.
func (m *Manager) SyncYAML() error { return m.renderYAML() }
