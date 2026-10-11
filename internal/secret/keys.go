package secret

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// versionExpr reads a row's key version; unversioned (0) rows are
// version 1 by ADR-021.
const versionExpr = `CASE WHEN key_version = 0 THEN 1 ELSE key_version END`

func (s *Store) keyFile(version int) string {
	return s.keyPath + ".v" + strconv.Itoa(version)
}

// versionedFiles lists existing key file versions, ascending. Temp files
// and anything not matching keyPath.v<digits> are ignored.
func (s *Store) versionedFiles() ([]int, error) {
	dir := filepath.Dir(s.keyPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("secret: read key directory: %w", err)
	}
	prefix := filepath.Base(s.keyPath) + ".v"
	var out []int
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := name[len(prefix):]
		n, err := strconv.Atoi(suffix)
		if err != nil || n < 1 || strconv.Itoa(n) != suffix {
			continue
		}
		out = append(out, n)
	}
	sort.Ints(out)
	return out, nil
}

// readKeyFile reads and validates one 32-byte key file.
func readKeyFile(path string) ([32]byte, error) {
	var key [32]byte
	raw, err := os.ReadFile(path)
	if err != nil {
		return key, err
	}
	if len(raw) != 32 {
		return key, fmt.Errorf("secret: key file %q must hold 32 bytes", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return key, fmt.Errorf("secret: stat key: %w", err)
	}
	if info.Mode().Perm()&0077 != 0 {
		return key, fmt.Errorf("secret: key file %q must not be group/other readable", path)
	}
	copy(key[:], raw)
	return key, nil
}

// writeKeyFile makes a key durable (mode 0600): temp file, fsync, rename, directory
// fsync. A crash leaves either no file or a complete one.
func writeKeyFile(path string, key [32]byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("secret: write key: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(what string, err error) error {
		tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("secret: %s: %w", what, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail("write key", err)
	}
	if _, err := tmp.Write(key[:]); err != nil {
		return fail("write key", err)
	}
	if err := tmp.Sync(); err != nil {
		return fail("sync key", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("secret: write key: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("secret: install key: %w", err)
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("secret: open key directory: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("secret: sync key directory: %w", err)
	}
	return nil
}

// removeStaleTemps deletes leftover temp files from interrupted writes.
func (s *Store) removeStaleTemps() {
	matches, _ := filepath.Glob(s.keyPath + ".v*.tmp-*")
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// loadKeys loads every versioned key; with none present it migrates the
// legacy key to version 1 (leaving the legacy file untouched) or, on a
// fresh install, generates version 1.
func (s *Store) loadKeys() error {
	s.removeStaleTemps()
	versions, err := s.versionedFiles()
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		key, err := readKeyFile(s.keyPath)
		switch {
		case err == nil:
			// Migrate: the legacy file becomes version 1, byte for byte.
		case os.IsNotExist(err):
			if _, err := rand.Read(key[:]); err != nil {
				return fmt.Errorf("secret: generate key: %w", err)
			}
		default:
			return err
		}
		if err := writeKeyFile(s.keyFile(1), key); err != nil {
			return err
		}
		versions = []int{1}
	}
	s.keys = map[int][32]byte{}
	for _, v := range versions {
		key, err := readKeyFile(s.keyFile(v))
		if err != nil {
			return err
		}
		s.keys[v] = key
	}
	s.current = versions[len(versions)-1]
	return nil
}

// cleanupOlder deletes key files older than the newest once every row
// is on the newest version and decrypts (ADR-021 step 5).
func (s *Store) cleanupOlder() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.keys) < 2 {
		return nil
	}
	if err := s.verifyAllLocked(s.current); err != nil {
		return nil // not fully migrated: keep every key, stay readable
	}
	return s.retireOlderLocked(s.current)
}

// retireOlderLocked removes key files below keep.
func (s *Store) retireOlderLocked(keep int) error {
	removed := false
	for v := range s.keys {
		if v >= keep {
			continue
		}
		if err := os.Remove(s.keyFile(v)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("secret: remove old key: %w", err)
		}
		delete(s.keys, v)
		removed = true
	}
	if removed {
		return syncDir(filepath.Dir(s.keyPath))
	}
	return nil
}

// verifyAllLocked requires every row to sit on version want and decrypt.
func (s *Store) verifyAllLocked(want int) error {
	rows, err := s.db.Query(`SELECT name, nonce, ciphertext, ` + versionExpr + ` FROM secrets`)
	if err != nil {
		return fmt.Errorf("secret: verify: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var nonce, ct []byte
		var v int
		if err := rows.Scan(&name, &nonce, &ct, &v); err != nil {
			return fmt.Errorf("secret: verify: %w", err)
		}
		if v != want {
			return fmt.Errorf("secret: row %q is on key version %d, want %d", name, v, want)
		}
		if _, err := s.open(v, nonce, ct); err != nil {
			return fmt.Errorf("secret: row %q does not verify", name)
		}
	}
	return rows.Err()
}
