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

// errUnsafeSessionPath is returned when the session directory or file
// is a symlink (or otherwise not what we created), so a token is never
// written through a link planted by another party.
var errUnsafeSessionPath = errors.New("refusing to use a symlinked or unsafe session path")

// saveSession writes the session atomically with restrictive modes. It
// never follows a symlinked directory or file. A directory that already
// exists is left as the user made it (no chmod), but must not be
// group/world writable; a new directory is created 0700.
func saveSession(sess storedSession) error {
	path, err := sessionFilePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("session dir: %w", err)
		}
	case err != nil:
		return fmt.Errorf("session dir: %w", err)
	case info.Mode()&fs.ModeSymlink != 0 || !info.IsDir():
		return fmt.Errorf("session dir %s: %w", dir, errUnsafeSessionPath)
	case info.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("session dir %s is group/world writable: %w", dir, errUnsafeSessionPath)
	}
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("session file %s: %w", path, errUnsafeSessionPath)
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
// unreadable, a symlink, or group/world accessible.
func loadSession(server string) string {
	token, _ := loadSessionHint(server)
	return token
}

// loadSessionHint is loadSession plus, when a stored session exists but
// belongs to a different server, a hint telling the user how to proceed.
func loadSessionHint(server string) (token, hint string) {
	path, err := sessionFilePath()
	if err != nil {
		return "", ""
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var sess storedSession
	if json.Unmarshal(data, &sess) != nil || sess.Token == "" {
		return "", ""
	}
	if !sess.ExpiresAt.After(time.Now()) {
		return "", ""
	}
	if sess.Server != server {
		return "", fmt.Sprintf("stored session is for %s, not %s; run homesteadctl login", sess.Server, server)
	}
	return sess.Token, ""
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
