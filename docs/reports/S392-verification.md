# S392 bounded verification

Date: 2026-10-29

## Scope and result

Verified the fresh `/config` guard and Komga 1.29.1 local recovery claim path.
This does not verify or enable a Komga Authentik adapter or per-person access
enforcement; both remain unsupported because Komga does not enforce account
disablement or membership-derived roles across its authentication methods.

## Proof

- `go test ./internal/firstrun ./internal/server -run 'Komga|FreshKomga' -count=1`
  passed for both packages. Tests cover a missing/nonempty `/config` refusal,
  empty config acceptance, an already claimed account with no password
  generation or follow-up request, claim failure, unexpected account refusal,
  and recovery administrator role verification.
- `docker pull docker.io/gotson/komga:1.29.1` completed. Image digest:
  `sha256:9fa656e8259bbd75c0f654bdfa91abf2b4813cbf4f40e80eb292495446ffdf1f`.
- Ran a disposable named container with a disposable named volume, no host
  port mapping. Using `docker exec -i` and curl config on stdin, live results
  were: fresh claim status `200`; recovery claim status `200`; recovery
  administrator Basic login (`/api/v2/users/me`) status `200`; a second claim
  status `400`. The generated password was not printed, passed as stdin config,
  and response bodies were discarded. The shell cleanup trap stopped the
  container (`--rm`) and removed its named volume.
- Source review: `komgaHTTP` accepts only fixed methods and paths, puts
  credentials in curl config on stdin, and converts execution failures to a
  generic Komga error. The command arguments do not contain the credentials.

## Limits

- The live run exercised upstream fresh claim, owner login, and duplicate-claim
  refusal. It did not seed a pre-claimed database; claimed-account non-mutation
  is established by the focused mocked test, which asserts exactly one
  unauthenticated status request and no secret creation.
- The `/config` refusal is verified by the server unit test against temporary
  host paths; the full install operation was not run against Docker.
- This is not an Authentik integration or end-to-end authorization test.
  Adapter support, per-person `admin`/`user`/`none` enforcement, account
  linking, and later revocation remain unsupported.
