package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/claim"
)

// Operator session boundary (ADR-020, S255).
//
// A single middleware wraps the TCP mux and classifies every request
// against routeClasses. In S255 it is REPORT-ONLY: requests it would
// reject are logged and audited (aggregated) but always served. S256 adds
// the cookie/CSRF transport (recognised here); S257 flips enforcement.

// RouteClass says what the boundary requires of a route.
type RouteClass int

const (
	// ClassPublic is reachable without a session in every claim state.
	ClassPublic RouteClass = iota + 1
	// ClassClaimWindow is reachable without a session only until claim
	// completes (claim-wizard saves, ADR-020 amending ADR-015).
	ClassClaimWindow
	// ClassSession requires a valid operator session and a claimed
	// appliance. Pending enforcement by S257.
	ClassSession
)

func (c RouteClass) String() string {
	switch c {
	case ClassPublic:
		return "public"
	case ClassClaimWindow:
		return "claim-window"
	case ClassSession:
		return "session"
	}
	return "unclassified"
}

// Would-be-rejection reasons.
const (
	reasonPreClaim       = "pre-claim"
	reasonNoSession      = "no-session"
	reasonInvalidSession = "invalid-session"
	reasonWizardClaimed  = "claim-window-closed"
	reasonUnclassified   = "unclassified-route"
	reasonClaimStateErr  = "claim-state-error"
	reasonCSRFMissing    = "csrf-missing"
	reasonCSRFMismatch   = "csrf-mismatch"
)

// auditActionWouldReject is the audit action of aggregated report-only
// entries.
const auditActionWouldReject = "session.boundary.would_reject"

// routeClasses is the code-declared classification of every TCP route.
// Deny-by-default: a registered route missing here is treated as
// ClassSession and reported as unclassified, and the classification test
// fails. Session routes are pending enforcement by S257.
var routeClasses = map[string]RouteClass{
	// Public allowlist (ADR-020).
	"GET /healthz":              ClassPublic,
	"GET /version":              ClassPublic,
	"GET /api/v1/identity":      ClassPublic,
	"POST /api/v1/claim":        ClassPublic,
	"POST /api/v1/admins/setup": ClassPublic,
	"POST /api/v1/sessions":     ClassPublic,
	"/":                         ClassPublic, // dashboard static catch-all

	// Claim window: claim-wizard saves, pre-login.
	"GET /api/v1/wizards/{id}":    ClassClaimWindow,
	"PUT /api/v1/wizards/{id}":    ClassClaimWindow,
	"DELETE /api/v1/wizards/{id}": ClassClaimWindow,

	// Session required (pending enforcement: S257).
	"DELETE /api/v1/sessions":                    ClassSession,
	"POST /api/v1/admins/password":               ClassSession,
	"GET /api/v1/admins/lockout":                 ClassSession,
	"GET /api/v1/audit":                          ClassSession,
	"GET /api/v1/backups/destination":            ClassSession,
	"GET /api/v1/backups/schedules":              ClassSession,
	"POST /api/v1/backups/schedules":             ClassSession,
	"GET /api/v1/backups/schedules/{package}":    ClassSession,
	"DELETE /api/v1/backups/schedules/{package}": ClassSession,
	"GET /api/v1/catalog":                        ClassSession,
	"GET /api/v1/catalog/{name}":                 ClassSession,
	"POST /api/v1/catalog/{name}/plan":           ClassSession,
	"POST /api/v1/catalog/{name}/install":        ClassSession,
	"POST /api/v1/catalog/{name}/configure":      ClassSession,
	"POST /api/v1/catalog/{name}/uninstall":      ClassSession,
	"GET /api/v1/catalog/{name}/drift":           ClassSession,
	"POST /api/v1/catalog/{name}/drift":          ClassSession,
	"GET /api/v1/config":                         ClassSession,
	"GET /api/v1/config/schema":                  ClassSession,
	"POST /api/v1/config/plan":                   ClassSession,
	"PUT /api/v1/config":                         ClassSession,
	"GET /api/v1/config/export":                  ClassSession,
	"POST /api/v1/config/import":                 ClassSession,
	"GET /api/v1/docker/health":                  ClassSession,
	"GET /api/v1/dockge":                         ClassSession,
	"GET /api/v1/doctor":                         ClassSession,
	"POST /api/v1/edge/deploy":                   ClassSession,
	"POST /api/v1/edge/routes":                   ClassSession,
	"GET /api/v1/exposure/review":                ClassSession,
	"GET /api/v1/exposure/approvals":             ClassSession,
	"POST /api/v1/exposure/approvals":            ClassSession,
	"POST /api/v1/exposure/apply":                ClassSession,
	"GET /api/v1/firewall/status":                ClassSession,
	"GET /api/v1/firewall/plan":                  ClassSession,
	"POST /api/v1/firewall/apply":                ClassSession,
	"GET /api/v1/operations":                     ClassSession,
	"GET /api/v1/operations/{id}":                ClassSession,
	"GET /api/v1/operations/{id}/events":         ClassSession,
	"GET /api/v1/storage/mounts":                 ClassSession,
	"GET /api/v1/storage/registrations":          ClassSession,
	"POST /api/v1/storage/registrations":         ClassSession,
	"GET /api/v1/storage/assignments":            ClassSession,
	"POST /api/v1/storage/assignments":           ClassSession,
	"GET /api/v1/tunnel/status":                  ClassSession,
	"POST /api/v1/tunnel/setup":                  ClassSession,
	"DELETE /api/v1/tunnel/tunnels/{id}":         ClassSession,
	"GET /api/v1/updates":                        ClassSession,
	"POST /api/v1/updates/{name}/apply":          ClassSession,
}

// Classify returns the class declared for a registered pattern.
func Classify(pattern string) (RouteClass, bool) {
	c, ok := routeClasses[pattern]
	return c, ok
}

// sessionVerifier and claimReader are the slices of auth.Service and
// claim.Service the boundary needs; tests inject fakes.
type sessionVerifier interface {
	Verify(token string) (auth.VerifiedSession, error)
}

type claimReader interface {
	CurrentState() (claim.State, error)
}

type auditRecorder interface {
	Record(audit.Entry) (audit.Entry, error)
}

// reportWindow is the aggregation period: one audit entry per route and
// reason per window, carrying the count.
const reportWindow = time.Minute

type reportKey struct{ pattern, reason string }

type reportBucket struct {
	count int
	first time.Time
}

// boundary is the report-only session middleware state.
type boundary struct {
	sessions sessionVerifier
	claims   claimReader
	audit    auditRecorder
	log      interface {
		Warn(msg string, args ...any)
	}
	now func() time.Time

	mu              sync.Mutex
	lastStateErrLog time.Time
	pending         map[reportKey]*reportBucket
}

// MountSessionBoundary wraps the TCP mux in the report-only session
// middleware (ADR-020 rollout step 1). The Unix-socket mux is untouched.
func (s *Server) MountSessionBoundary(sessions *auth.Service, claims *claim.Service, auditSvc *audit.Service) {
	s.mountBoundary(sessions, claims, auditSvc, time.Now)
}

func (s *Server) mountBoundary(sessions sessionVerifier, claims claimReader, rec auditRecorder, now func() time.Time) {
	b := &boundary{
		sessions: sessions, claims: claims, audit: rec, log: s.log, now: now,
		pending: map[reportKey]*reportBucket{},
	}
	s.boundary = b
	s.http.Handler = b.wrap(s.mux)
}

func (b *boundary) wrap(mux *recordingMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.check(mux, r)
		mux.ServeHTTP(w, r) // report-only: always served (S257 enforces)
	})
}

// check classifies r and reports what enforcement would have done. It
// never reads credentials into logs or audit and never fails the request.
func (b *boundary) check(mux *recordingMux, r *http.Request) {
	_, pattern := mux.Handler(r)
	if pattern == "" {
		return // no route: the mux answers 404/405 itself
	}
	class, known := Classify(pattern)
	if !known {
		b.report(pattern, reasonUnclassified)
		return
	}
	if pattern == dashboardPattern && !isDashboardRequest(r) {
		// The method-less catch-all also receives unregistered methods
		// and unknown API paths. Only static dashboard reads are public;
		// everything else needs a session like any other API route.
		class = ClassSession
	}
	if class == ClassPublic {
		return
	}
	state, err := b.claims.CurrentState()
	if err != nil {
		// Fail closed, but say so: this is not a closed claim window.
		b.logClaimStateError(err)
		b.report(pattern, reasonClaimStateErr)
		return
	}
	if state != claim.StateClaimed {
		if class != ClassClaimWindow {
			b.report(pattern, reasonPreClaim)
		}
		return
	}
	token, viaCookie, ok := sessionCredential(r)
	if !ok {
		if class == ClassClaimWindow {
			b.report(pattern, reasonWizardClaimed)
		} else {
			b.report(pattern, reasonNoSession)
		}
		return
	}
	session, err := b.sessions.Verify(token)
	if err != nil {
		b.report(pattern, reasonInvalidSession)
		return
	}
	// Cookies are ambient, so a mutating cookie-authenticated request
	// must echo the per-session CSRF token. Bearer requests are exempt.
	if viaCookie && isMutating(r.Method) {
		presented := r.Header.Get(CSRFHeader)
		switch {
		case presented == "":
			b.report(pattern, reasonCSRFMissing)
		case !session.CSRFMatches(presented):
			b.report(pattern, reasonCSRFMismatch)
		}
	}
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// dashboardPattern is the method-less static dashboard catch-all.
const dashboardPattern = "/"

// isDashboardRequest reports whether r is a plain static dashboard read.
func isDashboardRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return r.URL.Path != "/api" && !strings.HasPrefix(r.URL.Path, "/api/")
}

// logClaimStateError logs a claim-state read failure at most once per
// reportWindow so a down database cannot flood the journal.
func (b *boundary) logClaimStateError(err error) {
	now := b.now()
	b.mu.Lock()
	due := b.lastStateErrLog.IsZero() || now.Sub(b.lastStateErrLog) >= reportWindow
	if due {
		b.lastStateErrLog = now
	}
	b.mu.Unlock()
	if due {
		b.log.Warn("session boundary: claim state unreadable; failing closed", "error", err)
	}
}

// report counts a would-be rejection. It only touches memory: the
// ticker in run flushes counts as one audit entry per route and reason
// per window, so a polling dashboard cannot flood audit and requests
// never write audit.
func (b *boundary) report(pattern, reason string) {
	now := b.now()
	b.mu.Lock()
	key := reportKey{pattern, reason}
	bucket := b.pending[key]
	if bucket == nil {
		bucket = &reportBucket{first: now}
		b.pending[key] = bucket
	}
	bucket.count++
	b.mu.Unlock()
}

type flushItem struct {
	key    reportKey
	bucket reportBucket
}

func (b *boundary) drainLocked() []flushItem {
	items := make([]flushItem, 0, len(b.pending))
	for k, v := range b.pending {
		items = append(items, flushItem{k, *v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].key.pattern != items[j].key.pattern {
			return items[i].key.pattern < items[j].key.pattern
		}
		return items[i].key.reason < items[j].key.reason
	})
	b.pending = map[reportKey]*reportBucket{}
	return items
}

// flush emits everything pending now (window end, shutdown, tests).
func (b *boundary) flush() {
	b.mu.Lock()
	due := b.drainLocked()
	b.mu.Unlock()
	b.emit(due)
}

func (b *boundary) emit(items []flushItem) {
	for _, it := range items {
		detail := fmt.Sprintf("report-only: would reject %s (%s) x%d since %s",
			it.key.pattern, it.key.reason, it.bucket.count, it.bucket.first.UTC().Format(time.RFC3339))
		b.log.Warn("session boundary would reject", "route", it.key.pattern,
			"reason", it.key.reason, "count", it.bucket.count)
		if _, err := b.audit.Record(audit.Entry{
			Actor: "anonymous", Action: auditActionWouldReject,
			Outcome: audit.OutcomeError, Detail: detail,
		}); err != nil {
			b.log.Warn("audit record failed", "action", auditActionWouldReject, "error", err)
		}
	}
}

// run flushes on every tick so a quiet tail is not held back, and once
// more on shutdown before returning (callers wait for it).
func (b *boundary) run(ctx context.Context, tick <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			b.flush()
			return
		case <-tick:
			b.flush()
		}
	}
}
