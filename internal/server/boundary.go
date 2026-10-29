package server

import (
	"context"
	"errors"
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
// against routeClasses. Since S257 it ENFORCES: a request that fails the
// boundary is answered here and never reaches a handler. Rejections are
// counted in memory and audited aggregated (one entry per route and
// reason per window), never with credentials.
//
// Status codes: 401 session missing or invalid (also a wizard route once
// claim has completed); 403 pre-claim access to a non-public route, an
// unclassified route, or a CSRF failure; 503 claim state unreadable
// (fail closed).

// RouteClass says what the boundary requires of a route.
type RouteClass int

const (
	// ClassPublic is reachable without a session in every claim state.
	ClassPublic RouteClass = iota + 1
	// ClassClaimWindow is reachable without a session only until claim
	// completes (claim-wizard saves, ADR-020 amending ADR-015).
	ClassClaimWindow
	// ClassSession requires a valid operator session and a claimed
	// appliance.
	ClassSession
	// ClassCraftSession is a route on the craft-file unix socket (ADR-032).
	// The craft listener enforces it: a craft-scoped cookie bound to the
	// operator session (or, on the exchange route only, a one-time launch
	// code). Such a route is never registered on the TCP mux.
	ClassCraftSession
)

func (c RouteClass) String() string {
	switch c {
	case ClassPublic:
		return "public"
	case ClassClaimWindow:
		return "claim-window"
	case ClassSession:
		return "session"
	case ClassCraftSession:
		return "craft-session"
	}
	return "unclassified"
}

// Rejection reasons (the audit/log vocabulary).
const (
	reasonPreClaim       = "pre-claim"
	reasonNoSession      = "no-session"
	reasonInvalidSession = "invalid-session"
	reasonWizardClaimed  = "claim-window-closed"
	reasonUnclassified   = "unclassified-route"
	reasonClaimStateErr  = "claim-state-error"
	reasonCSRFMissing    = "csrf-missing"
	reasonCSRFMismatch   = "csrf-mismatch"
	reasonVerifyError    = "session-verify-error"
	reasonReauth         = "reauth-required"
	reasonStepUp         = "stepup-required"
	reasonLocalOnly      = "local-only"
)

// auditActionRejected is the audit action of aggregated rejection entries.
const auditActionRejected = "session.boundary.rejected"

// routeClasses is the code-declared classification of every TCP route.
// Deny-by-default: a registered route missing here is treated as
// ClassSession, rejected as unclassified, and the classification test
// fails.
var routeClasses = map[string]RouteClass{
	// Public allowlist (ADR-020).
	"GET /healthz":              ClassPublic,
	"GET /version":              ClassPublic,
	"GET /api/v1/identity":      ClassPublic,
	"POST /api/v1/claim":        ClassPublic,
	"POST /api/v1/admins/setup": ClassPublic,
	"POST /api/v1/sessions":     ClassPublic,
	"/":                         ClassPublic, // dashboard static catch-all
	// ADR-030: the one read-only unauthenticated exception beyond the
	// above. A device fetching the root cannot have an operator session.
	// Both serve ONLY the public root certificate; the fingerprint the
	// authenticated dashboard and CLI show is the owner's check.
	"GET /api/v1/tls/local-ca/root.pem": ClassPublic,
	"GET /api/v1/tls/local-ca/root.der": ClassPublic,
	// ADR-033: the three browser-protocol routes of sign-in with Authentik.
	// Each answers 404 before claim and unless SSO is configured and linked.
	// Every other SSO route below is session class.
	"GET /api/v1/sso/options":  ClassPublic,
	"GET /api/v1/sso/login":    ClassPublic,
	"GET /api/v1/sso/callback": ClassPublic,

	// Claim window: claim-wizard saves, pre-login.
	"GET /api/v1/wizards/{id}":    ClassClaimWindow,
	"PUT /api/v1/wizards/{id}":    ClassClaimWindow,
	"DELETE /api/v1/wizards/{id}": ClassClaimWindow,

	// Session required.
	// Craft file proxy, on the dedicated craft socket only (ADR-032).
	"GET /_homestead/open.html":              ClassCraftSession,
	"GET /_homestead/open.js":                ClassCraftSession,
	"GET /_homestead/open.css":               ClassCraftSession,
	"POST /_homestead/files/session":         ClassCraftSession,
	"GET /_homestead/files/session":          ClassCraftSession,
	"DELETE /_homestead/files/session":       ClassCraftSession,
	"PROPFIND /_homestead/files/f/{path...}": ClassCraftSession,
	"GET /_homestead/files/f/{path...}":      ClassCraftSession,
	"PUT /_homestead/files/f/{path...}":      ClassCraftSession,
	"MKCOL /_homestead/files/f/{path...}":    ClassCraftSession,
	"DELETE /_homestead/files/f/{path...}":   ClassCraftSession,
	"MOVE /_homestead/files/f/{path...}":     ClassCraftSession,
	"COPY /_homestead/files/f/{path...}":     ClassCraftSession,
	"HEAD /_homestead/files/f/{path...}":     ClassCraftSession,

	"DELETE /api/v1/sessions":                             ClassSession,
	"GET /api/v1/craft/apps":                              ClassSession,
	"GET /api/v1/craft/files":                             ClassSession,
	"POST /api/v1/craft/files/plan":                       ClassSession,
	"POST /api/v1/craft/files/approve":                    ClassSession,
	"POST /api/v1/craft/files/local-only":                 ClassSession,
	"POST /api/v1/craft/files/launch":                     ClassSession,
	"POST /api/v1/admins/password":                        ClassSession,
	"GET /api/v1/admins/lockout":                          ClassSession,
	"GET /api/v1/audit":                                   ClassSession,
	"GET /api/v1/scheduler/jobs":                          ClassSession,
	"GET /api/v1/drift/schedule":                          ClassSession,
	"POST /api/v1/drift/schedule":                         ClassSession,
	"GET /api/v1/tls/renewal":                             ClassSession,
	"GET /api/v1/tls/local-ca":                            ClassSession,
	"POST /api/v1/tls/local-ca":                           ClassSession,
	"GET /api/v1/tls/local-ca/leaves":                     ClassSession,
	"POST /api/v1/tls/local-ca/leaves/reissue":            ClassSession,
	"GET /api/v1/tls/local-ca/rotation":                   ClassSession,
	"POST /api/v1/tls/local-ca/rotation":                  ClassSession,
	"GET /api/v1/tls/issuance/plans":                      ClassSession,
	"GET /api/v1/tls/issuance/approvals":                  ClassSession,
	"POST /api/v1/tls/issuance/approvals":                 ClassSession,
	"POST /api/v1/tls/issuance/apply":                     ClassSession,
	"POST /api/v1/tls/issuance/account/reregister":        ClassSession,
	"GET /api/v1/tls/issuance/account/terms":              ClassSession,
	"POST /api/v1/tls/issuance/credentials":               ClassSession,
	"GET /api/v1/backups/destination":                     ClassSession,
	"GET /api/v1/backups/schedules":                       ClassSession,
	"POST /api/v1/backups/schedules":                      ClassSession,
	"GET /api/v1/backups/schedules/{package}":             ClassSession,
	"DELETE /api/v1/backups/schedules/{package}":          ClassSession,
	"GET /api/v1/backups/policy":                          ClassSession,
	"POST /api/v1/backups/policy/plan":                    ClassSession,
	"POST /api/v1/backups/policy/apply":                   ClassSession,
	"GET /api/v1/backups/recipients":                      ClassSession,
	"POST /api/v1/backups/recipients":                     ClassSession,
	"DELETE /api/v1/backups/recipients/{fingerprint}":     ClassSession,
	"GET /api/v1/backups/identity":                        ClassSession,
	"POST /api/v1/backups/identity/export":                ClassSession,
	"POST /api/v1/backups/identity/ack":                   ClassSession,
	"GET /api/v1/catalog":                                 ClassSession,
	"GET /api/v1/catalog/{name}":                          ClassSession,
	"POST /api/v1/catalog/{name}/plan":                    ClassSession,
	"POST /api/v1/catalog/{name}/install":                 ClassSession,
	"POST /api/v1/catalog/{name}/configure":               ClassSession,
	"GET /api/v1/catalog/{name}/secrets":                  ClassSession,
	"POST /api/v1/catalog/{name}/secrets/{secret}":        ClassSession,
	"POST /api/v1/catalog/{name}/secrets/{secret}/reveal": ClassSession,
	"POST /api/v1/catalog/{name}/uninstall":               ClassSession,
	"GET /api/v1/catalog/{name}/sign-in":                  ClassSession,
	"POST /api/v1/catalog/{name}/sign-in/plan":            ClassSession,
	"POST /api/v1/catalog/{name}/sign-in/apply":           ClassSession,
	"POST /api/v1/catalog/{name}/sign-in/access":          ClassSession,
	// S290 (ADR-041 §1): public app names on the tunnel.
	"GET /api/v1/catalog/{name}/public":        ClassSession,
	"GET /api/v1/catalog/{name}/reach":         ClassSession,
	"POST /api/v1/catalog/{name}/public/plan":  ClassSession,
	"POST /api/v1/catalog/{name}/public/apply": ClassSession,
	// S324 (ADR-048 §3): the setup approved with an install.
	"GET /api/v1/catalog/{name}/setup":         ClassSession,
	"POST /api/v1/catalog/{name}/setup/resume": ClassSession,
	"POST /api/v1/catalog/{name}/setup/skip":   ClassSession,
	// S315 (ADR-044 §5): moving an installed app to its path.
	"GET /api/v1/catalog/{name}/move":                    ClassSession,
	"POST /api/v1/catalog/{name}/move/plan":              ClassSession,
	"POST /api/v1/catalog/{name}/move/apply":             ClassSession,
	"GET /api/v1/catalog/{name}/drift":                   ClassSession,
	"GET /api/v1/catalog/{name}/drift/live":              ClassSession,
	"POST /api/v1/catalog/{name}/drift":                  ClassSession,
	"GET /api/v1/config":                                 ClassSession,
	"GET /api/v1/config/schema":                          ClassSession,
	"POST /api/v1/config/plan":                           ClassSession,
	"PUT /api/v1/config":                                 ClassSession,
	"GET /api/v1/config/export":                          ClassSession,
	"POST /api/v1/config/import":                         ClassSession,
	"GET /api/v1/docker/health":                          ClassSession,
	"GET /api/v1/dockge":                                 ClassSession,
	"GET /api/v1/doctor":                                 ClassSession,
	"POST /api/v1/edge/deploy":                           ClassSession,
	"GET /api/v1/edge/routes":                            ClassSession,
	"POST /api/v1/edge/routes":                           ClassSession,
	"GET /api/v1/exposure/review":                        ClassSession,
	"GET /api/v1/exposure/approvals":                     ClassSession,
	"POST /api/v1/exposure/approvals":                    ClassSession,
	"POST /api/v1/exposure/apply":                        ClassSession,
	"GET /api/v1/firewall/status":                        ClassSession,
	"GET /api/v1/firewall/plan":                          ClassSession,
	"POST /api/v1/firewall/apply":                        ClassSession,
	"GET /api/v1/operations":                             ClassSession,
	"GET /api/v1/operations/{id}":                        ClassSession,
	"GET /api/v1/operations/{id}/events":                 ClassSession,
	"GET /api/v1/secrets/key":                            ClassSession,
	"POST /api/v1/secrets/rekey":                         ClassSession,
	"GET /api/v1/storage/mounts":                         ClassSession,
	"GET /api/v1/storage/registrations":                  ClassSession,
	"POST /api/v1/storage/registrations":                 ClassSession,
	"GET /api/v1/storage/assignments":                    ClassSession,
	"POST /api/v1/storage/assignments":                   ClassSession,
	"DELETE /api/v1/storage/assignments/{class}":         ClassSession,
	"DELETE /api/v1/storage/registrations":               ClassSession,
	"GET /api/v1/names":                                  ClassSession,
	"POST /api/v1/names/resolver/plan":                   ClassSession,
	"POST /api/v1/names/resolver/apply":                  ClassSession,
	"GET /api/v1/tunnel/status":                          ClassSession,
	"POST /api/v1/tunnel/plan":                           ClassSession,
	"POST /api/v1/tunnel/setup":                          ClassSession,
	"DELETE /api/v1/tunnel/tunnels/{id}":                 ClassSession,
	"GET /api/v1/updates":                                ClassSession,
	"GET /api/v1/platform/updates":                       ClassSession,
	"POST /api/v1/platform/updates/verify":               ClassSession,
	"POST /api/v1/platform/updates/apply":                ClassSession,
	"POST /api/v1/platform/updates/clear-apply":          ClassSession,
	"POST /api/v1/platform/updates/rollback":             ClassSession,
	"GET /api/v1/craft":                                  ClassSession,
	"POST /api/v1/craft/plan":                            ClassSession,
	"POST /api/v1/craft/build":                           ClassSession,
	"GET /api/v1/craft/trust-keys":                       ClassSession,
	"POST /api/v1/craft/trust-keys":                      ClassSession,
	"PUT /api/v1/craft/trust-keys/{id}":                  ClassSession,
	"DELETE /api/v1/craft/trust-keys/{id}":               ClassSession,
	"GET /api/v1/platform/trust-keys":                    ClassSession,
	"POST /api/v1/platform/trust-keys":                   ClassSession,
	"PUT /api/v1/platform/trust-keys/{id}":               ClassSession,
	"DELETE /api/v1/platform/trust-keys/{id}":            ClassSession,
	"POST /api/v1/updates/{name}/apply":                  ClassSession,
	"GET /api/v1/data-migrations":                        ClassSession,
	"POST /api/v1/data-migrations/{name}/plan":           ClassSession,
	"POST /api/v1/data-migrations/{name}/approve":        ClassSession,
	"POST /api/v1/data-migrations/{name}/remove-volumes": ClassSession,
	"GET /api/v1/setup":                                  ClassSession,
	"PUT /api/v1/setup/steps/{name}":                     ClassSession,
	"GET /api/v1/sso":                                    ClassSession,
	"POST /api/v1/sso/plan":                              ClassSession,
	"POST /api/v1/sso/access":                            ClassSession,
	"POST /api/v1/sso/approve":                           ClassSession,
	"POST /api/v1/sso/apply":                             ClassSession,
	"POST /api/v1/sso/link":                              ClassSession,
	"POST /api/v1/sso/step-up":                           ClassSession,
	"DELETE /api/v1/sso/link":                            ClassSession,
	"DELETE /api/v1/sso":                                 ClassSession,
	"GET /api/v1/dashboard/address":                      ClassSession,
	"PUT /api/v1/dashboard/address":                      ClassSession,
	// The apps list (S337, ADR-050 §4); S338 adds the person allowlist.
	"GET /api/v1/me/apps":          ClassSession,
	"GET /api/v1/people/{id}/apps": ClassSession,
	// People (S317, ADR-043).
	"GET /api/v1/people":                         ClassSession,
	"POST /api/v1/people":                        ClassSession,
	"POST /api/v1/people/adopt":                  ClassSession,
	"PATCH /api/v1/people/{id}":                  ClassSession,
	"POST /api/v1/people/{id}/password-reset":    ClassSession,
	"POST /api/v1/people/{id}/mfa-reset":         ClassSession,
	"POST /api/v1/people/{id}/disable":           ClassSession,
	"POST /api/v1/people/{id}/enable":            ClassSession,
	"POST /api/v1/people/{id}/remove":            ClassSession,
	"PUT /api/v1/people/{id}/apps/{app}":         ClassSession,
	"PUT /api/v1/people/{id}/apps/{app}/admin":   ClassSession,
	"POST /api/v1/people/{id}/apps/{app}/revoke": ClassSession,
	"POST /api/v1/people/{id}/resolve":           ClassSession,
}

// stepUpMode says who checks the fresh local password for a step-up route.
type stepUpMode int

const (
	// stepUpBoundary: the boundary requires the password from an oidc
	// session before the handler runs. Password sessions pass as before.
	stepUpBoundary stepUpMode = iota + 1
	// stepUpHandler: the handler requires a fresh password from every
	// session (link and unlink do, ADR-033), so the boundary leaves it.
	stepUpHandler
)

// stepUpRoutes is the code-declared step-up list (ADR-033): the most
// damaging actions, where a session obtained through Authentik must present
// the local password again. The password travels in the ReauthHeader of the
// request. The list is tested against the registered routes.
var stepUpRoutes = map[string]stepUpMode{
	"POST /api/v1/sso/link":                               stepUpHandler,
	"DELETE /api/v1/sso/link":                             stepUpHandler,
	"POST /api/v1/sso/plan":                               stepUpBoundary,
	"POST /api/v1/sso/approve":                            stepUpBoundary,
	"POST /api/v1/sso/apply":                              stepUpBoundary,
	"DELETE /api/v1/sso":                                  stepUpBoundary,
	"POST /api/v1/secrets/rekey":                          stepUpBoundary,
	"POST /api/v1/backups/identity/export":                stepUpBoundary,
	"POST /api/v1/catalog/{name}/secrets/{secret}/reveal": stepUpBoundary,
	// People (S317): admin grants, password and second-factor resets,
	// removal, and settling drift (which can restore or accept an admin).
	"PUT /api/v1/people/{id}/apps/{app}/admin": stepUpBoundary,
	"POST /api/v1/people/{id}/password-reset":  stepUpBoundary,
	"POST /api/v1/people/{id}/mfa-reset":       stepUpBoundary,
	"POST /api/v1/people/{id}/remove":          stepUpBoundary,
	"POST /api/v1/people/{id}/resolve":         stepUpBoundary,
}

// publicStepUpRoutes are the step-up routes a public dashboard name accepts
// on a fresh Authentik step-up instead of the local password (ADR-049 §1).
var publicStepUpRoutes = map[string]bool{
	"PUT /api/v1/people/{id}/apps/{app}/admin": true,
	"POST /api/v1/people/{id}/password-reset":  true,
	"POST /api/v1/people/{id}/mfa-reset":       true,
	"POST /api/v1/people/{id}/remove":          true,
	"POST /api/v1/people/{id}/resolve":         true,
}

// ReauthHeader carries the local password for fresh re-authentication.
const ReauthHeader = "X-Homestead-Reauth"

// Re-authentication error codes (X-Homestead-Error). They ride on 403 and
// 429, never 401, so the dashboard does not treat them as a dead session.
const (
	ErrorCodeReauthRequired = "reauth-required"
	ErrorCodeReauthInvalid  = "reauth-invalid"
	ErrorCodeReauthLocked   = "reauth-locked"
	// ErrorCodePublicHost refuses a local-only action on a public
	// dashboard name (ADR-037).
	ErrorCodePublicHost = "public-host"
	// ErrorCodeStepUpRequired: a People action on the public dashboard
	// name needs a fresh Authentik sign-in (ADR-049 §1).
	ErrorCodeStepUpRequired = "stepup-required"
	// ErrorCodeLocalOnly: a step-up route that is not a People route, on a
	// public dashboard name; it only works from the local or overlay
	// address (S335).
	ErrorCodeLocalOnly = "local-only"
)

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

// passwordVerifier is fresh re-authentication; auth.Service provides it.
type passwordVerifier interface {
	VerifyPassword(username, password string) error
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

type reportKey struct {
	pattern, reason string
	// edge: the request came through the dashboard socket (ADR-035).
	edge bool
}

// maxReportClients bounds the addresses listed per audit entry.
const maxReportClients = 5

type reportBucket struct {
	count   int
	first   time.Time
	clients []string
}

// boundary is the enforcing session middleware state.
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

// MountSessionBoundary wraps the TCP mux in the enforcing session
// middleware (ADR-020). The Unix-socket mux is untouched.
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

// operatorSession is what the boundary proved about a request; handlers
// read it from the request context instead of re-checking credentials.
type operatorSession struct {
	Token   string
	Session auth.VerifiedSession
	// stillValid re-verifies the session now; long-lived streams call
	// it so a revoked or expired session stops being served.
	stillValid func() bool
}

type operatorKey struct{}

// operatorFromContext returns the session the boundary verified, if any.
func operatorFromContext(ctx context.Context) (operatorSession, bool) {
	op, ok := ctx.Value(operatorKey{}).(operatorSession)
	return op, ok
}

// sessionStillValid is true only when the request carries a boundary
// session that still verifies; it fails closed without one.
func sessionStillValid(r *http.Request) bool {
	op, ok := operatorFromContext(r.Context())
	return ok && op.stillValid()
}

func (b *boundary) wrap(mux *recordingMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op, v := b.check(mux, r)
		if v.status != 0 {
			b.reportFrom(r, v.pattern, v.reason)
			if v.status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", "Bearer")
			}
			if v.code != "" {
				w.Header().Set(ErrorCodeHeader, v.code)
			}
			http.Error(w, v.message, v.status)
			return
		}
		if op != nil {
			r = r.WithContext(context.WithValue(r.Context(), operatorKey{}, *op))
		}
		mux.ServeHTTP(w, r)
	})
}

// verdict is a rejection: HTTP status, client-facing message, and the
// audit pattern and reason. The zero value means "serve".
type verdict struct {
	status          int
	message         string
	pattern, reason string
	// code, when set, is sent as X-Homestead-Error.
	code string
}

func reject(status int, message, pattern, reason string) verdict {
	return verdict{status: status, message: message, pattern: pattern, reason: reason}
}

// check classifies r. It returns either a rejection or, for session
// routes, the verified operator session. It never reads credentials into
// logs or audit.
func (b *boundary) check(mux *recordingMux, r *http.Request) (*operatorSession, verdict) {
	_, pattern := mux.Handler(r)
	if pattern == "" {
		return nil, verdict{} // no route: the mux answers 404/405 itself
	}
	class, known := Classify(pattern)
	if !known {
		return nil, reject(http.StatusForbidden, "route not classified", pattern, reasonUnclassified)
	}
	if class == ClassCraftSession {
		// Craft-socket routes are never served by the TCP listener.
		return nil, reject(http.StatusForbidden, "route not served here", pattern, reasonUnclassified)
	}
	if pattern == dashboardPattern && !isDashboardRequest(r) {
		// The method-less catch-all also receives unregistered methods
		// and unknown API paths. Only static dashboard reads are public;
		// everything else needs a session like any other API route.
		class = ClassSession
	}
	if class == ClassPublic {
		return nil, verdict{}
	}
	state, err := b.claims.CurrentState()
	if err != nil {
		// Fail closed, but say so: this is not a closed claim window.
		b.logClaimStateError(err)
		return nil, reject(http.StatusServiceUnavailable, "claim state unavailable", pattern, reasonClaimStateErr)
	}
	if state != claim.StateClaimed {
		if class == ClassClaimWindow {
			return nil, verdict{}
		}
		return nil, reject(http.StatusForbidden, "appliance is not claimed", pattern, reasonPreClaim)
	}
	token, viaCookie, ok := sessionCredential(r)
	if !ok {
		reason := reasonNoSession
		if class == ClassClaimWindow {
			reason = reasonWizardClaimed
		}
		return nil, reject(http.StatusUnauthorized, "session required", pattern, reason)
	}
	session, err := b.sessions.Verify(token)
	if err != nil {
		if !errors.Is(err, auth.ErrInvalidSession) {
			// A store failure is not the caller's fault; still closed.
			b.log.Warn("session boundary: session verification failed", "error", err)
			return nil, reject(http.StatusServiceUnavailable, "session check unavailable", pattern, reasonVerifyError)
		}
		return nil, reject(http.StatusUnauthorized, "invalid or expired session", pattern, reasonInvalidSession)
	}
	// Cookies are ambient, so a mutating cookie-authenticated request
	// must echo the per-session CSRF token. Bearer requests are exempt.
	if viaCookie && isMutating(r.Method) {
		presented := r.Header.Get(CSRFHeader)
		switch {
		case presented == "":
			return nil, reject(http.StatusForbidden, "CSRF token required", pattern, reasonCSRFMissing)
		case !session.CSRFMatches(presented):
			return nil, reject(http.StatusForbidden, "CSRF token mismatch", pattern, reasonCSRFMismatch)
		}
	}
	if session.Method == auth.MethodOIDC && stepUpRoutes[pattern] == stepUpBoundary {
		if onPublicHost(r) && publicStepUpRoutes[pattern] {
			if v := b.publicStepUp(r, session, pattern); v.status != 0 {
				return nil, v
			}
		} else if onPublicHost(r) {
			// The other step-up routes need the local password, which a
			// public name never takes: say where to go instead (S335).
			v := reject(http.StatusForbidden, "this action only works from your home network or overlay address", pattern, reasonLocalOnly)
			v.code = ErrorCodeLocalOnly
			return nil, v
		} else if v := b.stepUp(r, session.Username, pattern); v.status != 0 {
			return nil, v
		}
	}
	return &operatorSession{
		Token: token, Session: session,
		stillValid: func() bool { _, err := b.sessions.Verify(token); return err == nil },
	}, verdict{}
}

// publicStepUp lets a People step-up route through on a public dashboard
// name when the oidc session was stepped up by a fresh Authentik sign-in
// that has not expired. The password is never used here. Each authorised
// action leaves an audit entry saying so.
func (b *boundary) publicStepUp(r *http.Request, session auth.VerifiedSession, pattern string) verdict {
	if !session.SteppedUpUntil.After(b.now()) {
		v := reject(http.StatusForbidden, "sign in with Authentik again to continue", pattern, reasonStepUp)
		v.code = ErrorCodeStepUpRequired
		return v
	}
	_, _ = b.audit.Record(audit.Entry{Actor: session.Username, Action: "session.step-up.authorised", Outcome: audit.OutcomeOK,
		Detail: "authorised by Authentik step-up until " + session.SteppedUpUntil.UTC().Format(time.RFC3339) + " for " + r.Method + " " + r.URL.Path})
	return verdict{}
}

// stepUp checks the fresh local password an oidc session must present for
// the routes in stepUpRoutes. A missing or wrong password is refused with
// a stable code; wrong guesses feed the same lockout as login.
func (b *boundary) stepUp(r *http.Request, username, pattern string) verdict {
	password := r.Header.Get(ReauthHeader)
	if password == "" {
		v := reject(http.StatusForbidden, "enter your local password to continue", pattern, reasonReauth)
		v.code = ErrorCodeReauthRequired
		return v
	}
	pv, ok := b.sessions.(passwordVerifier)
	if !ok {
		return reject(http.StatusForbidden, "re-authentication unavailable", pattern, reasonReauth)
	}
	switch err := pv.VerifyPassword(username, password); {
	case err == nil:
		return verdict{}
	case errors.Is(err, auth.ErrAccountLocked):
		v := reject(http.StatusTooManyRequests, "account temporarily locked after repeated failures", pattern, reasonReauth)
		v.code = ErrorCodeReauthLocked
		return v
	case errors.Is(err, auth.ErrInvalidCredentials):
		v := reject(http.StatusForbidden, "that password is not right", pattern, reasonReauth)
		v.code = ErrorCodeReauthInvalid
		return v
	default:
		b.log.Warn("session boundary: re-authentication failed", "error", err)
		return reject(http.StatusServiceUnavailable, "re-authentication unavailable", pattern, reasonVerifyError)
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

// report counts a rejection. It only touches memory: the
// ticker in run flushes counts as one audit entry per route and reason
// per window, so a polling dashboard cannot flood audit and requests
// never write audit.
func (b *boundary) report(pattern, reason string) {
	b.reportFrom(nil, pattern, reason)
}

// reportFrom is report for a request: edge requests are counted apart
// and record the forwarded client addresses (bounded) in the audit entry.
func (b *boundary) reportFrom(r *http.Request, pattern, reason string) {
	now := b.now()
	client, edge := "", false
	if r != nil {
		client, edge = edgeClientFrom(r.Context())
	}
	b.mu.Lock()
	key := reportKey{pattern: pattern, reason: reason, edge: edge}
	bucket := b.pending[key]
	if bucket == nil {
		bucket = &reportBucket{first: now}
		b.pending[key] = bucket
	}
	bucket.count++
	if edge && len(bucket.clients) < maxReportClients {
		known := false
		for _, c := range bucket.clients {
			known = known || c == client
		}
		if !known {
			bucket.clients = append(bucket.clients, client)
		}
	}
	b.mu.Unlock()
}

type flushItem struct {
	key    reportKey
	bucket reportBucket
}

func (b *boundary) drainLocked() []flushItem {
	items := make([]flushItem, 0, len(b.pending))
	for k, v := range b.pending {
		cp := *v
		cp.clients = append([]string(nil), v.clients...)
		items = append(items, flushItem{k, cp})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].key.pattern != items[j].key.pattern {
			return items[i].key.pattern < items[j].key.pattern
		}
		if items[i].key.reason != items[j].key.reason {
			return items[i].key.reason < items[j].key.reason
		}
		return !items[i].key.edge && items[j].key.edge
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
		detail := fmt.Sprintf("rejected %s (%s) x%d since %s",
			it.key.pattern, it.key.reason, it.bucket.count, it.bucket.first.UTC().Format(time.RFC3339))
		if it.key.edge {
			detail += " via edge from " + strings.Join(it.bucket.clients, ", ")
		}
		b.log.Warn("session boundary rejected", "route", it.key.pattern,
			"reason", it.key.reason, "count", it.bucket.count)
		if _, err := b.audit.Record(audit.Entry{
			Actor: "anonymous", Action: auditActionRejected,
			Outcome: audit.OutcomeError, Detail: detail,
		}); err != nil {
			b.log.Warn("audit record failed", "action", auditActionRejected, "error", err)
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
