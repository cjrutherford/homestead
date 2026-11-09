package firstrun

import (
	"context"
	"errors"
	"strings"

	"homestead/internal/catalog"
)

// freshrssRoot is the web root of the pinned image.
const freshrssRoot = "/var/www/FreshRSS"

// freshrssCLI runs one of the image's cli/*.php scripts as the web
// server's user, the owner of its data directory.
//
// VERIFIED AGAINST freshrss/freshrss:1.30.4: `cli/do-install.php
// --default-user --auth-type --db-type`, `cli/create-user.php --user
// --password --no-default-feeds`, `cli/list-users.php`,
// `cli/reconfigure.php --default-user`, `cli/delete-user.php --user`;
// the install marker is data/applied_migrations.txt. runuser, not su: su
// takes the scripts' options for its own.
func freshrssCLI(ctx context.Context, container, stdin string, args ...string) (string, error) {
	return execIn(ctx, container, stdin, append([]string{"runuser", "-u", "www-data", "--", "sh", "-c", "cd " + freshrssRoot + " && exec php \"$@\"", "sh"}, args...)...)
}

// freshrssCreateUser creates a user with the password read from stdin: the
// script takes it only as an option, so the shell inside the container
// reads it from its own stdin and the password is never an argument of
// anything Homestead runs.
func freshrssCreateUser(ctx context.Context, container, user, password string) error {
	_, err := execIn(ctx, container, password,
		"runuser", "-u", "www-data", "--", "sh", "-c",
		"cd "+freshrssRoot+" && exec php cli/create-user.php --no-default-feeds --user \"$1\" --password \"$(cat)\"", "sh", user)
	return err
}

func freshrssUsers(ctx context.Context, container string) ([]string, error) {
	out, err := freshrssCLI(ctx, container, "", "cli/list-users.php")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// freshrssFirstRun installs FreshRSS without its web installer. The
// default user, FreshRSS's administrator, is the owner's Authentik
// username when it is known (sign-in maps preferred_username to the
// user), else the manifest's account. The user's password is generated,
// and sealed and revealable once FreshRSS has accepted it; it is for the
// form login that works while sign-in is off. Idempotent: an installed
// app keeps its users, only a missing owner user is added and made the
// default.
func freshrssFirstRun(ctx context.Context, m catalog.Manifest, secrets Secrets, owner string) (Result, error) {
	f := m.FirstRun
	if f == nil || m.Name != "freshrss" || secrets == nil {
		return "", errors.New("FreshRSS first-run configuration is unsupported")
	}
	user := strings.TrimSpace(owner)
	if user == "" {
		user = f.Account
	}
	container := m.Name + "-" + f.Service
	store := catalog.SecretStoreName(m.Name, f.Secret)
	return waitFor(ctx, m.Title, func() (Result, bool, error) {
		if _, err := execIn(ctx, container, "", "test", "-d", freshrssRoot+"/data"); err != nil {
			return "", false, errors.New("FreshRSS is starting")
		}
		installed := true
		if _, err := execIn(ctx, container, "", "test", "-f", freshrssRoot+"/data/applied_migrations.txt"); err != nil {
			installed = false
		}
		result := Ready
		if !installed {
			if _, err := freshrssCLI(ctx, container, "", "cli/do-install.php", "--default-user", user, "--auth-type", "form", "--db-type", "sqlite"); err != nil {
				return "", true, errors.New("FreshRSS refused to install; inspect its container log before retrying")
			}
			result = Created
		}
		users, err := freshrssUsers(ctx, container)
		if err != nil {
			return "", true, errors.New("FreshRSS first run could not list its users")
		}
		for _, u := range users {
			if strings.EqualFold(u, user) {
				return result, true, nil
			}
		}
		password, err := secrets.Get(store)
		sealed := err == nil && password != ""
		if !sealed {
			password, err = generatePassword()
			if err != nil {
				return "", true, err
			}
		}
		if err := freshrssCreateUser(ctx, container, user, password); err != nil {
			return "", true, errors.New("FreshRSS refused to create its administrator; inspect FreshRSS's users before retrying")
		}
		if !sealed {
			if err := secrets.SetApp(store, password); err != nil {
				_, _ = freshrssCLI(ctx, container, "", "cli/delete-user.php", "--user", user)
				return "", true, errors.New("FreshRSS administrator was not kept because its password could not be sealed")
			}
		}
		if installed {
			// An install made before the owner was known: the owner's
			// user becomes the administrator (the default user).
			if _, err := freshrssCLI(ctx, container, "", "cli/reconfigure.php", "--default-user", user); err != nil {
				return "", true, errors.New("FreshRSS could not make the owner its administrator")
			}
		}
		return Created, true, nil
	})
}
