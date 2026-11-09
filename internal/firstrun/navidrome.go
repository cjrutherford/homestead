package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"homestead/internal/catalog"
)

// NavidromeAdminAccount is the local administrator Homestead creates. It is
// a name no Authentik username is likely to use: the forward-auth plan
// refuses a person whose username matches an existing account.
const NavidromeAdminAccount = "homestead-admin"

// navidromeAdminRow matches a `navidrome user list` row of an administrator:
// the admin column is followed by the created-at timestamp.
var navidromeAdminRow = regexp.MustCompile(`,true,\d{4}-\d\d-\d\dT`)

// navidromeWget talks to the pinned image's busybox wget on loopback. The
// request body (it holds the password) goes through stdin, never argv.
//
// VERIFIED AGAINST Navidrome 0.64.2: GET /ping answers once the server is
// up; POST /auth/createAdmin (JSON {username,password}) works only while the
// user table is empty (403 afterwards) and creates an administrator, the
// call its first-visit screen makes; `navidrome user list` prints a CSV row
// per account with the admin flag; `navidrome user create` needs a terminal
// for its password, so it cannot be used here; busybox wget supports
// --post-file /dev/stdin.
func navidromeWget(ctx context.Context, container, body, path string) (string, error) {
	url := "http://127.0.0.1:4533" + path
	if body == "" {
		return execIn(ctx, container, "", "wget", "-q", "-O", "-", "-T", "10", url)
	}
	return execIn(ctx, container, body, "wget", "-q", "-O", "-", "-T", "10", "--header", "Content-Type: application/json", "--post-file", "/dev/stdin", url)
}

// navidromeFirstRun makes sure Navidrome has an administrator nobody can
// claim first. An app that already has any administrator is left alone;
// otherwise one is created with a generated password that is sealed first
// (revealable), so it is never lost; a sealed password whose account was
// not created is reused on retry.
func navidromeFirstRun(ctx context.Context, m catalog.Manifest, secrets Secrets) (Result, error) {
	f := m.FirstRun
	if f == nil || m.Name != "navidrome" || f.Account != NavidromeAdminAccount || secrets == nil {
		return "", errors.New("Navidrome first-run configuration is unsupported")
	}
	container := m.Name + "-" + f.Service
	store := catalog.SecretStoreName(m.Name, f.Secret)
	return waitFor(ctx, m.Title, func() (Result, bool, error) {
		if _, err := navidromeWget(ctx, container, "", "/ping"); err != nil {
			return "", false, errors.New("Navidrome is starting")
		}
		users, err := execIn(ctx, container, "", "/app/navidrome", "user", "list")
		if err != nil {
			return "", true, errors.New("Navidrome first run could not list its users")
		}
		if navidromeAdminRow.MatchString(users) {
			return Ready, true, nil
		}
		password, err := secrets.Get(store)
		if err != nil || password == "" {
			if password, err = generatePassword(); err != nil {
				return "", true, err
			}
			if err := secrets.SetApp(store, password); err != nil {
				return "", true, errors.New("Navidrome administrator was not created because its password could not be sealed")
			}
		}
		body, _ := json.Marshal(map[string]string{"username": f.Account, "password": password})
		out, err := navidromeWget(ctx, container, string(body), "/auth/createAdmin")
		if err != nil || !strings.Contains(out, `"isAdmin":true`) {
			return "", true, errors.New("Navidrome refused to create its administrator; inspect Navidrome's users before retrying")
		}
		return Created, true, nil
	})
}
