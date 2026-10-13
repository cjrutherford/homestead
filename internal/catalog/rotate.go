package catalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"homestead/internal/secret"
)

// dbExec runs a command in a database container with stdin, stubbed in
// tests. Secrets travel on stdin, never argv or environment, so they
// stay out of process listings and `docker inspect`.
var dbExec = func(ctx context.Context, container, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", container}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("exec %s: %w: %s", container, err, strings.TrimSpace(redact(out.String(), stdin)))
	}
	return out.String(), nil
}

// redact strips any occurrence of the secret from engine output.
func redact(text, value string) string {
	if value == "" {
		return text
	}
	for _, line := range strings.Split(value, "\n") {
		if line != "" {
			text = strings.ReplaceAll(text, line, "[redacted]")
		}
	}
	return text
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// dbContainer is the declared database service's container name.
func dbContainer(m Manifest) string { return m.Name + "-" + m.Database.Service }

// AlterDatabasePassword sets the declared Postgres role's password in
// the running database (over the container's local socket), so a
// rotated secret and the database agree.
func AlterDatabasePassword(ctx context.Context, m Manifest, password string) error {
	if m.Database == nil || m.Database.Kind != DatabasePostgres {
		return fmt.Errorf("catalog: %q declares no postgres database", m.Name)
	}
	db := m.Database
	script := fmt.Sprintf("ALTER ROLE %q WITH PASSWORD %s;\n", db.Username, sqlQuote(password))
	if _, err := dbExec(ctx, dbContainer(m), script, "psql", "-q", "-v", "ON_ERROR_STOP=1",
		"--username", db.Username, "--dbname", db.Database); err != nil {
		return fmt.Errorf("catalog: set database password: %w", err)
	}
	return nil
}

// VerifyDatabasePassword proves a password by authenticating over the
// container's own network address (the loopback and socket paths trust
// all callers, so they would accept any password). The password is read
// from stdin.
func VerifyDatabasePassword(ctx context.Context, m Manifest, password string) error {
	if m.Database == nil || m.Database.Kind != DatabasePostgres {
		return fmt.Errorf("catalog: %q declares no postgres database", m.Name)
	}
	db := m.Database
	script := `IFS= read -r PGPASSWORD; export PGPASSWORD; exec psql -w -h "$(hostname)" -U "$1" -d "$2" -tAc "select 1"`
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := dbExec(call, dbContainer(m), password+"\n", "sh", "-c", script, "sh", db.Username, db.Database); err != nil {
		return fmt.Errorf("catalog: database rejected the password (or %s is not running): %w", dbContainer(m), err)
	}
	return nil
}

// RegisterSecret stores an owner-supplied value for a declared secret,
// for installs that predate S266 (the stack is not regenerated). The
// database password is verified against the running database first
// unless skipVerify is set. Returns the store name registered.
func RegisterSecret(ctx context.Context, m Manifest, name, value string, src SecretStore, skipVerify bool) (string, error) {
	var decl *SecretDecl
	for i := range m.Secrets {
		if m.Secrets[i].Name == name {
			decl = &m.Secrets[i]
		}
	}
	if decl == nil {
		return "", fmt.Errorf("catalog: %q declares no secret %q", m.Name, name)
	}
	if value == "" {
		return "", fmt.Errorf("catalog: secret value must not be empty")
	}
	if m.Database != nil && m.Database.PasswordSecret == name && !skipVerify {
		if err := VerifyDatabasePassword(ctx, m, value); err != nil {
			return "", err
		}
	}
	store := SecretStoreName(m.Name, name)
	if err := src.Set(store, value); err != nil {
		return "", fmt.Errorf("catalog: store secret %q: %w", store, err)
	}
	return store, nil
}

// rotateSecrets replaces each named secret with a fresh strong value.
// A database password changes in the database first, then in the store;
// if the store write fails the role is restored. Files are rewritten
// in place and the consuming services restart to read them.
func rotateSecrets(ctx context.Context, m Manifest, dir, project string, names []string, src SecretStore) error {
	byName := map[string]SecretDecl{}
	for _, d := range m.Secrets {
		byName[d.Name] = d
	}
	var restart []string
	seen := map[string]bool{}
	for _, name := range names {
		decl := byName[name]
		store := SecretStoreName(m.Name, name)
		old, err := src.Get(store)
		if err != nil && !errors.Is(err, secret.ErrNotFound) {
			return fmt.Errorf("catalog: read secret %q: %w", store, err)
		}
		fresh, err := generateSecretValue()
		if err != nil {
			return err
		}
		isDB := m.Database != nil && m.Database.PasswordSecret == name
		if isDB {
			if err := AlterDatabasePassword(ctx, m, fresh); err != nil {
				return err
			}
		}
		if err := src.Set(store, fresh); err != nil {
			if isDB && old != "" {
				_ = AlterDatabasePassword(ctx, m, old)
			}
			return fmt.Errorf("catalog: store secret %q: %w", store, err)
		}
		for _, c := range decl.Consumers {
			if !seen[c.Service] {
				seen[c.Service] = true
				restart = append(restart, c.Service)
			}
		}
	}
	if err := WriteSecretFiles(dir, SecretFilesFor(m), src); err != nil {
		return err
	}
	return composeRun(ctx, dir, project, append([]string{"restart"}, restart...)...)
}
