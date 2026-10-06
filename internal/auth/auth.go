// Package auth implements administrator authentication (S020).
//
// After the appliance is CLAIMED (S019), the owner creates the first
// administrator once and then authenticates with durable, database-backed
// sessions. Passwords are bcrypt hashes — plaintext never persists.
// Session tokens are SHA-256-hashed like claim tokens.
//
// Scope: setup, login, verify, logout. Requiring authentication on other
// endpoints, password rotation, and lockout policy belong to later slices
// (S200 hardens identity); recovery belongs to S021.
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
// returned once; only its hash is stored.
func (s *Service) Login(username, password string) (Session, error) {
	var id int64
	var hash []byte
	err := s.db.QueryRow(`SELECT id, password_hash FROM identities WHERE username = ?`, username).Scan(&id, &hash)
	if err == sql.ErrNoRows {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, fmt.Errorf("auth: find admin: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return Session{}, ErrInvalidCredentials
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
