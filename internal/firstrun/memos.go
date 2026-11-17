package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"homestead/internal/catalog"
)

// MemosAdminAccount is the local administrator Homestead creates. Memos
// makes the first account created in it the administrator and never gives
// the role to anyone who signs in through an identity provider.
const MemosAdminAccount = "homestead-admin"

// memosWget talks to the pinned image's own busybox wget on the loopback
// address. Request bodies (they hold the password) go through stdin, never
// argv.
//
// VERIFIED AGAINST Memos 26.10.0: GET /api/instance/profile answers
// {"needsSetup": true} until the first account exists;
// POST /memos.api.UserService/CreateUser (Connect, JSON) with
// {"user":{"username","password","role":"USER"}} needs no sign-in while
// needsSetup is true and creates the account with role ADMIN;
// busybox wget supports --post-file /dev/stdin.
func memosWget(ctx context.Context, container, body string, path string) (string, error) {
	url := "http://127.0.0.1:5230" + path
	if body == "" {
		return execIn(ctx, container, "", "wget", "-q", "-O", "-", "-T", "10", url)
	}
	return execIn(ctx, container, body, "wget", "-q", "-O", "-", "-T", "10", "--header", "Content-Type: application/json", "--post-file", "/dev/stdin", url)
}

// memosFirstRun makes sure Memos has an administrator nobody can claim
// first. If the app no longer needs setup (an account exists) it is left
// alone. Otherwise the administrator is created with a generated password
// that is sealed first (revealable), so a password is never lost; a
// sealed password whose account was not created is reused on retry.
func memosFirstRun(ctx context.Context, m catalog.Manifest, secrets Secrets) (Result, error) {
	f := m.FirstRun
	if f == nil || m.Name != "memos" || f.Account != MemosAdminAccount || secrets == nil {
		return "", errors.New("Memos first-run configuration is unsupported")
	}
	container := m.Name + "-" + f.Service
	store := catalog.SecretStoreName(m.Name, f.Secret)
	return waitFor(ctx, m.Title, func() (Result, bool, error) {
		out, err := memosWget(ctx, container, "", "/api/instance/profile")
		var profile struct {
			NeedsSetup *bool `json:"needsSetup"`
		}
		if err != nil || json.Unmarshal([]byte(out), &profile) != nil {
			return "", false, errors.New("Memos is starting")
		}
		if profile.NeedsSetup == nil || !*profile.NeedsSetup {
			return Ready, true, nil
		}
		password, err := secrets.Get(store)
		if err != nil || password == "" {
			if password, err = generatePassword(); err != nil {
				return "", true, err
			}
			if err := secrets.SetApp(store, password); err != nil {
				return "", true, errors.New("Memos administrator was not created because its password could not be sealed")
			}
		}
		body, _ := json.Marshal(map[string]any{"user": map[string]string{"username": f.Account, "password": password, "role": "USER"}})
		out, err = memosWget(ctx, container, string(body), "/memos.api.UserService/CreateUser")
		if err != nil || !strings.Contains(out, `"role":"ADMIN"`) {
			return "", true, errors.New("Memos refused to create its administrator; inspect Memos's users before retrying")
		}
		return Created, true, nil
	})
}
