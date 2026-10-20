package cert

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"homestead/internal/docker"
	"homestead/internal/store"
)

// Local leaf certificates (S280, ADR-030). Every local-only host the
// edge serves with TLS mode "internal" gets a short-lived leaf from the
// persisted internal CA, placed and presented through the same validated
// placement (docker.PlaceCert) and forced reload as S278. Public routes
// and hosts with a public-provider certificate are never touched.
const (
	// DefaultLocalLeafLifetime keeps leaves short so rotation is routine;
	// they renew when a third of the lifetime remains (the S272 rule).
	DefaultLocalLeafLifetime = 90 * 24 * time.Hour
	// orphanGrace protects a freshly issued leaf from the orphan sweep:
	// the edge route table is saved asynchronously after issuance, so a
	// leaf can briefly precede its route.
	orphanGrace = 15 * time.Minute
)

// Leaf statuses.
const (
	LeafOK           = "ok"
	LeafRenewalDue   = "renewal-due"
	LeafExpired      = "expired"
	LeafMissing      = "missing"
	LeafMismatch     = "mismatch"      // placed chain does not cover the host or chain to the current root
	LeafNotPresented = "not-presented" // the edge snippet does not load the leaf
	LeafUncoverable  = "uncoverable"   // host outside the root's name constraints
	LeafOrphan       = "orphan"        // recorded leaf whose route is gone or not local-only
)

// Leaf is the public view of one local leaf; never key material.
type Leaf struct {
	Host              string    `json:"host"`
	CertName          string    `json:"cert_name"`
	Serial            string    `json:"serial,omitempty"`
	FingerprintSHA256 string    `json:"fingerprint_sha256,omitempty"`
	CAFingerprint     string    `json:"ca_fingerprint_sha256,omitempty"`
	NotBefore         time.Time `json:"not_before,omitempty"`
	NotAfter          time.Time `json:"not_after,omitempty"`
	IssuedAt          time.Time `json:"issued_at,omitempty"`
	Status            string    `json:"status"`
	Detail            string    `json:"detail,omitempty"`
}

// LocalTLS issues, renews and audits local leaves.
type LocalTLS struct {
	CA       *LocalCA
	Store    *store.Store
	StackDir string
	// Routes lists the routes the edge serves (table plus approved).
	Routes func() ([]docker.Route, error)
	// Reload asks the edge to adopt snippets; nil means docker.ReloadProxy.
	Reload func(context.Context) error
	Audit  func(actor, action, outcome, detail string) error
	// LeafLifetime of new leaves; zero means DefaultLocalLeafLifetime.
	LeafLifetime time.Duration
	Now          func() time.Time

	mu sync.Mutex
}

func (t *LocalTLS) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *LocalTLS) lifetime() time.Duration {
	if t.LeafLifetime > 0 {
		return t.LeafLifetime
	}
	return DefaultLocalLeafLifetime
}

func (t *LocalTLS) audit(actor, action, outcome, detail string) {
	if t.Audit != nil {
		_ = t.Audit(actor, action, outcome, detail)
	}
}

// EligibleLocal reports a route that gets a local leaf: internal TLS and
// not public. Public exposure keeps its ADR-010 provider.
func EligibleLocal(r docker.Route) bool {
	return r.TLS == docker.TLSInternal && r.Exposure != docker.ExposurePublic
}

func aliasStrings(r docker.Route) []string {
	var out []string
	for _, a := range r.Aliases {
		out = append(out, string(a))
	}
	return out
}

// aliasesAllowed: every extra name of a route must be one the root can
// vouch for, or the single leaf could not be issued.
func aliasesAllowed(r docker.Route) bool {
	for _, a := range r.Aliases {
		if !LocalNameAllowed(string(a)) {
			return false
		}
	}
	return true
}

func (t *LocalTLS) records() (map[string]Leaf, error) {
	rows, err := t.Store.DB().Query(`SELECT host, cert_name, serial, fingerprint_sha256, ca_fingerprint, not_before, not_after, issued_at FROM local_leaves`)
	if err != nil {
		return nil, fmt.Errorf("cert: read local leaves: %w", err)
	}
	defer rows.Close()
	out := map[string]Leaf{}
	for rows.Next() {
		var l Leaf
		var nb, na, ia string
		if err := rows.Scan(&l.Host, &l.CertName, &l.Serial, &l.FingerprintSHA256, &l.CAFingerprint, &nb, &na, &ia); err != nil {
			return nil, err
		}
		l.NotBefore, l.NotAfter, l.IssuedAt = parseTime(nb), parseTime(na), parseTime(ia)
		out[l.Host] = l
	}
	return out, rows.Err()
}

func (t *LocalTLS) save(l Leaf) error {
	_, err := t.Store.DB().Exec(`INSERT INTO local_leaves (host, cert_name, serial, fingerprint_sha256, ca_fingerprint, not_before, not_after, issued_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host) DO UPDATE SET cert_name = excluded.cert_name, serial = excluded.serial,
		fingerprint_sha256 = excluded.fingerprint_sha256, ca_fingerprint = excluded.ca_fingerprint,
		not_before = excluded.not_before, not_after = excluded.not_after, issued_at = excluded.issued_at`,
		l.Host, l.CertName, l.Serial, l.FingerprintSHA256, l.CAFingerprint, fmtTime(l.NotBefore), fmtTime(l.NotAfter), fmtTime(l.IssuedAt))
	if err != nil {
		return fmt.Errorf("cert: record local leaf: %w", err)
	}
	return nil
}

// assess grades one recorded leaf against disk and the current root.
func (t *LocalTLS) assess(rec Leaf, ca CAInfo, routeOK bool, route docker.Route, now time.Time) Leaf {
	out := rec
	chain, cerr := docker.ReadPlacedChainAny(t.StackDir, rec.CertName)
	switch {
	case !routeOK:
		out.Status, out.Detail = LeafOrphan, "no local-only HTTPS route uses this leaf any more"
		return out
	case cerr != nil:
		out.Status, out.Detail = LeafMissing, "the placed certificate is missing or unreadable"
		return out
	}
	leaf, err := parseCert(chain)
	if err != nil {
		out.Status, out.Detail = LeafMismatch, "the placed certificate is unreadable"
		return out
	}
	if !now.Before(leaf.NotAfter) {
		out.Status, out.Detail = LeafExpired, "the leaf expired "+leaf.NotAfter.UTC().Format(time.RFC3339)
		return out
	}
	pool := x509.NewCertPool()
	if root, err := t.CA.RootPEM(); err == nil {
		if rc, err := parseCert(root); err == nil {
			pool.AddCert(rc)
		}
	}
	for _, name := range append([]string{rec.Host}, aliasStrings(route)...) {
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: pool, CurrentTime: now,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			out.Status = LeafMismatch
			out.Detail = "the placed certificate does not cover " + name + " under the current root: " + err.Error()
			return out
		}
	}
	if !docker.RouteServesCert(t.StackDir, route.Host, rec.CertName) && docker.RouteSnippetExists(t.StackDir, route.Host) {
		out.Status, out.Detail = LeafNotPresented, "the edge route does not present the internal-CA leaf; re-issue or reconcile"
		return out
	}
	total := leaf.NotAfter.Sub(leaf.NotBefore)
	if leaf.NotAfter.Sub(now) <= total/3 {
		out.Status, out.Detail = LeafRenewalDue, "renews automatically; "+leaf.NotAfter.UTC().Format(time.RFC3339)+" expiry is near"
		return out
	}
	out.Status = LeafOK
	out.NotBefore, out.NotAfter = leaf.NotBefore, leaf.NotAfter
	return out
}

// Report is the local-TLS state: the root plus every leaf graded.
type Report struct {
	CA       CAInfo `json:"ca"`
	Leaves   []Leaf `json:"leaves"`
	Eligible int    `json:"eligible_routes"`
	// OrphanedKey: a CA key exists in the secret store with no CA row.
	OrphanedKey bool `json:"orphaned_key,omitempty"`
	// LeftoverKeys: old-generation CA key secrets not yet destroyed.
	LeftoverKeys []string `json:"leftover_keys,omitempty"`
}

// Report grades the root and all leaves at the injected clock.
func (t *LocalTLS) Report() (Report, error) {
	now := t.now()
	ca, err := t.CA.Info()
	if err != nil {
		return Report{}, err
	}
	routes, err := t.Routes()
	if err != nil {
		return Report{}, err
	}
	recs, err := t.records()
	if err != nil {
		return Report{}, err
	}
	byHost := map[string]docker.Route{}
	var eligible []string
	for _, r := range routes {
		if EligibleLocal(r) {
			byHost[string(r.Host)] = r
			eligible = append(eligible, string(r.Host))
		}
	}
	sort.Strings(eligible)
	leaves := []Leaf{}
	seen := map[string]bool{}
	for _, host := range eligible {
		seen[host] = true
		rec, ok := recs[host]
		switch {
		case !LocalNameAllowed(host):
			leaves = append(leaves, Leaf{Host: host, Status: LeafUncoverable,
				Detail: "outside the root's name constraints (" + strings.Join(LocalPermittedDomains, ", ") + "); the edge keeps its own internal issuance for this host"})
		case !ok || !ca.Present:
			leaves = append(leaves, Leaf{Host: host, CertName: docker.LocalCertName(docker.Hostname(host)), Status: LeafMissing,
				Detail: "no leaf issued yet; the next reconcile issues one"})
		default:
			leaves = append(leaves, t.assess(rec, ca, true, byHost[host], now))
		}
	}
	var orphans []string
	for host := range recs {
		if !seen[host] {
			orphans = append(orphans, host)
		}
	}
	sort.Strings(orphans)
	for _, host := range orphans {
		leaves = append(leaves, t.assess(recs[host], ca, false, docker.Route{}, now))
	}
	return Report{CA: ca, Leaves: leaves, Eligible: len(eligible),
		OrphanedKey: !ca.Present && t.CA.OrphanedKey(), LeftoverKeys: t.CA.LeftoverKeys()}, nil
}

// Leaves lists the graded leaves.
func (t *LocalTLS) Leaves() ([]Leaf, error) {
	r, err := t.Report()
	return r.Leaves, err
}

// Result summarizes one reconcile.
type Result struct {
	Issued   []string `json:"issued"`
	Renewed  []string `json:"renewed"`
	Removed  []string `json:"removed"`
	Reloaded bool     `json:"reloaded"`
}

// issue mints and places one leaf, recording it. No reload.
func (t *LocalTLS) issue(authority Authority, caFP string, host string, aliases []docker.Hostname, now time.Time) (Leaf, error) {
	names := []string{host}
	for _, a := range aliases {
		names = append(names, string(a))
	}
	chain, key, err := authority.IssueServerCert(names, t.lifetime(), now)
	if err != nil {
		return Leaf{}, err
	}
	name := docker.LocalCertName(docker.Hostname(host))
	if err := docker.PlaceCert(t.StackDir, docker.PlacedCert{Name: name, Names: names}, chain, key); err != nil {
		return Leaf{}, err
	}
	cert, err := parseCert(chain)
	if err != nil {
		return Leaf{}, err
	}
	leaf := Leaf{Host: host, CertName: name, Serial: cert.SerialNumber.Text(16),
		FingerprintSHA256: CertFingerprint(cert.Raw), CAFingerprint: caFP,
		NotBefore: cert.NotBefore, NotAfter: cert.NotAfter, IssuedAt: now}
	if err := t.save(leaf); err != nil {
		return Leaf{}, err
	}
	return leaf, nil
}

// EnsureLeaves issues (or, when due, mismatched or force, re-issues)
// leaves for the eligible routes among routes, placing them without
// refreshing snippets or reloading — the edge reconcile renders them in.
// The CA is created on first need. A failure for one host never blocks
// the others; the first error is returned after all were attempted.
func (t *LocalTLS) EnsureLeaves(routes []docker.Route, force bool, actor string) (issued, renewed []string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ensureLocked(routes, force, "", actor)
}

func (t *LocalTLS) ensureLocked(routes []docker.Route, force bool, only, actor string) (issued, renewed []string, err error) {
	var hosts []string
	byHost := map[string]docker.Route{}
	for _, r := range routes {
		h := string(r.Host)
		if !EligibleLocal(r) || !LocalNameAllowed(h) || (only != "" && only != h) || !aliasesAllowed(r) {
			continue
		}
		byHost[h] = r
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	if len(hosts) == 0 {
		return nil, nil, nil
	}
	if _, _, cerr := t.CA.Ensure(actor); cerr != nil {
		t.audit(actor, "tls.local-ca.ensure", "error", cerr.Error())
		return nil, nil, cerr
	}
	authority, aerr := t.CA.Authority()
	if aerr != nil {
		t.audit(actor, "tls.local-ca.ensure", "error", aerr.Error())
		return nil, nil, aerr
	}
	caFP := CertFingerprint(authority.Cert.Raw)
	ca, _ := t.CA.Info()
	recs, rerr := t.records()
	if rerr != nil {
		return nil, nil, rerr
	}
	now := t.now()
	var errs []error
	for _, host := range hosts {
		rec, had := recs[host]
		reason := ""
		switch {
		case force:
			reason = "re-issued on request"
		case !had:
			reason = "new local-only route"
		default:
			switch st := t.assess(rec, ca, true, byHost[host], now).Status; st {
			case LeafOK, LeafNotPresented:
			default:
				reason = st
			}
		}
		if reason == "" {
			continue
		}
		leaf, ierr := t.issue(authority, caFP, host, byHost[host].Aliases, now)
		if ierr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", host, ierr))
			t.audit(actor, "tls.local-leaf.issue", "error", host+": "+ierr.Error())
			continue
		}
		action := "tls.local-leaf.issue"
		if had {
			action = "tls.local-leaf.renew"
			renewed = append(renewed, host)
		} else {
			issued = append(issued, host)
		}
		t.audit(actor, action, "ok", fmt.Sprintf("%s: %s, serial %s, SHA-256 %s, valid until %s", host, reason, leaf.Serial,
			leaf.FingerprintSHA256, leaf.NotAfter.UTC().Format(time.RFC3339)))
	}
	return issued, renewed, errors.Join(errs...)
}

// Reconcile is the unattended and owner-driven maintenance run: ensure
// leaves, refresh the snippets that should present them, remove leaves
// of routes that are gone, and force-reload the edge when the presented
// material changed. Hosts whose snippet does not yet present their
// leaf (a refused earlier reload) are retried. only narrows to one host;
// force re-issues even valid leaves.
func (t *LocalTLS) Reconcile(ctx context.Context, actor, only string, force bool) (Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reconcileLocked(ctx, actor, only, force)
}

func (t *LocalTLS) reconcileLocked(ctx context.Context, actor, only string, force bool) (Result, error) {
	var res Result
	routes, err := t.Routes()
	if err != nil {
		return res, err
	}
	issued, renewed, issueErr := t.ensureLocked(routes, force, only, actor)
	res.Issued, res.Renewed = issued, renewed
	changed := map[string]bool{}
	for _, h := range append(append([]string{}, issued...), renewed...) {
		changed[h] = true
	}
	var refresh []string
	for _, r := range routes {
		h := string(r.Host)
		if !EligibleLocal(r) || !LocalNameAllowed(h) || (only != "" && only != h) {
			continue
		}
		rec, ok := mustRecord(t, h)
		if !ok {
			continue
		}
		if changed[h] || (docker.RouteSnippetExists(t.StackDir, r.Host) && !docker.RouteServesCert(t.StackDir, r.Host, rec.CertName)) {
			refresh = append(refresh, h)
		}
	}
	var undo func() error
	reload := false
	if len(refresh) > 0 {
		undo, err = docker.RefreshCertRoutes(t.StackDir, routes, refresh)
		if err != nil {
			return res, errors.Join(issueErr, err)
		}
		for _, h := range refresh {
			if docker.RouteSnippetExists(t.StackDir, docker.Hostname(h)) {
				reload = true
			}
		}
	}
	if only == "" {
		if removed, rmErr := t.removeOrphansLocked(routes, actor); rmErr != nil {
			issueErr = errors.Join(issueErr, rmErr)
		} else {
			res.Removed = removed
		}
	}
	if reload {
		fn := t.Reload
		if fn == nil {
			fn = docker.ReloadProxy
		}
		if rerr := fn(ctx); rerr != nil {
			if undo != nil {
				if rb := undo(); rb != nil {
					rerr = fmt.Errorf("%w (rollback also failed: %v)", rerr, rb)
				}
			}
			t.audit(actor, "tls.local-leaf.reload", "error", rerr.Error())
			return res, errors.Join(issueErr, fmt.Errorf("local leaves placed, but the edge reload was refused: %w", rerr))
		}
		res.Reloaded = true
	}
	return res, issueErr
}

func mustRecord(t *LocalTLS, host string) (Leaf, bool) {
	var l Leaf
	err := t.Store.DB().QueryRow(`SELECT host, cert_name FROM local_leaves WHERE host = ?`, host).Scan(&l.Host, &l.CertName)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return Leaf{}, false
	}
	return l, true
}

// removeOrphansLocked deletes the placements and records of leaves whose
// host is no longer a local-only internal-TLS route.
func (t *LocalTLS) removeOrphansLocked(routes []docker.Route, actor string) ([]string, error) {
	recs, err := t.records()
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, r := range routes {
		if EligibleLocal(r) {
			live[string(r.Host)] = true
		}
	}
	var removed []string
	now := t.now()
	for host, rec := range recs {
		if live[host] || now.Sub(rec.IssuedAt) < orphanGrace {
			continue
		}
		if err := docker.RemoveCert(t.StackDir, rec.CertName); err != nil {
			return removed, err
		}
		if _, err := t.Store.DB().Exec(`DELETE FROM local_leaves WHERE host = ?`, host); err != nil {
			return removed, err
		}
		removed = append(removed, host)
		t.audit(actor, "tls.local-leaf.remove", "ok", host+": route is gone or no longer local-only")
	}
	sort.Strings(removed)
	return removed, nil
}

// RotationPlan previews a root rotation: what changes and what the owner
// must do on devices. Pure; nothing is modified.
type RotationPlan struct {
	CurrentFingerprint string    `json:"current_fingerprint_sha256"`
	CurrentNotAfter    time.Time `json:"current_not_after"`
	Generation         int       `json:"generation"`
	Leaves             []string  `json:"leaves_to_reissue"`
	DevicesMustRetrust string    `json:"devices_must_retrust"`
	Steps              []string  `json:"steps"`
	Digest             string    `json:"digest"`
}

// PlanRotation builds the preview and its approval digest.
func (t *LocalTLS) PlanRotation() (RotationPlan, error) {
	ca, err := t.CA.Info()
	if err != nil {
		return RotationPlan{}, err
	}
	if !ca.Present {
		return RotationPlan{}, ErrNoCA
	}
	recs, err := t.records()
	if err != nil {
		return RotationPlan{}, err
	}
	hosts := make([]string, 0, len(recs))
	for h := range recs {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	sum := sha256.Sum256([]byte(fmt.Sprintf("rotate|%s|%d|%s", ca.FingerprintSHA256, ca.Generation, strings.Join(hosts, ","))))
	return RotationPlan{
		CurrentFingerprint: ca.FingerprintSHA256, CurrentNotAfter: ca.NotAfter, Generation: ca.Generation,
		Leaves:             hosts,
		DevicesMustRetrust: "Every device that trusts the current root (fingerprint above) will reject these hosts after rotation until it trusts the new root. Homestead does not track devices; trust the new root on each one, then remove the old.",
		Steps: []string{
			"A new root is created and its key custody-sealed; the old key is destroyed.",
			"Every local leaf above is re-issued under the new root and the edge reloads.",
			"Download the new root from the Trust page, verify its fingerprint, install it on each device, and remove the old root.",
		},
		Digest: fmt.Sprintf("%x", sum[:]),
	}, nil
}

// ErrStalePlan reports an approval digest that no longer matches.
var ErrStalePlan = errors.New("cert: the rotation plan changed since it was reviewed; review it again")

// Rotate performs an approved root rotation, then re-issues every leaf.
//
// The plan check and the CA swap happen under the LocalTLS lock, and the
// reviewed fingerprint is re-checked under the CA lock, so two approvals
// of the same digest rotate exactly once (the loser gets ErrStalePlan).
func (t *LocalTLS) Rotate(ctx context.Context, actor, digest string) (Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	plan, err := t.PlanRotation()
	if err != nil {
		return Result{}, err
	}
	if digest != plan.Digest {
		return Result{}, ErrStalePlan
	}
	if _, _, err := t.CA.Rotate(actor, plan.CurrentFingerprint); err != nil {
		if errors.Is(err, ErrRotationStale) {
			return Result{}, ErrStalePlan
		}
		return Result{}, err
	}
	return t.reconcileLocked(ctx, actor, "", true)
}

// ExpiringRoot grades the root at now: "", "warn" or "fail".
func ExpiringRoot(ca CAInfo, now time.Time) string {
	switch left := ca.NotAfter.Sub(now); {
	case left <= LocalRootFailWindow:
		return "fail"
	case left <= LocalRootWarnWindow:
		return "warn"
	}
	return ""
}
