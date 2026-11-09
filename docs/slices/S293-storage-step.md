# S293 — Storage step UX

## Goal
Make the Storage step and page usable by an owner who doesn't know Homestead's storage classes. Show the real data disks first and explain what each class is for. Propose a layout the owner can apply with one approval, and stop asking the owner to type their own name.

## Dependencies
S292.

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

This covers finding F4 from the 2026-10-20 KVM walkthrough. The step's done rule is unchanged: it is done when the application class is assigned (ADR-036). Mounted storage only (ADR-013): nothing formats, partitions or mounts.

## Acceptance Criteria
- `GET /api/v1/storage/mounts` adds `kind` and `suggested` to each entry:
  - `kind`:
    - `data`: a registrable filesystem on a block device other than the root filesystem's, outside system paths;
    - `system`: the root filesystem, a path on its device, a non-`/dev/` device, or a path under `/boot`, `/etc`, `/proc`, `/sys`, `/dev`, `/run`, `/usr`, `/var`, `/opt/homestead`, `/snap`;
    - `unusable`: never registrable.
  - `suggested` lists only unassigned classes, and only on data disks:
    - Name hints come first: a label or path that says backup gets backup; media, bulk, photos and the like get bulk; app, db, ssd or fast get application and database.
    - The remaining disks go by size: the smallest gets application and database, the largest gets bulk, and a third disk gets backup.
    - A single disk gets application, database and bulk, never backup.
    - Cache is never suggested.
  - A suggestion is only a proposal. Nothing is registered or assigned until the owner acts.
- Register and assign record the caller (the session user, or `local` on the root-only socket) when no actor is sent.
  - The CLI's `--actor` flag becomes optional.
  - The dashboard drops both "Acting owner" fields.
- `homesteadctl storage mounts` lists data disks first and prints `kind=` and `suggested=`.
- The Storage page:
  - shows data disks in the Discovered mounts table, and folds system and unusable mounts into "Other mounts (N)";
  - explains the five classes;
  - names the classes that are still unassigned;
  - marks unassigned classes in the class picker;
  - offers a Suggested layout with "Use the suggested layout". This registers each suggested mount that is not yet registered (label from the disk label or path, sanitized and made unique), then assigns each class, one call at a time. It stops and reports at the first refusal.
- The Storage step says that bulk and backup can wait, because an app that needs them asks at install which registered mount to use.
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- `docs/PARITY.md` (mounts fields, optional actor), `docs/DEFERRED.md`, `docs/DEVELOPMENT.md`.

## Handoff
Next: S294 domain and tunnel (ADR first), then S295 app names, app SSO and backups in setup (ADR first).
