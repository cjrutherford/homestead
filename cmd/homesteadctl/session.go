package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Stored CLI session (ADR-020, S256): `homesteadctl login` writes the
// bearer token to ~/.config/homestead/session (file 0600, directory
// 0700) and later commands reuse it until it expires or is revoked.
// The file records the server it was minted for so a token is never
// sent to a different daemon.

type storedSession struct {
	Server    string    `json:"server"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// sessionFilePath is ~/.config/homestead/session.
func sessionFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "homestead", "session"), nil
}

// saveSession writes the session atomically with restrictive modes.
func saveSession(sess storedSession) error {
	path, err := sessionFilePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("session dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("session dir: %w", err)
	}
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".session-*") // created 0600
	if err != nil {
		return fmt.Errorf("session file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("session file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("session file: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// loadSession returns the stored token for server, or "" when there is
// none, it belongs to another server, it has expired, or the file is
// unreadable or group/world accessible.
func loadSession(server string) string {
	path, err := sessionFilePath()
	if err != nil {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var sess storedSession
	if json.Unmarshal(data, &sess) != nil || sess.Token == "" {
		return ""
	}
	if sess.Server != server || !sess.ExpiresAt.After(time.Now()) {
		return ""
	}
	return sess.Token
}

// deleteSession removes the stored session file; absence is fine.
func deleteSession() error {
	path, err := sessionFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
