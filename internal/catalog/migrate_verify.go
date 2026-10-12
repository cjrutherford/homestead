package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Verification is deliberately thorough because the stack is stopped
// and the old volume is the only other copy: every entry is compared
// by type, mode, owner, symlink target, extended attributes (where
// readable), and — for regular files — a streamed SHA-256 of the
// content. The cost is one extra read of both trees, paid once during
// the downtime window the plan already estimates. Sockets are skipped
// on both sides: tar cannot carry them and nothing meaningful lives in
// one at rest.

// entrySignature describes one tree entry for comparison.
func entrySignature(path string, info fs.FileInfo) (string, error) {
	var parts []string
	mode := info.Mode()
	switch {
	case mode.IsDir():
		parts = append(parts, "dir")
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		parts = append(parts, "link:"+target)
	case mode.IsRegular():
		sum, err := fileHash(path)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("file:%d:%s", info.Size(), sum))
	case mode&os.ModeNamedPipe != 0:
		parts = append(parts, "fifo")
	case mode&os.ModeSocket != 0:
		return "", nil
	default:
		parts = append(parts, "special")
	}
	if mode&os.ModeSymlink == 0 {
		parts = append(parts, fmt.Sprintf("mode:%o", mode&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky)))
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		parts = append(parts, fmt.Sprintf("uid:%d gid:%d", stat.Uid, stat.Gid))
	}
	if mode&os.ModeSymlink == 0 {
		parts = append(parts, xattrSignature(path))
	}
	return strings.Join(parts, " "), nil
}

// fileHash streams a file through SHA-256.
func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// xattrSignature lists extended attributes with values hashed. An
// unreadable or unsupported set (filesystem without xattrs, a
// namespace needing privilege) reads as empty on both sides, so it can
// only hide a difference the platform would not let us see anyway.
func xattrSignature(path string) string {
	size, err := syscall.Listxattr(path, nil)
	if err != nil || size <= 0 {
		return "xattr:-"
	}
	buf := make([]byte, size)
	size, err = syscall.Listxattr(path, buf)
	if err != nil {
		return "xattr:-"
	}
	var names []string
	for _, name := range strings.Split(string(buf[:size]), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out []string
	for _, name := range names {
		valueSize, err := syscall.Getxattr(path, name, nil)
		if err != nil {
			continue
		}
		value := make([]byte, valueSize)
		if valueSize > 0 {
			if _, err := syscall.Getxattr(path, name, value); err != nil {
				continue
			}
		}
		sum := sha256.Sum256(value)
		out = append(out, name+"="+hex.EncodeToString(sum[:8]))
	}
	return "xattr:" + strings.Join(out, ",")
}

// signature maps relative paths ("." is the root) to entry signatures,
// skipping excluded sub-trees. A missing root is an error unless
// allowMissing (a nested path absent from its volume is simply empty).
func signature(root string, excludes []string, allowMissing bool) (map[string]string, error) {
	out := map[string]string{}
	if _, err := os.Lstat(root); err != nil {
		if os.IsNotExist(err) && allowMissing {
			return out, nil
		}
		return nil, err
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, exclude := range excludes {
			if relative == exclude || strings.HasPrefix(relative, exclude+"/") {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		sig, err := entrySignature(path, info)
		if err != nil {
			return err
		}
		if sig != "" {
			out[relative] = sig
		}
		return nil
	})
	return out, err
}

// verifyCopy requires the destination to hold exactly the source's
// entries with identical attributes and content. A missing source is a
// failure except for nested items, whose path may legitimately be
// absent from the volume.
func verifyCopy(item MigrationItem) error {
	want, err := signature(item.Source, item.Excludes, item.Nested)
	if err != nil {
		return fmt.Errorf("catalog: read source %s: %w", item.Source, err)
	}
	got, err := signature(item.Dest, nil, false)
	if err != nil {
		return fmt.Errorf("catalog: read copy %s: %w", item.Dest, err)
	}
	if len(want) == 0 {
		// Absent nested source: nothing to compare beyond an empty copy.
		return nil
	}
	for name, sig := range want {
		if got[name] != sig {
			return fmt.Errorf("catalog: copy %s differs from %s at %q (want %q, got %q)", item.Dest, item.Source, name, sig, got[name])
		}
	}
	for name, sig := range got {
		if _, ok := want[name]; !ok {
			// A nested mountpoint stub provisioned beside the copy is
			// not a difference.
			if strings.HasPrefix(sig, "dir ") {
				continue
			}
			return fmt.Errorf("catalog: copy %s has unexpected %q", item.Dest, name)
		}
	}
	return nil
}
