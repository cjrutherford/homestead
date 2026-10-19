package claim

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultTokenPath is the claim token file. It lives in the daemon's
// systemd RuntimeDirectory (/run/homestead, writable under
// ProtectSystem=strict), which is tmpfs: a reboot starts clean and the
// token never reaches disk (S288, ADR-015).
const DefaultTokenPath = "/run/homestead/claim-token"

// TokenFile is the parsed claim token file: the plaintext token and its
// expiry.
type TokenFile struct {
	Token   string
	Expires time.Time
}

// WriteTokenFile replaces the token file atomically (temp file in the same
// directory, mode 0600, then rename), so a reader never sees a partial
// token and the file is never group- or world-readable.
func WriteTokenFile(path, token string, expires time.Time) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claim-token-*")
	if err != nil {
		return fmt.Errorf("claim: token file: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("claim: token file: %w", err)
	}
	body := token + "\n" + expires.UTC().Format(time.RFC3339) + "\n"
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return fmt.Errorf("claim: token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("claim: token file: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("claim: token file: %w", err)
	}
	return nil
}

// ReadTokenFile parses the token file. A missing file returns an error
// satisfying errors.Is(err, os.ErrNotExist).
func ReadTokenFile(path string) (TokenFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TokenFile{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || lines[0] == "" {
		return TokenFile{}, errors.New("claim: token file is malformed")
	}
	expires, err := time.Parse(time.RFC3339, strings.TrimSpace(lines[1]))
	if err != nil {
		return TokenFile{}, errors.New("claim: token file is malformed")
	}
	return TokenFile{Token: lines[0], Expires: expires}, nil
}

// RemoveTokenFile deletes the file; absence is fine.
func RemoveTokenFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("claim: remove token file: %w", err)
	}
	return nil
}

// EnsureTokenFile makes the file hold exactly one live token while the
// appliance is unclaimed, and removes it once claimed. A file whose token
// is still live is kept, so a daemon restart reuses it (plaintext exists
// only in the file). It returns the expiry of the live token, or the zero
// time when the appliance is claimed.
func (s *Service) EnsureTokenFile(path string, ttl time.Duration) (time.Time, error) {
	state, err := s.CurrentState()
	if err != nil {
		return time.Time{}, err
	}
	if state == StateClaimed {
		return time.Time{}, RemoveTokenFile(path)
	}
	if tf, err := ReadTokenFile(path); err == nil {
		if expiry, live, err := s.LiveUntil(tf.Token); err == nil && live {
			return expiry, nil
		}
	}
	token, expires, err := s.pendingOrRotate(ttl)
	if errors.Is(err, ErrAlreadyClaimed) {
		return time.Time{}, RemoveTokenFile(path)
	}
	if err != nil {
		return time.Time{}, err
	}
	if err := WriteTokenFile(path, token, expires); err != nil {
		return time.Time{}, err
	}
	s.mu.Lock()
	s.pending = ""
	s.mu.Unlock()
	if afterTokenWrite != nil {
		afterTokenWrite()
	}
	// A claim may have committed (and run its remove hook) between the
	// state check and the write: re-check, and if so take the file and the
	// unused token back out so no dead token is left behind.
	if state, err := s.CurrentState(); err == nil && state == StateClaimed {
		_, _ = s.db.Exec(`DELETE FROM claim_tokens WHERE used_at IS NULL`)
		return time.Time{}, RemoveTokenFile(path)
	}
	return expires, nil
}

// pendingOrRotate returns the minted-but-unwritten token while it is still
// live, else rotates a new one and remembers it until the file is written.
func (s *Service) pendingOrRotate(ttl time.Duration) (string, time.Time, error) {
	s.mu.Lock()
	token, expires := s.pending, s.pendingExpires
	s.mu.Unlock()
	if token != "" && expires.After(time.Now()) {
		if _, live, err := s.LiveUntil(token); err == nil && live {
			return token, expires, nil
		}
	}
	token, expires, err := s.Rotate(ttl)
	if err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	s.pending, s.pendingExpires = token, expires
	s.mu.Unlock()
	return token, expires, nil
}

// MaintainTokenFile keeps the token file fresh until ctx ends or the
// appliance is claimed: it ensures a live token now, sleeps on a timer
// until that token expires, then re-mints. Claiming removes the file
// through the OnClaimed hook and ends the loop. This is a dedicated timer
// rather than a scheduler job: scheduler jobs run through the audited
// operation runner on a coarse cadence and persist last-run state, while
// the token needs an exact-expiry wake with no restart and no record.
// onError receives failures, deduplicated: the first failure is reported,
// then the same failure again only after repeatErrorEvery; nil reports
// recovery once. The loop retries after a minute. On ctx end the file is
// removed so a clean stop leaves no dead token (the unit preserves the
// runtime directory across restarts).
func (s *Service) MaintainTokenFile(ctx context.Context, path string, ttl time.Duration, onError func(error)) {
	report := func(err error) {
		if onError != nil {
			onError(err)
		}
	}
	claimed := make(chan struct{})
	var once sync.Once
	s.OnClaimed(func() {
		once.Do(func() {
			if err := RemoveTokenFile(path); err != nil {
				report(err)
			}
			close(claimed)
		})
	})
	var failing bool
	var lastReported time.Time
	for {
		wait := retryEvery
		expiry, err := s.EnsureTokenFile(path, ttl)
		switch {
		case err != nil:
			if !failing || time.Since(lastReported) >= repeatErrorEvery {
				report(err)
				lastReported = time.Now()
			}
			failing = true
		default:
			if failing {
				report(nil)
				failing = false
			}
			if expiry.IsZero() {
				return
			}
			wait = time.Until(expiry)
			if wait < time.Second {
				wait = time.Second
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			if err := RemoveTokenFile(path); err != nil {
				report(err)
			}
			return
		case <-claimed:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// repeatErrorEvery is how long a persistent token file failure stays quiet
// before it is logged again.
const repeatErrorEvery = time.Hour

// retryEvery is the wait after a failed ensure; tests shorten it.
var retryEvery = time.Minute

// afterTokenWrite is a test seam between the file write and the claim
// state re-check.
var afterTokenWrite func()
