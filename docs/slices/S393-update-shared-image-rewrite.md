# S393 — Update shared Compose image references safely

## Goal
Allow package updates to rewrite Compose image values when multiple services
share one image, while preserving owner formatting and refusing plans whose
named service is absent or its current image has drifted.

## Dependencies
S243; ADR-005 (Compose remains inspectable and usable without Homestead).

## Scope
Update image rewriting in `internal/update` and cover the generated Authentik
stack, quoted YAML values, tag/digest transitions, and missing-image behavior.
Do not change catalog definitions, server routes, CLI/dashboard surfaces, or
Compose generation.

## Acceptance Criteria
- A plan with separate moves for Authentik server and worker updates both
  references to their shared image.
- Quoted image scalars, indentation, inline comments, and unrelated services
  remain intact across tag/digest transitions.
- An absent current image fails before writing the Compose file.
- Focused update package tests pass.

## Rewrite behavior and limits
Moves apply only to their named services, and every service must contain the
exact planned source image before any file is written. Other services sharing
that image remain untouched. Rollback resolves each service separately, so
multiple source images may converge on one target; a failed rollback rewrite is
reported and does not trigger a rollback Compose up.

Single-line plain, single-quoted, and double-quoted image scalars are supported
in block and flow mappings, including tag and digest references. Existing
formatting and comments are preserved. Multiline scalars, aliases, and other
scalar syntax that cannot be safely located are rejected before writing; the
rewritten document must also parse successfully. Duplicate planned services or
duplicate service/image keys are rejected.
