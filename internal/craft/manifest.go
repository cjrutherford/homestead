// Package craft is the verify-then-build pipeline for craft container
// images (S279, ADR-028). The appliance never pulls a craft image: it
// verifies a signed craft manifest, obtains and checks the upstream
// artifact, checks the build-context digest, and only then builds the
// image locally and records its local image ID. Authenticity failures
// (signature, artifact digest, context digest) have no override; only
// the ADR-025 counter rollback check does.
package craft

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"homestead/internal/release"
)

// ManifestFormat is the mandatory `format` value.
const ManifestFormat = "homestead-craft-manifest/1"

// Manifest limits.
const (
	maxApps         = 16
	maxMirrors      = 4
	maxURLLength    = 2048
	maxContextFiles = 1000
	maxContextBytes = 16 << 20
)

var (
	appNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	versionPattern  = regexp.MustCompile(`^\d+\.\d+(\.\d+)?$`)
	fileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ErrManifest marks a signed manifest that does not parse or validate.
var ErrManifest = errors.New("craft: invalid craft manifest")

// Artifact is one upstream release artifact: mirrors of the same file,
// its SHA-256 and exact size (both signed).
type Artifact struct {
	Name   string   `yaml:"name" json:"name"`
	URLs   []string `yaml:"urls" json:"urls"`
	SHA256 string   `yaml:"sha256" json:"sha256"`
	Size   int64    `yaml:"size" json:"size"`
}

// App is one craft app release.
type App struct {
	Name     string   `yaml:"name" json:"name"`
	Version  string   `yaml:"version" json:"version"`
	Artifact Artifact `yaml:"artifact" json:"artifact"`
}

// Manifest is the signed craft release manifest. Counter follows ADR-025
// (positive, incremented per release); the baseline is kept per app.
// ContextDigest is the TreeDigest of the Dockerfile and build context.
type Manifest struct {
	Format        string `yaml:"format" json:"format"`
	Counter       int64  `yaml:"counter" json:"counter"`
	ContextDigest string `yaml:"context_digest" json:"context_digest"`
	Apps          []App  `yaml:"apps" json:"apps"`
}

// ParseManifest strictly parses and validates manifest bytes: unknown
// fields, duplicate keys, a second document and trailing content are
// refused.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("%w: only one YAML document is allowed", ErrManifest)
	}
	if err := m.validate(); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	return m, nil
}

func (m Manifest) validate() error {
	if m.Format != ManifestFormat {
		return fmt.Errorf("format must be %q", ManifestFormat)
	}
	if m.Counter < 1 {
		return errors.New("counter must be a positive integer")
	}
	if !digestPattern.MatchString(m.ContextDigest) {
		return errors.New("context_digest must be sha256:<64 hex>")
	}
	if len(m.Apps) == 0 || len(m.Apps) > maxApps {
		return fmt.Errorf("apps must list 1 to %d entries", maxApps)
	}
	seen := map[string]bool{}
	for _, app := range m.Apps {
		if !appNamePattern.MatchString(app.Name) {
			return fmt.Errorf("invalid app name %q", app.Name)
		}
		if seen[app.Name] {
			return fmt.Errorf("duplicate app %q", app.Name)
		}
		seen[app.Name] = true
		if !versionPattern.MatchString(app.Version) {
			return fmt.Errorf("app %q: version %q must look like 1.2 or 1.2.3", app.Name, app.Version)
		}
		a := app.Artifact
		if !fileNamePattern.MatchString(a.Name) || !(strings.HasSuffix(a.Name, ".tar.gz") || strings.HasSuffix(a.Name, ".tgz") || strings.HasSuffix(a.Name, ".zip")) {
			return fmt.Errorf("app %q: artifact name %q must be a bare .tar.gz, .tgz or .zip file name", app.Name, a.Name)
		}
		if !sha256Pattern.MatchString(a.SHA256) {
			return fmt.Errorf("app %q: artifact sha256 must be 64 lower-case hex", app.Name)
		}
		if a.Size < 1 || a.Size > release.MaxArtifactBytes {
			return fmt.Errorf("app %q: artifact size must be between 1 and %d bytes", app.Name, release.MaxArtifactBytes)
		}
		if len(a.URLs) > maxMirrors {
			return fmt.Errorf("app %q: at most %d artifact urls", app.Name, maxMirrors)
		}
		for _, raw := range a.URLs {
			u, err := url.Parse(raw)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(raw) > maxURLLength {
				return fmt.Errorf("app %q: artifact url %q must be an https URL without credentials", app.Name, raw)
			}
		}
	}
	return nil
}

// App returns one app release.
func (m Manifest) App(name string) (App, bool) {
	for _, a := range m.Apps {
		if a.Name == name {
			return a, true
		}
	}
	return App{}, false
}

// Tag is the local image tag for an app version. It is never
// registry-qualified.
func Tag(app, version string) string { return "homestead/craft-" + app + ":" + version }

// BridgeTag is the local image tag of an Everything bridge app (ADR-057).
func BridgeTag(app, version string) string { return "homestead/everything-" + app + ":" + version }

// IsBridgeRef reports whether image is an Everything bridge image reference.
func IsBridgeRef(image string) bool {
	_, _, ok := ParseRef(image)
	return ok && strings.HasPrefix(image, "homestead/everything-")
}

// ParseRef recognizes a local craft image reference
// (`homestead/craft-<app>:<version>`, or an Everything bridge image
// `homestead/everything-<app>:<version>`), the extension point catalog
// manifests use to name a locally built image (S249).
func ParseRef(image string) (app, version string, ok bool) {
	rest, found := strings.CutPrefix(image, "homestead/craft-")
	if !found {
		rest, found = strings.CutPrefix(image, "homestead/everything-")
	}
	if !found {
		return "", "", false
	}
	app, version, found = strings.Cut(rest, ":")
	if !found || !appNamePattern.MatchString(app) || !versionPattern.MatchString(version) {
		return "", "", false
	}
	return app, version, true
}

// ReservedContextEntry is the build-context path the pipeline fills with
// the verified artifact; a context directory must not contain it.
const ReservedContextEntry = "artifact"

// ErrContext marks a build context that cannot be digested or copied
// (symlink, special file, reserved name, over limits).
var ErrContext = errors.New("craft: build context not acceptable")

// contextFile is one regular file of a build context.
type contextFile struct {
	rel, abs string
}

// listContext walks dir and returns its regular files sorted bytewise by
// relative path. Anything that is not a regular file or directory is
// refused, as is a reserved top-level name or an unprintable path.
func listContext(dir string) ([]contextFile, error) {
	var files []contextFile
	var total int64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ReservedContextEntry {
			return fmt.Errorf("%w: %q is reserved for the verified artifact", ErrContext, rel)
		}
		if strings.ContainsAny(rel, "\n\r\x00") || strings.HasPrefix(rel, "-") {
			return fmt.Errorf("%w: unacceptable path", ErrContext)
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %q is not a regular file", ErrContext, rel)
		}
		total += info.Size()
		if len(files) >= maxContextFiles || total > maxContextBytes {
			return fmt.Errorf("%w: more than %d files or %d bytes", ErrContext, maxContextFiles, maxContextBytes)
		}
		files = append(files, contextFile{rel: rel, abs: path})
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrContext) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrContext, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, nil
}

// TreeDigest is the build-context digest: SHA-256 over the text
// `<file sha256>  <relative path>\n` (sha256sum layout, `/` separators)
// for every regular file, sorted bytewise by path. File modes, empty
// directories and timestamps are not part of it. scripts/craft-manifest.sh
// computes the same value with find and sha256sum.
func TreeDigest(dir string) (string, error) {
	files, err := listContext(dir)
	if err != nil {
		return "", err
	}
	return digestFiles(files)
}

func digestFiles(files []contextFile) (string, error) {
	h := sha256.New()
	for _, f := range files {
		sum, err := fileDigest(f.abs)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrContext, err)
		}
		fmt.Fprintf(h, "%s  %s\n", sum, f.rel)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func fileDigest(path string) (string, error) {
	f, err := release.OpenRegularFile(path, maxContextBytes)
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

// copyContext copies the regular files of src into dst (created 0700) so
// the digest is taken of, and the build runs from, bytes no one can
// change afterwards.
func copyContext(src, dst string) error {
	files, err := listContext(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, f := range files {
		target := filepath.Join(dst, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		in, err := release.OpenRegularFile(f.abs, maxContextBytes)
		if err != nil {
			return fmt.Errorf("%w: %q unreadable", ErrContext, f.rel)
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, io.LimitReader(in, maxContextBytes))
		in.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
