# S390 disposable Jellyfin / Authentik attempt

## Result

Blocked before the S390 end-to-end exercise. On 2026-10-30, from this worktree, `timeout 390s tests/e2e/s390-disposable-probe.sh` started the existing isolated Compose fixture. Jellyfin (`jellyfin/jellyfin:12.2`) and Authentik (`ghcr.io/goauthentik/server:2026.8.3`, matching the catalog) both passed their HTTP readiness probes. The script exited 2 deliberately: it is a startup probe, not an S390 test. These were real containers, not mocks; neither readiness check authenticated a user.

The Compose project was `homestead-s390-homestead-s390-b55lzpbl`. After exit, Docker queries filtered by its Compose project label showed no containers, networks, or volumes; its temporary directory was absent. The probe generated disposable secrets in private temporary files and did not print credentials. No installed stack, VM, production account, or production data was used.

## Exact missing prerequisite and unproven cases

The existing reproducible fixture (`tests/e2e/s390-disposable-probe.sh` and `s390-disposable-compose.yaml`) only starts Authentik, PostgreSQL and Jellyfin. It has no isolated Homestead daemon and state, managed-person roster, initialized Authentik API identity, TLS edge/issuer, Jellyfin SSO plugin configuration, browser sign-in driver, or publication observation/injection point. Those components must be provisioned against disposable resources before the S390 install workflow can be exercised. A real public tunnel additionally requires a separately authorized disposable tunnel and domain; this local-only attempt did not use one. The startup fixture is not a complete reproducible S390 E2E harness.

No claim is made that owner full access, ordinary user access, app administrator access, denial for a person assigned none, or publication ordering works. In particular, checking Authentik group membership alone would not prove that the Jellyfin SSO session enforces the resulting role; and a mocked publication observer would only prove local orchestration ordering, not actual tunnel publication.

## Next bounded attempt

Extend the disposable fixture to bootstrap a separate Homestead daemon and SQLite/YAML state with a disposable edge and HTTPS issuer; initialize Authentik and four disposable identities using private files or stdin; install the catalog-pinned Jellyfin SSO plugin through the actual Homestead path; and execute the reviewed install with owner, user, admin and none decisions. Drive real Authentik-to-Jellyfin sign-ins for all four, assert app-side role and denied access, and capture the setup events and publication boundary. Inject failures separately before sign-in and during grants to show publication has not started. Label any substituted tunnel as a mock and retain the separate real-tunnel release gate. Always tear down only the unique project and its temporary data, including on timeout.

## Isolated appliance initialization attempt (2026-10-30)

The new fixture `tests/e2e/scripts/s390-isolated-init.sh` is **incomplete**. It builds the daemon and runs it inside a uniquely named privileged Docker-in-Docker appliance with its own engine and filesystem, exposing an ephemeral loopback port. This confines the daemon's fixed appliance paths and Docker network names to the disposable appliance. It does not mount the host Docker socket or checkout. Cleanup removes only its own container and network; its mode-0700 diagnostic directory is retained. A privileged nested container is required; this confines appliance state but is not a security boundary against a hostile container.

Command run once: `timeout 330s tests/e2e/scripts/s390-isolated-init.sh`. The daemon built and the nested engine started. `GET /api/v1/identity` returned **200**; `POST /api/v1/claim` returned **401**, `invalid or expired claim token`. The fixture submitted the entire token file, which contains the token and an expiry line, as the token. It now reads the first line; this correction has not been rerun. Private diagnostics are in `/tmp/homestead-s390-init.rKlDSyCd` (`build.log`, `daemon.log`, `api-error.txt`); delete that directory after inspection. The disposable container and network were removed.

Credentials are held in process memory or private files, not command arguments or output. The fixture deliberately stops after owner setup: Authentik installation and bootstrap, local TLS edge, real Authentik owner link, managed-person roster, and reviewed Jellyfin access/install API calls remain to be implemented. No installation, access, role, sign-in, publication, or real tunnel result is claimed.

Next exact step: rerun `timeout 330s tests/e2e/scripts/s390-isolated-init.sh` to verify the corrected claim and owner setup, then implement the real Authentik installation and owner-link flow before invoking Jellyfin's plan, access-plan, and install APIs. The script intentionally exits nonzero at that unimplemented prerequisite; initialization alone does not complete the milestone.

## Reviewed Authentik install attempt (2026-10-30)

The corrected initialization was run: identity **200**, claim **200**, owner setup **201**, and authenticated `/me` **200**. A later run submitted a curated Authentik plan and its returned digest to the install API; both returned **200**. Operation 4 failed at `provision:authentik/media` with `storage: mountpoint not registered`, before writing or starting the stack. Its private diagnostic directory is `/tmp/homestead-s390-init.Ebqo8pwt`. An earlier plan returned **422** because the fixture omitted the server port; the request now supplies port 9000.

The fixture now mounts private disposable storage and registers and assigns it through Homestead's API before planning. This correction has not been exercised: the next run stopped at daemon build because concurrent production work currently leaves `internal/server/installaccess.go:6:2: "database/sql" imported and not used` (`/tmp/homestead-s390-init.CGQej39M/build.log`). Production files are outside this fixture's ownership. Once that build is repaired, rerun `timeout 330s tests/e2e/scripts/s390-isolated-init.sh`; inspect any operation failure privately. Each run removes its own appliance and network; the diagnostic directories remain mode 0700.

Authentik readiness and bootstrap, local TLS edge, owner SSO link, managed-person roster, and reviewed Jellyfin installation and user/admin/none sign-in remain unimplemented and unverified. This is not a full E2E result.
