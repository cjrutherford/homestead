# S319 — Jellyfin sets itself up

## Goal
From the owner's VM 2 acceptance run (2026-10-27): after installing
Jellyfin, the owner still had to name the server, create the root account
and set up media libraries in Jellyfin's startup wizard.

## Owner decisions (2026-10-27)
- Create `tv`, `movies` and `music` folders on bulk storage and make each a
  library.

## Scope
- The S318 first-run mechanism gains a `jellyfin` routine and optional
  `libraries:` (name, kind `movies`|`tvshows`|`music`, container path). A
  library's path must be one of the service's persistent data paths, and
  only Jellyfin's routine takes libraries.
- The Jellyfin package declares three bulk data paths, provisioned at
  install: `<bulk>/jellyfin/media/{movies,tv,music}` mounted at
  `/media/{movies,tv,music}`. It also declares a held, revealable
  `admin_password` secret.
- The routine drives the API the wizard page uses, through curl in the
  container, with request bodies (the password among them) on stdin. It
  runs only while `/System/Info/Public` reports `StartupWizardCompleted:
  false`. It names the server **Homestead** (en-US metadata defaults),
  creates the administrator `admin`, adds the libraries "Movies", "TV
  Shows" and "Music" (skipping any that already exist; real-time
  monitoring on), keeps remote access on without router port mapping,
  and completes the wizard. It tries the package's Base URL
  (`/apps/jellyfin`) first and then the root, for installs made before
  S315.
- Like Immich (S318), it runs at the end of install and before sign-in
  changes anything.

## Rules
AGENTS.md slice rules apply. No route, command or surface changes, so
PARITY is unchanged. No ADR: this extends S318's manifest-named
first-party routine.

## Acceptance Criteria
- Go tests: the wizard sequence and bodies, password only on stdin,
  existing libraries skipped, a completed wizard is not touched, the root
  fallback, and a refusal stops at the failing step. Validation covers the
  library kind and path, and libraries on a routine that takes none.
- Live, against a fresh Jellyfin 12.2 on VM 2: the wizard completes, the
  server is named Homestead, the three libraries exist and the
  administrator signs in with the generated password.
