# S326 — Craft installs use the same setup, by path, linked to Nextcloud

## Goal
ADR-048 §4. Craft apps already install through the catalog wizard with
the S324 appliance-run setup. This slice:
- fixes the defect that put a craft app on a name of its own
  (lightcraft at `lightcraft.lan`);
- makes linking craft apps to Nextcloud for files part of that setup.

## Root cause of the own-name route
The dashboard's `routeFromManifest` (`catalog/app-routes.ts`) built the
route behind the wizard's "Give <app> an address" button. That button is
shown when an installed app has no route, for example when the wizard was
closed before the address was added. It built a host route and never set
`Path`, `StripPrefix` or `Redirects`, even for a `serve: path` package.
The edge reconcile accepted the own-name craft route. The ADR-045 §5 move
runs only when the daemon starts, so the app stayed on its own name. The
same bug also hit Nextcloud and the other path apps.

## Scope
- Always by path:
  - `routeFromManifest` gives a path package its path, prefix strip and
    redirects.
  - The edge reconcile moves any craft route without a path to
    `/apps/<name>/` with the prefix stripped, no tunnel names and
    unpublished, whichever client sent it.
  - An app's address step finds the app already on its own name and
    reconciles the table, which moves it.
- `nextcloud-link` setup step (`internal/server/craftlink.go`):
  - A hooked craft app (cadcraft, effectcraft, filmcraft, gridcraft,
    pdfcraft) installed while Nextcloud is installed, or a link exists,
    gets the step right after its address.
  - Nextcloud installed with no link yet links the hooked craft apps
    already installed with an address, as the last step of its own
    setup.
  - Apps set to browser-local are left out. Lightcraft has no file hook
    and is never linked.
  - The install plan carries `files_link` (apps, review lines
    "Link <app> to Nextcloud for files", base URL, account, root,
    `new_link`, digest).
  - The install approves it with `setup.files_link {digest}`. There is
    no other approval, and a stale digest answers 409.
  - The step re-plans and compares its digest. When nothing is linked,
    it links this appliance's Nextcloud through the craft files manager.
    It retries for up to 5 minutes while Nextcloud is still starting,
    then checks each app is ready.
  - A failure stops the setup. Each app's link is audited as
    `craftfiles.link`, as are failures.
  - Resume, skip, the card, Home and Doctor are S324's.
- Linking the appliance's Nextcloud (`craftfiles.Manager.LinkAppliance`,
  and `appliance: true` on `POST /api/v1/craft/files/plan`):
  - The base URL is `https://<appliance name>/apps/nextcloud`, or its own
    name for an install that predates ADR-044.
  - The account comes from `NEXTCLOUD_ADMIN_USER` (`admin`).
  - The app password is created with the administrator password that
    Homestead generated at the install (the package secret
    `admin_password`). The approval carries no password.
  - `GET /api/v1/craft/files` carries `appliance_nextcloud`.
- CLI:
  - `apps plan` prints `files:` lines.
  - `apps install` plans first and, when the plan has a link, sends
    `setup.files_link` with the install digest. `--follow` follows the
    setup.
  - `apps install --public --approve-public D` also sends the link.
  - `craft files link --appliance [--root F] [--yes]`.
- Dashboard:
  - The wizard's install permit lists the link under "Files in
    Nextcloud" and sends `setup.files_link`.
  - The setup panel names the step and shows its plan on Review.
  - The Craft files page offers "Review link to this appliance's
    Nextcloud" and keeps the manual link, rotate and unlink.

## Rules
AGENTS.md slice rules apply. There are no new routes, migrations or CLI
commands. PARITY records the new fields and flag.

## Acceptance criteria
- Go tests cover:
  - a craft route always by path (the lightcraft regression);
  - the link step after a craft app's address;
  - Nextcloud's setup linking the installed craft apps;
  - retry while Nextcloud starts;
  - a failure stopping the setup;
  - `LinkAppliance` and the appliance plan.
- CLI tests cover `apps plan`/`apps install` with a link and
  `craft files link --appliance`.
- Dashboard specs cover `routeFromManifest`, the wizard review, the setup
  panel and the Craft files page.
- All Go, Python and dashboard checks pass.

## Live check
On VM 2 with Nextcloud installed:
1. Install gridcraft from the wizard. The review lists "Link GridCraft to
   Nextcloud for files".
2. The card shows the setup done.
3. The Craft files page shows the link, and the app opens a document from
   Nextcloud.
4. The app answers only at `/apps/gridcraft/`.
