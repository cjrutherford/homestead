# S397 — Publish when the Cloudflare token cannot read certificates

## Status
Complete (automated tests). Live check on VM 2 pending.

## Regression
S390 (4ec3efc) made `publicationCertificateState` refuse with "cannot
review publication: Cloudflare certificate state is unavailable" whenever
the zone's certificates or Total TLS setting were unreadable. The owner's
token lacks Zone → SSL and Certificates → Read, which INSTALL.md lists as
recommended, so every `apps public plan`, public install and app-setup
publish step failed with HTTP 502. Before S390 the plan carried
`Checked:false` coverage plus a warning and went through.

## Decision
Unreadable state is no longer refused. `publicationCertificateState` binds
a stable marker (`unreadable` or `unreadable-total-tls`) into the digest,
so a plan stays valid while the state stays unreadable and goes stale
(409, "public address plan changed") if readability or the relevant packs
change. Readable state is unchanged, including the "relevant certificate
pack has no identity" refusal and the not-covered warning; readable digests
are byte-identical to before. Every plan made while unreadable carries a
warning that Homestead cannot confirm the name is covered, that Universal
SSL covers first-level names unless disabled, and that adding the
permission enables the check.

The callers all share `publicationCoverage`: app public plan, apply
re-plan, install-time public approval, and the setup publish step.

## Amendment to S390 / ADR-054
S390 did not explicitly require refusal on unreadable state; this slice
supersedes that behaviour. The permission stays recommended, not required
(ADR-047 only needs coverage for the issuer name, which is warned about).

## Tests
Unreadable plan succeeds with the warning; digest differs per readability
kind and from readable; plan then apply succeeds unreadable; install
approval goes stale (409) when readability flips; anticipated-sign-in
"certificate" case follows the stale path.
