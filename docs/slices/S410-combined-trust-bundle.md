# S410: a combined trust bundle for Linkding

## Problem
S409 sets `REQUESTS_CA_BUNDLE` to the appliance root when Linkding signs in
at the appliance's private issuer. Python requests treats that file as its
whole trust store, so Linkding then failed every public HTTPS fetch (page
titles, favicons, archives).

## Change
- `EnvOIDC.trustbundle: combined` (opt-in, needs `trust`). With a private
  issuer the trust variable points at `/etc/homestead/ca/bundle.pem`: the
  host's system CA bundle followed by the appliance root.
- The sign-in wiring step writes the bundle into the stack's `homestead-ca`
  directory, which the existing read-only CA mount already delivers, so no
  extra mount entry is needed. Mode 0644 (public certificates only), written
  atomically on every apply. System bundle paths (`SystemBundlePaths`):
  `/etc/ssl/certs/ca-certificates.crt`, `/etc/pki/tls/certs/ca-bundle.crt`,
  `/etc/ssl/cert.pem`; none found fails the step with a clear message.
- A public issuer or turning sign-in off removes the variable (value-checked,
  S406; the bare-root value S409 set is also recognised) and the bundle file.
- `catalog/linkding.yaml` sets `trustbundle: combined`. Mealie is unchanged:
  its `OIDC_TLS_CACERTFILE` is used only for OIDC calls.

## Tests
Bundle contents and mode, missing system bundle, trust variable per issuer,
the private/public/off lifecycle including a legacy S409 value, and manifest
validation of `trustbundle`.

## Out of scope
Bundle freshness follows re-apply only. Recorded in DEFERRED.
