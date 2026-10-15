# Third-party dependencies

Dependencies added under an ADR carry their license here.

## filippo.io/age v1.3.2 (ADR-022)
Backup encryption, X25519 recipients. License: BSD-3-Clause
(Copyright The age Authors, Google LLC, Filippo Valsorda). Pulls in
`filippo.io/hpke` v0.4.0 (BSD-3-Clause, The Go Authors). The `age` CLI is a test and runbook prerequisite, not a runtime one.

## nginx 1.27-alpine base image (S279, ADR-028)
Base of the shared craft image (`images/craft/Dockerfile`), pulled once by
the local build and pinned by the multi-arch index digest
`nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10`
(recorded 2026-10-14). nginx is BSD-2-Clause (Copyright Igor Sysoev,
Nginx, Inc.); Alpine Linux components in the image carry their own
licences (MIT, GPL and others as packaged by Alpine). Homestead does not
redistribute the image: the appliance builds it locally. Changing the
digest changes the build-context digest and needs a re-signed craft
manifest.


## storytold craft suite (source recorded 2026-10-15, ADR-028)
Source: `https://github.com/storytold` (Apache-2.0 across the repositories,
per the owner's survey; each slice re-reads the licence and NOTICE of the
exact release it vendors). Each repository's GitHub release ships
`<app>-web-<version>.zip` and an unsigned `SHA256SUMS.txt`. Release URL
pattern: `https://github.com/storytold/<repo>/releases/download/<tag>/<asset>`.
Latest releases at planning: wordcraft v0.7.0, gridcraft v0.7.0, pdfcraft
v0.8.0, deckcraft v0.7.0, vectorcraft v0.11.0, photocraft v0.9.0, lightcraft
v0.8.0, figcraft v0.6.0, filmcraft v0.8.0, effectcraft v0.10.0, soundcraft
v0.7.0, designcraft v0.8.0, cadcraft v0.7.0, craft-launcher v0.4.1 (asset
`artcraft-launcher-web-0.4.1.zip`). Per-app entries (licence text, version,
artifact URL and SHA-256) are added by S249 to S253 as each app lands.
