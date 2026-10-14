package docker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

// PlaceCert writes a certificate for the edge: key 0600, chain 0644,
// metadata last, each through a temporary file and rename so Caddy never
// reads a half-written file. The key is written before the chain.
func PlaceCert(stackDir string, cert PlacedCert, chainPEM, keyPEM []byte) error {
	if !validCertName.MatchString(cert.Name) {
		return fmt.Errorf("certs: invalid certificate name %q", cert.Name)
	}
	if len(cert.Names) == 0 || len(chainPEM) == 0 || len(keyPEM) == 0 {
		return fmt.Errorf("certs: names, chain and key are all required")
	}
	dir := CertDir(stackDir, cert.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("certs: dir: %w", err)
	}
	if err := os.Chmod(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("certs: dir mode: %w", err)
	}
	meta, err := json.Marshal(PlacedCert{Name: cert.Name, Names: cert.Names})
	if err != nil {
		return fmt.Errorf("certs: meta: %w", err)
	}
	for _, f := range []struct {
		name string
		body []byte
		mode os.FileMode
	}{
		{certKeyFile, keyPEM, 0o600},
		{certChainFile, chainPEM, 0o644},
		{certMetaFile, meta, 0o644},
	} {
		if err := writeAtomic(filepath.Join(dir, f.name), f.body, f.mode); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, body []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
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

// PlacedCerts lists the certificates placed under stackDir (ordered by
// name). A missing directory is none; an unreadable entry is skipped,
// never fatal, so one bad placement cannot stop route rendering.
func PlacedCerts(stackDir string) []PlacedCert {
	entries, err := os.ReadDir(filepath.Join(stackDir, "routes", certsDirName))
	if err != nil {
		return nil
	}
	var out []PlacedCert
	for _, entry := range entries {
		if !entry.IsDir() || !validCertName.MatchString(entry.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(CertDir(stackDir, entry.Name()), certMetaFile))
		if err != nil {
			continue
		}
		var placed PlacedCert
		if json.Unmarshal(raw, &placed) != nil || placed.Name != entry.Name() || len(placed.Names) == 0 {
			continue
		}
		if _, err := os.Stat(filepath.Join(CertDir(stackDir, entry.Name()), certKeyFile)); err != nil {
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

// RenderRouteIn renders a route as RenderRoute does, plus: a route
// using automatic TLS whose host is covered by a certificate placed
// under stackDir serves that certificate (`tls <chain> <key>`) instead
// of leaving issuance to Caddy. Other modes are rendered unchanged.
func RenderRouteIn(stackDir string, r Route) (string, error) {
	if r.TLS == TLSAuto && stackDir != "" {
		if cert, ok := ServingCert(stackDir, r.Host); ok {
			return renderRoute(r, &cert)
		}
	}
	return renderRoute(r, nil)
}

// WouldServe reports whether a route using automatic TLS would serve a
// certificate covering names, before anything is placed (plan preview).
func WouldServe(names []string, r Route) bool {
	return r.TLS == TLSAuto && certNamesCover(names, string(r.Host))
}

// RouteSnippetExists reports whether a route snippet for host is on
// disk. Certificate refresh only rewrites snippets that already exist,
// so issuing a certificate can never expose a route nobody applied.
func RouteSnippetExists(stackDir string, host Hostname) bool {
	info, err := os.Stat(filepath.Join(stackDir, "routes", RouteFilename(host)))
	return err == nil && info.Mode().IsRegular()
}

// RefreshCertRoutes re-renders the existing snippets of automatic-TLS
// routes now covered by a placed certificate, so they serve it. Routes
// without a snippet, with other TLS modes, or without a covering
// certificate are untouched. The returned undo restores every rewritten
// snippet; it never reloads.
func RefreshCertRoutes(stackDir string, routes []Route) (undo func() error, err error) {
	type saved struct {
		path    string
		content []byte
	}
	var log []saved
	undo = func() error {
		var first error
		for i := len(log) - 1; i >= 0; i-- {
			if err := os.WriteFile(log[i].path, log[i].content, 0o644); err != nil && first == nil {
				first = fmt.Errorf("certs: rollback %s: %w", log[i].path, err)
			}
		}
		return first
	}
	seen := map[Hostname]bool{}
	for _, r := range routes {
		if seen[r.Host] || r.TLS != TLSAuto || !RouteSnippetExists(stackDir, r.Host) {
			continue
		}
		seen[r.Host] = true
		if _, ok := ServingCert(stackDir, r.Host); !ok {
			continue
		}
		content, err := RenderRouteIn(stackDir, r)
		if err != nil {
			_ = undo()
			return nil, err
		}
		path := filepath.Join(stackDir, "routes", RouteFilename(r.Host))
		prior, err := os.ReadFile(path)
		if err != nil {
			_ = undo()
			return nil, fmt.Errorf("certs: read %s: %w", path, err)
		}
		if string(prior) == content {
			continue
		}
		log = append(log, saved{path, prior})
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			_ = undo()
			return nil, fmt.Errorf("certs: write %s: %w", path, err)
		}
	}
	return undo, nil
}
