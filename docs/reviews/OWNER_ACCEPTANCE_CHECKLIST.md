# Owner acceptance checklist

Run on VM 2 from a **freshly reset appliance** and hand back filled in. Companion documents: `docs/reviews/TESTING_REPORT.md` (what is already tested and what is not) and `docs/reviews/EVERYTHING_REVIEW_PLAN.md` (per-slice detail for the Everything apps; the ADR-058 owner section S441 is folded in below as `EV-ADR058-*`).

## How to run the acceptance

1. Open `docs/reviews/acceptance-form.html` in a browser (double-click it; it works offline, and a copy can be taken to any machine, such as a laptop next to the VM or a phone). It lists every step below with the same IDs.
2. Walk this file in order. For each step choose a result (PASS, FAIL, SKIP, BLOCKED) in the form and add notes. A FAIL or BLOCKED step opens an issue panel (severity, what happened, steps to reproduce, URL, screenshot file name, error text, workaround). **Do not enter passwords, tokens or secrets** in the form or in this file.
3. Answers are saved in the browser as you type. When done (or at the end of each day) press **Export results**: it downloads a Markdown file and a JSON file.
4. Hand both files (and the screenshots they name) to Claude. The JSON can be re-imported into the form to continue a run on another machine.

The form is generated from this file by `python3 -I scripts/review/make_acceptance_form.py`; if this file changes, regenerate it.

## How to use this file

1. **Starting point.** The appliance was factory-reset (`scripts/factory-reset.sh` with all three mounts wiped, then `--reinstall`) and deployed from the current commit by Claude. It is **active and unclaimed**; nothing is installed (no Authentik, no apps, no tunnel, no public DNS records for the zone's Homestead names); `/mnt/app`, `/mnt/bulk` and `/mnt/backup` are empty; the appliance has a **new root CA**, so browsers distrust it until you trust it (PLAT-CLAIM-1a). Every app install below is therefore a **first install**.
2. **Order:** Platform (start, claim, wizard, people, tunnel), server apps (SRV), platform checks that need apps (PLAT-AFTER), Everything apps (EV), craft apps (CRAFT), then cross-cutting END steps. Installs and features on day 1. Backup steps that need the nightly 02:00 run are checked on day 2. END steps last.
3. **Prerequisites:**
   - The claim token Claude handed you (it lasts 30 minutes; if it expired, run `sudo homesteadctl claim token` on the VM for a new one).
   - A Cloudflare API token for the zone you want to use, with the permissions listed in `docs/INSTALL.md` D4 (Zone Read, DNS Edit, Account Cloudflare Tunnel Edit, SSL and Certificates Read), and that zone active on Cloudflare. Keep it in your password manager; paste it only into the Homestead form that asks for it.
   - A password manager for the owner, `akadmin`, person and app passwords.
   - Names for people A, B and C (A and B get every app you test; C is a Homestead person with **no** app access, closed by default, ADR-043).
   - A phone or second browser (not just a private window of the same profile) and a public internet connection.
   - The host reaches the VM by `/etc/hosts` names (`homestead.lan` and the app names) and by `homestead.local` over mDNS.
   - Placeholders: `<server>` is `homestead.lan` or `homestead.local`; `<public-domain>` is your tunnel zone (the public dashboard is `homestead.<public-domain>`); `<lan-suffix>` is `lan` or `local`.
   - Container names are `<package>-<service>`; for a few server apps the service name differs from the package name, so confirm with `docker ps` before a restart step.
4. **Recording:** use the form (or tick the box and fill `Result:` here) with PASS, FAIL, SKIP (not applicable) or BLOCKED (could not get that far; say why). Notes always. For a FAIL write what you saw, the URL and the screenshot filename.
5. **Hand back** the exported files (or this whole file) to Claude.


## Platform

### Starting point

- [ ] **PLAT-DEPLOY-1 — Confirm the starting point**
  - Action: On the VM (SSH): `systemctl is-active homestead-server docker`; `curl -s localhost:8080/api/v1/identity`; `sudo docker ps --format '{{.Names}}'`; `findmnt /mnt/app /mnt/bulk /mnt/backup`; `ls -A /mnt/app /mnt/bulk /mnt/backup`; `ls /opt/homestead/stacks`; `homesteadctl version` and `cat ~/homestead/.deployed-commit` (the commit Claude deployed). In a browser open `https://homestead.local/` (continue past the certificate warning for now).
  - Expected: The daemon is `active`; identity says `UNCLAIMED`, and opening the dashboard takes you to the claim page (S443; the Status page also reads **Unclaimed** with a **Claim this appliance** option); no login screen and no setup wizard. `docker ps` lists only `homestead-caddy` (the edge proxy the daemon starts; no Authentik and no app containers). The three mounts are mounted and empty (only `lost+found` may exist); `/opt/homestead/stacks` holds only the `caddy` edge directory. The Status page version and `homesteadctl version` show `0.0.1` (the release version; the build commit is in `~/homestead/.deployed-commit`, expected `ad89b28`). Write the commit in the form's run info.
  - If it fails: INSTALL Part B and Part F; `journalctl -u homestead-server -n 100`. If anything is installed or claimed, stop and tell Claude: the reset did not complete.
  - Result: ____  Notes: ____

### Claim

- [ ] **PLAT-CLAIM-1 — Claim the appliance with the token**
  - Action: Open `https://homestead.local/` (or `https://homestead.lan/`), choose **Claim this appliance**, paste the claim token and create the owner account with a password from your password manager (INSTALL Part C). After you are signed in, try to claim again (reload the claim page or `homesteadctl claim ...` with the same token).
  - Expected: Claim succeeds; you are signed in and the setup wizard opens; a second claim attempt is refused.
  - If it fails: INSTALL Part C. If the token is refused, make a new one with `sudo homesteadctl claim token`.
  - Result: ____  Notes: ____

- [ ] **PLAT-CLAIM-1a — Trust the appliance root certificate**
  - Action: The reset made a **new** appliance CA, so earlier trust is void. Open the **Trust** page (Network group, `/admin/trust`) and download the root certificate; check its SHA-256 fingerprint against the page (or `homesteadctl tls root show`). Install it as trusted on the browser/device you are using, following `docs/architecture/INTERNAL_CA_TRUST.md` for your OS; remove any older Homestead root first. Restart the browser, then open `https://homestead.local/` and `https://homestead.lan/`.
  - Expected: Fingerprints match. Both names open with no certificate warning on a device that trusts the root. Homestead did not install the certificate for you.
  - If it fails: INSTALL C3 and INTERNAL_CA_TRUST.md. Record the OS/browser and whether a stale old root is still trusted.
  - Result: ____  Notes: ____

- [ ] **PLAT-DEPLOY-2 — Catalog shows all packages**
  - Action: Sign in as the owner on the CLI (`homesteadctl login`), then `homesteadctl apps list`, and open the dashboard Catalog page.
  - Expected: 46 packages (18 server, 14 Everything, 13 craft, plus Authentik). Counts match on both.
  - If it fails: Catalog page and CLI disagree: record both counts.
  - Result: ____  Notes: ____

### Setup wizard

- [ ] **PLAT-SETUP-1 — Wizard step 1, Account**
  - Action: The wizard opens on **Account**. On the **Backups** page, under the recovery identity, **export** the file (or `homesteadctl backup identity export --out <file>`), store it off the appliance, and acknowledge the export. Back in the wizard choose **Confirm my password**.
  - Expected: The step reads done; the progress list shows Account, Storage, Sign-in, Domain and access, Apps, Backups. The recovery file is not shown or pasted anywhere.
  - If it fails: INSTALL D1; docs/architecture/SETUP_WALKTHROUGH.md.
  - Result: ____  Notes: ____

- [ ] **PLAT-SETUP-2 — Wizard step 2, Storage**
  - Action: On the Storage step (Storage page) register each mount with a short label (`app` = `/mnt/app`, `bulk` = `/mnt/bulk`, `backup` = `/mnt/backup`) and assign classes: `application` and `database` to `/mnt/app`, `bulk` to `/mnt/bulk`, `backup` to `/mnt/backup`. Compare `homesteadctl storage mounts`, `storage registrations` and `storage assignments`.
  - Expected: All three mounts are listed with free space; no partition or format action exists anywhere; the step is done once `application` is assigned; CLI and page agree.
  - If it fails: INSTALL D2.
  - Result: ____  Notes: ____

- [ ] **PLAT-SETUP-3 — Wizard step 3, Sign-in: install Authentik**
  - Action: On the Sign-in step choose **install Authentik**. Read the requirements check and the plan, approve, and follow the operation to the end (the first start takes a few minutes).
  - Expected: Plan lists ports, volumes and names before approval; afterwards `docker ps` shows the Authentik containers and the app card says running. Authentik is reachable at `https://<server>/apps/auth/`.
  - If it fails: Operations page; `docker logs` for the Authentik containers; INSTALL D3.
  - Result: ____  Notes: ____

- [ ] **PLAT-SETUP-3a — Authentik first run and API token**
  - Action: Open `https://homestead.local/apps/auth/` (Authentik starts its first-run setup only at this address). Create the `akadmin` account with a strong password from your password manager. In Authentik: Admin interface, Directory, Tokens and App passwords, Create; make a token for `akadmin` with intent **API Token** and copy the key.
  - Expected: You can sign in to the Authentik admin interface as `akadmin`; you hold an API token (do not paste it anywhere but the next step).
  - If it fails: INSTALL D3 steps 2 and 3; Authentik container log. Vendor menus may be renamed: record the menu path you actually used.
  - Result: ____  Notes: ____

- [ ] **PLAT-SETUP-4 — Wizard step 3, Sign-in: plan, approve, link owner SSO**
  - Action: On the Sign-in page paste the token, tick **Keep the token, sealed on this appliance, for later changes** (needed for People and for the tunnel to set up public sign-in in one approval, ADR-046), choose **Plan**, read what will be created in Authentik, then **Approve and set up sign-in**. Choose **Link**, sign in to Authentik, return, then sign out of Homestead.
  - Expected: The plan lists what is created; the token field is cleared; after sign-out the login page offers **Sign in with Authentik** and the local password still works as the way back in; the wizard shows Sign-in done.
  - If it fails: INSTALL D3 steps 4 and 5; docs/architecture/DASHBOARD_SSO.md.
  - Result: ____  Notes: ____

- [ ] **PLAT-AUTH-1 — Authentik admin works**
  - Action: Open `https://<server>/apps/auth/` and sign in as `akadmin`; look for the Homestead application/provider and the `homestead-` groups the sign-in plan created.
  - Expected: Admin interface opens.
  - If it fails: Authentik container log.
  - Result: ____  Notes: ____

- [ ] **PLAT-OWNER-1 — Owner sign-in and apps page**
  - Action: Sign in as the owner with **Sign in with Authentik** on `https://<server>/` (apps page; it lists no apps yet) and then `https://<server>/admin`.
  - Expected: Signed in as the owner; apps page shows nothing installed yet; admin opens only for the owner (ADR-050).
  - If it fails: Sign-in page loops: record URL.
  - Result: ____  Notes: ____

### People A, B and C

- [ ] **PLAT-PEOPLE-1 — Add people A, B and C**
  - Action: `homesteadctl people add` (the People page is not offered until the setup wizard finishes; add them by CLI, or return to the People page afterwards): add A, B and C, each with a one-time link, generated password or chosen password (shown once; store it in your password manager). Do not grant any app yet.
  - Expected: Each appears in People and in Authentik (`homestead-` groups only); none has app access.
  - If it fails: docs/INSTALL.md People section; ADR-043/049. If a one-time link is not offered, record the dialog text.
  - Result: ____  Notes: ____

- [ ] **PLAT-OWNER-2 — A person sees only the apps page**
  - Action: In a separate browser sign in as person A at `https://<server>/` and try `https://<server>/admin`.
  - Expected: A reaches the apps page (empty, nothing granted yet) and `/admin` is refused.
  - If it fails: People page grants; ADR-050.
  - Result: ____  Notes: ____

### Domain and access: tunnel and public sign-in

- [ ] **PLAT-SETUP-4a — Wizard step 4, Domain and access**
  - Action: In the wizard Domain step: add your domain to Cloudflare if needed (it must be **Active**), choose **Cloudflare** as provider and **Cloudflare Tunnel** as access, paste your Cloudflare token (it is sealed and the form cleared), enter the public domain and choose **Save domain**.
  - Expected: Domain saved; a local-only suffix (`.lan`, `.local`) would be refused; with a token a domain not in the token's zones is refused; the token field is empty afterwards.
  - If it fails: INSTALL D4; SETUP_WALKTHROUGH.md Domain completion.
  - Result: ____  Notes: ____

- [ ] **PLAT-TUNNEL-1 — Tunnel plan preview**
  - Action: Open the **Tunnel** page (or `homesteadctl tunnel plan`). Name a **new** tunnel (for example `homestead-vm2`); the form proposes `homestead.<public-domain>` for the dashboard and `homestead.<public-domain>/apps/auth/` for Authentik (the same name as the dashboard; keep it). Choose **Review plan** and read it.
  - Expected: Plan lists the tunnel to create, each CNAME (none exists yet), the Let's Encrypt certificate, the public dashboard sign-in change and each affected app; certificate lamps for each name; nothing changes before approval.
  - If it fails: Doctor Tunnel and Certificates sections; ADR-046/047/048.
  - Result: ____  Notes: ____

- [ ] **PLAT-TUNNEL-2 — Approve by digest**
  - Action: Choose **Approve and set up** (or `tunnel approve --digest`) and follow the operation to the end.
  - Expected: The appliance runs the steps itself (DNS, connector, certificate, sign-in); progress shows each step; `systemctl is-active homestead-tunnel` is active; audit shows `tunnel.*` and `tunnel.sign-in` entries. No separate Sign-in re-plan was needed.
  - If it fails: Doctor Tunnel and Certificates sections; ADR-046/047/048.
  - Result: ____  Notes: ____

- [ ] **PLAT-TUNNEL-3 — Public dashboard sign-in**
  - Action: From a phone off the LAN open `https://homestead.<public-domain>/` and sign in as the owner.
  - Expected: You are sent to Authentik's public sign-in at `https://homestead.<public-domain>/apps/auth/` (the public name; locally Authentik stays at `https://<server>/apps/auth/`); a password login on the Homestead name is refused; after sign-in the apps page shows.
  - If it fails: Doctor Tunnel and Certificates sections; ADR-046/047/048.
  - Result: ____  Notes: ____

- [ ] **PLAT-TUNNEL-4 — Public admin is gated**
  - Action: On the public name open `/admin` as the owner, then as person A.
  - Expected: Allowed only after Authentik sign-in; a person is refused.
  - If it fails: Doctor Tunnel and Certificates sections; ADR-046/047/048.
  - Result: ____  Notes: ____

- [ ] **PLAT-TUNNEL-6 — Stop-on-drift and resume**
  - Action: Only if a step failed or the Tunnel page shows drift: use Resume and read what it says. Otherwise SKIP.
  - Expected: It stops on failure, never retries blindly, and resumes only after approval.
  - If it fails: Doctor Tunnel and Certificates sections; ADR-046/047/048.
  - Result: ____  Notes: ____

- [ ] **PLAT-PEOPLE-5 — Public step-up**
  - Action: On the public name open People as the owner (the page itself loads), then take an action there: reset a password or MFA, or remove a person.
  - Expected: Opening People does not prompt; the action makes Authentik ask for a fresh sign-in (step-up on the action, by design, ADR-049).
  - If it fails: docs/INSTALL.md People section; ADR-043/049.
  - Result: ____  Notes: ____

- [ ] **PLAT-PEOPLE-6 — Forgot password**
  - Action: As A on the public sign-in page click forgot password.
  - Expected: A page tells A to ask the owner (flow title hint); no email recovery.
  - If it fails: docs/INSTALL.md People section; ADR-043/049.
  - Result: ____  Notes: ____

### Remaining wizard steps and first look

- [ ] **PLAT-SETUP-4b — Wizard step 4, finish Domain; step 5, Apps**
  - Action: Back in the wizard choose **I have finished here** on Domain and access. On the Apps step acknowledge it without installing anything (apps are installed in the server-app sections below).
  - Expected: Both steps read done; Next became enabled only when each was done or skipped.
  - If it fails: SETUP_WALKTHROUGH.md; the Domain step explains what is still missing if it will not finish.
  - Result: ____  Notes: ____

- [ ] **PLAT-SETUP-5 — Wizard step 6, Backups**
  - Action: On the Backups step read the proposed nightly policy (02:00 local, keep 7) and the apps it covers and leaves out; adjust if you like and **Approve**. Backups go to the `backup`-class disk.
  - Expected: Policy is saved and visible on the Backups page.
  - If it fails: INSTALL D6.
  - Result: ____  Notes: ____

- [ ] **PLAT-SETUP-6 — Wizard summary and finish**
  - Action: Read the summary (each step's outcome) and finish.
  - Expected: You land on the dashboard; the wizard does not return.
  - If it fails: INSTALL D6.
  - Result: ____  Notes: ____

- [ ] **PLAT-DASH-1 — Dashboard pages load**
  - Action: Click through every admin page by its menu entry: Status, Setup, Catalog, Craft apps, Craft files, Updates, Data, Edge, Exposure, Tunnel, Names, Certificates, Trust, Firewall, Storage, Backups, Configuration, Doctor, Drift, Secrets, People, Sign-in, Operations, Audit, Account, Advanced.
  - Expected: Each loads without error.
  - If it fails: docs/PARITY.md.
  - Result: ____  Notes: ____

- [ ] **PLAT-DASH-2 — Dashboard/CLI parity spot check**
  - Action: Compare `homesteadctl apps list`, `people list`, `backups list`, `audit list` with the matching pages (nothing is installed yet; People and Audit have entries).
  - Expected: Same data in both. Also check `docs/PARITY.md` lists any control you notice missing from one side.
  - If it fails: docs/PARITY.md.
  - Result: ____  Notes: ____

- [ ] **PLAT-DOCTOR-1 — Doctor page and live probes**
  - Action: Open Doctor and run `homesteadctl doctor --live` on the near-empty appliance.
  - Expected: Sections System, Storage, Network, Certificates, Tunnel, Sign-in, Apps; guidance only, nothing is changed; page and CLI match; no red items (record amber ones with their guidance).
  - If it fails: Record any red or amber item and its guidance text.
  - Result: ____  Notes: ____

- [ ] **PLAT-NAMES-1 — Names resolve**
  - Action: From a second device on the LAN open `https://homestead.lan/` and `https://homestead.local/` (app names are checked in each app section).
  - Expected: Both resolve; no certificate warning on a device that trusts the root (PLAT-CLAIM-1a), the documented warning on one that does not.
  - If it fails: Network page, Doctor.
  - Result: ____  Notes: ____

- [ ] **PLAT-CATALOG-1 — Plan preview before every install**
  - Action: Start an install from Catalog (any app) and read the plan before approving; cancel once.
  - Expected: Plan lists ports, names, volumes, exposure; cancelling changes nothing.
  - If it fails: 
  - Result: ____  Notes: ____

- [ ] **PLAT-BACKUP-1 — Backup policy visible**
  - Action: Open Backups / `homesteadctl backup schedules` (and `backup destination`).
  - Expected: Nightly 02:00, keep 7, target mount shown.
  - If it fails: docs/architecture/BACKUP_RESTORE_RUNBOOK.md.
  - Result: ____  Notes: ____

- [ ] **PLAT-BACKUP-2 — Export the recovery identity**
  - Action: Done in PLAT-SETUP-1; confirm the file is stored off the appliance (do not paste it anywhere), and that `homesteadctl backup identity export --out <file>` writes a file with mode 0600.
  - Expected: File is written; do not paste it anywhere.
  - If it fails: docs/architecture/BACKUP_RESTORE_RUNBOOK.md.
  - Result: ____  Notes: ____

- [ ] **PLAT-UPDATE-1 — Updates list**
  - Action: `homesteadctl updates list` and the Updates page (no apps yet; platform updates only).
  - Expected: Same list in both; each item states whether a backup is made first.
  - If it fails: 
  - Result: ____  Notes: ____

- [ ] **PLAT-UPDATE-3 — Platform verify**
  - Action: Platform update verify (dashboard or CLI) without applying.
  - Expected: Verify reports OK or the reason; rollback option is shown.
  - If it fails: 
  - Result: ____  Notes: ____


## Server apps


### SRV-ACTUAL-BUDGET: Actual Budget

_Known: bank-sync providers are not configured (DEFERRED S358)._

- [ ] **SRV-ACTUAL-BUDGET-1 — Install: plan preview, then approve**
  - Action: Catalog > Actual Budget > Install. Read the plan, then approve (or `homesteadctl apps plan actual-budget -f FILE`, then `homesteadctl apps install actual-budget -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-ACTUAL-BUDGET-1a — You are already the server owner**
  - Action: Homestead made you the Actual server owner at install, under your Authentik username, so there is no race to sign in first. Sign in through Authentik as the owner, then as a person with access.
  - Expected: You are the Actual server owner whoever signs in first; later people get ordinary accounts. There is no first-run screen; a new user may be offered to create a budget file, which is normal use.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-ACTUAL-BUDGET-2 — Opens at the right path or name**
  - Action: Open https://actual-budget.<lan-suffix>/ (own name; cannot run under a path).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-ACTUAL-BUDGET-3 — Owner sign-in**
  - Action: Sign in with the Authentik/OpenID button on the app's login page as the owner. Actual's login page offers OpenID (Authentik); env-oidc adapter, set by environment variables. Once the app is published, start from its public name: Actual accepts one sign-in address, and the .lan name shows "Invalid redirect URL" (DEFERRED S447).
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-ACTUAL-BUDGET-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-ACTUAL-BUDGET-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-ACTUAL-BUDGET-6 — Create a budget**
  - Action: Sign in, choose to create a new budget file, add an account with a starting balance and one transaction in a category.
  - Expected: The budget opens, the account balance and the transaction show; reload keeps them.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-ACTUAL-BUDGET-7 — Data persists across restart**
  - Action: Note what exists (the budget, account and transaction). Run `docker restart actual-budget-actual-budget`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-ACTUAL-BUDGET-8 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/actual-budget/<UTC timestamp>/backup.json`. Contents cover `data/` (includes `server-files/account.sqlite`, user files).
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-ACTUAL-BUDGET-9 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Actual Budget, then open https://actual-budget.<lan-suffix>/ (own name; cannot run under a path) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-AUDIOBOOKSHELF: Audiobookshelf

_DB-write sign-in: re-verify after EVERY Audiobookshelf update (pin test TestAudiobookshelfSignInPinnedToVerifiedVersion). Client secret is stored in plaintext in absdatabase.sqlite (accepted). Browser token exchange has not been recorded live._

- [ ] **SRV-AUDIOBOOKSHELF-1 — Install: plan preview, then approve**
  - Action: Catalog > Audiobookshelf > Install. Read the plan, then approve (or `homesteadctl apps plan audiobookshelf -f FILE`, then `homesteadctl apps install audiobookshelf -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-AUDIOBOOKSHELF-2 — Opens at the right path or name**
  - Action: Open https://audiobookshelf.<lan-suffix>/ (own name; the web UI lives under /audiobookshelf inside the app).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-AUDIOBOOKSHELF-3 — Owner sign-in**
  - Action: Open the app, use the Authentik/OpenID sign-in option as the owner. Login page offers OpenID; DB-write sign-in (Homestead writes the app's SQLite settings, ABS 2.37.1 pinned). First sign-in logs the person's name, email and groups at info level (owner accepted).
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-AUDIOBOOKSHELF-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-AUDIOBOOKSHELF-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-AUDIOBOOKSHELF-6 — Add a library and play**
  - Action: Create a library over a folder on bulk storage (the install asks for the media directory), put one short audio file in it, scan, open the item and press play.
  - Expected: The item appears after the scan and audio plays; progress is saved when you stop.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-AUDIOBOOKSHELF-7 — Recovery admin still works**
  - Action: Reveal the app's recovery administrator password (Manage > Secrets > Reveal, step-up may ask you to sign in again) and sign in with the local username `homestead-admin` on the login page.
  - Expected: Local login works after sign-in was applied. Do not write the password in Notes.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-AUDIOBOOKSHELF-8 — Re-check sign-in after an update**
  - Action: After any update of this package run `homesteadctl updates apply audiobookshelf --backup` (or note the version change) and sign in again.
  - Expected: Sign-in still works; the version pin test forces this re-check because the sign-in writes the app database.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-AUDIOBOOKSHELF-9 — Data persists across restart**
  - Action: Note what exists (the library, the played item and listening progress). Run `docker restart audiobookshelf-audiobookshelf`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-AUDIOBOOKSHELF-10 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/audiobookshelf/<UTC timestamp>/backup.json`. Contents cover `config/` (absdatabase.sqlite) and `metadata/`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-AUDIOBOOKSHELF-11 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Audiobookshelf, then open https://audiobookshelf.<lan-suffix>/ (own name; the web UI lives under /audiobookshelf inside the app) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-BOOKSTACK: BookStack

_Browser sign-ins have not been recorded yet._

- [ ] **SRV-BOOKSTACK-1 — Install: plan preview, then approve**
  - Action: Catalog > BookStack > Install. Read the plan, then approve (or `homesteadctl apps plan bookstack -f FILE`, then `homesteadctl apps install bookstack -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-BOOKSTACK-2 — Opens at the right path or name**
  - Action: Open https://bookstack.<lan-suffix>/ (own name; cannot run under a path).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-BOOKSTACK-3 — Owner sign-in**
  - Action: Sign in with the Authentik/OpenID button on the app's login page as the owner. Login page offers the OIDC button (Authentik); roles come from Authentik groups; LinuxServer image with a MariaDB service.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-BOOKSTACK-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-BOOKSTACK-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-BOOKSTACK-6 — Write a page**
  - Action: Sign in, create a Shelf, a Book, a Chapter and a Page with a heading, a list and an image; save.
  - Expected: The page renders with the image; the page is found by search.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-BOOKSTACK-7 — Default admin refused**
  - Action: Try to sign in to the local form as `admin@admin.com` with the well-known default password (do not use the generated one).
  - Expected: Refused: the default administrator password was replaced at install.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-BOOKSTACK-8 — Data persists across restart**
  - Action: Note what exists (the page and its image). Run `docker restart bookstack-bookstack`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-BOOKSTACK-9 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/bookstack/<UTC timestamp>/backup.json`. Contents cover `config/` plus a MariaDB dump (the first nightly dump was due 02:00 UTC 2026-11-02).
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-BOOKSTACK-10 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall BookStack, then open https://bookstack.<lan-suffix>/ (own name; cannot run under a path) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-CALIBRE-WEB-AUTOMATED: Calibre-Web Automated

_Re-verify the sign-in script after every CWA update (pin test). app.db holds the client secret in plaintext (mode 644 inside the container). An address change (public refresh or move) needs sign-in planned and applied again (DEFERRED S415)._

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-1 — Install: plan preview, then approve**
  - Action: Catalog > Calibre-Web Automated > Install. Read the plan, then approve (or `homesteadctl apps plan calibre-web-automated -f FILE`, then `homesteadctl apps install calibre-web-automated -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-2 — Opens at the right path or name**
  - Action: Open https://calibre-web-automated.<lan-suffix>/ (own name; no path support).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-3 — Owner sign-in**
  - Action: Open the app, use the Authentik/OpenID sign-in option as the owner. Login page offers 'Login with Authentik'; DB-write sign-in (writes app.db generic OAuth row; CWA 4.0.8 pinned). Local login remains.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-6 — Ingest and read a book**
  - Action: Drop one EPUB into the app's ingest folder (install shows it; Nextcloud can share it), wait for the ingest, open the book in the web reader.
  - Expected: The book appears in the library with a cover and opens in the reader.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-7 — Name collision warning**
  - Action: Note whether signing in as an Authentik user named like an existing CWA user (including the default admin) takes over that account, as the sign-in review warns.
  - Expected: Matches the warning shown at install; record what you saw.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-8 — Re-check sign-in after an update**
  - Action: After any update of this package run `homesteadctl updates apply calibre-web-automated --backup` (or note the version change) and sign in again.
  - Expected: Sign-in still works; the version pin test forces this re-check because the sign-in writes the app database.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-9 — Data persists across restart**
  - Action: Note what exists (the book and its metadata). Run `docker restart calibre-web-automated-calibre-web-automated`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-10 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/calibre-web-automated/<UTC timestamp>/backup.json`. Contents cover `config/` (app.db), `library/`, `ingest/` as listed in the backup entries.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-CALIBRE-WEB-AUTOMATED-11 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Calibre-Web Automated, then open https://calibre-web-automated.<lan-suffix>/ (own name; no path support) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-FRESHRSS: FreshRSS

_FreshRSS is installed by Homestead (no web installer, ADR-059; SRV-FRESHRSS-1a). First install on this appliance; source/unit/contract tests only (DEFERRED S412: return URL behind the edge, auto-registration, off restoring the form login unproven)._

- [ ] **SRV-FRESHRSS-1 — Install: plan preview, then approve**
  - Action: Catalog > FreshRSS > Install. Read the plan, then approve (or `homesteadctl apps plan freshrss -f FILE`, then `homesteadctl apps install freshrss -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-FRESHRSS-1a — No web installer**
  - Action: After install, open https://freshrss.<lan-suffix>/ before signing in. FreshRSS's default user is your Authentik username; its password is under Secrets in Manage (Reveal).
  - Expected: FreshRSS shows its sign-in, never the web installer ("installation" page); the default user matches the Authentik owner's username.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-FRESHRSS-2 — Opens at the right path or name**
  - Action: Open https://freshrss.<lan-suffix>/ (own name).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-FRESHRSS-3 — Owner sign-in**
  - Action: Open the app; the Authentik sign-in appears (no form login, by design). Authentik ONLY (Apache mod_auth_openidc); FreshRSS's own login form stops working while sign-in is on. The Authentik person whose username equals FreshRSS's default user becomes its administrator.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-FRESHRSS-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-FRESHRSS-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-FRESHRSS-6 — Subscribe to a feed**
  - Action: Sign in, add the feed `https://hnrss.org/frontpage` (or any public feed you trust), refresh.
  - Expected: Articles appear; marking one read persists after reload.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-FRESHRSS-7 — Sign-in off restores the form**
  - Action: Only if you have time: turn sign-in off (Manage > Sign-in) and check the local form login works again; turn it back on.
  - Expected: Form login returns when off.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-FRESHRSS-8 — Data persists across restart**
  - Action: Note what exists (the subscription and read state). Run `docker restart freshrss-freshrss`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-FRESHRSS-9 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/freshrss/<UTC timestamp>/backup.json`. Contents cover `data/` (SQLite) and `extensions/`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-FRESHRSS-10 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall FreshRSS, then open https://freshrss.<lan-suffix>/ (own name) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-GITEA: Gitea

_Re-verify after every Gitea update (pin test). Per-person `none` is NOT enforced for the local login form: local passwords work (DEFERRED S416). Local registration is closed and Homestead creates the `homestead-admin` administrator at install (ADR-059, S444). Client secret in plaintext in gitea.db._

- [ ] **SRV-GITEA-1 — Install: plan preview, then approve**
  - Action: Catalog > Gitea > Install. Read the plan, then approve (or `homesteadctl apps plan gitea -f FILE`, then `homesteadctl apps install gitea -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-1a — No first-run screen; administrator already exists**
  - Action: Open the app's address. Confirm there is no installer and no Register link. Reveal the `homestead-admin` password in the app card Manage panel, Secrets (or `homesteadctl apps secrets reveal gitea admin_password`) only if you need it.
  - Expected: The sign-in page appears at once with the Authentik option and no registration; the revealed password opens `homestead-admin`.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-2 — Opens at the right path or name**
  - Action: Open https://gitea.<lan-suffix>/ (own name).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-3 — Owner sign-in**
  - Action: Open the app, use the Authentik/OpenID sign-in option as the owner. Login page offers 'Sign in with authentik' (OAuth2 source). Once Gitea is published, start from its public name (its ROOT_URL): starting from the LAN name ends with 'could not find a matching session'; DB-write sign-in (login_source row; Gitea 28.2.0 pinned). The first-run administrator is `homestead-admin`; owner (Homestead admin group) becomes a Gitea administrator at sign-in.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-6 — Create a repo and push**
  - Action: Create a repository in the web UI, clone it over HTTPS (or add your SSH key), commit one file, push.
  - Expected: The file shows in the web UI after the push; a second clone sees it.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-7 — Authentik user links to an existing user**
  - Action: If a local Gitea user has the same username as an Authentik person, first sign-in asks for that local password before linking.
  - Expected: No silent takeover.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-8 — Re-check sign-in after an update**
  - Action: After any update of this package run `homesteadctl updates apply gitea --backup` (or note the version change) and sign in again.
  - Expected: Sign-in still works; the version pin test forces this re-check because the sign-in writes the app database.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-9 — Data persists across restart**
  - Action: Note what exists (the repository and commit). Run `docker restart gitea-gitea`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-10 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/gitea/<UTC timestamp>/backup.json`. Contents cover `data/` (includes `gitea/gitea.db`).
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-GITEA-11 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Gitea, then open https://gitea.<lan-suffix>/ (own name) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-IMMICH: Immich

_Immich 3.3.0. S318 first-run admin fix is live-unverified after reset; S298 live check showed the sign-in redirect to Authentik with PKCE. Public name is one label deep under the zone (ADR-044)._

- [ ] **SRV-IMMICH-1 — Install: plan preview, then approve**
  - Action: Catalog > Immich > Install. Read the plan, then approve (or `homesteadctl apps plan immich -f FILE`, then `homesteadctl apps install immich -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-IMMICH-2 — Opens at the right path or name**
  - Action: Open https://immich.<lan-suffix>/ (own name BY DESIGN; Immich cannot run under a path, ADR-044). Public: https://immich.<public-domain>/.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-IMMICH-3 — Owner sign-in**
  - Action: Open the app, choose Login with Authentik as the owner. Login page offers 'Login with OAuth'/Authentik; Immich adapter configures it through the app API. First-run admin is created by Homestead (generated secret).
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-IMMICH-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-IMMICH-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-IMMICH-6 — Upload a photo**
  - Action: Sign in as the owner, upload one JPEG from your computer.
  - Expected: The photo shows in the timeline with a thumbnail; the detail view opens it.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-IMMICH-7 — Mobile app address**
  - Action: Optional: in the Immich mobile app, set the server URL to the public name and log in with OAuth.
  - Expected: Login works; upload one photo from the phone.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-IMMICH-8 — Own name, not a path**
  - Action: Check the address bar and the apps page link.
  - Expected: Immich is at `immich.<lan-suffix>` / `immich.<public-domain>`, not under `/apps/`.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-IMMICH-9 — Data persists across restart**
  - Action: Note what exists (the photo and its thumbnail (Immich keeps library, model cache and Postgres separately)). Run `docker restart immich-immich`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-IMMICH-10 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/immich/<UTC timestamp>/backup.json`. Contents cover library and the Postgres dump.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-IMMICH-11 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Immich, then open https://immich.<lan-suffix>/ (own name BY DESIGN; Immich cannot run under a path, ADR-044). Public: https://immich.<public-domain>/ and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-JELLYFIN: Jellyfin

_An earlier live check saw the SSO start 302 to the public Authentik; a real browser login to completion has not been recorded. Jellyfin sets itself up at install (S319)._

- [ ] **SRV-JELLYFIN-1 — Install: plan preview, then approve**
  - Action: Catalog > Jellyfin > Install. Read the plan, then approve (or `homesteadctl apps plan jellyfin -f FILE`, then `homesteadctl apps install jellyfin -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-JELLYFIN-2 — Opens at the right path or name**
  - Action: Open https://<server>/apps/jellyfin/ (public: https://homestead.<public-domain>/apps/jellyfin/).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-JELLYFIN-3 — Owner sign-in**
  - Action: On the Jellyfin login page choose the Authentik single-sign-on button as the owner. Login page shows a 'Sign in with Authentik' button (login disclaimer) via the pinned Jellyfin SSO plugin 4.0.0.4 (the one allowed third-party plugin, ADR-039); owner's own disclaimer is kept.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-JELLYFIN-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-JELLYFIN-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-JELLYFIN-6 — Play a file**
  - Action: Copy one short video into the bulk folder `<bulk mount>/jellyfin/media/movies/` (a small MP4 is enough), run a library scan in Jellyfin (Dashboard > Libraries), open it and press play.
  - Expected: The movie appears with a poster/placeholder and plays; seeking works.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-JELLYFIN-7 — Libraries pre-created**
  - Action: Open Dashboard > Libraries.
  - Expected: Movies, TV Shows and Music libraries exist (created by first run, S319).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-JELLYFIN-8 — SSO button path**
  - Action: On the login page click the Authentik button from a fresh private window.
  - Expected: Lands on Authentik, then back in Jellyfin signed in (S327 button path).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-JELLYFIN-9 — Data persists across restart**
  - Action: Note what exists (the library entry and watched state). Run `docker restart jellyfin-jellyfin`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-JELLYFIN-10 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/jellyfin/<UTC timestamp>/backup.json`. Contents cover `config/` (media is bulk and not backed up).
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-JELLYFIN-11 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Jellyfin, then open https://<server>/apps/jellyfin/ (public: https://homestead.<public-domain>/apps/jellyfin/) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-KOMGA: Komga

_First install on this appliance; sign-in confirmed from source only (DEFERRED S411). Every Authentik person becomes an ordinary user; no group mapping._

- [ ] **SRV-KOMGA-1 — Install: plan preview, then approve**
  - Action: Catalog > Komga > Install. Read the plan, then approve (or `homesteadctl apps plan komga -f FILE`, then `homesteadctl apps install komga -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-KOMGA-2 — Opens at the right path or name**
  - Action: Open https://komga.<lan-suffix>/ (own name).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-KOMGA-3 — Owner sign-in**
  - Action: Sign in with the Authentik/OpenID button on the app's login page as the owner. Login page shows the Authentik OAuth2 button (Spring client by environment, Java trust store); local login kept; recovery admin generated at install.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-KOMGA-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-KOMGA-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-KOMGA-6 — Read a comic**
  - Action: Put one CBZ/PDF into the Komga library folder, scan, open and page through it.
  - Expected: It appears with a cover and the reader pages forward; progress saved.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-KOMGA-7 — Data persists across restart**
  - Action: Note what exists (the library and read progress). Run `docker restart komga-komga`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-KOMGA-8 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/komga/<UTC timestamp>/backup.json`. Contents cover `config/` (database.sqlite).
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-KOMGA-9 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Komga, then open https://komga.<lan-suffix>/ (own name) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-LINKDING: Linkding

_First install on this appliance (S409/S410 unit and contract tests only). No group claim: Authentik people are ordinary users; accounts match by email._

- [ ] **SRV-LINKDING-1 — Install: plan preview, then approve**
  - Action: Catalog > Linkding > Install. Read the plan, then approve (or `homesteadctl apps plan linkding -f FILE`, then `homesteadctl apps install linkding -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-LINKDING-2 — Opens at the right path or name**
  - Action: Open https://linkding.<lan-suffix>/ (own name).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-LINKDING-3 — Owner sign-in**
  - Action: Sign in with the Authentik/OpenID button on the app's login page as the owner. Login page offers the OIDC button (mozilla-django-oidc); endpoints derived from the Authentik base; combined CA trust bundle (S410).
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-LINKDING-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-LINKDING-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-LINKDING-6 — Save a bookmark**
  - Action: Sign in, add a bookmark with a title and tag, open the list and click it.
  - Expected: The bookmark appears, the tag filter works; a second sign-in keeps it.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-LINKDING-7 — Data persists across restart**
  - Action: Note what exists (the bookmark and tag). Run `docker restart linkding-linkding`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-LINKDING-8 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/linkding/<UTC timestamp>/backup.json`. Contents cover `data/`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-LINKDING-9 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Linkding, then open https://linkding.<lan-suffix>/ (own name) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-MEALIE: Mealie

_An earlier live check saw Authentik accept the Mealie redirect URI; a browser login to completion has not been recorded._

- [ ] **SRV-MEALIE-1 — Install: plan preview, then approve**
  - Action: Catalog > Mealie > Install. Read the plan, then approve (or `homesteadctl apps plan mealie -f FILE`, then `homesteadctl apps install mealie -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEALIE-2 — Opens at the right path or name**
  - Action: Open https://mealie.<lan-suffix>/ (own name; public mealie.<public-domain> once published).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEALIE-3 — Owner sign-in**
  - Action: Sign in with the Authentik/OpenID button on the app's login page as the owner. Login page offers 'Login with OAuth' (OIDC env); BASE_URL follows the public name once published.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEALIE-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEALIE-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEALIE-6 — Import and view a recipe**
  - Action: Sign in, import a recipe from a URL (a public recipe page) or create one by hand with two ingredients, open it.
  - Expected: The recipe shows ingredients and steps; add it to the meal plan.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEALIE-7 — Data persists across restart**
  - Action: Note what exists (the recipe and meal plan entry). Run `docker restart mealie-mealie`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEALIE-8 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/mealie/<UTC timestamp>/backup.json`. Contents cover `data/`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEALIE-9 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Mealie, then open https://mealie.<lan-suffix>/ (own name; public mealie.<public-domain> once published) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-MEMOS: Memos

_Browser sign-in has not been recorded yet._

- [ ] **SRV-MEMOS-1 — Install: plan preview, then approve**
  - Action: Catalog > Memos > Install. Read the plan, then approve (or `homesteadctl apps plan memos -f FILE`, then `homesteadctl apps install memos -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEMOS-1a — No first-account screen; administrator already exists**
  - Action: Open the app's address. Confirm there is no first-account (setup) screen and no password sign-up. Reveal the `homestead-admin` password in the app card Manage panel, Secrets (or `homesteadctl apps secrets reveal memos admin_password`) only if you need it, and sign in with it at /auth/admin.
  - Expected: The sign-in page appears at once offering Authentik; the revealed password opens `homestead-admin` as administrator (Settings available). Authentik sign-in creates ordinary accounts.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEMOS-2 — Opens at the right path or name**
  - Action: Open https://memos.<lan-suffix>/ (own name; cannot run under a path).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEMOS-3 — Owner sign-in**
  - Action: Sign in with the Authentik/OpenID button on the app's login page as the owner. Login page offers the Authentik SSO button (provider written via a Memos config file); new SSO users get the USER role. Password sign-in and sign-up are off while sign-in is on; the administrator is `homestead-admin`.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEMOS-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEMOS-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEMOS-6 — Write a memo**
  - Action: Sign in, write a memo with a #tag, a checklist and an attached small image, save.
  - Expected: The memo is listed, the tag filter finds it.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEMOS-7 — Data persists across restart**
  - Action: Note what exists (the memo and attachment). Run `docker restart memos-memos`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEMOS-8 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/memos/<UTC timestamp>/backup.json`. Contents cover `data/` (memos_prod.db).
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-MEMOS-9 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Memos, then open https://memos.<lan-suffix>/ (own name; cannot run under a path) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-NAVIDROME: Navidrome

_Install refuses if no Navidrome admin exists or a username collides with a Homestead person, so create the Navidrome admin first (SRV-NAVIDROME-1a). Keep its password in your password manager, not in this file. Owner browser sign-in has not been recorded yet._

- [ ] **SRV-NAVIDROME-1 — Install: plan preview, then approve**
  - Action: Catalog > Navidrome > Install. Read the plan, then approve (or `homesteadctl apps plan navidrome -f FILE`, then `homesteadctl apps install navidrome -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-1a — Create the Navidrome admin**
  - Action: If install or the sign-in step says no Navidrome admin exists, open https://navidrome.<lan-suffix>/ and create the admin account when Navidrome asks, then retry the step (Apply sign-in again). Keep the password in your password manager.
  - Expected: The admin exists; the install or sign-in step then goes through; a username that collides with a Homestead person is refused with a clear message.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-2 — Opens at the right path or name**
  - Action: Open https://navidrome.<lan-suffix>/ (own name; one canonical name: public if published, else .lan; other names redirect).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-3 — Owner sign-in**
  - Action: Open the app; Authentik asks for sign-in at the edge, then you are in. Authentik forward-auth at the edge (ADR-055): the edge asks Authentik first; Navidrome trusts the X-Authentik-Username header from the edge (/32 pin). `/rest/*` and `/share/*` bypass the gate for native clients and share links. Navidrome admin is chosen inside Navidrome.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-6 — Play a track**
  - Action: Copy one MP3 into `<bulk mount>/navidrome/music/` (the install shows the folder), wait for the scan (or trigger a rescan in the UI), open and play it.
  - Expected: The artist/album appears and audio plays.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-7 — Subsonic client bypass**
  - Action: From a Subsonic client (for example a phone app) or `curl 'https://navidrome.<lan-suffix>/rest/ping.view?...'` with Navidrome credentials, call `/rest/ping`.
  - Expected: Answers without an Authentik redirect (the bypass), but needs Navidrome credentials.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-8 — Subsonic client bypass**
  - Action: Use a Subsonic client against `/rest/` with the Navidrome app credentials.
  - Expected: Works without Authentik (edge bypass for `/rest/*` and `/share/*`).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-9 — Data persists across restart**
  - Action: Note what exists (the library scan result and a playlist). Run `docker restart navidrome-navidrome`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-10 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/navidrome/<UTC timestamp>/backup.json`. Contents cover `data/` (navidrome.db) and `cache/` is skipped.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NAVIDROME-11 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Navidrome, then open https://navidrome.<lan-suffix>/ (own name; one canonical name: public if published, else .lan; other names redirect) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-NEXTCLOUD: Nextcloud

_Nextcloud 35.0.1; sign-in verified live (S298, S408); public base URL for path apps is not rewritten (DEFERRED S407)._

- [ ] **SRV-NEXTCLOUD-1 — Install: plan preview, then approve**
  - Action: Catalog > Nextcloud > Install. Read the plan, then approve (or `homesteadctl apps plan nextcloud -f FILE`, then `homesteadctl apps install nextcloud -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NEXTCLOUD-1a — Admin account is created at install**
  - Action: Nextcloud sets itself up: install creates the `admin` account with a generated password and there is no setup page to finish. In the app card Manage panel, Secrets, **Reveal** the admin password (it asks you to sign in again; the reveal is audited) or `homesteadctl apps secrets reveal nextcloud admin_password`. Wait a few minutes for Nextcloud to finish installing; if sign-in says it is not ready use **Apply sign-in again**.
  - Expected: Reveal works and is audited; `admin` signs in locally; no Nextcloud setup page appears. (If it does, that contradicts the S312 change: FAIL.)
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NEXTCLOUD-2 — Opens at the right path or name**
  - Action: Open https://<server>/apps/nextcloud/.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NEXTCLOUD-3 — Owner sign-in**
  - Action: Choose Log in with Authentik as the owner. Login page shows 'Log in with Authentik' (user_oidc app configured with occ); admin account `admin` created at first run (generated secret).
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NEXTCLOUD-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NEXTCLOUD-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NEXTCLOUD-6 — Upload a file**
  - Action: Sign in, upload a text file, then download it again.
  - Expected: File uploads, shows in Files and downloads identical (compare `sha256sum`).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NEXTCLOUD-7 — WebDAV for craft apps**
  - Action: Used later by GridCraft/CADCraft/PdfCraft; see PLAT-CRAFTLINK-1.
  - Expected: Linked in the Craft files page.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NEXTCLOUD-8 — Data persists across restart**
  - Action: Note what exists (the uploaded file). Run `docker restart nextcloud-nextcloud`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NEXTCLOUD-9 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/nextcloud/<UTC timestamp>/backup.json`. Contents cover `html/` and `data/` plus the database dump.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-NEXTCLOUD-10 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Nextcloud, then open https://<server>/apps/nextcloud/ and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-PAPERLESS-NGX: Paperless-ngx

_First install on this appliance; sign-in confirmed from docs and source only (DEFERRED S413). Admin rights and group sync are not wired._

- [ ] **SRV-PAPERLESS-NGX-1 — Install: plan preview, then approve**
  - Action: Catalog > Paperless-ngx > Install. Read the plan, then approve (or `homesteadctl apps plan paperless-ngx -f FILE`, then `homesteadctl apps install paperless-ngx -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-PAPERLESS-NGX-2 — Opens at the right path or name**
  - Action: Open https://paperless-ngx.<lan-suffix>/ (own name).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-PAPERLESS-NGX-3 — Owner sign-in**
  - Action: Sign in with the Authentik/OpenID button on the app's login page as the owner. Login page keeps the local form and offers Authentik (allauth openid_connect provider JSON in a derived secret file). New people get an ordinary account with NO permissions until you grant them in Paperless.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-PAPERLESS-NGX-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-PAPERLESS-NGX-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-PAPERLESS-NGX-6 — Consume a document**
  - Action: Put one PDF into the consume folder shown at install (or upload in the web UI), wait for processing, search for a word in it.
  - Expected: The document appears with a thumbnail and OCR text; search finds it.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-PAPERLESS-NGX-7 — Data persists across restart**
  - Action: Note what exists (the document and its OCR text). Run `docker restart paperless-ngx-paperless-ngx`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-PAPERLESS-NGX-8 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/paperless-ngx/<UTC timestamp>/backup.json`. Contents cover `data/`, `media/`, `export/` and the Postgres dump.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-PAPERLESS-NGX-9 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Paperless-ngx, then open https://paperless-ngx.<lan-suffix>/ (own name) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-UPTIME-KUMA: Uptime Kuma

_Kuma's first-run setup screen is behind the Authentik gate; owner browser sign-in has not been recorded yet. NOTE: an earlier memory says Kuma login stays on; S422 later turned it off. The host firewall gate table (S421) must be active (PLAT-FIREWALL-3)._

- [ ] **SRV-UPTIME-KUMA-1 — Install: plan preview, then approve**
  - Action: Catalog > Uptime Kuma > Install. Read the plan, then approve (or `homesteadctl apps plan uptime-kuma -f FILE`, then `homesteadctl apps install uptime-kuma -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-2 — Opens at the right path or name**
  - Action: Open https://uptime-kuma.<lan-suffix>/ (own name; one canonical name).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-3 — Owner sign-in**
  - Action: Open the app; Authentik asks for sign-in at the edge, then you are in. Authentik forward-auth, ADMIN-ONLY gate (ADR-055). Kuma's own login is turned OFF behind the gate (S422): you sign in once, to Authentik. Open paths: /api/push/*, status pages, /api/status-page/*, /api/badge/* and their assets. /metrics stays behind the gate.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-6 — Add a monitor**
  - Action: Sign in, add an HTTP monitor for `https://<server>/`, wait two checks.
  - Expected: The monitor shows UP with a response time.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-7 — Status page stays open**
  - Action: Create a status page with that monitor and open its link in a private window with no session.
  - Expected: The status page loads without Authentik; the dashboard (`/`) in the same window redirects to Authentik.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-8 — Kuma's own login is off**
  - Action: Open the app after Authentik sign-in as the owner.
  - Expected: No second Kuma login (S422). Memory notes say it stays on; the repo says off. Record what you see.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-9 — Open paths work without sign-in**
  - Action: Open a status page, a push URL and a badge URL in a private window.
  - Expected: They answer without sign-in; the dashboard itself does not.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-10 — Data persists across restart**
  - Action: Note what exists (the monitor and its history). Run `docker restart uptime-kuma-uptime-kuma`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-11 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/uptime-kuma/<UTC timestamp>/backup.json`. Contents cover `data/` (SQLite).
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-UPTIME-KUMA-12 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Uptime Kuma, then open https://uptime-kuma.<lan-suffix>/ (own name; one canonical name) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-VAULTWARDEN: Vaultwarden

_Public authorize redirect was verified live earlier; NO real browser login done yet; existing-account email match and email_verified claim unproven (DEFERRED S414)._

- [ ] **SRV-VAULTWARDEN-1 — Install: plan preview, then approve**
  - Action: Catalog > Vaultwarden > Install. Read the plan, then approve (or `homesteadctl apps plan vaultwarden -f FILE`, then `homesteadctl apps install vaultwarden -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VAULTWARDEN-1a — Create the first Vaultwarden account**
  - Action: Open the app and create your own account (signup is how the first account is made; the admin page is off by design). Use a strong master password and store it safely; it cannot be recovered.
  - Expected: Account created; the Authentik sign-in option is offered.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VAULTWARDEN-2 — Opens at the right path or name**
  - Action: Open https://vaultwarden.<lan-suffix>/ (own name; public vaultwarden.<public-domain> once published).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VAULTWARDEN-3 — Owner sign-in**
  - Action: Sign in with the Authentik/OpenID button on the app's login page as the owner. Login page offers 'Enterprise single sign-on' (Authentik, SSO by environment, Vaultwarden 1.37.4); the master password still unlocks the vault after SSO. Sign-in completes only at the address named by DOMAIN.
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VAULTWARDEN-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VAULTWARDEN-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VAULTWARDEN-6 — Save an entry**
  - Action: Create an account (or SSO in and set a master password), add a Login item with a username and password, lock, unlock.
  - Expected: The entry reappears after unlock and reload; the browser extension/app can sync if you have one.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VAULTWARDEN-7 — Data persists across restart**
  - Action: Note what exists (the vault entry). Run `docker restart vaultwarden-vaultwarden`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VAULTWARDEN-8 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/vaultwarden/<UTC timestamp>/backup.json`. Contents cover `data/` (SQLite).
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VAULTWARDEN-9 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Vaultwarden, then open https://vaultwarden.<lan-suffix>/ (own name; public vaultwarden.<public-domain> once published) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### SRV-VIKUNJA: Vikunja

_Browser sign-in has not been recorded yet. Create the first Vaultwarden account yourself (SRV-VAULTWARDEN-1a)._

- [ ] **SRV-VIKUNJA-1 — Install: plan preview, then approve**
  - Action: Catalog > Vikunja > Install. Read the plan, then approve (or `homesteadctl apps plan vikunja -f FILE`, then `homesteadctl apps install vikunja -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VIKUNJA-2 — Opens at the right path or name**
  - Action: Open https://vikunja.<lan-suffix>/ (own name).
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VIKUNJA-3 — Owner sign-in**
  - Action: Sign in with the Authentik/OpenID button on the app's login page as the owner. Login page offers the Authentik button (provider list rendered into a config file at apply time).
  - Expected: Owner is signed in and named correctly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VIKUNJA-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VIKUNJA-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VIKUNJA-6 — Create a task**
  - Action: Sign in, create a project, add two tasks, set a due date on one and mark the other done.
  - Expected: Tasks list correctly and keep state after reload.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VIKUNJA-7 — Data persists across restart**
  - Action: Note what exists (the project and tasks). Run `docker restart vikunja-vikunja`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VIKUNJA-8 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/vikunja/<UTC timestamp>/backup.json`. Contents cover `data/` (files and database).
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **SRV-VIKUNJA-9 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Vikunja, then open https://vikunja.<lan-suffix>/ (own name) and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

## Platform checks with apps installed (PLAT-AFTER)

Do these after the server apps above are installed. Steps that use an app say which.

### Sign-in, access and groups

- [ ] **PLAT-TUNNEL-5 — Apps move to the public issuer**
  - Action: Open an OIDC app you installed with a public name (for example Mealie at `https://mealie.<public-domain>/`).
  - Expected: Sign-in redirects to the public Authentik and returns.
  - If it fails: Doctor Tunnel and Certificates sections; ADR-046/047/048.
  - Result: ____  Notes: ____

- [ ] **PLAT-PEOPLE-2 — Closed by default**
  - Action: As C open an installed app. Grant A and B on one app and re-test.
  - Expected: Refused. Grant A and B on one app and re-test: A and B in, C out.
  - If it fails: docs/INSTALL.md People section; ADR-043/049.
  - Result: ____  Notes: ____

- [ ] **PLAT-PEOPLE-3 — App-admin groups**
  - Action: In Authentik check that the `homestead-<app>-admins` group exists for an installed app and make a member an app admin in an app that honours it (Gitea, for example).
  - Expected: Group exists; behaviour matches the app.
  - If it fails: docs/INSTALL.md People section; ADR-043/049.
  - Result: ____  Notes: ____

- [ ] **PLAT-PEOPLE-4 — Reset password and MFA**
  - Action: `homesteadctl people reset-password A`, `homesteadctl people reset-mfa A`.
  - Expected: A can sign in again after the reset flow.
  - If it fails: docs/INSTALL.md People section; ADR-043/049.
  - Result: ____  Notes: ____

- [ ] **PLAT-PEOPLE-7 — Disable and enable**
  - Action: `homesteadctl people disable B`, B tries to sign in, `homesteadctl people enable B`.
  - Expected: Refused while disabled; works after. Disabling does not purge data.
  - If it fails: docs/INSTALL.md People section; ADR-043/049.
  - Result: ____  Notes: ____

- [ ] **PLAT-PEOPLE-8 — Adopt and resolve**
  - Action: If an app has a user that matches an Authentik person (for example a local account you made in Gitea), use `people adopt` / `resolve`; else SKIP.
  - Expected: Link is made; no duplicate person.
  - If it fails: docs/INSTALL.md People section; ADR-043/049.
  - Result: ____  Notes: ____

- [ ] **PLAT-DOCTOR-2 — Doctor finds a deliberate fault**
  - Action: Stop one app container (`docker stop <package>-<service>`), reload Doctor.
  - Expected: Doctor reports it with guidance and does not restart it. Start it again.
  - If it fails: 
  - Result: ____  Notes: ____

### Firewall

- [ ] **PLAT-FIREWALL-1a — Plan and apply the firewall**
  - Action: Open the **Firewall** page (or `homesteadctl firewall plan`), read the plan, approve and apply it (`homesteadctl firewall apply`).
  - Expected: The plan shows the nftables rules before anything changes; after apply the page reads applied; no router port forward is needed for the tunnel.
  - If it fails: INSTALL Part E (optional hardening); Doctor Network section.
  - Result: ____  Notes: ____

- [ ] **PLAT-FIREWALL-1 — Gate table present**
  - Action: `sudo nft list table inet homestead_gate`.
  - Expected: Table exists and covers gated app ports (S421).
  - If it fails: Doctor Network section.
  - Result: ____  Notes: ____

- [ ] **PLAT-FIREWALL-2 — Non-root local access is blocked**
  - Action: As a normal user on the VM `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:<gated-port>/` (port from the app's compose/plan).
  - Expected: Connection refused or blocked; via the Caddy name it works.
  - If it fails: 
  - Result: ____  Notes: ____

- [ ] **PLAT-FIREWALL-3 — Forged identity header is ignored**
  - Action: `curl -H 'X-Authentik-Username: admin' https://<server>/apps/<forward-auth-app>/` from the LAN without a session.
  - Expected: Redirected to sign-in, not logged in as admin.
  - If it fails: Forward-auth edge config.
  - Result: ____  Notes: ____

### Backups and updates

- [ ] **PLAT-BACKUP-3 — A run exists after 02:00 (day 2)**
  - Action: Look at `<backup root>/<package>/<UTC timestamp>/backup.json`.
  - Expected: One run per backup-capable package, encrypted; old runs pruned beyond 7.
  - If it fails: docs/architecture/BACKUP_RESTORE_RUNBOOK.md.
  - Result: ____  Notes: ____

- [ ] **PLAT-UPDATE-2 — Apply one app update with backup**
  - Action: `homesteadctl updates apply <pkg> --backup` for an app with an update (else SKIP).
  - Expected: A backup run appears, then the update; the app still works. For vaultwarden/CWA/Gitea/ABS re-check sign-in (see their SRV update step).
  - If it fails: 
  - Result: ____  Notes: ____


## Everything apps

The 14 Everything apps share one bridge (ADR-053/057): the first install of each builds its image on the appliance (`homestead/everything-<app>:0.1.0`). Apps are served at `https://<server>/apps/<app>/`; sign-in is Authentik; each person's data is separate (scoped by app and person); quota is 256 MiB per person. For each app, the review-plan section named in the heading has the same steps in more detail.


### EV-TASKS: Tasks

_Review plan section: S368 and S437. Automated: tests/test_s368_tasks_package.py; gate: browser-check.mjs, browser-check-tasks-sharing.mjs; bridge-gate.test.js._

- [ ] **EV-TASKS-1 — Install: plan preview, then approve**
  - Action: Catalog > Tasks > Install. Read the plan, then approve (or `homesteadctl apps plan tasks -f FILE`, then `homesteadctl apps install tasks -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/tasks/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `tasks-tasks`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-6 — Create a list**
  - Action: Create a list and two tasks; reload.
  - Expected: They remain.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-7 — Offline queue**
  - Action: DevTools > Network > Offline, add a task, go Online.
  - Expected: It syncs. Reloading while offline shows the token-free offline page, not the app.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-8 — No Puter wording**
  - Action: Look through the UI for Puter wording or links.
  - Expected: None. Share and the calendar-feed setting exist only because ADR-058 (S437) re-enabled them; test them in EV-TASKS sharing steps.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-9 — Offline behaviour**
  - Action: Offline test: Offline page (token-free); an already-open page keeps working.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-10 — Limits**
  - Action: Person quota is 256 MiB: add data (or lower the quota in the package environment) to see a clear quota error with existing data intact.
  - Expected: A clear error and existing data intact.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-11 — Invite and live updates (S437)**
  - Action: A opens a list, Share, invites B by Homestead name; B opens it; A adds a task.
  - Expected: B sees it within seconds without reloading; an unknown name is refused.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-12 — C sees nothing; removal stops changes**
  - Action: C opens Tasks; A removes B.
  - Expected: C sees nothing; B's changes stop and B's save fails visibly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-13 — Read link and calendar feed (S437)**
  - Action: A creates a read link and opens the calendar feed on the LAN.
  - Expected: Plain script-free page on the app's local name; Stop sharing gives 404.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-14 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart tasks-tasks`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-15 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/tasks/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TASKS-16 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Tasks, then open `https://<server>/apps/tasks/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-PROJECTS: Projects

_Review plan section: S369. Automated: tests/test_s369_projects_package.py; gate: browser-check-projects.mjs._

- [ ] **EV-PROJECTS-1 — Install: plan preview, then approve**
  - Action: Catalog > Projects > Install. Read the plan, then approve (or `homesteadctl apps plan projects -f FILE`, then `homesteadctl apps install projects -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/projects/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `projects-projects`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-6 — Project with columns, tasks, file**
  - Action: Create a project, add a column and two tasks, attach a small file; reload.
  - Expected: All remain; the attachment opens.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-7 — Conflict prompt**
  - Action: Open the project in two tabs and edit in both.
  - Expected: A conflict prompt appears; no silent loss.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-8 — Attachment limit**
  - Action: Attach a file over 8 MiB.
  - Expected: A clear size error; existing data intact.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-9 — Offline behaviour**
  - Action: Offline test: Go Offline, edit, go Online: edits sync. Reload while offline shows the offline page.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-10 — Limits**
  - Action: 8 MiB attachment limit (see above).
  - Expected: A clear error and existing data intact.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-11 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart projects-projects`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-12 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/projects/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PROJECTS-13 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Projects, then open `https://<server>/apps/projects/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-RECORDS: Records

_Review plan section: S370. Automated: tests/test_s370_records_package.py; gate: browser-check-records.mjs; upstream 445/445._

- [ ] **EV-RECORDS-1 — Install: plan preview, then approve**
  - Action: Catalog > Records > Install. Read the plan, then approve (or `homesteadctl apps plan records -f FILE`, then `homesteadctl apps install records -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/records/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `records-records`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-6 — Base, table, views**
  - Action: Create a base with a table of text, number, single-select and link fields and several records; switch grid/board/calendar/gallery/form views, add a filter and a sort; reload.
  - Expected: Records, views and settings remain.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-7 — Attachment, import, export**
  - Action: Attach a small file, reload and download it; import a CSV as a new base; export one. Try a file over 8 MiB.
  - Expected: All work; the oversized file gives a generic 'Could not upload the file' error; data intact.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-8 — Trash and purge**
  - Action: Delete a base to the trash, then purge it.
  - Expected: Gone, and its files are removed.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-9 — Conflict message**
  - Action: Edit the same base in two tabs.
  - Expected: A conflict message, no silent loss.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-10 — Offline behaviour**
  - Action: Offline test: No offline page (upstream has no service worker): reloading offline shows the browser error page. Edits made while briefly offline save on return.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-11 — Limits**
  - Action: 8 MiB attachment limit (above).
  - Expected: A clear error and existing data intact.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-12 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart records-records`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-13 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/records/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-RECORDS-14 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Records, then open `https://<server>/apps/records/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-NOTES: Notes

_Review plan section: S371, S423, S438. Automated: tests/test_s371_notes_package.py, tests/test_everything_source.py, tests/test_s423_source_offer.py; gate: browser-check-notes.mjs, browser-check-notes-sharing.mjs, browser-check-viewer.mjs, browser-check-source-offer.mjs; upstream 275/275._

- [ ] **EV-NOTES-1 — Install: plan preview, then approve**
  - Action: Catalog > Notes > Install. Read the plan, then approve (or `homesteadctl apps plan notes -f FILE`, then `homesteadctl apps install notes -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/notes/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `notes-notes`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-6 — Write a page**
  - Action: Add a section and a page, type text, add a to-do tag, draw with the pen, insert a picture; reload; search for a word on the page.
  - Expected: All remain and the page is found.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-7 — PDF printout, backup, restore**
  - Action: Insert a PDF printout; export a notebook backup and import it again; delete a page and restore it from Deleted Notes.
  - Expected: Pages appear as pictures; import makes a second notebook with the same pages; the deleted page returns.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-8 — AI assist absent**
  - Action: Open Assist from the ribbon.
  - Expected: 'AI assist is not available on Homestead'; no AI buttons; DevTools Network shows no AI request.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-9 — Source offer (AGPL-3.0)**
  - Action: Click the small 'Source' link at the bottom of the page; download the tarball and run `sha256sum` on it.
  - Expected: A plain page names AGPL-3.0-only, the HeyPuter/everything repository and the pinned commit; the checksum equals the page's. In a private window with no session the URL asks for sign-in instead.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-10 — Offline behaviour**
  - Action: Offline test: Reload while the container is stopped shows the browser or offline page; record which.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-11 — Read link through the viewer (S438)**
  - Action: A writes a page, Share > Create Link (7 days); open the link in a private window.
  - Expected: Plain page with the text; picture and handwriting replaced by 'not shown in this viewer'; no sign-in; Stop Sharing gives 'not found'.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-12 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart notes-notes`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-13 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/notes/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-NOTES-14 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Notes, then open `https://<server>/apps/notes/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-CONTACTS: Contacts

_Review plan section: S372. Automated: tests/test_s372_contacts_package.py; gate: browser-check-contacts.mjs; upstream 313/313 (mock bridge)._

- [ ] **EV-CONTACTS-1 — Install: plan preview, then approve**
  - Action: Catalog > Contacts > Install. Read the plan, then approve (or `homesteadctl apps plan contacts -f FILE`, then `homesteadctl apps install contacts -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/contacts/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `contacts-contacts`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-6 — Add, import, export, search**
  - Action: New contact with name, company, email and a photo; import a `.vcf` with several cards (menu item and drag-drop); export all; search by name, company and email.
  - Expected: All appear and remain after reload; export contains every contact; search narrows as you type; no Smart add button; no Puter wording.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-7 — Large address book**
  - Action: Import a large `.vcf` (a few thousand cards) and reload.
  - Expected: They all load (may take seconds).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-8 — Account menu**
  - Action: Open the account menu and a contact's menu.
  - Expected: No Sign out and no raw user id; no 'Add birthday to Calendar'.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-9 — Offline behaviour**
  - Action: Offline test: Stop the container and reload: the 'You are offline' page, no contacts shown. Start it again.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-10 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart contacts-contacts`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-11 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/contacts/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CONTACTS-12 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Contacts, then open `https://<server>/apps/contacts/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-INVENTORY: Inventory

_Review plan section: S373. Automated: tests/test_s373_inventory_package.py; gate: browser-check-inventory.mjs; upstream 722/722 (mock bridge)._

- [ ] **EV-INVENTORY-1 — Install: plan preview, then approve**
  - Action: Catalog > Inventory > Install. Read the plan, then approve (or `homesteadctl apps plan inventory -f FILE`, then `homesteadctl apps install inventory -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/inventory/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `inventory-inventory`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-6 — Product and stock**
  - Action: Skip the welcome, add a product with name, SKU, opening stock and a photo; import a CSV of products; add a location and a supplier; record a stock movement; open Reports; export a CSV.
  - Expected: All work; the thumbnail shows after reload.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-7 — Barcode scan**
  - Action: Press Scan on a device with a camera and allow the camera.
  - Expected: The camera opens and a scanned code finds or starts a product.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-8 — Settings and backup**
  - Action: Open Settings; create a backup and restore it.
  - Expected: No Documentation/Guides/Puter links, no Sign out; the account row says 'Homestead user'; data returns after restore.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-9 — Offline behaviour**
  - Action: Offline test: Stop the container and reload: 'You are offline' page, no product names.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-10 — Limits**
  - Action: A large (12 megapixel) photo is shrunk and saved; storage use stays small.
  - Expected: A clear error and existing data intact.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-11 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart inventory-inventory`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-12 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/inventory/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-INVENTORY-13 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Inventory, then open `https://<server>/apps/inventory/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-TIME-TRACKING: Time tracking

_Review plan section: S374. Automated: tests/test_s374_time_tracking_package.py; gate: browser-check-time-tracking.mjs; upstream 205/205 (mock bridge)._

- [ ] **EV-TIME-TRACKING-1 — Install: plan preview, then approve**
  - Action: Catalog > Time tracking > Install. Read the plan, then approve (or `homesteadctl apps plan time-tracking -f FILE`, then `homesteadctl apps install time-tracking -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/time-tracking/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `time-tracking-time-tracking`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-6 — Track time**
  - Action: Choose 'Set up tracking', add a client and a project, start and stop the timer, log a manual entry `1:30`; reload.
  - Expected: Everything remains.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-7 — Invoice, import, reports**
  - Action: Create a client invoice from unbilled time, record a payment, open the printable invoice and timesheet, upload a logo; import a Toggl/Clockify/Harvest CSV; run Reports with CSV export.
  - Expected: All work; the logo shows after a reload.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-8 — Backup and PWA**
  - Action: Use 'Back up now' in Settings and restore; install the app as a PWA and open its 'Log time' shortcut.
  - Expected: Data returns; the shortcut opens the form.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-9 — Offline behaviour**
  - Action: Offline test: Stop the container and reload: 'You are offline' page, no entry names.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-10 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart time-tracking-time-tracking`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-11 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/time-tracking/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-TIME-TRACKING-12 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Time tracking, then open `https://<server>/apps/time-tracking/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-CIRCUITS: Circuits

_Review plan section: S375 and S439. Automated: tests/test_s375_circuits_package.py; gate: browser-check-circuits.mjs, browser-check-circuits-sharing.mjs; upstream vitest 206/207 on the patched tree._

- [ ] **EV-CIRCUITS-1 — Install: plan preview, then approve**
  - Action: Catalog > Circuits > Install. Read the plan, then approve (or `homesteadctl apps plan circuits -f FILE`, then `homesteadctl apps install circuits -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/circuits/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `circuits-circuits`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-6 — Example project**
  - Action: Choose 'Open the example' and Save; Open project lists it; run Design rule check and a copper pour (Repour); rename the project; reload; File > Version history restores a version.
  - Expected: All work with no console error and no CSP violation; the old name is gone after rename.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-7 — Schematic to board**
  - Action: Draw a part into a schematic, update the board from it, route a track, export Gerber, open the 3D view; open a KiCad schematic or board file.
  - Expected: All work; the KiCad file imports.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-8 — Account menu**
  - Action: Open the account menu.
  - Expected: 'Homestead user' (not your user id), Preferences, no Sign out; the File menu has Share.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-9 — Offline behaviour**
  - Action: Offline test: No offline page and not installable as a PWA (upstream has no service worker): a lost connection shows the browser error page and a save while offline fails with a message.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-10 — Sharing (S439)**
  - Action: A saves the example and File > Share to B as Can view, then Can edit.
  - Expected: B sees 'Shared with me'; view-only save is refused; edit lands in A's single file; C sees nothing; removal makes B's next save fail visibly.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-11 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart circuits-circuits`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-12 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/circuits/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-CIRCUITS-13 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Circuits, then open `https://<server>/apps/circuits/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-PARAMETRIC: Parametric

_Review plan section: S376, S423 and S440. Automated: tests/test_s376_parametric_package.py; gate: browser-check-parametric.mjs, browser-check-parametric-sharing.mjs; upstream 104/104._

- [ ] **EV-PARAMETRIC-1 — Install: plan preview, then approve**
  - Action: Catalog > Parametric > Install. Read the plan, then approve (or `homesteadctl apps plan parametric -f FILE`, then `homesteadctl apps install parametric -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/parametric/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `parametric-parametric`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-6 — Sketch and extrude**
  - Action: Wait for the kernel to load, create a sketch and Extrude, Save; rename the part; reload.
  - Expected: The part saves, appears in Open and reopens with its features. Record whether a 'Geometry engine failed to start' toast appears (an unexplained one-off, see Known issues).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-7 — Export and import**
  - Action: Export STL and STEP; import a STEP file.
  - Expected: All work.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-8 — No AI or Publish**
  - Action: Look for an AI/assistant button and Publish.
  - Expected: None; DevTools Network shows no AI request.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-9 — Licence and source offer**
  - Action: Help: open the OpenCascade licence notice and the source page.
  - Expected: Both open; the source page names replicad-opencascadejs 1.1.0 (LGPL-2.1-only) with no tarball link.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-10 — Offline behaviour**
  - Action: Offline test: No offline page.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-11 — Sharing (S440)**
  - Action: A saves the Mounting Bracket and File > Share to B as Can view, then Can edit.
  - Expected: B's view-only Save makes B's own copy; edit lands in A's file; C sees nothing; removal makes B's next save fail with 'Could not save'.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-12 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart parametric-parametric`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-13 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/parametric/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-PARAMETRIC-14 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Parametric, then open `https://<server>/apps/parametric/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-THREE-D: 3D

_Review plan section: S377 and S423. Automated: tests/test_s377_three_d_package.py; gate: browser-check-three-d.mjs; upstream 3,018 passed._

- [ ] **EV-THREE-D-1 — Install: plan preview, then approve**
  - Action: Catalog > 3D > Install. Read the plan, then approve (or `homesteadctl apps plan three-d -f FILE`, then `homesteadctl apps install three-d -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/three-d/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `three-d-three-d`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-6 — Editor basics**
  - Action: The editor opens with a cube; change the theme and reload; add an object, wait 3 seconds, reload.
  - Expected: The preference is kept; the object is recovered (known startup race, see Known issues).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-7 — Unsupported features absent**
  - Action: Look for AI, Report a Bug, Python and any Puter link; try the script console.
  - Expected: None offered; Python and the script console say they cannot run.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-8 — Open Folder**
  - Action: In Chrome or Edge use Open Folder in the file browser (Firefox/Safari offer Add Files only).
  - Expected: The picker opens in Chrome/Edge.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-9 — Offline behaviour**
  - Action: Offline test: No service worker, so no offline page; the first load is about 8 MB of script. Scenes live in the browser; the backup holds settings only.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-10 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart three-d-three-d`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-11 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/three-d/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-THREE-D-12 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall 3D, then open `https://<server>/apps/three-d/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-SOLITAIRE: Solitaire

_Review plan section: S378. Automated: tests/test_s378_solitaire_package.py; gate: browser-check-solitaire.mjs; upstream 209._

- [ ] **EV-SOLITAIRE-1 — Install: plan preview, then approve**
  - Action: Catalog > Solitaire > Install. Read the plan, then approve (or `homesteadctl apps plan solitaire -f FILE`, then `homesteadctl apps install solitaire -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SOLITAIRE-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/solitaire/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SOLITAIRE-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `solitaire-solitaire`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SOLITAIRE-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SOLITAIRE-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SOLITAIRE-6 — Play**
  - Action: The board is dealt; make a few moves; reload.
  - Expected: The game and move count are kept; no 'Powered by Puter'; no outbound link.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SOLITAIRE-7 — Offline behaviour**
  - Action: Offline test: Stop the container and reload: the fixed 'You are offline' page, not an error page.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SOLITAIRE-8 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart solitaire-solitaire`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SOLITAIRE-9 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/solitaire/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SOLITAIRE-10 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Solitaire, then open `https://<server>/apps/solitaire/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-SUDOKU: Sudoku

_Review plan section: S379. Automated: tests/test_s379_sudoku_package.py; gate: browser-check-sudoku.mjs; upstream 333._

- [ ] **EV-SUDOKU-1 — Install: plan preview, then approve**
  - Action: Catalog > Sudoku > Install. Read the plan, then approve (or `homesteadctl apps plan sudoku -f FILE`, then `homesteadctl apps install sudoku -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SUDOKU-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/sudoku/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SUDOKU-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `sudoku-sudoku`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SUDOKU-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SUDOKU-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SUDOKU-6 — Play**
  - Action: A puzzle is dealt; place a few digits; reload.
  - Expected: The puzzle and entries are kept; the account label reads 'Homestead user' and cannot be clicked; no outbound link.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SUDOKU-7 — Offline behaviour**
  - Action: Offline test: Stop the container and reload: the fixed 'You are offline' page.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SUDOKU-8 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart sudoku-sudoku`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SUDOKU-9 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/sudoku/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-SUDOKU-10 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Sudoku, then open `https://<server>/apps/sudoku/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-EXPENSE: Expenses

_Review plan section: S380. Automated: tests/test_s380_expense_package.py; gate: browser-check-expense.mjs; upstream 79._

- [ ] **EV-EXPENSE-1 — Install: plan preview, then approve**
  - Action: Catalog > Expenses > Install. Read the plan, then approve (or `homesteadctl apps plan expense -f FILE`, then `homesteadctl apps install expense -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-EXPENSE-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/expense/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-EXPENSE-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `expense-expense`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-EXPENSE-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-EXPENSE-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-EXPENSE-6 — Receipt and report**
  - Action: Capture a receipt photo (and a PDF), fill in merchant and amount, save, reload; create a report with it; export CSV.
  - Expected: Expense and receipt kept; DevTools Network shows only requests to this app's own path (no AI or other host); a file over 8 MB is refused with a notice; no AI/OCR/Puter wording; the name reads 'Homestead user'.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-EXPENSE-7 — Offline behaviour**
  - Action: Offline test: Stop the container and reload: the fixed 'You are offline' page.
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-EXPENSE-8 — Limits**
  - Action: 8 MB receipt limit.
  - Expected: A clear error and existing data intact.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-EXPENSE-9 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart expense-expense`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-EXPENSE-10 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/expense/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-EXPENSE-11 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall Expenses, then open `https://<server>/apps/expense/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-API-CLIENT: API Client

_Review plan section: S436 (net: S434, S435). Automated: tests/test_s436_api_client_package.py; gate: browser-check-api-client.mjs, scripts/everything/test_api_client_net.mjs; upstream 679 passed._

- [ ] **EV-API-CLIENT-1 — Install: plan preview, then approve**
  - Action: Catalog > API Client > Install. Read the plan, then approve (or `homesteadctl apps plan api-client -f FILE`, then `homesteadctl apps install api-client -f FILE`).
  - Expected: Plan shows name/path, ports, volumes and sign-in; after approval the app card shows running and Doctor Apps is clean.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-2 — Opens at the right path or name**
  - Action: Open `https://<server>/apps/api-client/`.
  - Expected: The app's own page loads (no 404, no certificate surprise beyond the documented one).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-3 — Owner sign-in**
  - Action: Open the app; the bridge signs you in through Authentik. Container: `api-client-api-client`.
  - Expected: Owner is named by Homestead user name, never an id or email; the container log shows no token.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-4 — Person A (with access) can sign in**
  - Action: Grant A in People for this app. In a separate browser sign in as A.
  - Expected: A reaches the app as A (a separate account/data from the owner).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-5 — Person C (no access) is refused**
  - Action: In another browser sign in as C and open the app.
  - Expected: C is refused or sees the closed-by-default message; no app data visible.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-6 — Public request**
  - Action: Send a GET to a public address you own (a test page).
  - Expected: Status, headers and body shown; the Network tab shows only this app's own path.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-7 — Private addresses refused**
  - Action: Send to `http://127.0.0.1/`, a LAN address and the appliance name.
  - Expected: Each refused with 'That address could not be reached from your Homestead'; `net-log` shows `blocked_address`, host and path only.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-8 — Limits**
  - Action: Send a request body over 2 MB, one whose response is over 8 MB, and 61 requests in a minute.
  - Expected: A clear message for each (too large, too large, rate limited).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-9 — Unsupported surfaces**
  - Action: Look for New WebSocket, Share, raw sockets and hosted-sync wording.
  - Expected: None offered; a WebSocket import fails with 'not available on Homestead'.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-10 — User scripts**
  - Action: Add a pre-request script and a tests script to a request and send.
  - Expected: UNPROVEN. The page policy probably blocks them. Record whether they run, fail visibly or fail silently (DEFERRED S436).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-11 — Offline behaviour**
  - Action: Offline test: n/a
  - Expected: Matches the statement (record the actual result if different).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-12 — Data persists across restart**
  - Action: Note what exists (your test items). Run `docker restart api-client-api-client`, wait, reload.
  - Expected: Same data present; still signed in or sign-in works again.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-13 — Appears in backups (day 2)**
  - Action: After the 02:00 run look for `<backup root>/api-client/<UTC timestamp>/backup.json`. Contents cover `data/bridge.db`.
  - Expected: A run exists and lists this package.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **EV-API-CLIENT-14 — Uninstall leaves no orphan route (END pass)**
  - Action: Done in END-UNINSTALL-*: uninstall API Client, then open `https://<server>/apps/api-client/` and check the Caddy/route list.
  - Expected: The URL no longer answers; app disappears from the apps page and Doctor; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### EV-ADR058: cross-cutting Everything checks (from S441)

Prerequisites: Tasks, Notes, Circuits, Parametric and API Client installed with sign-in applied; A and B each signed in to each of those apps at least once (a name is only shareable after first sign-in); C has Homestead but none of those apps; the public dashboard name set up (PLAT-TUNNEL). Container names: `<package>-<service>`; bridge DB `/data/bridge.db`. Full wording: S441 section of `docs/reviews/EVERYTHING_REVIEW_PLAN.md`.

- [ ] **EV-ADR058-1 — Sign-in and names**
  - Action: A and B sign in to Tasks in separate browsers.
  - Expected: Each is named by Homestead user name; the container log has no token.
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

- [ ] **EV-ADR058-2 — Tasks sharing**
  - Action: Run the review plan's S437 steps 1-6.
  - Expected: Invite by name works, unknown name refused, B sees A's changes within seconds, C sees nothing, removing B stops changes. `docker exec tasks-tasks /everything-bridge grants list -db /data/bridge.db -app tasks`.
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

- [ ] **EV-ADR058-3 — Circuits and Parametric sharing**
  - Action: Run S439 steps 1-6 and S440 steps 1-6.
  - Expected: View-only save refused (Circuits) or saved as B's copy (Parametric); edit lands in A's one file; removal makes B's next save fail visibly.
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

- [ ] **EV-ADR058-4 — Purge on People removal**
  - Action: A shares a Tasks list and Circuits project with B; B creates a Notes read link. Remove B in People.
  - Expected: Within about two minutes B's grants and links are revoked in every Everything app; B's stream ends; A's data intact; audit `people.purge` ok per app (pending means a container was stopped; it retries every 2 minutes). Disabling does not purge (DEFERRED S427).
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

- [ ] **EV-ADR058-5 — Local read links**
  - Action: Run S437 steps 5 and 7 and S438 steps 1-6.
  - Expected: Tasks and Notes view links open in a private window as a plain page; Stop sharing gives 404; links end in `/_homestead/view/<43 characters>`.
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

- [ ] **EV-ADR058-6 — Public read links plan**
  - Action: `homesteadctl apps read-links plan notes`, read it, then `approve notes --digest <digest>`. Repeat for tasks.
  - Expected: App restarts once; audit `readlinks.expose`; new links start with `https://homestead.<public-domain>/apps/notes/_homestead/view/`.
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

- [ ] **EV-ADR058-7 — What the public name answers**
  - Action: From mobile data with no session open the public view link, then `/apps/notes/`, `/apps/notes/_homestead/session`, `/apps/notes/_homestead/data/kv`, `/apps/tasks/_homestead/events`. Then `plan notes --off` and approve `--off`.
  - Expected: The view link opens the plain page; the other four answer 404; Stop sharing gives 404 within a moment; audit `readlinks.unexpose` and new links are local again.
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

- [ ] **EV-ADR058-8 — Events through the edge**
  - Action: A and B open the same Tasks list over the public name; A adds a task.
  - Expected: B sees it within seconds; Network tab shows one open `text/event-stream` request with events arriving individually (a 25 s stall means the edge buffers; Tasks falls back to polling).
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

- [ ] **EV-ADR058-9 — api-client egress**
  - Action: In API Client fetch a public host you own; then `http://127.0.0.1/`, a LAN address, the appliance name. Then `docker exec api-client-api-client /everything-bridge net-log -db /data/bridge.db -app api-client -limit 20`.
  - Expected: Public works; others refused with 'That address could not be reached from your Homestead'; log shows host/path/status/bytes and `blocked_address`, no headers or bodies.
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

- [ ] **EV-ADR058-10 — Limits**
  - Action: 61 requests in a minute from API Client; a 2 MB body; an 8 MB response; a 61st share attempt in an hour; an expired read link.
  - Expected: 61st rate limited; clear too-large messages; 61st share refused; expired link is 404.
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

- [ ] **EV-ADR058-11 — Backup and uninstall**
  - Action: Check `data/bridge.db` in a backup run (day 2); uninstall one app.
  - Expected: bridge.db is in the backup; route and container are removed.
  - If it fails: S441 section of the review plan; DEFERRED S427-S441.
  - Result: ____  Notes: ____

## Craft apps

Craft apps (ADR-028/029/045) are built on the appliance from the HAI-signed manifest, served at `https://<server>/apps/<app>/`, have no sign-in (local only; not exposed publicly) and are stateless. Hooked apps open/save through Nextcloud (needs an operator session); the rest are browser-local.

- [ ] **CRAFT-PLAT-1 — Craft plan and build**
  - Action: `homesteadctl craft plan wordcraft` then `craft build wordcraft` (or the Craft page).
  - Expected: Plan shows the signed manifest (counter 8); build succeeds; a bad signature would be refused.
  - If it fails: Craft page logs.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PLAT-2 — Start page**
  - Action: Open the craft start page.
  - Expected: All 13 listed with their paths.
  - If it fails: S281.
  - Result: ____  Notes: ____


- [ ] **PLAT-CRAFTLINK-1 — Nextcloud link for craft**
  - Action: Craft Files page / `homesteadctl files ...` after Nextcloud (SRV-NEXTCLOUD) and at least one hooked craft app are installed.
  - Expected: Link status shows linked; the craft install links automatically (S326).
  - If it fails: Craft page.
  - Result: ____  Notes: ____

### CRAFT-WORDCRAFT: WordCraft (word processor, browser-local)

- [ ] **CRAFT-WORDCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan wordcraft`, `craft build wordcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-WORDCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/wordcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-WORDCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-WORDCRAFT-4 — Core feature**
  - Action: Type a heading and two paragraphs, apply bold, save/export to a file, reload.
  - Expected: Text and formatting stay; export opens elsewhere.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-WORDCRAFT-5 — Data stays in the browser**
  - Action: Reload and close/reopen the browser tab.
  - Expected: Work is kept only in this browser; there is no server copy (expected).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-WORDCRAFT-6 — Survives restart**
  - Action: `docker restart wordcraft-wordcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-WORDCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-WORDCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/wordcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-GRIDCRAFT: GridCraft (spreadsheet, hooked (open/save to Nextcloud))

- [ ] **CRAFT-GRIDCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan gridcraft`, `craft build gridcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-GRIDCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/gridcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-GRIDCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-GRIDCRAFT-4 — Core feature**
  - Action: Enter numbers in a column, a SUM formula and a chart; Save to Nextcloud, then reopen it from Nextcloud.
  - Expected: Formula result and chart return.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-GRIDCRAFT-5 — Nextcloud hook**
  - Action: Open and save via Nextcloud (hooked (open/save to Nextcloud)).
  - Expected: Works as stated; needs Nextcloud installed and linked.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-GRIDCRAFT-6 — Survives restart**
  - Action: `docker restart gridcraft-gridcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-GRIDCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-GRIDCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/gridcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-PDFCRAFT: PdfCraft (PDF tools, hooked (open/save to Nextcloud))

- [ ] **CRAFT-PDFCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan pdfcraft`, `craft build pdfcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PDFCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/pdfcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PDFCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PDFCRAFT-4 — Core feature**
  - Action: Open a small PDF from Nextcloud, merge or reorder pages, save back to Nextcloud.
  - Expected: The edited PDF is in Nextcloud and opens.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PDFCRAFT-5 — Nextcloud hook**
  - Action: Open and save via Nextcloud (hooked (open/save to Nextcloud)).
  - Expected: Works as stated; needs Nextcloud installed and linked.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PDFCRAFT-6 — Survives restart**
  - Action: `docker restart pdfcraft-pdfcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PDFCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PDFCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/pdfcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-DECKCRAFT: DeckCraft (slides, browser-local)

- [ ] **CRAFT-DECKCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan deckcraft`, `craft build deckcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DECKCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/deckcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DECKCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DECKCRAFT-4 — Core feature**
  - Action: Make two slides with a title and an image, start the presentation.
  - Expected: Slides show and advance.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DECKCRAFT-5 — Data stays in the browser**
  - Action: Reload and close/reopen the browser tab.
  - Expected: Work is kept only in this browser; there is no server copy (expected).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DECKCRAFT-6 — Survives restart**
  - Action: `docker restart deckcraft-deckcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DECKCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DECKCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/deckcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-VECTORCRAFT: VectorCraft (vector graphics, browser-local)

- [ ] **CRAFT-VECTORCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan vectorcraft`, `craft build vectorcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-VECTORCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/vectorcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-VECTORCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-VECTORCRAFT-4 — Core feature**
  - Action: Draw a shape and a path, group them, export SVG.
  - Expected: The SVG opens in a browser and matches.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-VECTORCRAFT-5 — Data stays in the browser**
  - Action: Reload and close/reopen the browser tab.
  - Expected: Work is kept only in this browser; there is no server copy (expected).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-VECTORCRAFT-6 — Survives restart**
  - Action: `docker restart vectorcraft-vectorcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-VECTORCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-VECTORCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/vectorcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-PHOTOCRAFT: PhotoCraft (photo editor, browser-local)

- [ ] **CRAFT-PHOTOCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan photocraft`, `craft build photocraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PHOTOCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/photocraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PHOTOCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PHOTOCRAFT-4 — Core feature**
  - Action: Open a photo, crop and adjust brightness, export a PNG.
  - Expected: The export shows the edits.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PHOTOCRAFT-5 — Data stays in the browser**
  - Action: Reload and close/reopen the browser tab.
  - Expected: Work is kept only in this browser; there is no server copy (expected).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PHOTOCRAFT-6 — Survives restart**
  - Action: `docker restart photocraft-photocraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PHOTOCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-PHOTOCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/photocraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-LIGHTCRAFT: LightCraft (photo develop, browser-local)

- [ ] **CRAFT-LIGHTCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan lightcraft`, `craft build lightcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-LIGHTCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/lightcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-LIGHTCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-LIGHTCRAFT-4 — Core feature**
  - Action: Open a photo, change exposure and white balance, export.
  - Expected: The export shows the edits.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-LIGHTCRAFT-5 — Data stays in the browser**
  - Action: Reload and close/reopen the browser tab.
  - Expected: Work is kept only in this browser; there is no server copy (expected).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-LIGHTCRAFT-6 — Survives restart**
  - Action: `docker restart lightcraft-lightcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-LIGHTCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-LIGHTCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/lightcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-FIGCRAFT: FigCraft (interface design, browser-local)

- [ ] **CRAFT-FIGCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan figcraft`, `craft build figcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FIGCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/figcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FIGCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FIGCRAFT-4 — Core feature**
  - Action: Make a frame with a rectangle and text, duplicate it, export a PNG.
  - Expected: Both frames render and export.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FIGCRAFT-5 — Data stays in the browser**
  - Action: Reload and close/reopen the browser tab.
  - Expected: Work is kept only in this browser; there is no server copy (expected).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FIGCRAFT-6 — Survives restart**
  - Action: `docker restart figcraft-figcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FIGCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FIGCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/figcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-FILMCRAFT: FilmCraft (video editor, hooked (open only from Nextcloud))

- [ ] **CRAFT-FILMCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan filmcraft`, `craft build filmcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FILMCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/filmcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FILMCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FILMCRAFT-4 — Core feature**
  - Action: Open a short clip from Nextcloud, trim it, play it back.
  - Expected: Open from Nextcloud works; saving back is not offered (open-only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FILMCRAFT-5 — Nextcloud hook**
  - Action: Open and save via Nextcloud (hooked (open only from Nextcloud)).
  - Expected: Works as stated; needs Nextcloud installed and linked.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FILMCRAFT-6 — Survives restart**
  - Action: `docker restart filmcraft-filmcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FILMCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-FILMCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/filmcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-EFFECTCRAFT: EffectCraft (motion effects, hooked (open only from Nextcloud))

- [ ] **CRAFT-EFFECTCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan effectcraft`, `craft build effectcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-EFFECTCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/effectcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-EFFECTCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-EFFECTCRAFT-4 — Core feature**
  - Action: Open an image or clip from Nextcloud, add an effect, preview.
  - Expected: Open from Nextcloud works; the effect previews.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-EFFECTCRAFT-5 — Nextcloud hook**
  - Action: Open and save via Nextcloud (hooked (open only from Nextcloud)).
  - Expected: Works as stated; needs Nextcloud installed and linked.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-EFFECTCRAFT-6 — Survives restart**
  - Action: `docker restart effectcraft-effectcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-EFFECTCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-EFFECTCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/effectcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-SOUNDCRAFT: SoundCraft (audio editor, browser-local)

- [ ] **CRAFT-SOUNDCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan soundcraft`, `craft build soundcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-SOUNDCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/soundcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-SOUNDCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-SOUNDCRAFT-4 — Core feature**
  - Action: Load a short audio file, trim and fade, play it, export.
  - Expected: The export plays with the edits.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-SOUNDCRAFT-5 — Data stays in the browser**
  - Action: Reload and close/reopen the browser tab.
  - Expected: Work is kept only in this browser; there is no server copy (expected).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-SOUNDCRAFT-6 — Survives restart**
  - Action: `docker restart soundcraft-soundcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-SOUNDCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-SOUNDCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/soundcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-DESIGNCRAFT: DesignCraft (layout and design, browser-local)

- [ ] **CRAFT-DESIGNCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan designcraft`, `craft build designcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DESIGNCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/designcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DESIGNCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DESIGNCRAFT-4 — Core feature**
  - Action: Make a poster layout with text and an image, export.
  - Expected: The export matches the layout.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DESIGNCRAFT-5 — Data stays in the browser**
  - Action: Reload and close/reopen the browser tab.
  - Expected: Work is kept only in this browser; there is no server copy (expected).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DESIGNCRAFT-6 — Survives restart**
  - Action: `docker restart designcraft-designcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DESIGNCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-DESIGNCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/designcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____


### CRAFT-CADCRAFT: CADCraft (CAD, hooked (open/save to Nextcloud))

- [ ] **CRAFT-CADCRAFT-1 — Build and install**
  - Action: `homesteadctl craft plan cadcraft`, `craft build cadcraft`, then install (plan preview, approve).
  - Expected: Image built locally; app card running.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-CADCRAFT-2 — Opens at its path**
  - Action: Open `https://<server>/apps/cadcraft/`.
  - Expected: App loads; no sign-in prompt (class unsupported).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-CADCRAFT-3 — Not reachable publicly**
  - Action: From mobile data open the same path on the public name.
  - Expected: Refused or not routed (local only).
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-CADCRAFT-4 — Core feature**
  - Action: Draw a simple solid or sketch, save to Nextcloud, reopen it.
  - Expected: The model returns.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-CADCRAFT-5 — Nextcloud hook**
  - Action: Open and save via Nextcloud (hooked (open/save to Nextcloud)).
  - Expected: Works as stated; needs Nextcloud installed and linked.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-CADCRAFT-6 — Survives restart**
  - Action: `docker restart cadcraft-cadcraft`, reload.
  - Expected: App returns; browser-held work still there.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-CADCRAFT-7 — Backups**
  - Action: Backups page.
  - Expected: Not backup-schedulable (stateless); SKIP.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

- [ ] **CRAFT-CADCRAFT-8 — Uninstall leaves no orphan route (END pass)**
  - Action: After END-UNINSTALL, open `https://<server>/apps/cadcraft/`.
  - Expected: No longer answers; no leftover route or container.
  - If it fails: Container log (`docker logs <package>-<service>`), Doctor page, audit log.
  - Result: ____  Notes: ____

## End-of-run checks

- [ ] **END-PURGE-1 — People removal purge**
  - Action: Remove B (see EV-ADR058-4) and also check a server app: B's account/data in an OIDC app.
  - Expected: Everything data purged; for server apps record what remains (app-side accounts may remain).
  - If it fails: DEFERRED S427.
  - Result: ____  Notes: ____

- [ ] **END-BACKUP-1 — Backup and restore round trip**
  - Action: Follow `docs/architecture/BACKUP_RESTORE_RUNBOOK.md` for one small app (age decrypt, untar, restore); compare data.
  - Expected: Data restored; there is no run-now control, so use the nightly run or a platform backup.
  - If it fails: Runbook.
  - Result: ____  Notes: ____

- [ ] **END-DOCTOR-1 — Doctor clean**
  - Action: Doctor page and `homesteadctl doctor --live` with everything installed.
  - Expected: No red items; amber items have guidance. Record each.
  - If it fails: 
  - Result: ____  Notes: ____

- [ ] **END-AUDIT-1 — Audit entries**
  - Action: Audit page / `homesteadctl audit list`.
  - Expected: Entries exist for installs, grants, tunnel, readlinks, people.purge, backups; no secrets or tokens in any entry.
  - If it fails: 
  - Result: ____  Notes: ____

- [ ] **END-PARITY-1 — CLI/dashboard parity spot checks**
  - Action: Pick five actions (grant, install plan, backup list, tunnel plan, updates list) and do each on both sides.
  - Expected: Same result either way.
  - If it fails: docs/PARITY.md.
  - Result: ____  Notes: ____

- [ ] **PLAT-TUNNEL-7 — Remove the tunnel plan**
  - Action: Do this last of the tunnel work. Plan removal of the tunnel on the Tunnel page, approve.
  - Expected: Public names stop answering; local names still work.
  - If it fails: Doctor Tunnel and Certificates sections; ADR-046/047/048.
  - Result: ____  Notes: ____

- [ ] **END-UNINSTALL-1 — Uninstall: Everything apps**
  - Action: Uninstall each from the app card or `homesteadctl apps uninstall <pkg>` (plan preview, approve). Then open each former URL.
  - Expected: None answer; Caddy routes gone; Doctor clean; data directories handled as the plan said.
  - If it fails: 
  - Result: ____  Notes: ____

- [ ] **END-UNINSTALL-2 — Uninstall: craft apps**
  - Action: Uninstall each from the app card or `homesteadctl apps uninstall <pkg>` (plan preview, approve). Then open each former URL.
  - Expected: None answer; Caddy routes gone; Doctor clean; data directories handled as the plan said.
  - If it fails: 
  - Result: ____  Notes: ____

- [ ] **END-UNINSTALL-3 — Uninstall: server apps except Authentik**
  - Action: Uninstall each from the app card or `homesteadctl apps uninstall <pkg>` (plan preview, approve). Then open each former URL.
  - Expected: None answer; Caddy routes gone; Doctor clean; data directories handled as the plan said.
  - If it fails: 
  - Result: ____  Notes: ____

- [ ] **END-UNINSTALL-4 — Uninstall: Nextcloud, Immich, Jellyfin**
  - Action: Uninstall each from the app card or `homesteadctl apps uninstall <pkg>` (plan preview, approve). Then open each former URL.
  - Expected: None answer; Caddy routes gone; Doctor clean; data directories handled as the plan said.
  - If it fails: 
  - Result: ____  Notes: ____

- [ ] **END-UNINSTALL-5 — Uninstall: Authentik (last)**
  - Action: Uninstall each from the app card or `homesteadctl apps uninstall <pkg>` (plan preview, approve). Then open each former URL.
  - Expected: None answer; Caddy routes gone; Doctor clean; data directories handled as the plan said.
  - If it fails: 
  - Result: ____  Notes: ____

- [ ] **END-UNINSTALL-6 — Final Doctor and route check**
  - Action: Doctor and `homesteadctl apps list`.
  - Expected: Nothing orphaned.
  - If it fails: 
  - Result: ____  Notes: ____


## Results summary

| Group | Step IDs | PASS | FAIL | SKIP | BLOCKED |
|---|---|---|---|---|---|
| PLAT-DEPLOY | PLAT-DEPLOY-1 to PLAT-DEPLOY-2 (2) |  |  |  |  |
| PLAT-CLAIM | PLAT-CLAIM-1 to PLAT-CLAIM-1a (2) |  |  |  |  |
| PLAT-SETUP | PLAT-SETUP-1 to PLAT-SETUP-6 (9) |  |  |  |  |
| PLAT-AUTH | PLAT-AUTH-1 to PLAT-AUTH-1 (1) |  |  |  |  |
| PLAT-OWNER | PLAT-OWNER-1 to PLAT-OWNER-2 (2) |  |  |  |  |
| PLAT-PEOPLE | PLAT-PEOPLE-1 to PLAT-PEOPLE-8 (8) |  |  |  |  |
| PLAT-TUNNEL | PLAT-TUNNEL-1 to PLAT-TUNNEL-7 (7) |  |  |  |  |
| PLAT-DASH | PLAT-DASH-1 to PLAT-DASH-2 (2) |  |  |  |  |
| PLAT-DOCTOR | PLAT-DOCTOR-1 to PLAT-DOCTOR-2 (2) |  |  |  |  |
| PLAT-NAMES | PLAT-NAMES-1 to PLAT-NAMES-1 (1) |  |  |  |  |
| PLAT-CATALOG | PLAT-CATALOG-1 to PLAT-CATALOG-1 (1) |  |  |  |  |
| PLAT-BACKUP | PLAT-BACKUP-1 to PLAT-BACKUP-3 (3) |  |  |  |  |
| PLAT-UPDATE | PLAT-UPDATE-1 to PLAT-UPDATE-2 (3) |  |  |  |  |
| SRV-ACTUAL-BUDGET | SRV-ACTUAL-BUDGET-1 to SRV-ACTUAL-BUDGET-9 (10) |  |  |  |  |
| SRV-AUDIOBOOKSHELF | SRV-AUDIOBOOKSHELF-1 to SRV-AUDIOBOOKSHELF-11 (11) |  |  |  |  |
| SRV-BOOKSTACK | SRV-BOOKSTACK-1 to SRV-BOOKSTACK-10 (10) |  |  |  |  |
| SRV-CALIBRE-WEB-AUTOMATED | SRV-CALIBRE-WEB-AUTOMATED-1 to SRV-CALIBRE-WEB-AUTOMATED-11 (11) |  |  |  |  |
| SRV-FRESHRSS | SRV-FRESHRSS-1 to SRV-FRESHRSS-10 (11) |  |  |  |  |
| SRV-GITEA | SRV-GITEA-1 to SRV-GITEA-11 (12) |  |  |  |  |
| SRV-IMMICH | SRV-IMMICH-1 to SRV-IMMICH-11 (11) |  |  |  |  |
| SRV-JELLYFIN | SRV-JELLYFIN-1 to SRV-JELLYFIN-11 (11) |  |  |  |  |
| SRV-KOMGA | SRV-KOMGA-1 to SRV-KOMGA-9 (9) |  |  |  |  |
| SRV-LINKDING | SRV-LINKDING-1 to SRV-LINKDING-9 (9) |  |  |  |  |
| SRV-MEALIE | SRV-MEALIE-1 to SRV-MEALIE-9 (9) |  |  |  |  |
| SRV-MEMOS | SRV-MEMOS-1 to SRV-MEMOS-9 (10) |  |  |  |  |
| SRV-NAVIDROME | SRV-NAVIDROME-1 to SRV-NAVIDROME-11 (12) |  |  |  |  |
| SRV-NEXTCLOUD | SRV-NEXTCLOUD-1 to SRV-NEXTCLOUD-10 (11) |  |  |  |  |
| SRV-PAPERLESS-NGX | SRV-PAPERLESS-NGX-1 to SRV-PAPERLESS-NGX-9 (9) |  |  |  |  |
| SRV-UPTIME-KUMA | SRV-UPTIME-KUMA-1 to SRV-UPTIME-KUMA-12 (12) |  |  |  |  |
| SRV-VAULTWARDEN | SRV-VAULTWARDEN-1 to SRV-VAULTWARDEN-9 (10) |  |  |  |  |
| SRV-VIKUNJA | SRV-VIKUNJA-1 to SRV-VIKUNJA-9 (9) |  |  |  |  |
| PLAT-FIREWALL | PLAT-FIREWALL-1a to PLAT-FIREWALL-3 (4) |  |  |  |  |
| EV-TASKS | EV-TASKS-1 to EV-TASKS-16 (16) |  |  |  |  |
| EV-PROJECTS | EV-PROJECTS-1 to EV-PROJECTS-13 (13) |  |  |  |  |
| EV-RECORDS | EV-RECORDS-1 to EV-RECORDS-14 (14) |  |  |  |  |
| EV-NOTES | EV-NOTES-1 to EV-NOTES-14 (14) |  |  |  |  |
| EV-CONTACTS | EV-CONTACTS-1 to EV-CONTACTS-12 (12) |  |  |  |  |
| EV-INVENTORY | EV-INVENTORY-1 to EV-INVENTORY-13 (13) |  |  |  |  |
| EV-TIME-TRACKING | EV-TIME-TRACKING-1 to EV-TIME-TRACKING-12 (12) |  |  |  |  |
| EV-CIRCUITS | EV-CIRCUITS-1 to EV-CIRCUITS-13 (13) |  |  |  |  |
| EV-PARAMETRIC | EV-PARAMETRIC-1 to EV-PARAMETRIC-14 (14) |  |  |  |  |
| EV-THREE-D | EV-THREE-D-1 to EV-THREE-D-12 (12) |  |  |  |  |
| EV-SOLITAIRE | EV-SOLITAIRE-1 to EV-SOLITAIRE-10 (10) |  |  |  |  |
| EV-SUDOKU | EV-SUDOKU-1 to EV-SUDOKU-10 (10) |  |  |  |  |
| EV-EXPENSE | EV-EXPENSE-1 to EV-EXPENSE-11 (11) |  |  |  |  |
| EV-API-CLIENT | EV-API-CLIENT-1 to EV-API-CLIENT-14 (14) |  |  |  |  |
| EV-ADR058 | EV-ADR058-1 to EV-ADR058-11 (11) |  |  |  |  |
| CRAFT-PLAT | CRAFT-PLAT-1 to CRAFT-PLAT-2 (2) |  |  |  |  |
| PLAT-CRAFTLINK | PLAT-CRAFTLINK-1 to PLAT-CRAFTLINK-1 (1) |  |  |  |  |
| CRAFT-WORDCRAFT | CRAFT-WORDCRAFT-1 to CRAFT-WORDCRAFT-8 (8) |  |  |  |  |
| CRAFT-GRIDCRAFT | CRAFT-GRIDCRAFT-1 to CRAFT-GRIDCRAFT-8 (8) |  |  |  |  |
| CRAFT-PDFCRAFT | CRAFT-PDFCRAFT-1 to CRAFT-PDFCRAFT-8 (8) |  |  |  |  |
| CRAFT-DECKCRAFT | CRAFT-DECKCRAFT-1 to CRAFT-DECKCRAFT-8 (8) |  |  |  |  |
| CRAFT-VECTORCRAFT | CRAFT-VECTORCRAFT-1 to CRAFT-VECTORCRAFT-8 (8) |  |  |  |  |
| CRAFT-PHOTOCRAFT | CRAFT-PHOTOCRAFT-1 to CRAFT-PHOTOCRAFT-8 (8) |  |  |  |  |
| CRAFT-LIGHTCRAFT | CRAFT-LIGHTCRAFT-1 to CRAFT-LIGHTCRAFT-8 (8) |  |  |  |  |
| CRAFT-FIGCRAFT | CRAFT-FIGCRAFT-1 to CRAFT-FIGCRAFT-8 (8) |  |  |  |  |
| CRAFT-FILMCRAFT | CRAFT-FILMCRAFT-1 to CRAFT-FILMCRAFT-8 (8) |  |  |  |  |
| CRAFT-EFFECTCRAFT | CRAFT-EFFECTCRAFT-1 to CRAFT-EFFECTCRAFT-8 (8) |  |  |  |  |
| CRAFT-SOUNDCRAFT | CRAFT-SOUNDCRAFT-1 to CRAFT-SOUNDCRAFT-8 (8) |  |  |  |  |
| CRAFT-DESIGNCRAFT | CRAFT-DESIGNCRAFT-1 to CRAFT-DESIGNCRAFT-8 (8) |  |  |  |  |
| CRAFT-CADCRAFT | CRAFT-CADCRAFT-1 to CRAFT-CADCRAFT-8 (8) |  |  |  |  |
| END-PURGE | END-PURGE-1 to END-PURGE-1 (1) |  |  |  |  |
| END-BACKUP | END-BACKUP-1 to END-BACKUP-1 (1) |  |  |  |  |
| END-DOCTOR | END-DOCTOR-1 to END-DOCTOR-1 (1) |  |  |  |  |
| END-AUDIT | END-AUDIT-1 to END-AUDIT-1 (1) |  |  |  |  |
| END-PARITY | END-PARITY-1 to END-PARITY-1 (1) |  |  |  |  |
| END-UNINSTALL | END-UNINSTALL-1 to END-UNINSTALL-6 (6) |  |  |  |  |
| **Total** | 541 steps |  |  |  |  |
