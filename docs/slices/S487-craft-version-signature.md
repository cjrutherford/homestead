# S487 - Craft version and signature fields

## Goal and dependencies
Close flag gaps G6 and G7 from the S487 audit. Depends on S279 (craft images) and S485.

## What changed
- Craft images, Verify and build: optional `Version` input (`craft-version`), sent as `version` on `POST /api/v1/craft/plan` and `/build`.
- Updates, per-row "Craft image options": optional `Craft signature file` input (`craft-signature-<package>`), sent as `craft.signature` on `POST /api/v1/updates/{name}/apply`.
- Blank means today's behaviour. No daemon, CLI or route change.

## Verification
Specs in `craft-images.spec.ts` and `updates.spec.ts`, `tests/test_s487_craft_version_signature.py`, checklist row `END-PARITY-6`, live check on VM 2.
