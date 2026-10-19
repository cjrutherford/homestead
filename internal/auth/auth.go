// Package auth implements administrator authentication (S020).
//
// After the appliance is CLAIMED (S019), the owner creates the first
// administrator once and then authenticates with durable, database-backed
// sessions. Passwords are bcrypt hashes — plaintext never persists.
// Session tokens are SHA-256-hashed like claim tokens.
//
// Scope: setup, login, verify, logout. S200 hardens identity with
// password rotation, login lockout, and session revocation on
// credential change and recovery; recovery belongs to S021.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"

	"homestead/internal/claim"
	"homestead/internal/store"

	"golang.org/x/crypto/bcrypt"
)

// Session lifetime (ADR-020, S256): a session dies after IdleTTL without
// use or AbsoluteTTL after login, whichever comes first.
const (
	IdleTTL     = 12 * time.Hour
	AbsoluteTTL = 7 * 24 * time.Hour
	// touchInterval throttles the idle-clock write so verification
	// stays read-mostly.
	touchInterval = time.Minute
)

// MinPasswordLength is the floor for administrator passwords.
const MinPasswordLength = 12

// MaxPasswordLength is the ceiling in bytes: bcrypt rejects longer
// inputs, so policy refuses them up front with a clear error.
const MaxPasswordLength = 72

// Login lockout policy (S200): five failures inside ten minutes lock
// the attempted username for fifteen. Attempts track by attempted
// username before lookup, so unknown and known names behave
// identically and lockout never becomes an account oracle.
const (
	MaxLoginFailures   = 5
	LoginAttemptWindow = 10 * time.Minute
	LoginLockout       = 15 * time.Minute
)

// tokenBytes is the entropy per session token.
const tokenBytes = 32

var validUsername = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Sentinel authentication failures. Login never distinguishes unknown
// users from wrong passwords (no account oracle).
var (
	// ErrNotClaimed reports setup before the appliance is claimed.
	ErrNotClaimed = errors.New("appliance not claimed")
	// ErrAdminExists reports a second setup attempt.
	ErrAdminExists = errors.New("administrator already exists")
	// ErrWeakPassword reports a short password.
	ErrWeakPassword = errors.New("password must be at least 12 characters")
	// ErrPasswordTooLong reports a password bcrypt cannot hash.
	ErrPasswordTooLong = errors.New("password must be at most 72 bytes")
	// ErrBadUsername reports a malformed username.
	ErrBadUsername = errors.New("username must match [a-z][a-z0-9_-]{0,63}")
	// ErrInvalidCredentials reports a failed login without saying why.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrAccountLocked reports a login during lockout. It answers for
	// known and unknown usernames alike: lockout tracks attempted
	// names, so the response never confirms an account exists.
	ErrAccountLocked = errors.New("account temporarily locked after repeated failures")
	// ErrInvalidSession reports an unknown or expired session token.
	ErrInvalidSession = errors.New("invalid or expired session")
)

// Admin is the first-administrator record.
type Admin struct {
	ID       int64
	Username string
}

// Session is an authenticated session: plaintext token, per-session
// CSRF token, and absolute expiry. Both tokens are shown once at login
// and hashed in storage; the session is reusable until it expires or is
// revoked.
type Session struct {
	Token     string
	CSRFToken string
	ExpiresAt time.Time
}

// Session methods (ADR-033): how the session was obtained. An oidc
// session must re-enter the local password for the step-up routes.
const (
	MethodPassword = "password"
	MethodOIDC     = "oidc"
)

// VerifiedSession is a proven session with its owner.
type VerifiedSession struct {
	IdentityID int64
	Username   string
	// Method is MethodPassword or MethodOIDC.
	Method string
	// ExpiresAt is the absolute expiry; IdleExpiresAt is when the
	// session dies if unused.
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
	// CSRFHash is the stored SHA-256 of the session's CSRF token.
	CSRFHash []byte
}

// CSRFMatches reports, in constant time, whether presented is this
// session's CSRF token. An empty token or a session without one never
// matches.
func (v VerifiedSession) CSRFMatches(presented string) bool {
	if presented == "" || len(v.CSRFHash) == 0 {
		return false
	}
	sum := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(v.CSRFHash, sum[:]) == 1
}

// Service authenticates administrators over the operational store.
type Service struct {
	db    *sql.DB
	claim *claim.Service
	now   func() time.Time
}

// New returns a Service. Claim state gates first-admin setup.
func New(s *store.Store, c *claim.Service) *Service {
	return &Service{db: s.DB(), claim: c, now: time.Now}
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// HasAdmin reports whether any administrator identity exists: the
// readiness signal for setup completion (S222) without exposing who.
func (s *Service) HasAdmin() (bool, error) {
	count, err := s.adminCount()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// adminCount counts administrator identities.
func (s *Service) adminCount() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM identities WHERE username IS NOT NULL`).Scan(&count); err != nil {
		return 0, fmt.Errorf("auth: count admins: %w", err)
	}
	return count, nil
}

// SetupAdmin creates the first administrator. It succeeds exactly once:
// the appliance must be CLAIMED and no administrator may exist yet.
func (s *Service) SetupAdmin(username, password string) (Admin, error) {
	state, err := s.claim.CurrentState()
	if err != nil {
		return Admin{}, err
	}
	if state != claim.StateClaimed {
		return Admin{}, ErrNotClaimed
	}
	if !validUsername.MatchString(username) {
		return Admin{}, ErrBadUsername
	}
	if len(password) > MaxPasswordLength {
		return Admin{}, ErrPasswordTooLong
	}
	if len(password) < MinPasswordLength {
		return Admin{}, ErrWeakPassword
	}
	count, err := s.adminCount()
	if err != nil {
		return Admin{}, err
	}
	if count > 0 {
		return Admin{}, ErrAdminExists
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Admin{}, fmt.Errorf("auth: hash password: %w", err)
	}
	now := formatTime(time.Now())
	res, err := s.db.Exec(
		`INSERT INTO identities (username, password_hash, created_at, updated_at)
		 SELECT ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM identities WHERE username IS NOT NULL)`,
		username, hash, now, now,
	)
	if err != nil {
		return Admin{}, fmt.Errorf("auth: create admin: %w", err)
	}
	// The check and the insert are one statement, so concurrent setups
	// cannot both win; a loser inserted nothing.
	if n, err := res.RowsAffected(); err != nil {
		return Admin{}, fmt.Errorf("auth: create admin: %w", err)
	} else if n == 0 {
		return Admin{}, ErrAdminExists
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Admin{}, fmt.Errorf("auth: admin id: %w", err)
	}
	return Admin{ID: id, Username: username}, nil
}

// Login verifies credentials and mints a session. Plaintext token is
// returned once; only its hash is stored. Failures feed lockout and
// success clears it; locked usernames refuse before lookup so the
// response stays uniform for known and unknown names.
func (s *Service) Login(username, password string) (Session, error) {
	locked, err := s.lockedOut(username)
	if err != nil {
		return Session{}, err
	}
	if locked {
		return Session{}, ErrAccountLocked
	}
	var id int64
	var hash []byte
	err = s.db.QueryRow(`SELECT id, password_hash FROM identities WHERE username = ?`, username).Scan(&id, &hash)
	if err == sql.ErrNoRows {
		return Session{}, s.recordFailure(username)
	}
	if err != nil {
		return Session{}, fmt.Errorf("auth: find admin: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return Session{}, s.recordFailure(username)
	}
	if err := s.clearFailures(username); err != nil {
		return Session{}, err
	}
	return s.mintSession(id, MethodPassword)
}

// MintOIDCSession mints the standard session for an identity whose
// Authentik sign-in the caller has already verified (ADR-033). It checks
// no password and touches no lockout counter: a locked local account does
// not block SSO and SSO success does not clear local failures.
func (s *Service) MintOIDCSession(identityID int64) (Session, error) {
	var found int64
	if err := s.db.QueryRow(`SELECT id FROM identities WHERE id = ? AND username IS NOT NULL`, identityID).Scan(&found); err != nil {
		if err == sql.ErrNoRows {
			return Session{}, ErrInvalidCredentials
		}
		return Session{}, fmt.Errorf("auth: find admin: %w", err)
	}
	return s.mintSession(identityID, MethodOIDC)
}

// mintSession creates the session row with the given method.
func (s *Service) mintSession(id int64, method string) (Session, error) {
	token, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	sum := sha256.Sum256([]byte(token))
	csrfSum := sha256.Sum256([]byte(csrf))
	now := s.now()
	expires := now.Add(AbsoluteTTL)
	stamp := formatTime(now)
	// Expired rows are swept here, off the per-request path.
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at != '' AND expires_at <= ?`, stamp); err != nil {
		return Session{}, fmt.Errorf("auth: sweep sessions: %w", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO sessions (identity_id, token_hash, csrf_hash, expires_at, last_used_at, created_at, updated_at, method)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, sum[:], csrfSum[:], formatTime(expires), stamp, stamp, stamp, method,
	); err != nil {
		return Session{}, fmt.Errorf("auth: create session: %w", err)
	}
	return Session{Token: token, CSRFToken: csrf, ExpiresAt: expires}, nil
}

// RevokeMethodSessions destroys an identity's sessions of one method:
// unlinking Authentik ends the owner's oidc sessions (ADR-033).
func (s *Service) RevokeMethodSessions(identityID int64, method string) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE identity_id = ? AND method = ?`, identityID, method); err != nil {
		return fmt.Errorf("auth: revoke sessions: %w", err)
	}
	return nil
}

// VerifyPassword is fresh re-authentication (ADR-033): it checks the
// local password of username inside the request, not the cached session.
// Failures feed the same lockout as login, so re-auth is no way around
// the throttle. It mints nothing and clears nothing on success (login
// and password change are the only places that forgive failures).
func (s *Service) VerifyPassword(username, password string) error {
	locked, err := s.lockedOut(username)
	if err != nil {
		return err
	}
	if locked {
		return ErrAccountLocked
	}
	var hash []byte
	err = s.db.QueryRow(`SELECT password_hash FROM identities WHERE username = ?`, username).Scan(&hash)
	if err == sql.ErrNoRows {
		return s.recordFailure(username)
	}
	if err != nil {
		return fmt.Errorf("auth: find admin: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return s.recordFailure(username)
	}
	return nil
}

func randomToken() (string, error) {
	entropy := make([]byte, tokenBytes)
	if _, err := rand.Read(entropy); err != nil {
		return "", fmt.Errorf("auth: entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(entropy), nil
}

// lockedOut reports whether username sits inside a lockout. Absent
// rows and empty deadlines read as unlocked; corrupt deadlines fail
// loudly instead of guessing open or shut.
func (s *Service) lockedOut(username string) (bool, error) {
	var lockedUntil string
	err := s.db.QueryRow(`SELECT locked_until FROM login_attempts WHERE username = ?`, username).Scan(&lockedUntil)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("auth: read lockout: %w", err)
	}
	if lockedUntil == "" {
		return false, nil
	}
	deadline, err := time.Parse(time.RFC3339Nano, lockedUntil)
	if err != nil {
		return false, fmt.Errorf("auth: parse lockout: %w", err)
	}
	return deadline.After(time.Now()), nil
}

// recordFailure counts a failed login inside a sliding window and
// locks at the threshold. It answers invalid credentials until the
// trip and locked once locked — identically for unknown usernames,
// which track rows of their own so lockout never oracles accounts.
func (s *Service) recordFailure(username string) error {
	now := time.Now()
	var failures int
	var firstFailureAt string
	err := s.db.QueryRow(`SELECT failures, first_failure_at FROM login_attempts WHERE username = ?`, username).Scan(&failures, &firstFailureAt)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("auth: read attempts: %w", err)
	}
	if err == sql.ErrNoRows || firstFailureAt == "" {
		failures, firstFailureAt = 0, formatTime(now)
	} else if start, parseErr := time.Parse(time.RFC3339Nano, firstFailureAt); parseErr != nil || now.Sub(start) > LoginAttemptWindow {
		failures, firstFailureAt = 0, formatTime(now)
	}
	failures++
	lockedUntil := ""
	result := ErrInvalidCredentials
	if failures >= MaxLoginFailures {
		lockedUntil = formatTime(now.Add(LoginLockout))
		result = ErrAccountLocked
	}
	if _, err := s.db.Exec(
		`INSERT INTO login_attempts (username, failures, first_failure_at, locked_until) VALUES (?, ?, ?, ?)
		 ON CONFLICT(username) DO UPDATE SET failures = excluded.failures, first_failure_at = excluded.first_failure_at, locked_until = excluded.locked_until`,
		username, failures, firstFailureAt, lockedUntil,
	); err != nil {
		return fmt.Errorf("auth: record attempt: %w", err)
	}
	return result
}

// clearFailures drops username's attempt row after a good login:
// success forgives, so stale failures never pile into a lockout.
func (s *Service) clearFailures(username string) error {
	if _, err := s.db.Exec(`DELETE FROM login_attempts WHERE username = ?`, username); err != nil {
		return fmt.Errorf("auth: clear attempts: %w", err)
	}
	return nil
}

// revokeSessions destroys every session for an identity. Credential
// change and recovery call it so pre-change sessions — possibly held
// by whoever forced the change — stop working at the boundary.
func (s *Service) revokeSessions(identityID int64) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE identity_id = ?`, identityID); err != nil {
		return fmt.Errorf("auth: revoke sessions: %w", err)
	}
	return nil
}

// revokeOtherSessions destroys every session for an identity except the
// one identified by keepToken.
func (s *Service) revokeOtherSessions(identityID int64, keepToken string) error {
	keep := sha256.Sum256([]byte(keepToken))
	if _, err := s.db.Exec(
		`DELETE FROM sessions WHERE identity_id = ? AND (token_hash IS NULL OR token_hash != ?)`,
		identityID, keep[:],
	); err != nil {
		return fmt.Errorf("auth: revoke sessions: %w", err)
	}
	return nil
}

// ChangePasswordKeepingSession is ChangePassword for a logged-in
// caller: every other session of the administrator is revoked and the
// caller's own (keepToken) survives.
func (s *Service) ChangePasswordKeepingSession(username, currentPassword, newPassword, keepToken string) (Admin, error) {
	return s.changePassword(username, currentPassword, newPassword, &keepToken)
}

// ChangePassword rotates username's password with the current
// credential: wrong current answers invalid without saying more,
// weak replacements refuse under the same policy as setup, and
// success revokes every session so only fresh logins continue.
// Guesses feed the same lockout as logins — rotation never bypasses
// the throttle — and success forgives it.
func (s *Service) ChangePassword(username, currentPassword, newPassword string) (Admin, error) {
	return s.changePassword(username, currentPassword, newPassword, nil)
}

func (s *Service) changePassword(username, currentPassword, newPassword string, keepToken *string) (Admin, error) {
	// Refused before any credential check so an unhashable password
	// never reaches bcrypt (which would surface as a 500).
	if len(newPassword) > MaxPasswordLength {
		return Admin{}, ErrPasswordTooLong
	}
	locked, err := s.lockedOut(username)
	if err != nil {
		return Admin{}, err
	}
	if locked {
		return Admin{}, ErrAccountLocked
	}
	var id int64
	var hash []byte
	err = s.db.QueryRow(`SELECT id, password_hash FROM identities WHERE username = ?`, username).Scan(&id, &hash)
	if err == sql.ErrNoRows {
		return Admin{}, s.recordFailure(username)
	}
	if err != nil {
		return Admin{}, fmt.Errorf("auth: find admin: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(currentPassword)); err != nil {
		return Admin{}, s.recordFailure(username)
	}
	if len(newPassword) < MinPasswordLength {
		return Admin{}, ErrWeakPassword
	}
	replacement, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return Admin{}, fmt.Errorf("auth: hash password: %w", err)
	}
	if _, err := s.db.Exec(
		`UPDATE identities SET password_hash = ?, updated_at = ? WHERE id = ?`,
		replacement, formatTime(time.Now()), id,
	); err != nil {
		return Admin{}, fmt.Errorf("auth: rotate password: %w", err)
	}
	if keepToken != nil {
		err = s.revokeOtherSessions(id, *keepToken)
	} else {
		err = s.revokeSessions(id)
	}
	if err != nil {
		return Admin{}, err
	}
	if err := s.clearFailures(username); err != nil {
		return Admin{}, err
	}
	return Admin{ID: id, Username: username}, nil
}

// Locked reports whether username currently sits inside a login
// lockout: the read half of the S200 throttle for status surfaces.
// Like the throttle itself it tracks attempted names, so the answer
// never confirms an account exists.
func (s *Service) Locked(username string) (bool, error) {
	return s.lockedOut(username)
}

// LockoutInfo is the readable state of a username's login throttle.
type LockoutInfo struct {
	Locked bool
	// LockedUntil is set only while Locked.
	LockedUntil time.Time
	// Failures counts failed attempts inside the current window.
	Failures int
}

// LockoutDetail reads the throttle row: whether the name is locked, until
// when, and how many failures the current window holds.
func (s *Service) LockoutDetail(username string) (LockoutInfo, error) {
	var failures int
	var firstFailureAt, lockedUntil string
	err := s.db.QueryRow(`SELECT failures, first_failure_at, locked_until FROM login_attempts WHERE username = ?`, username).Scan(&failures, &firstFailureAt, &lockedUntil)
	if err == sql.ErrNoRows {
		return LockoutInfo{}, nil
	}
	if err != nil {
		return LockoutInfo{}, fmt.Errorf("auth: read lockout: %w", err)
	}
	info := LockoutInfo{}
	now := time.Now()
	if lockedUntil != "" {
		deadline, err := time.Parse(time.RFC3339Nano, lockedUntil)
		if err != nil {
			return LockoutInfo{}, fmt.Errorf("auth: parse lockout: %w", err)
		}
		if deadline.After(now) {
			info.Locked, info.LockedUntil = true, deadline
		}
	}
	if start, err := time.Parse(time.RFC3339Nano, firstFailureAt); err == nil && now.Sub(start) <= LoginAttemptWindow {
		info.Failures = failures
	}
	if info.Locked && info.Failures == 0 {
		info.Failures = failures
	}
	return info, nil
}

// Verify proves a session token through the unique token-hash index
// (one row, no scan). Unknown, revoked, idle-expired (IdleTTL) and
// absolute-expired (AbsoluteTTL) sessions are refused; expired rows are
// deleted. A successful check refreshes the idle clock, throttled to
// once per touchInterval.
func (s *Service) Verify(token string) (VerifiedSession, error) {
	if token == "" {
		return VerifiedSession{}, ErrInvalidSession
	}
	sum := sha256.Sum256([]byte(token))
	var (
		id                             int64
		v                              VerifiedSession
		expiresAt, createdAt, lastUsed string
	)
	err := s.db.QueryRow(
		`SELECT s.id, s.identity_id, i.username, s.expires_at, s.created_at, s.last_used_at, s.csrf_hash, s.method
		 FROM sessions s JOIN identities i ON i.id = s.identity_id
		 WHERE s.token_hash = ?`, sum[:],
	).Scan(&id, &v.IdentityID, &v.Username, &expiresAt, &createdAt, &lastUsed, &v.CSRFHash, &v.Method)
	if err == sql.ErrNoRows {
		return VerifiedSession{}, ErrInvalidSession
	}
	if err != nil {
		return VerifiedSession{}, fmt.Errorf("auth: find session: %w", err)
	}
	expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return VerifiedSession{}, fmt.Errorf("auth: parse expiry: %w", err)
	}
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return VerifiedSession{}, fmt.Errorf("auth: parse created: %w", err)
	}
	used := created
	if lastUsed != "" {
		if used, err = time.Parse(time.RFC3339Nano, lastUsed); err != nil {
			return VerifiedSession{}, fmt.Errorf("auth: parse last use: %w", err)
		}
	}
	// Legacy rows carry a 30-day expiry; the absolute cap applies to
	// them too.
	if capped := created.Add(AbsoluteTTL); capped.Before(expiry) {
		expiry = capped
	}
	now := s.now()
	idleExpiry := used.Add(IdleTTL)
	if !expiry.After(now) || !idleExpiry.After(now) {
		if _, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
			return VerifiedSession{}, fmt.Errorf("auth: delete expired session: %w", err)
		}
		return VerifiedSession{}, ErrInvalidSession
	}
	if now.Sub(used) >= touchInterval {
		if _, err := s.db.Exec(`UPDATE sessions SET last_used_at = ? WHERE id = ?`, formatTime(now), id); err != nil {
			return VerifiedSession{}, fmt.Errorf("auth: refresh session: %w", err)
		}
		idleExpiry = now.Add(IdleTTL)
	}
	v.ExpiresAt = expiry
	v.IdleExpiresAt = idleExpiry
	return v, nil
}

// Logout destroys the session for token. Unknown tokens fail.
func (s *Service) Logout(token string) error {
	if _, err := s.Verify(token); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(token))
	res, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, sum[:])
	if err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	if affected == 0 {
		return ErrInvalidSession
	}
	return nil
}

// Username names an administrator identity (audit actors).
func (s *Service) Username(identityID int64) (string, error) {
	var name string
	err := s.db.QueryRow(`SELECT username FROM identities WHERE id = ? AND username IS NOT NULL`, identityID).Scan(&name)
	if err == sql.ErrNoRows {
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", fmt.Errorf("auth: find admin: %w", err)
	}
	return name, nil
}
