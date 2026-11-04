# Testing report

Dated 2026-11-03 at commit `905773d`. Companion: `docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md` (the live walkthrough that closes the gaps below) and `docs/reviews/EVERYTHING_REVIEW_PLAN.md`.

## Test layers

| Layer | Where | What it proves |
|---|---|---|
| Go unit and integration | `go test ./internal/... ./cmd/...` | Daemon logic, planners, SSO adapters (`internal/appsso`), catalog loading, craft build, Everything bridge, backups |
| pytest | `tests/` (package, catalog, docs, slice contract tests) | YAML packages parse, pins and version tests, docs/format, one test file per slice |
| Parity test | `tests/test_s037_parity.py` with `docs/PARITY.md` | Every API route, CLI command and dashboard surface is listed |
| Dashboard specs | `dashboard/` (83 spec.ts files) | Angular components and services against mocks |
| Everything gate | `scripts/everything/gate/*` (`bridge-gate.test.js`) | The bridge's contract: auth, scoping, quota, sharing, SSE, read links, net fence |
| Headless-Chrome browser checks | `scripts/everything/gate/browser-check-*.mjs` with `run_browser_check_*.sh` | The built app images behave in a real browser against the bridge (not against Authentik) |
| Upstream suites | run during packaging of each Everything app | The upstream apps still pass on the patched tree |

## Results at 905773d (2026-11-03)

- Go: `go test ./internal/... ./cmd/...` passes.
- pytest: 945 passed.
- Everything gate bridge tests: 13/13.
- Dashboard: 1171 of 1172 pass. The one failure is the actual-budget curated first-login copy in `dashboard/src/app/catalog/curated-copy.spec.ts`.
- Upstream suites: Contacts 313, Inventory 722, Time tracking 205, Circuits 206/207 (patched), Parametric 104, 3D 3,018 (5 skipped), Solitaire 209, Sudoku 333, Expense 79, API Client 679, Records 445, Notes 275. Tasks was 172/235 against the bridge at S367 chunk 2 (final count not recorded). Projects count not recorded.

## Verified live on VM 2, and what never was

Verified or partly verified (from the memory notes; VM 2 last known at ee51984 for Wave 3, later 32c45d6 for path apps):
- Authentik sign-in redirect with PKCE for Nextcloud, Jellyfin and Immich; Mealie public SSO redirect.
- Vaultwarden 1.37.4 with sign-in applied; CWA, Gitea, Audiobookshelf redirects; Navidrome gate; Kuma login-off.
- BookStack live-checked; all 13 craft apps validated (S382).
- Tunnel setup, public dashboard sign-in and People step-up (ADR-046/049) were verified at their slices.

Never verified live:
- All 14 Everything apps beyond the S367 spike, and every ADR-058 feature (sharing, read links, events, net fence, purge).
- A real browser sign-in for most newer server apps (Vaultwarden, CWA, ABS token exchange, Actual, Memos, Vikunja, Navidrome).
- FreshRSS, Paperless, Linkding and Komga were not installed on VM 2 at all.
- The ADR-043 People live check; Nightly backups across a restore of a real app; update flows for pinned DB-write apps.
- Firefox and Safari behaviour of the share dialogs.

## Per-app table

| Group | Package | Sign-in | Access path | Automated coverage | Live on VM 2 | Known issues |
|---|---|---|---|---|---|---|
| Server | actual-budget | OIDC via Authentik (env-oidc) | actual-budget.lan/.local (own name) | Go internal/appsso/actual-budget*_test.go, internal/catalog tests, tests/ for S358 | Partially: installed with sign-in on (wave3-plan memory); browser login pending | Dashboard curated-copy test fails. Bank sync unconfigured (DEFERRED S358). |
| Server | audiobookshelf | DB-write sign-in (app DB) | audiobookshelf.lan/.local (own name) | Go internal/appsso/audiobookshelf*_test.go, internal/catalog tests, tests/ for S353 | Partially: installed, redirect verified, token exchange not done (S417 memory) | Userinfo logging accepted by owner; pin test requires re-check per update. |
| Server | bookstack | OIDC via Authentik (env-oidc) | bookstack.lan/.local (own name) | Go internal/appsso/bookstack*_test.go, internal/catalog tests, tests/ for S366 | Verified: live-checked (d836c73, wave3-plan memory) | - |
| Server | calibre-web-automated | DB-write sign-in (app DB) | calibre-web-automated.lan/.local (own name) | Go internal/appsso/calibre-web-automated*_test.go, internal/catalog tests, tests/ for S356 | Partially: sign-in applied, 302 to Authentik, no browser login (S415 memory) | Pin test requires re-check per update; plaintext client secret in app DB. |
| Server | freshrss | Authentik only via Apache mod_auth_openidc | freshrss.lan/.local (own name) | Go internal/appsso/freshrss*_test.go, internal/catalog tests, tests/ for S348 | Not yet: not installed on VM 2 as of 2026-11-01 | Form login stops while sign-in on (owner exception). |
| Server | gitea | DB-write sign-in (app DB) | gitea.lan/.local (own name) | Go internal/appsso/gitea*_test.go, internal/catalog tests, tests/ for S347 | Partially: installed at gitea.lan, redirect verified (S416 memory) | Pin test requires re-check per update (S416). |
| Server | immich | OIDC via Authentik (Immich API adapter) | immich.lan/.local (own name) | Go internal/appsso/immich*_test.go, internal/catalog tests, tests/ for S171 | Partially: redirect to Authentik with PKCE (s298 memory) | Own name by design (cannot run under a path). |
| Server | jellyfin | OIDC via Authentik (pinned SSO plugin, ADR-039) | /apps/jellyfin/ | Go internal/appsso/jellyfin*_test.go, internal/catalog tests, tests/ for S160 | Partially: redirect to Authentik with PKCE (s298 memory) | - |
| Server | komga | OIDC via Authentik (env-oidc) | komga.lan/.local (own name) | Go internal/appsso/komga*_test.go, internal/catalog tests, tests/ for S355 | Not yet: not installed on VM 2 as of 2026-11-01 | - |
| Server | linkding | OIDC via Authentik (env-oidc) | linkding.lan/.local (own name) | Go internal/appsso/linkding*_test.go, internal/catalog tests, tests/ for S349 | Not yet: not installed on VM 2 as of 2026-11-01 | - |
| Server | mealie | OIDC via Authentik (env-oidc) | mealie.lan/.local (own name) | Go internal/appsso/mealie*_test.go, internal/catalog tests, tests/ for S350 | Partially: public SSO redirect works (memory); sign-in as a person on /apps path apps noted in vm2-state | - |
| Server | memos | OIDC via Authentik (env-oidc) | memos.lan/.local (own name) | Go internal/appsso/memos*_test.go, internal/catalog tests, tests/ for S359 | Partially: installed with sign-in on; browser login pending | - |
| Server | navidrome | Forward-auth (ADR-055) | navidrome.lan/.local (own name) | Go internal/appsso/navidrome*_test.go, internal/catalog tests, tests/ for S354 | Partially: gate verified (S418/S420); browser checks pending | Admin chosen inside Navidrome; `/rest/*` and `/share/*` bypass the gate. |
| Server | nextcloud | OIDC via Authentik (user_oidc) | /apps/nextcloud/ | Go internal/appsso/nextcloud*_test.go, internal/catalog tests, tests/ for S170 | Partially: redirect to Authentik with PKCE (s298 memory) | - |
| Server | paperless-ngx | OIDC via Authentik (env-oidc) | paperless-ngx.lan/.local (own name) | Go internal/appsso/paperless-ngx*_test.go, internal/catalog tests, tests/ for S346 | Not yet: not installed on VM 2 as of 2026-11-01 | - |
| Server | uptime-kuma | Forward-auth (ADR-055) | uptime-kuma.lan/.local (own name) | Go internal/appsso/uptime-kuma*_test.go, internal/catalog tests, tests/ for S351 | Partially: installed, login-off verified (S419/S422) | Memory notes disagree on Kuma login (see Contradictions). |
| Server | vaultwarden | OIDC via Authentik (env-oidc) | vaultwarden.lan/.local (own name) | Go internal/appsso/vaultwarden*_test.go, internal/catalog tests, tests/ for S345 | Partially: updated to 1.37.4, sign-in applied; real browser login not done | - |
| Server | vikunja | OIDC via Authentik (env-oidc) | vikunja.lan/.local (own name) | Go internal/appsso/vikunja*_test.go, internal/catalog tests, tests/ for S357 | Partially: installed with sign-in on; browser login pending | - |
| Everything | tasks | OIDC via Authentik (bridge, ADR-053/057) | /apps/tasks/ | tests/test_s368_tasks_package.py; gate: browser-check.mjs, browser-check-tasks-sharing.mjs; bridge-gate.test.js | Partially: only S367 plain-Compose spike; 172/235 vs bridge at chunk 2 | Sharing/read links/events never run live (S437). 63 sharing failures at S367 chunk 2. |
| Everything | projects | OIDC via Authentik (bridge, ADR-053/057) | /apps/projects/ | tests/test_s369_projects_package.py; gate: browser-check-projects.mjs | Not yet | - |
| Everything | records | OIDC via Authentik (bridge, ADR-053/057) | /apps/records/ | tests/test_s370_records_package.py; gate: browser-check-records.mjs; upstream 445/445 | Not yet | - |
| Everything | notes | OIDC via Authentik (bridge, ADR-053/057) | /apps/notes/ | tests/test_s371_notes_package.py, tests/test_everything_source.py, tests/test_s423_source_offer.py; gate: browser-check-notes.mjs, browser-check-notes-sharing.mjs, browser-check-viewer.mjs, browser-check-source-offer.mjs; upstream 275/275 | Not yet | Source offer page needs live check (S423). |
| Everything | contacts | OIDC via Authentik (bridge, ADR-053/057) | /apps/contacts/ | tests/test_s372_contacts_package.py; gate: browser-check-contacts.mjs; upstream 313/313 (mock bridge) | Not yet | - |
| Everything | inventory | OIDC via Authentik (bridge, ADR-053/057) | /apps/inventory/ | tests/test_s373_inventory_package.py; gate: browser-check-inventory.mjs; upstream 722/722 (mock bridge) | Not yet | - |
| Everything | time-tracking | OIDC via Authentik (bridge, ADR-053/057) | /apps/time-tracking/ | tests/test_s374_time_tracking_package.py; gate: browser-check-time-tracking.mjs; upstream 205/205 (mock bridge) | Not yet | - |
| Everything | circuits | OIDC via Authentik (bridge, ADR-053/057) | /apps/circuits/ | tests/test_s375_circuits_package.py; gate: browser-check-circuits.mjs, browser-check-circuits-sharing.mjs; upstream vitest 206/207 on the patched tree | Not yet | Upstream vitest 206/207 (GitHub-link test); concurrent edits overwrite (DEFERRED S439). |
| Everything | parametric | OIDC via Authentik (bridge, ADR-053/057) | /apps/parametric/ | tests/test_s376_parametric_package.py; gate: browser-check-parametric.mjs, browser-check-parametric-sharing.mjs; upstream 104/104 | Not yet | Unexplained one-time unsafe-eval toast; concurrent edits overwrite (S440). |
| Everything | three-d | OIDC via Authentik (bridge, ADR-053/057) | /apps/three-d/ | tests/test_s377_three_d_package.py; gate: browser-check-three-d.mjs; upstream 3,018 passed | Not yet | Startup race loses early object edits. |
| Everything | solitaire | OIDC via Authentik (bridge, ADR-053/057) | /apps/solitaire/ | tests/test_s378_solitaire_package.py; gate: browser-check-solitaire.mjs; upstream 209 | Not yet | - |
| Everything | sudoku | OIDC via Authentik (bridge, ADR-053/057) | /apps/sudoku/ | tests/test_s379_sudoku_package.py; gate: browser-check-sudoku.mjs; upstream 333 | Not yet | - |
| Everything | expense | OIDC via Authentik (bridge, ADR-053/057) | /apps/expense/ | tests/test_s380_expense_package.py; gate: browser-check-expense.mjs; upstream 79 | Not yet | - |
| Everything | api-client | OIDC via Authentik (bridge, ADR-053/057) | /apps/api-client/ | tests/test_s436_api_client_package.py; gate: browser-check-api-client.mjs, scripts/everything/test_api_client_net.mjs; upstream 679 passed | Not yet | User scripts probably blocked by CSP without a message (S436). |
| Craft | wordcraft | None (class unsupported) | /apps/wordcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S249) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: browser-local. Stateless. |
| Craft | gridcraft | None (class unsupported) | /apps/gridcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S250) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: hooked (open/save to Nextcloud). Stateless. |
| Craft | pdfcraft | None (class unsupported) | /apps/pdfcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S250) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: hooked (open/save to Nextcloud). Stateless. |
| Craft | deckcraft | None (class unsupported) | /apps/deckcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S250) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: browser-local. Stateless. |
| Craft | vectorcraft | None (class unsupported) | /apps/vectorcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S251) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: browser-local. Stateless. |
| Craft | photocraft | None (class unsupported) | /apps/photocraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S251) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: browser-local. Stateless. |
| Craft | lightcraft | None (class unsupported) | /apps/lightcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S251) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: browser-local. Stateless. |
| Craft | figcraft | None (class unsupported) | /apps/figcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S251) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: browser-local. Stateless. |
| Craft | filmcraft | None (class unsupported) | /apps/filmcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S252) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: hooked (open only from Nextcloud). Stateless. |
| Craft | effectcraft | None (class unsupported) | /apps/effectcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S252) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: hooked (open only from Nextcloud). Stateless. |
| Craft | soundcraft | None (class unsupported) | /apps/soundcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S252) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: browser-local. Stateless. |
| Craft | designcraft | None (class unsupported) | /apps/designcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S253) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: browser-local. Stateless. |
| Craft | cadcraft | None (class unsupported) | /apps/cadcraft/ | tests/test_s254_craft_wrapper.py, test_s382_craft_names.py, internal/craft/*_test.go (S253) | Verified: all 13 validated on VM 2 in S382 (244d91d) | Hook: hooked (open/save to Nextcloud). Stateless. |
| Platform | authentik | Is the identity provider | /apps/auth/ locally; auth.<public-domain> on the tunnel | tests for S201-S203, internal/appsso | Verified: used for every live sign-in on VM 2 | - |

## Known failures and open issues

- Dashboard `curated-copy.spec.ts` actual-budget first-login copy fails (1171/1172).
- Circuits upstream vitest 206/207: the "links to the source code on GitHub" test fails because the S375 patch removes the link (DEFERRED S439).
- Parametric: a one-time "Geometry engine failed to start / unsafe-eval" toast seen once; unexplained.
- API Client user scripts are probably blocked by the page CSP without a visible message (DEFERRED S436).
- Concurrent edits overwrite each other in Circuits and Parametric (last write wins; no merge).
- Caddy read-link route is checked as text only; never exercised through a real edge and tunnel.
- Disabling a person does not purge their Everything data; only removal does (DEFERRED S427).
- DB-write sign-in apps (CWA, Gitea, ABS) store the client secret in the app DB and need re-checking after every update.
- 3D: early edits at startup can be lost (startup race). Records, Circuits, Parametric and 3D have no offline page.
- FreshRSS form login is disabled while sign-in is on (owner exception).
- Restore is manual (runbook); no run-now backup control.
- The DEFERRED craft "unsigned" entries are stale; the manifest is signed (counter 8).

## Coverage gaps (no automated test proves these)

- A real Authentik round trip for any app (tests use mocks, redirects or the bridge).
- Real browser behaviour of the dashboard against a live daemon (specs use mocks).
- The public path: Cloudflare tunnel, Let's Encrypt issuance, edge buffering of SSE, 404s on the public name.
- Restore of a backup into a clean appliance; age key recovery.
- Update, rollback and data migration of real app versions.
- Person-versus-person isolation in each server app (closed by default is tested at the Authentik policy level only).
- Camera, drag-and-drop, PWA install and mobile browsers.
- Craft apps with Nextcloud open/save on a real Nextcloud.
- Nightly scheduler at 02:00 over several days.
