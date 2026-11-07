// Package firstrun finishes an app's own first-run setup (S318, S319):
// the steps its web installer would otherwise ask the owner for, done by
// first-party code the manifest names. Nextcloud sets itself up from its
// environment (S312); Immich and Jellyfin have no such setting, so
// Homestead drives their own setup APIs once they answer.
//
// Every routine is idempotent: an app already set up, by Homestead or by
// its owner, is left as it is. Passwords travel to the container on
// stdin only, never argv or environment (ADR-021).
package firstrun

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/secret"
)

// Secrets is the secret store slice a routine uses: the account
// password is generated once and kept (catalog app_ secret).
type Secrets interface {
	Get(name string) (string, error)
	SetApp(name, value string) error
}

// Result says what a routine did, for the operation's audit line.
type Result string

const (
	// Created means Homestead created the app's administrator.
	Created Result = "created"
	// Ready means the app was already set up; nothing changed.
	Ready Result = "ready"
)

// Tries and Pause bound the wait for the app to answer after start: an
// app migrating its database on first start takes a minute or two.
var (
	Tries = 60
	Pause = 5 * time.Second
)

// execIn runs a command in a container with stdin, stubbed in tests.
var execIn = func(ctx context.Context, container, stdin string, args ...string) (string, error) {
	argv := append([]string{"exec", "-i", container}, args...)
	cmd := exec.CommandContext(ctx, "docker", argv...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("exec in %s: %w: %s", container, err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// Run finishes m's first-run setup, waiting (bounded) for the app to
// answer. A manifest without a first-run routine returns Ready.
func Run(ctx context.Context, m catalog.Manifest, secrets Secrets) (Result, error) {
	f := m.FirstRun
	if f == nil {
		return Ready, nil
	}
	if f.Routine == catalog.FirstRunAudiobookshelf {
		return audiobookshelfFirstRun(ctx, m, secrets)
	}
	if f.Routine == catalog.FirstRunKomga {
		return komgaFirstRun(ctx, m, secrets)
	}
	if f.Routine == catalog.FirstRunGitea {
		return giteaFirstRun(ctx, m, secrets)
	}
	if f.Routine == catalog.FirstRunBookStack {
		return bookstackFirstRun(ctx, m, secrets)
	}
	password, err := Password(m, secrets)
	if err != nil {
		return "", err
	}
	container := m.Name + "-" + f.Service
	switch f.Routine {
	case catalog.FirstRunImmich:
		return waitFor(ctx, m.Title, func() (Result, bool, error) {
			return immich(ctx, container, f.Account, password)
		})
	case catalog.FirstRunJellyfin:
		j := jellyfinRun{container: container, account: f.Account, password: password, libraries: f.Libraries}
		// The Base URL the package seeds (ADR-044), then the root for an
		// install made before apps moved under a path.
		if p := strings.TrimSuffix(m.AppPath(), "/"); p != "" {
			j.bases = append(j.bases, p)
		}
		j.bases = append(j.bases, "")
		return waitFor(ctx, m.Title, func() (Result, bool, error) { return j.run(ctx) })
	}
	return "", fmt.Errorf("firstrun: unknown routine %q", f.Routine)
}

// Password returns the first-run account's password, generating and
// sealing it the first time.
func Password(m catalog.Manifest, secrets Secrets) (string, error) {
	if m.FirstRun == nil {
		return "", fmt.Errorf("firstrun: %s declares no first run", m.Name)
	}
	if secrets == nil {
		return "", fmt.Errorf("firstrun: %s needs the secret store", m.Name)
	}
	name := catalog.SecretStoreName(m.Name, m.FirstRun.Secret)
	v, err := secrets.Get(name)
	if err == nil && v != "" {
		return v, nil
	}
	if err != nil && !errors.Is(err, secret.ErrNotFound) {
		return "", fmt.Errorf("firstrun: read %s: %w", name, err)
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("firstrun: entropy: %w", err)
	}
	v = base64.RawURLEncoding.EncodeToString(b)
	if err := secrets.SetApp(name, v); err != nil {
		return "", fmt.Errorf("firstrun: seal %s: %w", name, err)
	}
	return v, nil
}

// waitFor retries step while the app does not answer yet.
func waitFor(ctx context.Context, title string, step func() (Result, bool, error)) (Result, error) {
	var last error
	for i := 0; i < Tries; i++ {
		res, answered, err := step()
		if answered {
			return res, err
		}
		last = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(Pause):
		}
	}
	return "", fmt.Errorf("%s did not answer in time to finish its setup: %v", title, last)
}

// immichScript runs in the server container with node (the image's own
// runtime): read {email, password, name} from stdin, create the first
// administrator if Immich has none, and finish the administrator's
// onboarding so the web app opens on its login page. It prints one JSON
// line: {"state": "waiting"|"created"|"ready"|"failed", "detail": ...}.
// When Immich is set up but the login fails, the administrator is the
// owner's own: nothing more is done.
const immichScript = `
const out = (state, detail) => { console.log(JSON.stringify({state, detail: detail || ""})); };
const input = JSON.parse(require("fs").readFileSync(0, "utf8"));
const base = "http://127.0.0.1:2283/api";
const call = async (path, body, token) => {
  const headers = {"content-type": "application/json"};
  if (token) headers.authorization = "Bearer " + token;
  const r = await fetch(base + path, body === undefined ? {headers} : {method: "POST", headers, body: JSON.stringify(body)});
  const text = await r.text();
  let json = null;
  try { json = text ? JSON.parse(text) : null; } catch (e) {}
  return {status: r.status, json};
};
const why = (step, r) => step + ": HTTP " + r.status + (r.json && r.json.message ? " " + JSON.stringify(r.json.message) : "");
(async () => {
  let cfg;
  try { cfg = await call("/server/config"); } catch (e) { return out("waiting", e.message); }
  if (cfg.status !== 200 || !cfg.json) return out("waiting", "server config: HTTP " + cfg.status);
  let state = "ready";
  if (!cfg.json.isInitialized) {
    const r = await call("/auth/admin-sign-up", {email: input.email, password: input.password, name: input.name});
    if (r.status >= 300) return out("failed", why("create the administrator", r));
    state = "created";
  }
  if (!cfg.json.isOnboarded) {
    const l = await call("/auth/login", {email: input.email, password: input.password});
    if (l.status >= 300 || !l.json || !l.json.accessToken) {
      if (state === "created") return out("failed", why("sign in as the new administrator", l));
      return out("ready", "the administrator is the owner's own");
    }
    const o = await call("/system-metadata/admin-onboarding", {isOnboarded: true}, l.json.accessToken);
    if (o.status >= 300) return out("failed", why("finish onboarding", o));
  }
  out(state);
})().catch((e) => out("waiting", e.message));
`

// immichName is the display name of the administrator Homestead creates.
const immichName = "Homestead administrator"

func immich(ctx context.Context, container, account, password string) (Result, bool, error) {
	stdin, _ := json.Marshal(map[string]string{"email": account, "password": password, "name": immichName})
	raw, err := execIn(ctx, container, string(stdin), "node", "-e", immichScript)
	raw = strings.ReplaceAll(raw, password, "[redacted]")
	if err != nil {
		// The container is not running yet, or node failed to start.
		return "", false, errors.New(strings.ReplaceAll(err.Error(), password, "[redacted]"))
	}
	var reply struct{ State, Detail string }
	if err := json.Unmarshal([]byte(lastLine(raw)), &reply); err != nil {
		return "", false, fmt.Errorf("unexpected reply: %s", strings.TrimSpace(raw))
	}
	switch reply.State {
	case "created":
		return Created, true, nil
	case "ready":
		return Ready, true, nil
	case "failed":
		return "", true, fmt.Errorf("Immich refused its first-run setup: %s", reply.Detail)
	}
	return "", false, fmt.Errorf("Immich is starting: %s", reply.Detail)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
