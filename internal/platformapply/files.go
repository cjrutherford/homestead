package platformapply

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"homestead/internal/release"
)

// openSource opens a regular, non-symlink file for reading.
func openSource(path string) (*os.File, os.FileInfo, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", release.ErrNotRegularFile, filepath.Base(path))
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%w: %s", release.ErrNotRegularFile, filepath.Base(path))
	}
	return f, info, nil
}

// copyFile copies src to a new file at dst (removed first), mode applied
// explicitly (umask-proof), data and file fsynced. It returns the
// SHA-256 and size of the bytes written, bounded by the artifact limit.
func copyFile(src, dst string, mode os.FileMode) (string, int64, error) {
	in, _, err := openSource(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return "", 0, err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, release.MaxArtifactBytes+1))
	if err == nil && n > release.MaxArtifactBytes {
		err = release.ErrTooLarge
	}
	if err == nil {
		err = out.Chmod(mode)
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// hashFile returns the SHA-256 of path, following symlinks (used for
// /proc/self/exe and the installed binary, never owner-supplied input).
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// syncDir fsyncs a directory so a rename inside it is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// RestorePrevious atomically puts the preserved previous binary back at
// binPath: copy to a sibling temp file (same filesystem), verify its
// digest equals wantSHA, then rename over binPath. It is the seam S273
// rollback calls; the runbook's manual restore is the same two steps
// (cp to a sibling, mv over the binary).
func RestorePrevious(binPath, previousPath, wantSHA string) error {
	tmp := binPath + ".restore"
	sum, _, err := copyFile(previousPath, tmp, 0o755)
	if err != nil {
		return fmt.Errorf("restore previous: %w", err)
	}
	if wantSHA != "" && sum != wantSHA {
		_ = os.Remove(tmp)
		return fmt.Errorf("restore previous: %s does not match its recorded digest", previousPath)
	}
	if err := os.Rename(tmp, binPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("restore previous: %w", err)
	}
	return syncDir(filepath.Dir(binPath))
}
