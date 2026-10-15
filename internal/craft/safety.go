package craft

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"syscall"

	"homestead/internal/release"
)

// Archive limits. MaxExpandedBytes is the default cap on the total
// expanded size of an artifact (a decompression-bomb guard); the disk
// preflight is sized from it.
const (
	DefaultMaxExpanded int64 = 1 << 30
	maxArchiveEntries        = 100000
)

// ErrArchive marks an artifact archive that is unsafe to extract.
var ErrArchive = errors.New("craft: unsafe artifact archive")

func unsafeEntry(name string) error {
	if name == "" || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) {
		return fmt.Errorf("%w: entry %q has an absolute, empty or backslash path", ErrArchive, name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("%w: entry %q escapes the site root", ErrArchive, name)
		}
	}
	if path.Clean(name) == ".." {
		return fmt.Errorf("%w: entry %q escapes the site root", ErrArchive, name)
	}
	return nil
}

// scanArchive reads the whole archive before any extraction and refuses
// anything but regular files and directories with safe relative paths,
// and a total expanded size above max. Sizes are counted from the bytes
// actually decompressed, not from headers.
func scanArchive(file, name string, max int64) error {
	if max <= 0 {
		max = DefaultMaxExpanded
	}
	f, err := release.OpenRegularFile(file, release.MaxArtifactBytes)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrArchive, err)
	}
	defer f.Close()
	switch {
	case strings.HasSuffix(name, ".zip"):
		info, err := f.Stat()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrArchive, err)
		}
		zr, err := zip.NewReader(f, info.Size())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrArchive, err)
		}
		if len(zr.File) > maxArchiveEntries {
			return fmt.Errorf("%w: more than %d entries", ErrArchive, maxArchiveEntries)
		}
		var total int64
		for _, zf := range zr.File {
			if err := unsafeEntry(zf.Name); err != nil {
				return err
			}
			mode := zf.Mode()
			if !(mode.IsRegular() || mode.IsDir()) {
				return fmt.Errorf("%w: entry %q is not a regular file or directory", ErrArchive, zf.Name)
			}
			if mode.IsDir() {
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				return fmt.Errorf("%w: %v", ErrArchive, err)
			}
			n, err := io.Copy(io.Discard, io.LimitReader(rc, max-total+1))
			rc.Close()
			if err != nil {
				return fmt.Errorf("%w: %v", ErrArchive, err)
			}
			total += n
			if total > max {
				return fmt.Errorf("%w: expands beyond %d bytes", ErrArchive, max)
			}
		}
		return nil
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		gz, err := gzip.NewReader(bufio.NewReader(f))
		if err != nil {
			return fmt.Errorf("%w: %v", ErrArchive, err)
		}
		// Bound the decompressed stream itself, headers included.
		limited := &io.LimitedReader{R: gz, N: max + 10<<20}
		tr := tar.NewReader(limited)
		var total int64
		entries := 0
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				if limited.N <= 0 {
					return fmt.Errorf("%w: expands beyond %d bytes", ErrArchive, max)
				}
				return fmt.Errorf("%w: %v", ErrArchive, err)
			}
			entries++
			if entries > maxArchiveEntries {
				return fmt.Errorf("%w: more than %d entries", ErrArchive, maxArchiveEntries)
			}
			switch hdr.Typeflag {
			case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
			case tar.TypeXGlobalHeader:
				continue
			default:
				return fmt.Errorf("%w: entry %q is a link, device or other special file", ErrArchive, hdr.Name)
			}
			if err := unsafeEntry(hdr.Name); err != nil {
				return err
			}
			n, err := io.Copy(io.Discard, io.LimitReader(tr, max-total+1))
			if err != nil {
				return fmt.Errorf("%w: %v", ErrArchive, err)
			}
			total += n
			if total > max {
				return fmt.Errorf("%w: expands beyond %d bytes", ErrArchive, max)
			}
		}
		return nil
	}
	return fmt.Errorf("%w: unsupported archive type %q", ErrArchive, name)
}

var (
	fromLine  = regexp.MustCompile(`(?i)^\s*FROM\s+(?:--\S+\s+)*(\S+)(?:\s+AS\s+(\S+))?\s*$`)
	pinnedRef = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
	argLine   = regexp.MustCompile(`(?i)^\s*ARG\s+([A-Za-z_][A-Za-z0-9_]*)(?:=(.*))?\s*$`)
)

// checkDockerfilePinned refuses a Dockerfile with any FROM whose base is
// not pinned by @sha256: digest. Global ARG defaults are substituted (the
// pipeline passes no build argument that can override them); references
// to earlier stages and `scratch` need no pin.
func checkDockerfilePinned(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("cannot read the Dockerfile: %v", err)
	}
	defer f.Close()
	args := map[string]string{}
	stages := map[string]bool{}
	seenFrom := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if m := argLine.FindStringSubmatch(line); m != nil && !seenFrom {
			args[m[1]] = strings.Trim(strings.TrimSpace(m[2]), `"'`)
			continue
		}
		m := fromLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		seenFrom = true
		ref := os.Expand(m[1], func(k string) string { return args[k] })
		switch {
		case strings.EqualFold(ref, "scratch"), stages[strings.ToLower(ref)] && !strings.Contains(ref, "@"):
			// no pull happens
		case !pinnedRef.MatchString(ref):
			return fmt.Errorf("Dockerfile base %q is not pinned by @sha256: digest", m[1])
		}
		if m[2] != "" {
			stages[strings.ToLower(m[2])] = true
		}
	}
	return sc.Err()
}

// ensureStaging creates the staging directory, or hardens an existing one:
// it must be a real directory (not a symlink) owned by this process, and
// its mode is forced to 0700.
func ensureStaging(dir string) error {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(dir)
		if err != nil {
			return err
		}
	case err != nil:
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is not a plain directory (symlinks are refused)", dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s is owned by uid %d, not by the daemon", dir, st.Uid)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}
