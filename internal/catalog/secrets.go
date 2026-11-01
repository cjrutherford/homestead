package catalog

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
	"homestead/internal/secret"
)

// Secret-backed container variables (S266, ADR-021). The secret store
// stays the encrypted source of truth; each value reaches a container as
// a mode 0600 file under the stack directory, referenced from the base
// Compose document through top-level `secrets:` with `file:` sources.
// The files live in the stack directory, never under
// <mount>/<package>, so plaintext backups cannot include them. No value
// appears in YAML, `environment:`, logs, audit, drift, or export.

// SecretsDirName is the stack-relative directory holding materialized
// secret files.
const SecretsDirName = "secrets"

// SecretFileMode is the only mode a secret file may carry.
const SecretFileMode os.FileMode = 0o600

var validSecretName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// SecretConsumer wires one secret into one service: Env is the
// environment variable that receives the in-container PATH of the file
// (the images' own `*_FILE` convention), or, with URI set, a
// `file://` URI of it (Authentik's convention). Never the value.
//
// Export covers the images that have no `*_FILE` input at all (S342,
// ADR-052): Env keeps carrying the path, and the generated
// secret-entrypoint supervisor exports the value into Env without its
// `_FILE` suffix at container start. The value therefore exists in the
// container's environment at runtime — the trade the owner accepted to
// make those apps configurable at all — but never in Compose YAML, in
// `docker inspect` before start, or in any file Homestead writes.
type SecretConsumer struct {
	Service string
	Env     string
	URI     bool
	Export  bool
}

// SecretDecl declares one secret-backed variable. Name is the logical
// name (also how configure addresses it); the store name derives from
// package and name (SecretStoreName). Immutable secrets are generated
// once and refuse rotation (rotating them would break existing data).
//
// Reveal marks an owner-facing credential (S312), such as an app's
// generated admin password: the owner may read it, by an explicit,
// audited reveal behind step-up. Every other secret stays presence-only.
// Note says what the credential is for (for example the account name).
type SecretDecl struct {
	Name      string
	Consumers []SecretConsumer
	Immutable bool
	Reveal    bool
	Note      string
	// Initial (S413) is a fixed, non-secret starting value stored
	// instead of a generated one. For a derived secret file an adapter
	// renders at sign-in time (EnvOIDC.SecretFile): before sign-in is on
	// it is the app's "nothing configured" value. Needs Immutable.
	Initial string
}

// SecretStoreName is the secret-store row holding a package secret.
// The `app_` prefix marks app-owned secrets, which configuration export
// leaves out (they are covered by encrypted backups, S259).
func SecretStoreName(pkg, name string) string {
	return secret.AppSecretPrefix + strings.ReplaceAll(pkg, "-", "_") + "_" + name
}

// secretKey is the Compose secret name and file name for one
// (service, secret) pair: one file per consuming service, because each
// file must be owned by that service's user to stay readable at 0600.
func secretKey(service, name string) string {
	return strings.NewReplacer("-", "_", ".", "_").Replace(service) + "_" + name
}

func secretContainerPath(key string) string { return "/run/secrets/" + key }

func (c SecretConsumer) value(key string) string {
	if c.URI {
		return "file://" + secretContainerPath(key)
	}
	return secretContainerPath(key)
}

func validateSecrets(m Manifest, services map[string]bool) error {
	seen := map[string]bool{}
	envs := map[string]bool{}
	envSvc := map[string]map[string]string{}
	entrypoints := map[string]bool{}
	exported := map[string]string{}
	for _, svc := range m.Services {
		envSvc[svc.Name] = svc.Environment
		entrypoints[svc.Name] = len(svc.Entrypoint) > 0
	}
	for _, decl := range m.Secrets {
		if !validSecretName.MatchString(decl.Name) {
			return fmt.Errorf("catalog: secret name %q must match [a-z][a-z0-9_]*", decl.Name)
		}
		if len(SecretStoreName(m.Name, decl.Name))+len(pendingSuffix) > 64 {
			return fmt.Errorf("catalog: secret %q store name exceeds 56 characters", decl.Name)
		}
		if strings.HasSuffix(decl.Name, pendingSuffix) {
			return fmt.Errorf("catalog: secret name %q must not end in %s (reserved for rotation)", decl.Name, pendingSuffix)
		}
		if seen[decl.Name] {
			return fmt.Errorf("catalog: duplicate secret %q", decl.Name)
		}
		seen[decl.Name] = true
		if decl.Initial != "" && (!decl.Immutable || decl.Reveal || strings.ContainsAny(decl.Initial, "\r\n")) {
			return fmt.Errorf("catalog: secret %q has an initial value, so it must be immutable, not revealed and one line", decl.Name)
		}
		if len(decl.Consumers) == 0 && !m.HeldSecret(decl.Name) {
			return fmt.Errorf("catalog: secret %q needs at least one consumer", decl.Name)
		}
		for _, c := range decl.Consumers {
			if !services[c.Service] {
				return fmt.Errorf("catalog: secret %q consumer service %q unknown", decl.Name, c.Service)
			}
			if !validEnvKey.MatchString(c.Env) {
				return fmt.Errorf("catalog: secret %q consumer variable %q invalid", decl.Name, c.Env)
			}
			id := c.Service + "\x00" + c.Env
			if envs[id] {
				return fmt.Errorf("catalog: service %q variable %q wired twice", c.Service, c.Env)
			}
			envs[id] = true
			if _, plain := envSvc[c.Service][c.Env]; plain {
				return fmt.Errorf("catalog: service %q variable %q is both plain and secret-backed", c.Service, c.Env)
			}
			if !c.Export {
				continue
			}
			if c.URI {
				return fmt.Errorf("catalog: secret %q consumer %q cannot export a URI", decl.Name, c.Env)
			}
			if !strings.HasSuffix(c.Env, "_FILE") {
				return fmt.Errorf("catalog: secret %q export needs a *_FILE variable, got %q", decl.Name, c.Env)
			}
			target := strings.TrimSuffix(c.Env, "_FILE")
			if !validEnvKey.MatchString(target) {
				return fmt.Errorf("catalog: secret %q export target %q invalid", decl.Name, target)
			}
			if prev, dup := exported[c.Service+"\x00"+target]; dup {
				return fmt.Errorf("catalog: service %q exports %q for both %q and %q", c.Service, target, prev, decl.Name)
			}
			exported[c.Service+"\x00"+target] = decl.Name
			if _, plain := envSvc[c.Service][target]; plain {
				return fmt.Errorf("catalog: service %q exports %q but also sets it plainly", c.Service, target)
			}
			if !entrypoints[c.Service] {
				return fmt.Errorf("catalog: service %q exports secret %q but declares no entrypoint", c.Service, decl.Name)
			}
		}
	}
	if m.Database != nil && m.Database.PasswordSecret != "" {
		if !seen[m.Database.PasswordSecret] {
			return fmt.Errorf("catalog: database password secret %q is not declared", m.Database.PasswordSecret)
		}
		if !m.Database.Kind.Server() {
			return fmt.Errorf("catalog: only server databases take a password secret")
		}
	}
	for _, svc := range m.Services {
		if len(svc.Entrypoint) == 0 {
			continue
		}
		exports := false
		for key := range exported {
			if strings.HasPrefix(key, svc.Name+"\x00") {
				exports = true
			}
		}
		if !exports {
			return fmt.Errorf("catalog: service %q declares an entrypoint but exports no secret", svc.Name)
		}
	}
	return nil
}

// SecretFile is one materialized file: metadata only, never a value.
type SecretFile struct {
	Key       string
	Service   string
	Name      string // logical secret name
	StoreName string
	UID, GID  *int
	// DatabasePassword marks the manifest's database password file.
	DatabasePassword bool
	// Initial is the declaration's fixed starting value (S413).
	Initial string
}

// SecretFilesFor lists the files a manifest needs, sorted by key, with
// the owner each needs (the consuming service's declared data owner).
func SecretFilesFor(m Manifest) []SecretFile {
	owners := map[string][2]*int{}
	for _, svc := range m.Services {
		for _, d := range svc.Data {
			if d.UID != nil || d.GID != nil {
				owners[svc.Name] = [2]*int{d.UID, d.GID}
				break
			}
		}
	}
	seen := map[string]bool{}
	var out []SecretFile
	for _, decl := range m.Secrets {
		for _, c := range decl.Consumers {
			key := secretKey(c.Service, decl.Name)
			if seen[key] {
				continue
			}
			seen[key] = true
			o := owners[c.Service]
			out = append(out, SecretFile{Key: key, Service: c.Service, Name: decl.Name,
				StoreName: SecretStoreName(m.Name, decl.Name), UID: o[0], GID: o[1],
				DatabasePassword: m.Database != nil && m.Database.PasswordSecret == decl.Name, Initial: decl.Initial})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// EntrypointName is the in-container path of the supervising entrypoint
// the generated Compose document sets as ENTRYPOINT (S342, ADR-052).
const EntrypointName = "/homestead/secret-entrypoint.sh"

// EntrypointScript is one generated supervisor for a service that
// exports secret values into the environment. Body holds paths, never
// values, so the script is safe to inspect and back up.
type EntrypointScript struct {
	Service string
	// File is the stack-relative file name the Compose document mounts.
	File string
	Body string
}

// EntrypointFile names a service's supervisor script inside the stack
// directory. One file per service keeps each generated document
// readable on its own.
func EntrypointFile(service string) string {
	return "secret-entrypoint." + strings.NewReplacer("-", "_", ".", "_").Replace(service) + ".sh"
}

// secretPair ties a secret declaration name to the consumer that
// exports it.
type secretPair struct {
	decl     string
	consumer SecretConsumer
}

// EntrypointScriptsFor renders the supervisors a manifest needs, sorted
// by service. A service appears only when it exports a secret.
func EntrypointScriptsFor(m Manifest) []EntrypointScript {
	byService := map[string][]secretPair{}
	for _, decl := range m.Secrets {
		for _, c := range decl.Consumers {
			if c.Export {
				byService[c.Service] = append(byService[c.Service], secretPair{decl.Name, c})
			}
		}
	}
	names := make([]string, 0, len(byService))
	for name := range byService {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]EntrypointScript, 0, len(names))
	for _, name := range names {
		out = append(out, EntrypointScript{
			Service: name,
			File:    EntrypointFile(name),
			Body:    entrypointBody(name, byService[name]),
		})
	}
	return out
}

// entrypointBody renders one supervisor: for each exported secret, fail
// closed when the file is unreadable, read it into the target variable,
// export it, and drop the path variable so the container sees exactly
// what the image expects.
func entrypointBody(service string, pairs []secretPair) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Generated by Homestead (S342, ADR-052). Exports secret files into the\n")
	b.WriteString("# environment for images that read only plaintext variables. This file\n")
	b.WriteString("# holds paths, never values. Regeneration overwrites edits.\n")
	b.WriteString("set -eu\n")
	for _, p := range pairs {
		path := secretContainerPath(secretKey(service, p.decl))
		target := strings.TrimSuffix(p.consumer.Env, "_FILE")
		fmt.Fprintf(&b, "if [ ! -r %s ]; then\n", path)
		fmt.Fprintf(&b, "  echo \"homestead: missing secret file for %s\" >&2\n", target)
		b.WriteString("  exit 1\nfi\n")
		fmt.Fprintf(&b, "%s=\"$(cat %s)\"\n", target, path)
		fmt.Fprintf(&b, "export %s\n", target)
		fmt.Fprintf(&b, "unset %s\n", p.consumer.Env)
	}
	b.WriteString("exec \"$@\"\n")
	return b.String()
}

// SecretStore is the slice of the secret store this package needs.
type SecretStore interface {
	Get(name string) (string, error)
	// SetApp writes a catalog-managed (app_) secret; the owner-facing
	// Set refuses that prefix.
	SetApp(name, value string) error
	Delete(name string) error
}

// pendingSuffix names the journal row holding a rotation's intended new
// value until the rotation settles (S266 review): written before the
// database changes, promoted or dropped afterwards, reconciled at start.
const pendingSuffix = "_pending"

func pendingName(store string) string { return store + pendingSuffix }

// overrideSource serves chosen store values (a not-yet-promoted
// rotation) ahead of the real store when writing files.
type overrideSource struct {
	SecretStore
	values map[string]string
}

func (o overrideSource) Get(name string) (string, error) {
	if v, ok := o.values[name]; ok {
		return v, nil
	}
	return o.SecretStore.Get(name)
}

// generateSecretValue makes a strong value: 32 random bytes as hex
// (safe in URIs, SQL literals, and shell-free contexts).
func generateSecretValue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("catalog: generate secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// EnsureSecrets generates a strong value into the store for every
// declared secret that has none. Existing values are never touched.
func EnsureSecrets(src SecretStore, m Manifest) error {
	var names []string
	initial := map[string]string{}
	for _, decl := range m.Secrets {
		names = append(names, SecretStoreName(m.Name, decl.Name))
		if decl.Initial != "" {
			initial[SecretStoreName(m.Name, decl.Name)] = decl.Initial
		}
	}
	return ensureNamesInitial(src, names, initial)
}

func ensureNames(src SecretStore, names []string) error { return ensureNamesInitial(src, names, nil) }

func ensureNamesInitial(src SecretStore, names []string, initial map[string]string) error {
	if len(names) == 0 {
		return nil
	}
	if src == nil {
		return fmt.Errorf("catalog: secrets need a secret store")
	}
	for _, name := range names {
		if _, err := src.Get(name); err == nil {
			continue
		} else if !errors.Is(err, secret.ErrNotFound) {
			return fmt.Errorf("catalog: read secret %q: %w", name, err)
		}
		value, ok := initial[name]
		if !ok {
			var err error
			if value, err = generateSecretValue(); err != nil {
				return err
			}
		}
		if err := src.SetApp(name, value); err != nil {
			return fmt.Errorf("catalog: store secret %q: %w", name, err)
		}
	}
	return nil
}

// ensureFileSecrets generates the values the files' secrets lack in the
// store, using a declaration's fixed starting value where it has one.
func ensureFileSecrets(src SecretStore, files []SecretFile) error {
	initial := map[string]string{}
	for _, f := range files {
		if f.Initial != "" {
			initial[f.StoreName] = f.Initial
		}
	}
	return ensureNamesInitial(src, storeNames(files), initial)
}

func storeNames(files []SecretFile) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		if !seen[f.StoreName] {
			seen[f.StoreName] = true
			out = append(out, f.StoreName)
		}
	}
	return out
}

// chownSecret hands an open secret file to its consuming user, stubbed
// in tests (the daemon runs as root; tests do not). It works on the open
// descriptor, so a swapped path cannot redirect it.
var chownSecret = func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }

// SetSecretChown replaces the ownership step and returns the restore
// function: a seam for tests of other packages that write secret files
// for a service with a non-root data owner, run by a non-root user.
func SetSecretChown(fn func(*os.File, int, int) error) (restore func()) {
	old := chownSecret
	chownSecret = fn
	return func() { chownSecret = old }
}

// prepareSecretsDir creates dir/secrets 0700 and refuses a symlink or
// non-directory in its place; the mode is set on the open descriptor,
// never following a link.
func prepareSecretsDir(dir string) (string, error) {
	secDir := filepath.Join(dir, SecretsDirName)
	info, err := os.Lstat(secDir)
	switch {
	case os.IsNotExist(err):
		if err := os.Mkdir(secDir, 0o700); err != nil {
			return "", fmt.Errorf("catalog: secrets dir: %w", err)
		}
	case err != nil:
		return "", fmt.Errorf("catalog: secrets dir: %w", err)
	case !info.IsDir():
		return "", fmt.Errorf("catalog: %s is not a directory (symlinks are refused)", secDir)
	}
	d, err := os.OpenFile(secDir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", fmt.Errorf("catalog: open secrets dir: %w", err)
	}
	defer d.Close()
	if err := d.Chmod(0o700); err != nil {
		return "", fmt.Errorf("catalog: secrets dir mode: %w", err)
	}
	return secDir, nil
}

// writeSecretFile writes one value atomically: a 0600 temp file created
// exclusively beside the target, owner and mode fixed on the descriptor
// before any content lands, fsynced, then renamed over the target (which
// must not be a symlink), and the directory fsynced. Containers pick up
// the new inode when they next start.
func writeSecretFile(secDir, key, value string, uid, gid *int) error {
	path := filepath.Join(secDir, key)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("catalog: secret file %s is not a regular file (symlinks are refused)", key)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("catalog: secret file %s: %w", key, err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("catalog: secret file %s: %w", key, err)
	}
	tmp := filepath.Join(secDir, "."+key+".tmp-"+hex.EncodeToString(suffix))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, SecretFileMode)
	if err != nil {
		return fmt.Errorf("catalog: secret file %s: %w", key, err)
	}
	cleanup := func() { _ = f.Close(); _ = os.Remove(tmp) }
	if err := f.Chmod(SecretFileMode); err != nil {
		cleanup()
		return fmt.Errorf("catalog: secret file mode %s: %w", key, err)
	}
	if uid != nil || gid != nil {
		u, g := -1, -1
		if uid != nil {
			u = *uid
		}
		if gid != nil {
			g = *gid
		}
		if err := chownSecret(f, u, g); err != nil {
			cleanup()
			return fmt.Errorf("catalog: secret file owner %s: %w", key, err)
		}
	}
	if _, err := f.WriteString(value); err != nil {
		cleanup()
		return fmt.Errorf("catalog: write secret file %s", key)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("catalog: sync secret file %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("catalog: close secret file %s: %w", key, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("catalog: place secret file %s: %w", key, err)
	}
	if d, err := os.Open(secDir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// WriteSecretFiles resolves each file's value from the store and writes
// it mode 0600 under dir/secrets (directory 0700), owned by the
// consuming service's user, through writeSecretFile. A missing secret
// fails naming the store entry, never writing an empty file.
func WriteSecretFiles(dir string, files []SecretFile, src SecretStore) error {
	if len(files) == 0 {
		return nil
	}
	if src == nil {
		return fmt.Errorf("catalog: secret files need a secret store")
	}
	secDir, err := prepareSecretsDir(dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		value, err := src.Get(f.StoreName)
		if err != nil {
			if errors.Is(err, secret.ErrNotFound) {
				return fmt.Errorf("catalog: secret %q is not in the secret store", f.StoreName)
			}
			return fmt.Errorf("catalog: read secret %q: %w", f.StoreName, err)
		}
		if err := writeSecretFile(secDir, f.Key, value, f.UID, f.GID); err != nil {
			return err
		}
	}
	return nil
}

// composeSecretsWired reports whether the stack's compose file carries
// secret wiring: false for stacks installed before S266.
func composeSecretsWired(dir string, m Manifest) bool {
	if len(m.Secrets) == 0 {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		return false
	}
	var doc struct {
		Secrets map[string]any `yaml:"secrets"`
	}
	if yaml.Unmarshal(raw, &doc) != nil {
		return false
	}
	for _, f := range SecretFilesFor(m) {
		if _, ok := doc.Secrets[f.Key]; !ok {
			return false
		}
	}
	return true
}

// detectSecretFiles reports secret files drift treats opaquely:
// presence and mode only, never content. Paths come from the stack's
// own compose `secrets:` file sources.
func detectSecretFiles(dir string) []Drift {
	raw, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		return nil
	}
	var doc struct {
		Secrets map[string]struct {
			File string `yaml:"file"`
		} `yaml:"secrets"`
	}
	if yaml.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var out []Drift
	for _, entry := range doc.Secrets {
		if entry.File == "" || !strings.HasPrefix(entry.File, "./"+SecretsDirName+"/") {
			continue
		}
		rel := strings.TrimPrefix(entry.File, "./")
		info, err := os.Stat(filepath.Join(dir, rel))
		switch {
		case err != nil:
			out = append(out, Drift{Path: rel, Kind: DriftRemoved})
		case info.Mode().Perm() != SecretFileMode:
			out = append(out, Drift{Path: rel, Kind: DriftMode})
		}
	}
	return out
}

func isSecretPath(path string) bool { return strings.HasPrefix(path, SecretsDirName+"/") }

// WriteSecretFilesIfWired refreshes secret files for stacks whose
// compose file carries the wiring; stacks that predate S266 are left
// untouched (their stack is never regenerated here).
func WriteSecretFilesIfWired(dir string, m Manifest, src SecretStore) error {
	if !composeSecretsWired(dir, m) {
		return nil
	}
	return WriteSecretFiles(dir, SecretFilesFor(m), src)
}

// MissingSecretFiles lists secret files the compose file references that
// are absent (drift cannot recreate them; configure does).
func MissingSecretFiles(dir string) []string {
	var out []string
	for _, d := range detectSecretFiles(dir) {
		if d.Kind == DriftRemoved {
			out = append(out, d.Path)
		}
	}
	sort.Strings(out)
	return out
}

// SecretsWired reports whether an installed stack carries secret wiring.
func SecretsWired(dir string, m Manifest) bool { return composeSecretsWired(dir, m) }
