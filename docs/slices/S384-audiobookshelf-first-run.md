# S384 — Audiobookshelf first-run administrator

## Goal
Prepare Audiobookshelf for an approved Authentik configuration by creating its local recovery administrator once, using the application's documented first-run API and a generated, sealed, revealable password. Never replace an existing administrator.

## Dependencies and scope
S318, S353, ADR-021 and ADR-039. Extend the existing first-run mechanism for the pinned Audiobookshelf image. This is a prerequisite, not an Authentik adapter: SSO still requires its own planned and approved slice. Do not configure or mutate an existing app administrator; fail closed on unexpected server responses.

## Acceptance
- On a fresh installation, wait for the application, initialize its first administrator using credentials passed over container stdin, then verify the app reports initialization. Store the password only in the existing app secret store and do not print or audit it.
- If the application is already initialized, leave all users and credentials untouched. A later Authentik adapter must not assume that a newly generated secret authenticates an administrator it did not create.
- Validate the manifest and exercise waiting, idempotency, failure/redaction and real HTTP request shape with tests; leave existing S367 work untouched.
- Update the slice tracker, catalog documentation and deferred Authentik work. App sign-in remains unavailable until the adapter is implemented and approved.

## Verification
The first-run script was run against the pinned 2.37.1 image in a disposable VM container with isolated tmpfs storage: it reported uninitialized, created a test administrator and verified login, then reported ready on recheck. The existing installation was not touched.
