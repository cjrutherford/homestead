package firstrun

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"homestead/internal/catalog"
)

// GiteaAdminAccount is the local administrator Homestead creates. It is a
// name no Authentik username is likely to use, and Gitea's account linking
// asks for this account's password before any sign-in could take it over.
const GiteaAdminAccount = "homestead-admin"

var giteaGenerated = regexp.MustCompile(`generated random password is '([^']+)'`)

// giteaCLI runs the pinned image's own gitea binary as its git user.
// `gitea admin user create` has no way to read a password from stdin or a
// file, so the routine asks Gitea to generate one (--random-password) and
// reads it from the command's output: no password is ever an argument.
//
// VERIFIED AGAINST Gitea 28.2.0: `gitea admin user list --admin`,
// `create --admin --random-password --random-password-length
// --must-change-password=false` printing "generated random password is
// '...'", `delete --username`, and su-exec in the image.
func giteaCLI(ctx context.Context, container string, args ...string) (string, error) {
	return execIn(ctx, container, "", append([]string{"su-exec", "git:git", "gitea"}, args...)...)
}

// giteaAdmins returns the user names of Gitea's administrators.
func giteaAdmins(ctx context.Context, container string) ([]string, error) {
	out, err := giteaCLI(ctx, container, "admin", "user", "list", "--admin")
	if err != nil {
		return nil, err
	}
	var names []string
	for i, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if i == 0 && len(f) > 1 && f[0] == "ID" || len(f) < 2 {
			continue
		}
		names = append(names, f[1])
	}
	return names, nil
}

// giteaFirstRun makes sure Gitea has an administrator nobody can claim
// first (registration is closed in the manifest). An app that already has
// any administrator is left alone; otherwise one is created with a
// generated password that is sealed and revealable. If the password
// cannot be sealed the account is removed again, so a password is never
// lost.
func giteaFirstRun(ctx context.Context, m catalog.Manifest, secrets Secrets) (Result, error) {
	f := m.FirstRun
	if f == nil || m.Name != "gitea" || f.Account != GiteaAdminAccount || secrets == nil {
		return "", errors.New("Gitea first-run configuration is unsupported")
	}
	container := m.Name + "-" + f.Service
	name := catalog.SecretStoreName(m.Name, f.Secret)
	return waitFor(ctx, m.Title, func() (Result, bool, error) {
		// The database is migrated by the server; wait until it answers so
		// the command line never races that migration.
		if _, err := execIn(ctx, container, "", "curl", "--silent", "--fail", "--max-time", "5", "--output", "/dev/null", "http://127.0.0.1:3000/api/healthz"); err != nil {
			return "", false, errors.New("Gitea is starting")
		}
		admins, err := giteaAdmins(ctx, container)
		if err != nil {
			return "", true, errors.New("Gitea first run could not list its administrators")
		}
		if len(admins) > 0 {
			return Ready, true, nil
		}
		out, err := giteaCLI(ctx, container, "admin", "user", "create", "--admin", "--username", f.Account, "--email", f.Account+"@localhost.invalid",
			"--random-password", "--random-password-length", "32", "--must-change-password=false")
		match := giteaGenerated.FindStringSubmatch(out)
		if err != nil || match == nil {
			if match != nil {
				_, _ = giteaCLI(ctx, container, "admin", "user", "delete", "--username", f.Account)
			}
			return "", true, errors.New("Gitea refused to create its administrator; inspect Gitea's users before retrying")
		}
		if err := secrets.SetApp(name, match[1]); err != nil {
			_, _ = giteaCLI(ctx, container, "admin", "user", "delete", "--username", f.Account)
			return "", true, errors.New("Gitea administrator was not kept because its password could not be sealed")
		}
		return Created, true, nil
	})
}
