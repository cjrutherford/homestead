package craftfiles

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Session errors.
var (
	// ErrInvalidCode: unknown, reused, expired or mismatched launch code.
	ErrInvalidCode = errors.New("invalid or expired launch code")
	// ErrNoSession: unknown, expired or revoked craft session.
	ErrNoSession = errors.New("craft session required")
	// ErrBusy: too many outstanding codes or sessions.
	ErrBusy = errors.New("too many craft sessions")
	// ErrOperatorInvalid: the bound operator session no longer verifies
	// (logout, password change, expiry).
	ErrOperatorInvalid = errors.New("operator session ended")
	// ErrOperatorUnavailable: the operator session could not be checked.
	ErrOperatorUnavailable = errors.New("operator session check unavailable")
)

// Operator re-verifies the operator session a craft session is bound to.
// It returns the operator session's current expiry (the earlier of its
// absolute and idle limits). The server adapts auth.Service.
type Operator interface {
	Check(token string) (expires time.Time, err error)
}

// Session lifetimes.
const (
	DefaultCodeTTL    = 60 * time.Second
	DefaultSessionTTL = 8 * time.Hour
	maxCodes          = 64
	maxSessions       = 256
)

// Session is a craft-scoped session: one craft app on one craft host,
// bound to one operator session.
type Session struct {
	ID       string // short non-secret identifier for audit
	App      string
	Host     string
	Operator string // operator username, for audit

	opToken  string
	expires  time.Time
	inflight atomic.Int32
}

// Expires is when the session ends at the latest.
func (s *Session) Expires() time.Time { return s.expires }

type launch struct {
	opToken  string
	username string
	app      string
	host     string
	expires  time.Time // code expiry
	opExpiry time.Time
}

// Sessions holds one-time launch codes and craft sessions in memory only:
// a daemon restart ends them (the owner relaunches). Tokens are stored as
// SHA-256 hashes.
type Sessions struct {
	Now        func() time.Time
	Operator   Operator
	CodeTTL    time.Duration
	SessionTTL time.Duration

	mu       sync.Mutex
	codes    map[[32]byte]launch
	sessions map[[32]byte]*Session
}

// NewSessions returns an empty store.
func NewSessions(op Operator) *Sessions {
	return &Sessions{
		Now: time.Now, Operator: op,
		CodeTTL: DefaultCodeTTL, SessionTTL: DefaultSessionTTL,
		codes: map[[32]byte]launch{}, sessions: map[[32]byte]*Session{},
	}
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func newToken() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw[:])
	return tok, sha256.Sum256([]byte(tok)), nil
}

func (s *Sessions) pruneLocked(now time.Time) {
	for k, c := range s.codes {
		if !c.expires.After(now) {
			delete(s.codes, k)
		}
	}
	for k, sess := range s.sessions {
		if !sess.expires.After(now) {
			delete(s.sessions, k)
		}
	}
}

// Mint issues a one-time launch code for app on host, bound to the
// operator session opToken (already authenticated and CSRF-checked by the
// caller). opExpires is that session's expiry.
func (s *Sessions) Mint(opToken, username string, opExpires time.Time, app, host string) (string, time.Time, error) {
	tok, h, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	if len(s.codes) >= maxCodes {
		return "", time.Time{}, ErrBusy
	}
	exp := now.Add(s.CodeTTL)
	s.codes[h] = launch{opToken: opToken, username: username, app: app, host: host, expires: exp, opExpiry: opExpires}
	return tok, exp, nil
}

// Exchange spends a launch code (one use, whatever the outcome) for a
// craft session on exactly the craft app and host it was minted for.
// The returned token is the cookie value.
func (s *Sessions) Exchange(code, app, host string) (string, *Session, error) {
	sum := sha256.Sum256([]byte(code))
	now := s.now()
	s.mu.Lock()
	l, ok := s.codes[sum]
	delete(s.codes, sum)
	s.mu.Unlock()
	if !ok || !l.expires.After(now) {
		return "", nil, ErrInvalidCode
	}
	if l.app != app || l.host != host {
		return "", nil, ErrInvalidCode
	}
	// The operator session must still verify at exchange time.
	opExp, err := s.Operator.Check(l.opToken)
	if err != nil {
		return "", nil, mapOperatorErr(err)
	}
	exp := now.Add(s.SessionTTL)
	for _, limit := range []time.Time{l.opExpiry, opExp} {
		if !limit.IsZero() && limit.Before(exp) {
			exp = limit
		}
	}
	if !exp.After(now) {
		return "", nil, ErrOperatorInvalid
	}
	tok, h, err := newToken()
	if err != nil {
		return "", nil, err
	}
	sess := &Session{
		ID: hex.EncodeToString(h[:4]), App: app, Host: host, Operator: l.username,
		opToken: l.opToken, expires: exp,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	if len(s.sessions) >= maxSessions {
		return "", nil, ErrBusy
	}
	s.sessions[h] = sess
	return tok, sess, nil
}

func mapOperatorErr(err error) error {
	if errors.Is(err, ErrOperatorUnavailable) {
		return ErrOperatorUnavailable
	}
	return ErrOperatorInvalid
}

// Lookup resolves a cookie value to a live session for app and host. It
// re-verifies the operator session on every call, so logout, password
// change and expiry end craft sessions immediately; the craft session
// never outlives the operator session it is bound to.
func (s *Sessions) Lookup(token, app, host string) (*Session, error) {
	if token == "" {
		return nil, ErrNoSession
	}
	sum := sha256.Sum256([]byte(token))
	now := s.now()
	s.mu.Lock()
	sess, ok := s.sessions[sum]
	if ok && !sess.expires.After(now) {
		delete(s.sessions, sum)
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		return nil, ErrNoSession
	}
	if subtle.ConstantTimeCompare([]byte(sess.App), []byte(app)) != 1 || sess.Host != host {
		return nil, ErrNoSession
	}
	if _, err := s.Operator.Check(sess.opToken); err != nil {
		if errors.Is(err, ErrOperatorUnavailable) {
			return nil, ErrOperatorUnavailable
		}
		s.mu.Lock()
		delete(s.sessions, sum)
		s.mu.Unlock()
		return nil, ErrNoSession
	}
	return sess, nil
}

// End drops a session by cookie value.
func (s *Sessions) End(token string) {
	sum := sha256.Sum256([]byte(token))
	s.mu.Lock()
	delete(s.sessions, sum)
	s.mu.Unlock()
}

// Counts reports outstanding codes and sessions (tests, status).
func (s *Sessions) Counts() (codes, sessions int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.codes), len(s.sessions)
}

func (s *Session) acquire(limit int) bool {
	if int(s.inflight.Add(1)) > limit {
		s.inflight.Add(-1)
		return false
	}
	return true
}

func (s *Session) release() { s.inflight.Add(-1) }
