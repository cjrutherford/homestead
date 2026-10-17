# Internal CA trust (S280, ADR-030)

Local-only hosts (for example `app.home.arpa`) are served over HTTPS by the
edge using certificates issued by this appliance's own certificate authority.
Browsers only treat such pages as secure contexts (WebGPU, clipboard, service
workers) once the device trusts the root. Homestead never installs the root
for you: you do it, on each device, deliberately.

Hosts that use a public-provider certificate (ACME) are never switched to the
internal CA.

## Get the root and check it

Three places show the same SHA-256 fingerprint:

- Dashboard: **Trust** page.
- CLI: `homesteadctl tls root show` (and `tls local status`).
- The downloaded file: `openssl x509 -in homestead-local-ca.pem -noout -fingerprint -sha256`
  (for the DER file add `-inform der`).

Download with the Trust page buttons, or `homesteadctl tls root export`, or
`GET /api/v1/tls/local-ca/root.pem` / `root.der` from the management address
(local or overlay only). The two download routes need no sign-in, because a
phone fetching the root has no operator session, and serve only the public
certificate. Because they are unauthenticated, always compare the fingerprint
with the one shown on the signed-in Trust page or CLI before installing.

The CA is created automatically when the first local-only HTTPS route is
applied, or on demand (`homesteadctl tls local create`, Trust page button).

## What trusting it permits

A device that trusts the root accepts any certificate the root signs. The root
carries an X.509 name constraint limiting it to `.lan`, `.local`, `.home.arpa`,
`.internal` and `.localhost`, so even a stolen key cannot impersonate public
sites to a constrained-aware client. Not every client enforces name constraints.
Only install the root on devices you own. Remove it when you stop using the
appliance (see below).

## Per-OS steps

Use the PEM file where it says PEM and the `.crt` (DER) file otherwise.

### Windows (.crt)

1. Double-click the `.crt`, choose Install Certificate.
2. Store location Local Machine (or Current User), then "Place all certificates
   in the following store" and select Trusted Root Certification Authorities.
3. Restart the browser. Firefox needs its own import (below).
4. Remove: `certmgr.msc` (or `certlm.msc`), Trusted Root Certification
   Authorities, delete "Homestead Local CA ...".

### macOS (.crt)

1. Double-click the file; add it to the System keychain.
2. Open the certificate, expand Trust, set "When using this certificate" to
   Always Trust, authenticate.
3. CLI equivalent: `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain homestead-local-ca.crt`
4. Remove: delete it in Keychain Access, or `sudo security delete-certificate -c "Homestead Local CA"`.

### iPhone and iPad (.crt)

1. Open the download link in Safari and allow the profile download.
2. Settings, "Profile Downloaded", Install.
3. Settings, General, About, Certificate Trust Settings: switch on full trust
   for the Homestead root. Without this toggle the profile is installed but
   websites are not trusted.
4. Remove: Settings, General, VPN & Device Management, delete the profile.

### Android (.crt)

1. Settings, Security (or Security & privacy), More security settings,
   Encryption & credentials, Install a certificate, CA certificate; confirm the
   warning. Menu names vary by vendor.
2. Chrome and the system browser honour the user store. Many apps do not: apps
   targeting recent Android versions ignore user-installed CAs unless they opt
   in through their network security config, and Firefox for Android has its own
   rules. Per-app trust is the app's decision, not Homestead's.
3. Remove: Encryption & credentials, User credentials, remove the certificate.

### Linux system store (PEM)

- Debian, Ubuntu: `sudo cp homestead-local-ca.pem /usr/local/share/ca-certificates/homestead-local-ca.crt && sudo update-ca-certificates`
- Fedora, RHEL: `sudo cp homestead-local-ca.pem /etc/pki/ca-trust/source/anchors/ && sudo update-ca-trust`
- Arch: `sudo trust anchor homestead-local-ca.pem`
- Remove: delete the copied file and rerun the same update command
  (`update-ca-certificates --fresh` on Debian).

This covers curl, wget and most CLI tools. It does not cover Firefox or
Chrome/Chromium.

### Firefox, all platforms (PEM)

Firefox keeps its own store. Settings, Privacy & Security, View Certificates,
Authorities, Import; tick "Trust this CA to identify websites". Remove from the
same dialog (Delete or Distrust). Alternatively set
`security.enterprise_roots.enabled` to true to follow the system store.

### Chrome and Chromium on Linux (PEM)

Chrome on Linux uses the per-user NSS database, not the system store.

    sudo apt install libnss3-tools
    certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n "Homestead Local CA" -i homestead-local-ca.pem

Or Settings, Privacy and security, Security, Manage certificates, Authorities,
Import. Remove: `certutil -d sql:$HOME/.pki/nssdb -D -n "Homestead Local CA"`.
Snap/Flatpak builds use their own profile directory.

## Leaves and rotation

- Leaves last 90 days by default and renew automatically on the daemon
  scheduler (job `tls:local-leaves`) when a third of their life remains. New
  local-only routes get a leaf at the next reconcile. Nothing on devices changes
  when a leaf renews; devices trust the root, not the leaf.
- `homesteadctl tls local leaves` / Trust page list each leaf and its status;
  `tls local reissue [HOST]` forces re-issue (audited).
- Root rotation is explicit. `homesteadctl tls local rotate` (no flags) prints
  the plan: the leaves that will be re-issued and a reminder that every device
  must then trust the new root. Approve with `--digest D`; a digest that no
  longer matches the plan is refused. After rotation, install the new root on
  every device and remove the old one.
- `homesteadctl doctor` and the dashboard Doctor surface report: root present,
  root expiry (warn inside a year, fail inside 30 days), each leaf's expiry
  window, a placed leaf that does not cover its host or chain to the root, and
  an edge route that does not present the internal-CA leaf.

## Where the key lives

The root's private key is a custody-sealed system secret (ADR-021). It is never
written to YAML, logs, audit, API responses, configuration export or the
inspectable mirror `internal-ca.yaml` (public certificate and metadata only).
See `BACKUP_RESTORE_RUNBOOK.md` for how the key survives a restore.
