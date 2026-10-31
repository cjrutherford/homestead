# S405: Mealie's sign-in works on a real appliance

## Goal
Two bugs found live on VM 2 after S403.

## Change
- `catalog/mealie.yaml` declares `uid: 911, gid: 911` on the data entry.
  Mealie's `run.sh` starts as root, chowns `/app` to PUID 911 and re-execs
  as that user before it reads `OIDC_CLIENT_SECRET_FILE`, so the secret file
  (owned by the consuming service's data owner, `SecretFilesFor`) must be
  911:911. The same owner is used when storage is provisioned (the data is
  already 911:911 from `run.sh`) and is kept by backup restore. The wrong
  "runs as root" comment is corrected. An existing install gets the
  corrected file owner the next time sign-in is applied.
- The trust variable (`OIDC_TLS_CACERTFILE`, Mealie's whole OIDC trust
  store) is set only when the issuer is the appliance's own: a private,
  CA-signed name (`docker.IsPrivateName`, the signal BuildPlan uses). A
  public issuer (ADR-047, Let's Encrypt) keeps the image's default trust.
  A local-to-public move removes the variable through the existing
  replaced-issuer rewire.
- Checked, no change: Immich's `NODE_EXTRA_CA_CERTS` adds to the trust
  store; Jellyfin's `SSL_CERT_DIR` is `/etc/ssl/certs:<appliance CA dir>`,
  so the system roots stay.

## Tests
Mealie data owner and secret file owner 911:911; env-oidc trust variable
for local and public issuers; local-to-public removes it.
