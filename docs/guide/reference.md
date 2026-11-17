# Command and page reference

Every `homesteadctl` action has a matching control in the dashboard, and both go through the same API. The only things that exist on the command line alone are `homesteadctl claim token` and `homesteadctl login --print-token`, plus the local `homesteadctl admin recover`. Run any command with no arguments to see its usage.

Global options: `--server URL` (the daemon address, default `http://127.0.0.1:8080`) and `--ca-file ROOT.pem`. Sign in first with `homesteadctl login`.

## Account and session

| Command | What it does | Dashboard page |
|---|---|---|
| `homesteadctl status` | Daemon health | Status |
| `homesteadctl version` | Daemon version | Status |
| `homesteadctl claim` | Claim the appliance with a token | Claim |
| `homesteadctl admin` | Set up, recover, change password, lockout state | Account |
| `homesteadctl login` | Sign in | Login |
| `homesteadctl logout` | Sign out | Account |
| `homesteadctl sso` | Authentik sign-in: status, plan, approve, apply, link, unlink, access, disable | Sign-in |
| `homesteadctl setup` | Setup wizard status, steps, skip and unskip | Setup |
| `homesteadctl secrets` | Secret store key status and re-key | Secrets |

## Apps and people

| Command | What it does | Dashboard page |
|---|---|---|
| `homesteadctl apps` | List, show, plan, install, configure, secrets, sign-in, public, move, setup, uninstall, bundles, mine, names, reach, read-links | Catalog, apps page |
| `homesteadctl people` | List, add, edit, grant, revoke, reset passwords and second factors, disable, enable, remove, adopt, resolve | People |
| `homesteadctl craft` | Craft apps: status, apps, plan, build, trust, files | Craft apps, Craft files |
| `homesteadctl drift` | Review, live, accept, restore, schedule | Drift |
| `homesteadctl data` | Plan and approve moving app data, remove old volumes | Data |

## Network and exposure

| Command | What it does | Dashboard page |
|---|---|---|
| `homesteadctl names` | Local names and the `.lan` resolver | Names |
| `homesteadctl dashboard` | The dashboard's local address | Names |
| `homesteadctl tunnel` | Status, plan, setup, teardown | Tunnel |
| `homesteadctl exposure` | Review, approvals, approve, apply | Exposure |
| `homesteadctl firewall` | Status, plan, apply | Firewall |
| `homesteadctl edge` | Deploy the edge, read and set routes | Edge |
| `homesteadctl tls` | Certificates, issuance, the local CA and the root | Certificates, Trust |

## Storage, backups and updates

| Command | What it does | Dashboard page |
|---|---|---|
| `homesteadctl storage` | Mounts, register, assign, unassign, unregister | Storage |
| `homesteadctl backup` | Destination, policy, schedules, recipients, recovery identity | Backups |
| `homesteadctl scheduler` | Scheduled jobs | Backups |
| `homesteadctl updates` | App updates, platform verify, apply, rollback, trusted keys | Updates |
| `homesteadctl config` | Get, plan, apply, export, import the settings | Config |

## Diagnostics

| Command | What it does | Dashboard page |
|---|---|---|
| `homesteadctl doctor` | All checks; `--live` adds probes | Doctor |
| `homesteadctl docker` | Docker engine health | Doctor |
| `homesteadctl audit` | The audit log | Audit |
| `homesteadctl operations` | List, get and follow running operations | Operations |

## Where the full list lives

The complete route-by-route table is `docs/PARITY.md`, and the flag-by-flag table is `docs/PARITY_FLAGS.md`, both in the Homestead source. A test keeps them in step with the code.
