package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"filippo.io/age"

	"homestead/internal/secret"
)

// Backup encryption (S259, ADR-022).
//
// Archive shape (format version 2): every file source becomes ONE
// age-encrypted tar stream, <run>/<entry>.tar.age, and every database
// export becomes one age-encrypted file, <run>/<name>.age. Streams are
// tar | age | file, so no plaintext of the data ever lands on disk. The
// manifest (backup.json) stays plaintext JSON with non-secret metadata
// only: package, timestamp, entry names, file counts, the entry-to-
// archive map, and format flags. The age CLI decrypts any artifact
// without Homestead: `age -d -i identity.txt X.tar.age | tar -x`.

const (
	// FormatPlaintext is the original layout: plain trees beside the
	// manifest. Manifests without a version are this format.
	FormatPlaintext = 1
	// FormatAge is the encrypted layout described above.
	FormatAge = 2
	// CurrentFormat is the format new runs are written in when they are
	// encrypted.
	CurrentFormat = FormatAge

	// ArchiveSuffix marks an encrypted tar stream; FileSuffix marks any
	// encrypted artifact.
	ArchiveSuffix = ".tar.age"
	FileSuffix    = ".age"

	// IdentitySecret is the sys_-reserved secret holding the appliance
	// backup identity (ADR-021/022). Neither owner routes nor the catalog
	// can write that namespace. It is generated on first use and is
	// never logged, listed with its value, or returned by any route.
	IdentitySecret = secret.BackupIdentityName

	// SecretsClass names the sensitive source class carrying a stack's
	// materialized secret files (S266); such sources only run encrypted.
	SecretsClass = "stack-secrets"
)

// KeyStore is the slice of the secret store the backup identity needs.
type KeyStore interface {
	Get(name string) (string, error)
	SetSystem(name, value string) error
}

var identityMu sync.Mutex

// ApplianceIdentity returns the appliance backup identity, generating
// and storing it on first use.
func ApplianceIdentity(store KeyStore) (*age.X25519Identity, error) {
	if store == nil {
		return nil, errors.New("backup: encryption needs the secret store")
	}
	identityMu.Lock()
	defer identityMu.Unlock()
	value, err := store.Get(IdentitySecret)
	switch {
	case err == nil:
		identity, perr := age.ParseX25519Identity(strings.TrimSpace(value))
		if perr != nil {
			// Never echo the stored value.
			return nil, errors.New("backup: stored backup identity is unreadable")
		}
		return identity, nil
	case errors.Is(err, secret.ErrNotFound):
		identity, gerr := age.GenerateX25519Identity()
		if gerr != nil {
			return nil, fmt.Errorf("backup: generate identity: %w", gerr)
		}
		if serr := store.SetSystem(IdentitySecret, identity.String()); serr != nil {
			return nil, fmt.Errorf("backup: store identity: %w", serr)
		}
		return identity, nil
	default:
		return nil, fmt.Errorf("backup: read identity: %w", err)
	}
}

// PeekApplianceIdentity reads the appliance identity without creating
// it: read-only routes must not generate key material as a side effect.
func PeekApplianceIdentity(store KeyStore) (*age.X25519Identity, bool, error) {
	if store == nil {
		return nil, false, errors.New("backup: encryption needs the secret store")
	}
	value, err := store.Get(IdentitySecret)
	switch {
	case err == nil:
		identity, perr := age.ParseX25519Identity(strings.TrimSpace(value))
		if perr != nil {
			return nil, false, errors.New("backup: stored backup identity is unreadable")
		}
		return identity, true, nil
	case errors.Is(err, secret.ErrNotFound):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("backup: read identity: %w", err)
	}
}

// Recipients returns the recipients new backups encrypt to: the
// appliance identity only; RecipientsWithOwner (S271) appends owners.
func Recipients(store KeyStore) ([]age.Recipient, error) {
	identity, err := ApplianceIdentity(store)
	if err != nil {
		return nil, err
	}
	return []age.Recipient{identity.Recipient()}, nil
}

// Identities returns the identities restore decrypts with.
func Identities(store KeyStore) ([]age.Identity, error) {
	identity, err := ApplianceIdentity(store)
	if err != nil {
		return nil, err
	}
	return []age.Identity{identity}, nil
}

// createEncrypted opens dst (0600, exclusive) and returns a writer
// whose Close finishes the age stream and the file. Callers remove the
// file on failure.
func createEncrypted(dst string, recipients []age.Recipient) (io.Writer, func() error, error) {
	if len(recipients) == 0 {
		return nil, nil, errors.New("backup: no recipients")
	}
	file, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nil, err
	}
	enc, err := age.Encrypt(file, recipients...)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(dst)
		return nil, nil, err
	}
	closeAll := func() error {
		if err := enc.Close(); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	}
	return enc, closeAll, nil
}

// encryptBytes writes data as one encrypted file.
func encryptBytes(dst string, data []byte, recipients []age.Recipient) error {
	w, finish, err := createEncrypted(dst, recipients)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		_ = finish()
		_ = os.Remove(dst)
		return err
	}
	if err := finish(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// encryptFile streams an existing file into one encrypted file.
func encryptFile(src, dst string, recipients []age.Recipient) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	w, finish, err := createEncrypted(dst, recipients)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, in); err != nil {
		_ = finish()
		_ = os.Remove(dst)
		return err
	}
	if err := finish(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// archiveTree writes src as an encrypted tar stream at dst, applying
// the same rules as copyTree: modes, ownership, in-tree relative
// symlinks only (never followed), nothing but dirs, files, and links.
func archiveTree(src, dst string, recipients []age.Recipient) (int, error) {
	files, _, err := archiveTreeOpts(src, dst, recipients, nil)
	return files, err
}

func archiveTreeOpts(src, dst string, recipients []age.Recipient, exclude []string) (int, treeStats, error) {
	var stats treeStats
	info, err := os.Stat(src)
	if err != nil {
		return 0, stats, fmt.Errorf("backup: source %q: %w", src, err)
	}
	if !info.IsDir() {
		return 0, stats, fmt.Errorf("backup: source %q is not a directory", src)
	}
	w, finish, err := createEncrypted(dst, recipients)
	if err != nil {
		return 0, stats, fmt.Errorf("backup: archive: %w", err)
	}
	files, stats, err := writeTar(w, src, exclude)
	if err != nil {
		_ = finish()
		_ = os.Remove(dst)
		return 0, stats, err
	}
	if err := finish(); err != nil {
		_ = os.Remove(dst)
		return 0, stats, fmt.Errorf("backup: archive: %w", err)
	}
	return files, stats, nil
}

func writeTar(w io.Writer, src string, exclude []string) (int, treeStats, error) {
	tw := tar.NewWriter(w)
	files := 0
	var stats treeStats
	skip := excludeSet(exclude)
	vanished := func() error { stats.Vanished++; return nil }
	walk := func(path string, entry fs.DirEntry, err error) error {
		if walkHook != nil {
			walkHook(path)
		}
		if err != nil {
			if path != src && errors.Is(err, fs.ErrNotExist) {
				return vanished()
			}
			return err
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return vanished()
		}
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.Format = tar.FormatPAX
		header.Uname, header.Gname = "", ""
		header.Mode = int64(info.Mode().Perm())
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			header.Uid, header.Gid = int(stat.Uid), int(stat.Gid)
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			link, err := symlinkTarget(src, path)
			if errors.Is(err, fs.ErrNotExist) {
				return vanished()
			}
			if err != nil {
				return err
			}
			header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, link, 0
			files++
		case entry.IsDir():
			header.Typeflag, header.Size = tar.TypeDir, 0
			header.Name += "/"
		case info.Mode().IsRegular():
			header.Typeflag = tar.TypeReg
			files++
		default:
			return fmt.Errorf("backup: %q is not a regular file", path)
		}
		var in *os.File
		if header.Typeflag == tar.TypeReg {
			// Open before the header is written: a file that vanished
			// after listing must leave no half entry in the stream.
			in, err = os.Open(path)
			if errors.Is(err, fs.ErrNotExist) {
				files--
				return vanished()
			}
			if err != nil {
				return err
			}
			defer in.Close()
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg {
			if header.Typeflag == tar.TypeDir && skip[filepath.ToSlash(relative)] {
				return fs.SkipDir
			}
			return nil
		}
		if n, err := io.CopyN(tw, in, header.Size); err != nil {
			return fmt.Errorf("backup: %q changed while copying (%d of %d bytes): %w", path, n, header.Size, err)
		}
		return nil
	}
	if err := filepath.WalkDir(src, walk); err != nil {
		return 0, stats, err
	}
	if err := tw.Close(); err != nil {
		return 0, stats, err
	}
	return files, stats, nil
}

// symlinkTarget validates a link like copySymlink and returns it.
func symlinkTarget(root, path string) (string, error) {
	link, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(filepath.Dir(path), link)
	inside, err := filepath.Rel(root, resolved)
	if filepath.IsAbs(link) || err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("backup: symlink at %q refused", path)
	}
	return link, nil
}

// isEncryptedFile reports whether path is an encrypted artifact.
func isEncryptedFile(path string) bool {
	if !strings.HasSuffix(path, FileSuffix) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func openDecrypted(path string, identities []age.Identity) (io.Reader, func() error, error) {
	if len(identities) == 0 {
		return nil, nil, fmt.Errorf("backup: %q is encrypted and no identity is available", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	r, err := age.Decrypt(file, identities...)
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("backup: decrypt %q: %w", path, err)
	}
	return r, file.Close, nil
}

// restoreEncrypted replaces target with one encrypted artifact, safely:
// the stream is extracted into a hidden sibling staging directory on the
// same filesystem and drained to EOF (so truncation or corruption
// anywhere in the stream fails here), and only then is the live target
// renamed aside, the staged payload renamed into place, and the old tree
// removed. Any failure restores the original and leaves the target
// untouched. Tar streams become trees, anything else a single 0600 file;
// sensitive entries get files forced to 0600 and must keep recorded
// ownership.
func restoreEncrypted(archive, target string, identities []age.Identity, sensitive bool) error {
	r, closeFn, err := openDecrypted(archive, identities)
	if err != nil {
		return err
	}
	defer closeFn()
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, "."+filepath.Base(target)+".restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	payload := filepath.Join(staging, "payload")
	if strings.HasSuffix(archive, ArchiveSuffix) {
		err = extractTar(r, payload, sensitive)
	} else {
		err = extractFile(r, payload)
	}
	if err != nil {
		return err
	}
	aside := filepath.Join(staging, "previous")
	moved := false
	if _, err := os.Lstat(target); err == nil {
		if err := os.Rename(target, aside); err != nil {
			return fmt.Errorf("backup: move aside %q: %w", target, err)
		}
		moved = true
	}
	if err := os.Rename(payload, target); err != nil {
		if moved {
			_ = os.Rename(aside, target)
		}
		return fmt.Errorf("backup: install %q: %w", target, err)
	}
	return nil
}

// extractFile writes the whole decrypted stream to dst (0600), draining
// to EOF so a truncated final chunk is an error.
func extractFile(r io.Reader, dst string) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

type dirMeta struct {
	name     string
	mode     os.FileMode
	uid, gid int
}

// warnf reports restore conditions that do not fail the run; the daemon
// log is the record. Replaced in tests.
var warnf = log.Printf

// extractTar unpacks into a new tree at target through an os.Root, so no
// in-tree symlink can redirect a create, chmod, or chown outside it.
func extractTar(r io.Reader, target string, sensitive bool) error {
	if err := os.Mkdir(target, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(r)
	var dirs []dirMeta
	chowner := &ownerSetter{root: root, sensitive: sensitive, target: target}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("backup: read archive: %w", err)
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("backup: archive entry %q escapes the tree", header.Name)
		}
		if name == "." && header.Typeflag != tar.TypeDir {
			return fmt.Errorf("backup: archive root entry must be a directory")
		}
		if err := refuseLinkedParents(root, name); err != nil {
			return err
		}
		mode := os.FileMode(header.Mode).Perm()
		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o700); err != nil {
				return err
			}
			dirs = append(dirs, dirMeta{name, mode, header.Uid, header.Gid})
		case tar.TypeSymlink:
			resolved := filepath.Join(filepath.Dir(name), header.Linkname)
			if filepath.IsAbs(header.Linkname) || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
				return fmt.Errorf("backup: archive symlink %q refused", header.Name)
			}
			if err := root.Symlink(header.Linkname, name); err != nil {
				return err
			}
			if err := chowner.set(name, header.Uid, header.Gid, true); err != nil {
				return err
			}
		case tar.TypeReg:
			if sensitive {
				mode &= 0o600
			}
			out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			if err := root.Chmod(name, mode); err != nil {
				return err
			}
			if err := chowner.set(name, header.Uid, header.Gid, false); err != nil {
				return err
			}
		default:
			return fmt.Errorf("backup: archive entry %q has unsupported type", header.Name)
		}
	}
	// Tar stops at its end marker; drain the age stream so truncation or
	// tampering in the final chunk is detected before anything installs.
	if _, err := io.Copy(io.Discard, r); err != nil {
		return fmt.Errorf("backup: read archive: %w", err)
	}
	// Directory modes and owners apply last, deepest first, so a
	// read-only directory never blocks its own contents.
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if err := root.Chmod(d.name, d.mode); err != nil {
			return err
		}
		if err := chowner.set(d.name, d.uid, d.gid, false); err != nil {
			return err
		}
	}
	return nil
}

// refuseLinkedParents rejects entries whose parent path passes through a
// symlink or non-directory (belt and braces beside os.Root).
func refuseLinkedParents(root *os.Root, name string) error {
	parent := filepath.Dir(name)
	if parent == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(parent, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("backup: archive entry %q passes through a link", name)
		}
	}
	return nil
}

// ownerSetter applies recorded ownership. Only root can chown: for
// sensitive entries a mismatch is an error (secret files must keep the
// owner their container needs), otherwise one warning per restore.
type ownerSetter struct {
	root      *os.Root
	sensitive bool
	target    string
	warned    bool
}

func (o *ownerSetter) set(name string, uid, gid int, _ bool) error {
	if os.Geteuid() == 0 {
		if err := o.root.Lchown(name, uid, gid); err != nil {
			return fmt.Errorf("backup: chown %q: %w", name, err)
		}
		return nil
	}
	if uid == os.Getuid() && gid == os.Getgid() {
		return nil
	}
	if o.sensitive {
		return fmt.Errorf("backup: restoring %q keeps recorded ownership %d:%d, which needs root", o.target, uid, gid)
	}
	if !o.warned {
		o.warned = true
		warnf("backup: restore of %s did not apply recorded ownership (not running as root)", o.target)
	}
	return nil
}

// archiveSignature maps relative non-directory paths to sizes from an
// encrypted artifact, the counterpart of treeSignature for verify.
func archiveSignature(archive string, identities []age.Identity) (map[string]int64, error) {
	r, closeFn, err := openDecrypted(archive, identities)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	if !strings.HasSuffix(archive, ArchiveSuffix) {
		n, err := io.Copy(io.Discard, r)
		if err != nil {
			return nil, err
		}
		return map[string]int64{".": n}, nil
	}
	signature := map[string]int64{}
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			if _, err := io.Copy(io.Discard, r); err != nil {
				return nil, err
			}
			return signature, nil
		}
		if err != nil {
			return nil, err
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		switch header.Typeflag {
		case tar.TypeDir:
		case tar.TypeSymlink:
			signature[name] = int64(len(header.Linkname))
		default:
			signature[name] = header.Size
		}
	}
}
