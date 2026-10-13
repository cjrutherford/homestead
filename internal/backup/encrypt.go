package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

	// IdentitySecret is the app_-reserved secret holding the appliance
	// backup identity (ADR-021/022). It is generated on first use and is
	// never logged, listed with its value, or returned by any route.
	IdentitySecret = secret.AppSecretPrefix + "backup_identity"

	// SecretsClass names the sensitive source class carrying a stack's
	// materialized secret files (S266); such sources only run encrypted.
	SecretsClass = "stack-secrets"
)

// KeyStore is the slice of the secret store the backup identity needs.
type KeyStore interface {
	Get(name string) (string, error)
	SetApp(name, value string) error
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
		if serr := store.SetApp(IdentitySecret, identity.String()); serr != nil {
			return nil, fmt.Errorf("backup: store identity: %w", serr)
		}
		return identity, nil
	default:
		return nil, fmt.Errorf("backup: read identity: %w", err)
	}
}

// Recipients returns the recipients new backups encrypt to: the
// appliance identity's. S271 appends owner-added recipients here.
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
	info, err := os.Stat(src)
	if err != nil {
		return 0, fmt.Errorf("backup: source %q: %w", src, err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("backup: source %q is not a directory", src)
	}
	w, finish, err := createEncrypted(dst, recipients)
	if err != nil {
		return 0, fmt.Errorf("backup: archive: %w", err)
	}
	files, err := writeTar(w, src)
	if err != nil {
		_ = finish()
		_ = os.Remove(dst)
		return 0, err
	}
	if err := finish(); err != nil {
		_ = os.Remove(dst)
		return 0, fmt.Errorf("backup: archive: %w", err)
	}
	return files, nil
}

func writeTar(w io.Writer, src string) (int, error) {
	tw := tar.NewWriter(w)
	files := 0
	walk := func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
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
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if n, err := io.CopyN(tw, in, header.Size); err != nil {
			return fmt.Errorf("backup: %q changed while copying (%d of %d bytes): %w", path, n, header.Size, err)
		}
		return nil
	}
	if err := filepath.WalkDir(src, walk); err != nil {
		return 0, err
	}
	if err := tw.Close(); err != nil {
		return 0, err
	}
	return files, nil
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

// restoreEncrypted extracts one encrypted artifact onto target: tar
// streams become trees, anything else a single 0600 file. Mirrors
// restoreEntry's contract; sensitive entries get files forced to 0600.
func restoreEncrypted(archive, target string, identities []age.Identity, sensitive bool) error {
	r, closeFn, err := openDecrypted(archive, identities)
	if err != nil {
		return err
	}
	defer closeFn()
	if !strings.HasSuffix(archive, ArchiveSuffix) {
		return extractFile(r, target)
	}
	return extractTar(r, target, sensitive)
}

func extractFile(r io.Reader, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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
	path     string
	mode     os.FileMode
	uid, gid int
}

func extractTar(r io.Reader, target string, sensitive bool) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	tr := tar.NewReader(r)
	var dirs []dirMeta
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
		dst := filepath.Join(target, name)
		if name == "." && header.Typeflag != tar.TypeDir {
			return fmt.Errorf("backup: archive root entry must be a directory")
		}
		if err := refuseLinkedParents(target, dst); err != nil {
			return err
		}
		mode := os.FileMode(header.Mode).Perm()
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o700); err != nil {
				return err
			}
			dirs = append(dirs, dirMeta{dst, mode, header.Uid, header.Gid})
		case tar.TypeSymlink:
			resolved := filepath.Join(filepath.Dir(dst), header.Linkname)
			inside, err := filepath.Rel(target, resolved)
			if filepath.IsAbs(header.Linkname) || err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
				return fmt.Errorf("backup: archive symlink %q refused", header.Name)
			}
			if err := os.Symlink(header.Linkname, dst); err != nil {
				return err
			}
			if err := chownIDs(dst, header.Uid, header.Gid); err != nil {
				return err
			}
		case tar.TypeReg:
			if sensitive {
				mode &= 0o600
			}
			out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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
			if err := os.Chmod(dst, mode); err != nil {
				return err
			}
			if err := chownIDs(dst, header.Uid, header.Gid); err != nil {
				return err
			}
		default:
			return fmt.Errorf("backup: archive entry %q has unsupported type", header.Name)
		}
	}
	// Directory modes and owners apply last, deepest first, so a
	// read-only directory never blocks its own contents.
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if err := os.Chmod(d.path, d.mode); err != nil {
			return err
		}
		if err := chownIDs(d.path, d.uid, d.gid); err != nil {
			return err
		}
	}
	return nil
}

// refuseLinkedParents rejects extraction through a symlinked ancestor:
// every parent between the tree root and dst must be a real directory.
func refuseLinkedParents(root, dst string) error {
	if dst == root {
		return nil
	}
	parent := filepath.Dir(dst)
	relative, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	if relative == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("backup: archive entry %q passes through a link", dst)
		}
	}
	return nil
}

func chownIDs(path string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if err := os.Lchown(path, uid, gid); err != nil {
		return fmt.Errorf("backup: chown %q: %w", path, err)
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

// checkDecryptable reads an artifact's age header with the identities,
// proving they can open it before anything live is replaced.
func checkDecryptable(archive string, identities []age.Identity) error {
	_, closeFn, err := openDecrypted(archive, identities)
	if err != nil {
		return err
	}
	return closeFn()
}
