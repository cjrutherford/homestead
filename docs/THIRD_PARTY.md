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

