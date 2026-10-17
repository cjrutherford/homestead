package catalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// ErrCannotVerify reports a database that accepts a deliberately wrong
// password over the verification path, so no password can be proven.
var ErrCannotVerify = errors.New("catalog: cannot verify the database password: the server accepted a wrong password (trust authentication); use skip-verify only if you are sure")

// dbAuthCheck authenticates once over the container's own network
// address (loopback and the socket trust every caller). The password is
// read from stdin.
func dbAuthCheck(ctx context.Context, m Manifest, password string) error {
	db := m.Database
	script := `IFS= read -r PGPASSWORD; export PGPASSWORD; exec psql -w -h "$(hostname)" -U "$1" -d "$2" -tAc "select 1"`
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := dbExec(call, dbContainer(m), password+"\n", "sh", "-c", script, "sh", db.Username, db.Database)
	return err
}

// VerifyDatabasePassword proves a password. It first confirms the
// server rejects a random wrong password over the same path; a server
// that accepts it cannot verify anything and is reported as such.
func VerifyDatabasePassword(ctx context.Context, m Manifest, password string) error {
	if m.Database == nil || m.Database.Kind != DatabasePostgres {
		return fmt.Errorf("catalog: %q declares no postgres database", m.Name)
	}
	wrong, err := generateSecretValue()
	if err != nil {
		return err
	}
	if dbAuthCheck(ctx, m, wrong) == nil {
		return ErrCannotVerify
	}
	if err := dbAuthCheck(ctx, m, password); err != nil {
		return fmt.Errorf("catalog: database rejected the password (or %s is not running): %w", dbContainer(m), err)
	}
	return nil
}

// waitVerifyDatabasePassword verifies like VerifyDatabasePassword but
// waits for a database that is still coming up (a first start runs
// initdb with only a socket listener, so TCP refuses connections for a
// while). A definite answer ends the wait: the password was rejected,
// or the server cannot verify anything. Found in KVM validation.
func waitVerifyDatabasePassword(ctx context.Context, m Manifest, password string) error {
	deadline := time.Now().Add(healthTimeout)
	for {
		err := VerifyDatabasePassword(ctx, m, password)
		if err == nil || errors.Is(err, ErrCannotVerify) ||
			strings.Contains(err.Error(), "password authentication failed") ||
			time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(healthPoll):
		}
	}
}

// RegisterOptions tunes RegisterSecret.
type RegisterOptions struct {
	// SkipVerify skips database verification; allowed only when no value
	// exists yet, or with Force.
	SkipVerify bool
	// Force overwrites an existing value without verification; the
	// caller audits it.
	Force bool
}

// RegisterResult reports what registration did.
type RegisterResult struct {
	StoreName string
	// Changed is true when the stored value differs from before.
	Changed bool
}

// RegisterSecret stores an owner-supplied value for a declared secret,
// for installs that predate S266 (the stack is not regenerated).
// Immutable secrets with a value refuse. A database password is verified
// against the running database; overwriting an existing value is allowed
// only when verified, or with Force.
func RegisterSecret(ctx context.Context, m Manifest, name, value string, src SecretStore, opts RegisterOptions) (RegisterResult, error) {
	var decl *SecretDecl
	for i := range m.Secrets {
		if m.Secrets[i].Name == name {
			decl = &m.Secrets[i]
		}
	}
	if decl == nil {
		return RegisterResult{}, fmt.Errorf("catalog: %q declares no secret %q", m.Name, name)
	}
	if value == "" {
		return RegisterResult{}, fmt.Errorf("catalog: secret value must not be empty")
	}
	store := SecretStoreName(m.Name, name)
	existing, err := src.Get(store)
	have := err == nil
	if err != nil && !errors.Is(err, secret.ErrNotFound) {
		return RegisterResult{}, fmt.Errorf("catalog: read secret %q: %w", store, err)
	}
	if have && existing == value {
		return RegisterResult{StoreName: store}, nil
	}
	if have && decl.Immutable {
		return RegisterResult{}, fmt.Errorf("catalog: secret %q already has a value and is immutable; replacing it would break existing data", name)
	}
	isDB := m.Database != nil && m.Database.PasswordSecret == name
	verified := false
	if isDB && !opts.SkipVerify {
		if err := VerifyDatabasePassword(ctx, m, value); err != nil {
			return RegisterResult{}, err
		}
		verified = true
	}
	if have && !verified && !opts.Force {
		return RegisterResult{}, fmt.Errorf("catalog: secret %q already has a value; overwriting needs a verified database password or an explicit force", name)
	}
	if err := src.SetApp(store, value); err != nil {
		return RegisterResult{}, fmt.Errorf("catalog: store secret %q: %w", store, err)
	}
	return RegisterResult{StoreName: store, Changed: true}, nil
}

// consumerServices lists the distinct services consuming a secret.
func consumerServices(decl SecretDecl) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range decl.Consumers {
		if !seen[c.Service] {
			seen[c.Service] = true
			out = append(out, c.Service)
		}
	}
	return out
}

func findSecret(m Manifest, name string) (SecretDecl, bool) {
	for _, d := range m.Secrets {
		if d.Name == name {
			return d, true
		}
	}
	return SecretDecl{}, false
}

// ApplyRegistered pushes a changed registered value to a wired stack:
// files rewritten and consumers restarted. Unwired stacks are untouched.
func ApplyRegistered(ctx context.Context, m Manifest, dir, project, name string, src SecretStore) error {
	if !composeSecretsWired(dir, m) {
		return nil
	}
	decl, ok := findSecret(m, name)
	if !ok {
		return nil
	}
	defer lockProject(project)()
	if err := WriteSecretFiles(dir, SecretFilesFor(m), src); err != nil {
		return err
	}
	return restartConsumers(ctx, dir, project, decl)
}

// restartConsumers restarts the services consuming a secret. Restart
// starts a stopped container, so it takes the craft image gate like every
// other start path (S249).
func restartConsumers(ctx context.Context, dir, project string, decl SecretDecl) error {
	if err := checkCraftStack(ctx, dir, project); err != nil {
		return err
	}
	return composeRun(ctx, dir, project, append([]string{"restart"}, consumerServices(decl)...)...)
}

// rotateSecret replaces one secret with a fresh strong value as one
// journaled unit: pending value written first, then (for the database
// password) ALTER ROLE, files, promotion, consumer restart, and a health
// check. Any failure after the role may have changed reverts the role to
// the old value; a failed revert is reported loudly and leaves the
// pending value for startup reconciliation to settle against what the
// database actually accepts. An unknown old value refuses up front.
func rotateSecret(ctx context.Context, m Manifest, dir, project, name string, src SecretStore) error {
	decl, ok := findSecret(m, name)
	if !ok {
		return fmt.Errorf("catalog: %q declares no secret %q", m.Name, name)
	}
	store := SecretStoreName(m.Name, name)
	pending := pendingName(store)
	old, err := src.Get(store)
	if err != nil || old == "" {
		return fmt.Errorf("catalog: secret %q has no known current value; register it before rotating", name)
	}
	if _, err := src.Get(pending); err == nil {
		return fmt.Errorf("catalog: a rotation of %q is pending; it settles automatically when the database is reachable (restart the daemon to retry now)", name)
	}
	fresh, err := generateSecretValue()
	if err != nil {
		return err
	}
	if err := src.SetApp(pending, fresh); err != nil {
		return fmt.Errorf("catalog: journal rotation: %w", err)
	}
	isDB := m.Database != nil && m.Database.PasswordSecret == name
	files := SecretFilesFor(m)
	withFresh := overrideSource{src, map[string]string{store: fresh}}

	revert := func(cause error) error {
		var problems []string
		if isDB {
			if err := AlterDatabasePassword(ctx, m, old); err != nil {
				problems = append(problems, "the database role could not be reverted")
			}
		}
		if len(problems) > 0 {
			return fmt.Errorf("catalog: rotation of %q failed (%v) AND %s: the database may hold the new password, which is kept as %q; recover by running `docker exec -i %s psql -U %s -d %s` and setting the role password to the value of secret %q, or restart the daemon to reconcile",
				name, cause, strings.Join(problems, "; "), pending, dbContainer(m), m.Database.Username, m.Database.Database, store)
		}
		if err := src.SetApp(store, old); err != nil {
			return fmt.Errorf("catalog: rotation of %q failed (%v) and the old value could not be restored in the store: %w", name, cause, err)
		}
		if err := WriteSecretFiles(dir, files, src); err != nil {
			return fmt.Errorf("catalog: rotation of %q failed (%v) and old secret files could not be rewritten: %w", name, cause, err)
		}
		restartErr := restartConsumers(ctx, dir, project, decl)
		_ = src.Delete(pending)
		if restartErr != nil {
			return fmt.Errorf("catalog: rotation of %q failed (%v), was reverted, but consumers could not be restarted: %w", name, cause, restartErr)
		}
		return fmt.Errorf("catalog: rotation of %q failed and was reverted: %w", name, cause)
	}

	if isDB {
		if err := AlterDatabasePassword(ctx, m, fresh); err != nil {
			return revert(err)
		}
	}
	if err := WriteSecretFiles(dir, files, withFresh); err != nil {
		return revert(err)
	}
	if err := src.SetApp(store, fresh); err != nil {
		return revert(err)
	}
	if err := restartConsumers(ctx, dir, project, decl); err != nil {
		return revert(err)
	}
	if isDB {
		if err := VerifyDatabasePassword(ctx, m, fresh); err != nil {
			return revert(err)
		}
	}
	// The journal stays until everything has been proven.
	_ = src.Delete(pending)
	return nil
}

// ReconcileRotation settles an interrupted rotation (a pending journal
// row) for one declared secret: for the database password it asks the
// database which value it accepts and makes store and files agree; for
// other secrets it follows what the files hold. It returns a short
// description, or "" when nothing was pending.
func ReconcileRotation(ctx context.Context, m Manifest, dir, project, name string, src SecretStore) (string, error) {
	decl, ok := findSecret(m, name)
	if !ok {
		return "", nil
	}
	store := SecretStoreName(m.Name, name)
	pending := pendingName(store)
	fresh, err := src.Get(pending)
	if err != nil {
		return "", nil
	}
	defer lockProject(project)()
	current, _ := src.Get(store)
	files := SecretFilesFor(m)
	settle := func(value, what string) (string, error) {
		if value != current {
			if err := src.SetApp(store, value); err != nil {
				return "", err
			}
		}
		if err := WriteSecretFiles(dir, files, src); err != nil {
			return "", err
		}
		if err := restartConsumers(ctx, dir, project, decl); err != nil {
			return "", err
		}
		_ = src.Delete(pending)
		return what, nil
	}
	if m.Database != nil && m.Database.PasswordSecret == name {
		if VerifyDatabasePassword(ctx, m, fresh) == nil {
			return settle(fresh, "database accepts the new password; settled to it")
		}
		if current != "" && VerifyDatabasePassword(ctx, m, current) == nil {
			return settle(current, "database kept the old password; rotation dropped")
		}
		return "", fmt.Errorf("catalog: cannot reconcile pending rotation of %q: the database accepted neither value (is %s running?)", name, dbContainer(m))
	}
	for _, f := range files {
		if f.Name != name {
			continue
		}
		if raw, err := os.ReadFile(filepath.Join(dir, SecretsDirName, f.Key)); err == nil && string(raw) == fresh {
			return settle(fresh, "files hold the new value; settled to it")
		}
	}
	return settle(current, "rotation had not reached the files; dropped")
}
