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

// SessionTTL bounds a session's absolute lifetime.
const SessionTTL = 30 * 24 * time.Hour

// MinPasswordLength is the floor for administrator passwords.
const MinPasswordLength = 12

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

// Session is an authenticated session: plaintext token plus expiry.
// The token is shown once at login and hashed in storage.
type Session struct {
	Token     string
	ExpiresAt time.Time
}

// VerifiedSession is a proven session with its owner.
type VerifiedSession struct {
	IdentityID int64
	Username   string
	ExpiresAt  time.Time
}

// Service authenticates administrators over the operational store.
type Service struct {
	db    *sql.DB
	claim *claim.Service
}

// New returns a Service. Claim state gates first-admin setup.
func New(s *store.Store, c *claim.Service) *Service {
	return &Service{db: s.DB(), claim: c}
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
		`INSERT INTO identities (username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		username, hash, now, now,
	)
	if err != nil {
		return Admin{}, fmt.Errorf("auth: create admin: %w", err)
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
	entropy := make([]byte, tokenBytes)
	if _, err := rand.Read(entropy); err != nil {
		return Session{}, fmt.Errorf("auth: entropy: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(entropy)
	sum := sha256.Sum256([]byte(token))
	expires := time.Now().Add(SessionTTL)
	now := formatTime(time.Now())
	if _, err := s.db.Exec(
		`INSERT INTO sessions (identity_id, token_hash, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		id, sum[:], formatTime(expires), now, now,
	); err != nil {
		return Session{}, fmt.Errorf("auth: create session: %w", err)
	}
	return Session{Token: token, ExpiresAt: expires}, nil
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

// ChangePassword rotates username's password with the current
// credential: wrong current answers invalid without saying more,
// weak replacements refuse under the same policy as setup, and
// success revokes every session so only fresh logins continue.
// Guesses feed the same lockout as logins — rotation never bypasses
// the throttle — and success forgives it.
func (s *Service) ChangePassword(username, currentPassword, newPassword string) (Admin, error) {
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
	if err := s.revokeSessions(id); err != nil {
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

// Verify proves a session token, rejecting unknown and expired sessions.
func (s *Service) Verify(token string) (VerifiedSession, error) {
	if token == "" {
		return VerifiedSession{}, ErrInvalidSession
	}
	// Collect first (single-connection store): no cursor open across use.
	type row struct {
		identityID int64
		username   string
		hash       []byte
		expiresAt  string
	}
	rows, err := s.db.Query(
		`SELECT s.identity_id, i.username, s.token_hash, s.expires_at
		 FROM sessions s JOIN identities i ON i.id = s.identity_id`,
	)
	if err != nil {
		return VerifiedSession{}, fmt.Errorf("auth: list sessions: %w", err)
	}
	defer rows.Close()
	sum := sha256.Sum256([]byte(token))
	now := time.Now()
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.identityID, &r.username, &r.hash, &r.expiresAt); err != nil {
			return VerifiedSession{}, fmt.Errorf("auth: scan session: %w", err)
		}
		if subtle.ConstantTimeCompare(r.hash, sum[:]) != 1 {
			continue
		}
		expiry, err := time.Parse(time.RFC3339Nano, r.expiresAt)
		if err != nil {
			return VerifiedSession{}, fmt.Errorf("auth: parse expiry: %w", err)
		}
		if !expiry.After(now) {
			return VerifiedSession{}, ErrInvalidSession
		}
		return VerifiedSession{IdentityID: r.identityID, Username: r.username, ExpiresAt: expiry}, nil
	}
	if err := rows.Err(); err != nil {
		return VerifiedSession{}, fmt.Errorf("auth: list sessions: %w", err)
	}
	return VerifiedSession{}, ErrInvalidSession
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
