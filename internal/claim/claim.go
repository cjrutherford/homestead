// Package claim implements the secure appliance claim (S019; ADR-015).
//
// A fresh appliance is UNCLAIMED and exposes only health/identity/claim.
// The daemon mints a short-lived single-use token (shown on the console);
// the owner redeems it once, flipping the appliance to CLAIMED. Plaintext
// tokens never touch storage — only SHA-256 hashes. Token validation is
// constant-time.
//
// First-administrator creation and authenticated sessions belong to S020,
// which builds on the CLAIMED state established here.
package claim

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"homestead/internal/store"
)

// TokenTTL bounds a claim token's redeemable lifetime.
const TokenTTL = 30 * time.Minute

// tokenBytes is the entropy per token (32 base64url characters on display).
const tokenBytes = 24

// State is the appliance claim state.
type State string

const (
	// StateUnclaimed is a fresh appliance awaiting its owner.
	StateUnclaimed State = "UNCLAIMED"
	// StateClaimed has a redeemed claim token.
	StateClaimed State = "CLAIMED"
)

func (s State) String() string {
	return string(s)
}

// Sentinel claim failures.
var (
	// ErrAlreadyClaimed reports any claim operation past the transition.
	ErrAlreadyClaimed = errors.New("appliance already claimed")
	// ErrTokenInvalid reports an unknown or consumed token.
	ErrTokenInvalid = errors.New("invalid claim token")
	// ErrTokenExpired reports a well-formed but lapsed token.
	ErrTokenExpired = errors.New("claim token expired")
)

// Service mints and redeems claim tokens over the operational store.
type Service struct {
	db *sql.DB

	mu      sync.Mutex
	claimed []func()

	// pending is a minted token whose file write has not succeeded yet;
	// retries reuse it instead of dropping and re-minting rows (S288).
	pending        string
	pendingExpires time.Time
}

// New returns a Service over an open store (S013 migrations provide v2+).
func New(s *store.Store) *Service {
	return &Service{db: s.DB()}
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// CurrentState reads the appliance state singleton.
func (s *Service) CurrentState() (State, error) {
	var state string
	if err := s.db.QueryRow(`SELECT state FROM appliance WHERE id = 1`).Scan(&state); err != nil {
		return "", fmt.Errorf("claim: read state: %w", err)
	}
	switch State(state) {
	case StateUnclaimed:
		return StateUnclaimed, nil
	case StateClaimed:
		return StateClaimed, nil
	default:
		return "", fmt.Errorf("claim: unknown state %q", state)
	}
}

// LiveToken reports whether an unexpired, unused token exists.
func (s *Service) LiveToken() (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM claim_tokens WHERE used_at IS NULL AND expires_at > ?`,
		formatTime(time.Now()),
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("claim: count live tokens: %w", err)
	}
	return count > 0, nil
}

// MintToken creates a single-use token valid for ttl and returns its
// plaintext (shown once, never stored). Expired tokens are pruned.
// Minting a claimed appliance is an error.
func (s *Service) MintToken(ttl time.Duration) (string, error) {
	state, err := s.CurrentState()
	if err != nil {
		return "", err
	}
	if state == StateClaimed {
		return "", ErrAlreadyClaimed
	}
	entropy := make([]byte, tokenBytes)
	if _, err := rand.Read(entropy); err != nil {
		return "", fmt.Errorf("claim: entropy: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(entropy)
	sum := sha256.Sum256([]byte(token))
	now := time.Now()
	if _, err := s.db.Exec(`DELETE FROM claim_tokens WHERE expires_at <= ?`, formatTime(now)); err != nil {
		return "", fmt.Errorf("claim: prune expired: %w", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO claim_tokens (token_hash, expires_at) VALUES (?, ?)`,
		sum[:], formatTime(now.Add(ttl)),
	); err != nil {
		return "", fmt.Errorf("claim: store token: %w", err)
	}
	return token, nil
}

// Claim redeems token: unknown/consumed tokens fail, expired tokens fail,
// and success marks the token used and flips the appliance to CLAIMED.
func (s *Service) Claim(token string) error {
	if token == "" {
		return ErrTokenInvalid
	}
	state, err := s.CurrentState()
	if err != nil {
		return err
	}
	if state == StateClaimed {
		return ErrAlreadyClaimed
	}
	sum := sha256.Sum256([]byte(token))
	// Collect first, transact after: the store serializes writes through a
	// single connection, so no query cursor may be open across Begin.
	type candidate struct {
		id        int64
		hash      []byte
		expiresAt string
	}
	candidates, err := func() ([]candidate, error) {
		rows, err := s.db.Query(
			`SELECT id, token_hash, expires_at FROM claim_tokens WHERE used_at IS NULL`,
		)
		if err != nil {
			return nil, fmt.Errorf("claim: list tokens: %w", err)
		}
		defer rows.Close()
		var out []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.hash, &c.expiresAt); err != nil {
				return nil, fmt.Errorf("claim: scan token: %w", err)
			}
			out = append(out, c)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("claim: list tokens: %w", err)
		}
		return out, nil
	}()
	if err != nil {
		return err
	}
	var match *candidate
	now := time.Now()
	expired := false
	for i := range candidates {
		c := &candidates[i]
		if subtle.ConstantTimeCompare(c.hash, sum[:]) != 1 {
			continue
		}
		match = c
		expiry, err := time.Parse(time.RFC3339Nano, c.expiresAt)
		if err != nil {
			return fmt.Errorf("claim: parse expiry: %w", err)
		}
		if !expiry.After(now) {
			expired = true
		}
		break
	}
	if match == nil {
		return ErrTokenInvalid
	}
	if expired {
		return ErrTokenExpired
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("claim: begin: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE claim_tokens SET used_at = ? WHERE id = ?`, formatTime(now), match.id); err != nil {
		return fmt.Errorf("claim: consume token: %w", err)
	}
	if _, err := tx.Exec(`UPDATE appliance SET state = 'CLAIMED' WHERE id = 1`); err != nil {
		return fmt.Errorf("claim: transition: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("claim: commit: %w", err)
	}
	s.mu.Lock()
	hooks := append([]func(){}, s.claimed...)
	s.mu.Unlock()
	for _, hook := range hooks {
		hook()
	}
	return nil
}

// OnClaimed registers fn to run after a successful Claim, so the token
// file can go the moment the appliance is claimed.
func (s *Service) OnClaimed(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimed = append(s.claimed, fn)
}

// Rotate leaves exactly one live token: every unused token is dropped
// (expired rows are pruned with them) and a fresh one is minted. It
// returns the plaintext and its expiry.
func (s *Service) Rotate(ttl time.Duration) (string, time.Time, error) {
	state, err := s.CurrentState()
	if err != nil {
		return "", time.Time{}, err
	}
	if state == StateClaimed {
		return "", time.Time{}, ErrAlreadyClaimed
	}
	if _, err := s.db.Exec(`DELETE FROM claim_tokens WHERE used_at IS NULL`); err != nil {
		return "", time.Time{}, fmt.Errorf("claim: drop old tokens: %w", err)
	}
	expires := time.Now().Add(ttl)
	token, err := s.MintToken(ttl)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// LiveUntil reports whether token is an unused, unexpired claim token and
// when it expires.
func (s *Service) LiveUntil(token string) (time.Time, bool, error) {
	sum := sha256.Sum256([]byte(token))
	var expiresAt string
	err := s.db.QueryRow(
		`SELECT expires_at FROM claim_tokens WHERE token_hash = ? AND used_at IS NULL`, sum[:],
	).Scan(&expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("claim: look up token: %w", err)
	}
	expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("claim: parse expiry: %w", err)
	}
	return expiry, expiry.After(time.Now()), nil
}
