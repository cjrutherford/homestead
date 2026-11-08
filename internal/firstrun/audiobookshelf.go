package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"homestead/internal/catalog"
)

// The pinned Audiobookshelf release exposes /status, /init and /login on
// loopback. Do not print responses: /login can contain session tokens.
// Input reaches node through stdin; the server's existing root is never changed.
const audiobookshelfScript = `
const input = JSON.parse(require('fs').readFileSync(0, 'utf8'));
const out = (state) => console.log(JSON.stringify({state}));
const base = 'http://127.0.0.1:80';
const options = { signal: AbortSignal.timeout(5000) };
(async () => {
  const response = await fetch(base + '/status', options);
  if (!response.ok) return out('waiting');
  const status = await response.json();
  if (status.app !== 'audiobookshelf' || status.serverVersion !== '2.37.1' || typeof status.isInit !== 'boolean') return out('failed');
  if (!input.initialize) return out(status.isInit ? 'ready' : 'uninitialized');
  if (status.isInit) return out('conflict');
  const init = await fetch(base + '/init', { ...options, method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({newRoot:{username:input.account,password:input.password}}) });
  if (!init.ok) return out('failed');
  const login = await fetch(base + '/login', { ...options, method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({username:input.account,password:input.password}) });
  if (!login.ok) return out('failed');
  const verified = await fetch(base + '/status', options);
  if (!verified.ok || !(await verified.json()).isInit) return out('failed');
  return out('created');
})().catch(() => out('waiting'));
`

// The libraries step signs in as Homestead's own recovery administrator
// (pinned 2.37.1: /login with x-return-tokens answers an accessToken) and
// adds each declared library the server lacks, matched by name. Audiobookshelf
// scans a new library at once, and a library is open to every user unless
// restricted. A sign-in that fails means the administrator is the owner's
// own: nothing is touched. Tokens are never printed.
const audiobookshelfLibrariesScript = `
const input = JSON.parse(require('fs').readFileSync(0, 'utf8'));
const out = (state) => console.log(JSON.stringify({state}));
const base = 'http://127.0.0.1:80';
const options = (extra) => ({ signal: AbortSignal.timeout(8000), ...extra });
const json = {'Content-Type':'application/json'};
(async () => {
  const login = await fetch(base + '/login', options({ method: 'POST', headers: {...json, 'x-return-tokens':'true'}, body: JSON.stringify({username:input.account,password:input.password}) }));
  if (!login.ok) return out('skipped');
  const token = ((await login.json()).user || {}).accessToken;
  if (!token) return out('failed');
  const auth = {...json, Authorization: 'Bearer ' + token};
  const list = await fetch(base + '/api/libraries', options({ headers: auth }));
  if (!list.ok) return out('failed');
  const have = new Set(((await list.json()).libraries || []).map((l) => l.name));
  for (const lib of input.libraries) {
    if (have.has(lib.name)) continue;
    const body = {name: lib.name, folders: [{fullPath: lib.path}], mediaType: lib.kind === 'podcasts' ? 'podcast' : 'book', icon: lib.kind === 'podcasts' ? 'podcast' : 'audiobookshelf', provider: lib.kind === 'podcasts' ? 'itunes' : 'audible'};
    const made = await fetch(base + '/api/libraries', options({ method: 'POST', headers: auth, body: JSON.stringify(body) }));
    if (!made.ok) return out('failed');
  }
  return out('ok');
})().catch(() => out('failed'));
`

func audiobookshelfLibraries(ctx context.Context, container, account, password string, libs []catalog.Library) error {
	if len(libs) == 0 {
		return nil
	}
	input, _ := json.Marshal(map[string]any{"account": account, "password": password, "libraries": libs})
	raw, err := execIn(ctx, container, string(input), "node", "-e", audiobookshelfLibrariesScript)
	if err != nil {
		return errors.New("Audiobookshelf could not add its libraries: " + strings.ReplaceAll(err.Error(), password, "[redacted]"))
	}
	var reply struct{ State string }
	if json.Unmarshal([]byte(lastLine(raw)), &reply) != nil || (reply.State != "ok" && reply.State != "skipped") {
		return errors.New("Audiobookshelf could not add its libraries; sign in as its administrator and add them, or try again")
	}
	return nil
}

func audiobookshelfStep(ctx context.Context, container, account, password string, initialize bool) (Result, bool, error) {
	input, _ := json.Marshal(map[string]any{"account": account, "password": password, "initialize": initialize})
	raw, err := execIn(ctx, container, string(input), "node", "-e", audiobookshelfScript)
	if password != "" {
		raw = strings.ReplaceAll(raw, password, "[redacted]")
	}
	if err != nil {
		detail := err.Error()
		if password != "" {
			detail = strings.ReplaceAll(detail, password, "[redacted]")
		}
		return "", false, errors.New(detail)
	}
	var reply struct{ State string }
	if json.Unmarshal([]byte(lastLine(raw)), &reply) != nil {
		return "", false, errors.New("Audiobookshelf returned an unexpected first-run response")
	}
	switch reply.State {
	case "ready":
		if initialize {
			return "", true, errors.New("Audiobookshelf was initialized concurrently; use its existing administrator")
		}
		return Ready, true, nil
	case "uninitialized":
		if initialize {
			return "", true, errors.New("Audiobookshelf did not finish initialization")
		}
		return Result("uninitialized"), true, nil
	case "created":
		if !initialize {
			return "", true, errors.New("Audiobookshelf returned an unexpected first-run state")
		}
		return Created, true, nil
	case "failed", "conflict":
		return "", true, errors.New("Audiobookshelf refused first-run setup or its version is unsupported; check the app and retry")
	case "waiting":
		return "", false, errors.New("Audiobookshelf is starting")
	default:
		return "", false, errors.New("Audiobookshelf returned an unknown first-run state")
	}
}

func audiobookshelfFirstRun(ctx context.Context, m catalog.Manifest, secrets Secrets) (Result, error) {
	f := m.FirstRun
	container := m.Name + "-" + f.Service
	status, err := waitFor(ctx, m.Title, func() (Result, bool, error) { return audiobookshelfStep(ctx, container, f.Account, "", false) })
	if err != nil {
		return status, err
	}
	if status == Ready {
		// An administrator exists. Finish the libraries only when it is
		// Homestead's own (its sealed password signs in); never generate
		// a password for someone else's server.
		if v, gerr := secrets.Get(catalog.SecretStoreName(m.Name, f.Secret)); gerr == nil && v != "" {
			if err := audiobookshelfLibraries(ctx, container, f.Account, v, f.Libraries); err != nil {
				return "", err
			}
		}
		return Ready, nil
	}
	password, err := Password(m, secrets)
	if err != nil {
		return "", err
	}
	res, err := waitFor(ctx, m.Title, func() (Result, bool, error) { return audiobookshelfStep(ctx, container, f.Account, password, true) })
	if err != nil {
		return res, err
	}
	if err := audiobookshelfLibraries(ctx, container, f.Account, password, f.Libraries); err != nil {
		return "", err
	}
	return res, nil
}
