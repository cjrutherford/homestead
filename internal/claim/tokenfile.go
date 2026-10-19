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
	token, expires, err := s.Rotate(ttl)
	if errors.Is(err, ErrAlreadyClaimed) {
		return time.Time{}, RemoveTokenFile(path)
	}
	if err != nil {
		return time.Time{}, err
	}
	if err := WriteTokenFile(path, token, expires); err != nil {
		return time.Time{}, err
	}
	return expires, nil
}

// MaintainTokenFile keeps the token file fresh until ctx ends or the
// appliance is claimed: it ensures a live token now, sleeps on a timer
// until that token expires, then re-mints. Claiming removes the file
// through the OnClaimed hook and ends the loop. This is a dedicated timer
// rather than a scheduler job: scheduler jobs run through the audited
// operation runner on a coarse cadence and persist last-run state, while
// the token needs an exact-expiry wake with no restart and no record.
// onError receives failures; the loop retries after a minute.
func (s *Service) MaintainTokenFile(ctx context.Context, path string, ttl time.Duration, onError func(error)) {
	claimed := make(chan struct{})
	var once sync.Once
	s.OnClaimed(func() {
		once.Do(func() {
			_ = RemoveTokenFile(path)
			close(claimed)
		})
	})
	for {
		wait := time.Minute
		expiry, err := s.EnsureTokenFile(path, ttl)
		switch {
		case err != nil:
			if onError != nil {
				onError(err)
			}
		case expiry.IsZero():
			return
		default:
			wait = time.Until(expiry)
			if wait < time.Second {
				wait = time.Second
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-claimed:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
