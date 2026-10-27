package docker

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Issued certificates (S278) live beside the route snippets, inside the
// directory Caddy already mounts read-only, so no Compose change is
// needed on installed edges:
//
//	<stack>/routes/certs/<name>/fullchain.pem  (0644, public)
//	<stack>/routes/certs/<name>/privkey.pem    (0600)
//	<stack>/routes/certs/<name>/meta.json      (names covered, public)
//
// Route reconciliation removes only *.caddy files, so this subtree
// survives it.
const (
	certsDirName   = "certs"
	certChainFile  = "fullchain.pem"
	certKeyFile    = "privkey.pem"
	certMetaFile   = "meta.json"
	caddyRoutesDir = "/etc/caddy/routes"
)

var validCertName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// CertDir is where a named certificate is placed for the edge.
func CertDir(stackDir, name string) string {
	return filepath.Join(stackDir, "routes", certsDirName, name)
}

// CertPaths returns the host-side chain and key paths of a placement.
func CertPaths(stackDir, name string) (chain, key string) {
	dir := CertDir(stackDir, name)
	return filepath.Join(dir, certChainFile), filepath.Join(dir, certKeyFile)
}

// PlacedCert describes one placed certificate: its name and the DNS
// names it covers. Key material is never part of this value.
type PlacedCert struct {
	Name  string   `json:"name"`
	Names []string `json:"names"`
}

// Covers reports whether the certificate serves host: an exact name, or
// a single-label wildcard (`*.example.com` covers `app.example.com`,
// never `example.com` or `a.b.example.com`).
func (p PlacedCert) Covers(host Hostname) bool {
	return certNamesCover(p.Names, string(host))
}

func certNamesCover(names []string, host string) bool {
	return exactCover(names, host) || wildcardCover(names, host)
}

func exactCover(names []string, host string) bool {
	for _, name := range names {
		if name == host {
			return true
		}
	}
	return false
}

func wildcardCover(names []string, host string) bool {
	dot := strings.Index(host, ".")
	if dot < 1 {
		return false
	}
	want := "*" + host[dot:]
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// ValidateCertPair proves chain and key belong together and that the
// leaf covers every name: tls.X509KeyPair rejects a key that does not
// match the leaf, and each approved name must appear literally in the
// leaf's SANs (wildcards compare literally). It checks no trust chain.
func ValidateCertPair(names []string, chainPEM, keyPEM []byte) (*x509.Certificate, error) {
	pair, err := tls.X509KeyPair(chainPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("certs: chain and key do not form a pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("certs: leaf unreadable: %w", err)
	}
	have := map[string]bool{}
	for _, name := range leaf.DNSNames {
		have[strings.ToLower(name)] = true
	}
	for _, name := range names {
		if !have[name] {
			return nil, fmt.Errorf("certs: the certificate does not cover %q", name)
		}
	}
	return leaf, nil
}

// regularFile reads a file only when it is a regular file (never a
// symlink), so a planted link cannot redirect key or chain reads.
func regularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("certs: %s is not a regular file", path)
	}
	return os.ReadFile(path)
}

// plainDir reports an existing real directory (not a symlink).
func plainDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("certs: %s is not a directory", path)
	}
	return nil
}

// PlaceCert validates then places a certificate for the edge. Validation
// comes first: the chain and key must pair, the leaf must cover the
// approved names and not be expired. Then key (0600), chain (0644) and
// metadata are written into a fresh sibling directory and swapped in
// together by renaming, so a failure never leaves a new key beside an old
// chain; the previous placement is restored if the swap fails. Symlinked
// placement directories are refused.
func PlaceCert(stackDir string, cert PlacedCert, chainPEM, keyPEM []byte) error {
	if !validCertName.MatchString(cert.Name) {
		return fmt.Errorf("certs: invalid certificate name %q", cert.Name)
	}
	if len(cert.Names) == 0 || len(chainPEM) == 0 || len(keyPEM) == 0 {
		return fmt.Errorf("certs: names, chain and key are all required")
	}
	leaf, err := ValidateCertPair(cert.Names, chainPEM, keyPEM)
	if err != nil {
		return err
	}
	if !time.Now().Before(leaf.NotAfter) {
		return fmt.Errorf("certs: the certificate is already expired")
	}
	parent := filepath.Join(stackDir, "routes", certsDirName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("certs: dir: %w", err)
	}
	if err := plainDir(parent); err != nil {
		return err
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return fmt.Errorf("certs: dir mode: %w", err)
	}
	final := filepath.Join(parent, cert.Name)
	if info, err := os.Lstat(final); err == nil && !info.IsDir() {
		return fmt.Errorf("certs: %s is not a directory", final)
	}
	meta, err := json.Marshal(PlacedCert{Name: cert.Name, Names: cert.Names})
	if err != nil {
		return fmt.Errorf("certs: meta: %w", err)
	}
	staged, err := os.MkdirTemp(parent, tempPrefix+cert.Name+"~")
	if err != nil {
		return fmt.Errorf("certs: stage: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staged)
		}
	}()
	for _, f := range []struct {
		name string
		body []byte
		mode os.FileMode
	}{
		{certKeyFile, keyPEM, 0o600},
		{certChainFile, chainPEM, 0o644},
		{certMetaFile, meta, 0o644},
	} {
		if err := writeSynced(filepath.Join(staged, f.name), f.body, f.mode); err != nil {
			return err
		}
	}
	var old string
	if _, err := os.Lstat(final); err == nil {
		old, err = os.MkdirTemp(parent, oldPrefix+cert.Name+"~")
		if err != nil {
			return fmt.Errorf("certs: swap: %w", err)
		}
		_ = os.Remove(old)
		if err := os.Rename(final, old); err != nil {
			return fmt.Errorf("certs: swap out previous: %w", err)
		}
	}
	if err := os.Rename(staged, final); err != nil {
		if old != "" {
			if rbErr := os.Rename(old, final); rbErr != nil {
				return fmt.Errorf("certs: swap in: %w (restoring the previous placement also failed: %v)", err, rbErr)
			}
		}
		return fmt.Errorf("certs: swap in: %w", err)
	}
	committed = true
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}

const (
	tempPrefix = ".tmp-"
	oldPrefix  = ".old-"
)

func writeSynced(path string, body []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("certs: create %s: %w", filepath.Base(path), err)
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return fmt.Errorf("certs: write %s: %w", filepath.Base(path), err)
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return fmt.Errorf("certs: mode %s: %w", filepath.Base(path), err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("certs: sync %s: %w", filepath.Base(path), err)
	}
	return f.Close()
}

// CleanCertTemps runs at startup: it removes staging directories and
// stale temporary files left by an interrupted placement, and restores a
// previous placement whose swap was interrupted before the new one landed.
func CleanCertTemps(stackDir string) error {
	parent := filepath.Join(stackDir, "routes", certsDirName)
	entries, err := os.ReadDir(parent)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("certs: clean: %w", err)
	}
	for _, e := range entries {
		path := filepath.Join(parent, e.Name())
		switch {
		case strings.HasPrefix(e.Name(), tempPrefix):
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("certs: clean %s: %w", e.Name(), err)
			}
		case strings.HasPrefix(e.Name(), oldPrefix):
			name := strings.TrimPrefix(e.Name(), oldPrefix)
			if i := strings.LastIndex(name, "~"); i >= 0 {
				name = name[:i]
			}
			final := filepath.Join(parent, name)
			if _, err := os.Lstat(final); os.IsNotExist(err) && validCertName.MatchString(name) {
				if err := os.Rename(path, final); err != nil {
					return fmt.Errorf("certs: restore %s: %w", name, err)
				}
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("certs: clean %s: %w", e.Name(), err)
			}
		case e.IsDir() && validCertName.MatchString(e.Name()):
			inner, _ := os.ReadDir(path)
			for _, f := range inner {
				if strings.HasPrefix(f.Name(), tempPrefix) {
					_ = os.Remove(filepath.Join(path, f.Name()))
				}
			}
		}
	}
	return nil
}

// ReadPlacedChain returns the placed chain of name when the placement is
// a valid pair covering names; used to reuse an already issued
// certificate after a failed reload.
func ReadPlacedChain(stackDir, name string, names []string) ([]byte, bool) {
	dir := CertDir(stackDir, name)
	chain, err := regularFile(filepath.Join(dir, certChainFile))
	if err != nil {
		return nil, false
	}
	key, err := regularFile(filepath.Join(dir, certKeyFile))
	if err != nil {
		return nil, false
	}
	leaf, err := ValidateCertPair(names, chain, key)
	if err != nil || !time.Now().Before(leaf.NotAfter) {
		return nil, false
	}
	return chain, true
}

// ReadPlacedChainAny returns the placed chain of name without checking
// names or expiry (callers grade it themselves).
func ReadPlacedChainAny(stackDir, name string) ([]byte, error) {
	if !validCertName.MatchString(name) {
		return nil, fmt.Errorf("certs: invalid certificate name %q", name)
	}
	return regularFile(filepath.Join(CertDir(stackDir, name), certChainFile))
}

// PlacedCerts lists the certificates placed under stackDir (ordered by
// name). A missing directory is none. An entry that is a symlink, is not
// a regular key/chain/meta set, or whose key and chain do not pair and
// cover its recorded names is skipped, never fatal and never served, so
// one bad placement cannot stop unrelated route applies.
func PlacedCerts(stackDir string) []PlacedCert {
	parent := filepath.Join(stackDir, "routes", certsDirName)
	if plainDir(parent) != nil {
		return nil
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}
	var out []PlacedCert
	for _, entry := range entries {
		if !entry.IsDir() || !validCertName.MatchString(entry.Name()) {
			continue
		}
		dir := CertDir(stackDir, entry.Name())
		raw, err := regularFile(filepath.Join(dir, certMetaFile))
		if err != nil {
			continue
		}
		var placed PlacedCert
		if json.Unmarshal(raw, &placed) != nil || placed.Name != entry.Name() || len(placed.Names) == 0 {
			continue
		}
		chain, err := regularFile(filepath.Join(dir, certChainFile))
		if err != nil {
			continue
		}
		key, err := regularFile(filepath.Join(dir, certKeyFile))
		if err != nil {
			continue
		}
		if _, err := ValidateCertPair(placed.Names, chain, key); err != nil {
			continue
		}
		out = append(out, placed)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ServingCert picks the placed certificate for host: an exact-name
// match beats a wildcard match; ties go to the lowest name.
func ServingCert(stackDir string, host Hostname) (PlacedCert, bool) {
	var wild *PlacedCert
	placed := PlacedCerts(stackDir)
	for i := range placed {
		if exactCover(placed[i].Names, string(host)) {
			return placed[i], true
		}
		if wild == nil && wildcardCover(placed[i].Names, string(host)) {
			wild = &placed[i]
		}
	}
	if wild != nil {
		return *wild, true
	}
	return PlacedCert{}, false
}

// usesPlacedCert reports the TLS modes that serve a certificate placed
// under the stack: automatic TLS (S278) and, since S280, the internal
// mode, where a leaf from Homestead's own CA replaces Caddy's private
// CA when one covers the host.
func (r Route) usesPlacedCert() bool { return r.TLS == TLSAuto || r.TLS == TLSInternal }

// LocalCertPrefix names the placements issued by the internal CA
// (S280). Internal-mode routes serve only these: an owner-imported or
// ACME certificate that happens to cover the name never replaces
// Caddy's internal issuance, and a local leaf never serves an
// automatic-TLS (public-provider) route.
const LocalCertPrefix = "local-"

// LocalCertName is the placement name for a local-only host: sanitized
// for the directory-name rules, with a hash so distinct hosts never
// collide.
func LocalCertName(host Hostname) string {
	sum := sha256.Sum256([]byte(host))
	label := strings.NewReplacer(".", "-").Replace(string(host))
	if len(label) > 40 {
		label = label[:40]
	}
	return LocalCertPrefix + label + "-" + hex.EncodeToString(sum[:4])
}

// servingCertFor picks the placed certificate a route would serve: local
// placements for internal-mode routes, any other for automatic mode;
// an exact-name match beats a wildcard, ties go to the lowest name.
func servingCertFor(stackDir string, r Route) (PlacedCert, bool) {
	var wild *PlacedCert
	placed := PlacedCerts(stackDir)
	for i := range placed {
		if strings.HasPrefix(placed[i].Name, LocalCertPrefix) != (r.TLS == TLSInternal) {
			continue
		}
		if exactCover(placed[i].Names, string(r.Host)) {
			return placed[i], true
		}
		if wild == nil && wildcardCover(placed[i].Names, string(r.Host)) {
			wild = &placed[i]
		}
	}
	if wild != nil {
		return *wild, true
	}
	return PlacedCert{}, false
}

// RemoveCert deletes a placed certificate directory (S280: a local leaf
// whose route is gone). A missing placement is not an error.
func RemoveCert(stackDir, name string) error {
	if !validCertName.MatchString(name) {
		return fmt.Errorf("certs: invalid certificate name %q", name)
	}
	parent := filepath.Join(stackDir, "routes", certsDirName)
	if plainDir(parent) != nil {
		return nil
	}
	return os.RemoveAll(filepath.Join(parent, name))
}

// RouteServesCert reports whether the on-disk snippet of host loads the
// chain placed under certName, i.e. the edge is configured to present it.
func RouteServesCert(stackDir string, host Hostname, certName string) bool {
	body, err := os.ReadFile(filepath.Join(stackDir, "routes", RouteFilename(host)))
	if err != nil {
		return false
	}
	want := fmt.Sprintf("%s/%s/%s/%s", caddyRoutesDir, certsDirName, certName, certChainFile)
	return strings.Contains(string(body), "tls "+want+" ")
}

// RenderRouteIn renders a route as RenderRoute does, plus: a route
// using automatic TLS whose host is covered by a certificate placed
// under stackDir serves that certificate (`tls <chain> <key>`) instead
// of leaving issuance to Caddy. Other modes are rendered unchanged.
func RenderRouteIn(stackDir string, r Route) (string, error) {
	if r.usesPlacedCert() && stackDir != "" {
		if cert, ok := servingCertFor(stackDir, r); ok {
			return renderRoute(r, &cert)
		}
	}
	return renderRoute(r, nil)
}

// WouldServe reports whether a route using automatic TLS would serve a
// certificate covering names, before anything is placed (plan preview).
func WouldServe(names []string, r Route) bool {
	return r.usesPlacedCert() && certNamesCover(names, string(r.Host))
}

// RouteSnippetExists reports whether a route snippet for host is on
// disk. Certificate refresh only rewrites snippets that already exist,
// so issuing a certificate can never expose a route nobody applied.
func RouteSnippetExists(stackDir string, host Hostname) bool {
	info, err := os.Stat(filepath.Join(stackDir, "routes", RouteFilename(host)))
	return err == nil && info.Mode().IsRegular()
}

// RefreshCertRoutes re-renders the existing snippets of the named hosts
// (the approved served routes) now covered by a placed certificate, so
// they serve it. Routes outside hosts, without a snippet, with other TLS
// modes, or without a covering certificate are untouched. Each rewrite is
// a temp file plus rename; the returned undo restores every rewritten
// snippet with its original mode. It never reloads.
func RefreshCertRoutes(stackDir string, routes []Route, hosts []string) (undo func() error, err error) {
	type saved struct {
		path    string
		content []byte
		mode    os.FileMode
	}
	var log []saved
	undo = func() error {
		var first error
		for i := len(log) - 1; i >= 0; i-- {
			if err := writeAtomic(log[i].path, log[i].content, log[i].mode); err != nil && first == nil {
				first = fmt.Errorf("certs: rollback %s: %w", log[i].path, err)
			}
		}
		return first
	}
	approved := map[Hostname]bool{}
	for _, h := range hosts {
		approved[Hostname(h)] = true
	}
	seen := map[Hostname]bool{}
	for _, r := range withPaths(routes) {
		if r.IsPath() {
			continue
		}
		if !approved[r.Host] || seen[r.Host] || !r.usesPlacedCert() || !RouteSnippetExists(stackDir, r.Host) {
			continue
		}
		seen[r.Host] = true
		if _, ok := servingCertFor(stackDir, r); !ok {
			continue
		}
		content, err := RenderRouteIn(stackDir, r)
		if err != nil {
			_ = undo()
			return nil, err
		}
		path := filepath.Join(stackDir, "routes", RouteFilename(r.Host))
		info, err := os.Stat(path)
		if err != nil {
			_ = undo()
			return nil, fmt.Errorf("certs: stat %s: %w", path, err)
		}
		prior, err := os.ReadFile(path)
		if err != nil {
			_ = undo()
			return nil, fmt.Errorf("certs: read %s: %w", path, err)
		}
		if string(prior) == content {
			continue
		}
		log = append(log, saved{path, prior, info.Mode().Perm()})
		if err := writeAtomic(path, []byte(content), info.Mode().Perm()); err != nil {
			_ = undo()
			return nil, err
		}
	}
	return undo, nil
}

// writeAtomic writes body through a temporary sibling and renames it
// over path, so readers never see a partial file.
func writeAtomic(path string, body []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), tempPrefix+"*")
	if err != nil {
		return fmt.Errorf("certs: temp file: %w", err)
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("certs: mode: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("certs: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("certs: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("certs: close: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return fmt.Errorf("certs: place %s: %w", filepath.Base(path), err)
	}
	return nil
}
