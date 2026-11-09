# Explicit app access decisions during installation

## Status
Accepted

Appliance owner direction, 2026-10-29. Amends ADR-039 §3,
ADR-043, ADR-048 §1 and ADR-049 as described below.

## Decision
- The appliance owner always receives full access to an installed app;
  any app-local recovery administrator remains available. For every other
  existing Homestead person, installation reviews an explicit decision:
  no access, user, or app administrator where supported. Disabled people
  cannot be granted access while disabled. This is an app role, not a
  Homestead administration role.
- Apps with no user accounts or permission system are open by nature;
  the installer says so. Their open access must not be represented as a
  per-person restriction. An app with its own accounts or permissions
  must instead have a verified Homestead integration able to apply the
  chosen access decisions. Authentik is the preferred integration. Until
  enforcement is implemented and ready, refuse an installation that would
  otherwise claim these selections protect the app.
- Authentik itself is the identity-provider bootstrap: it is installed and
  initialized through its own owner recovery flow before Homestead can
  manage people. It cannot be configured as its own client, and this
  bootstrap must not depend on an already installed Authentik provider.
- The dashboard and CLI use the same reviewed, digest-bound set of person
  IDs and roles. Changing a person's state, adding or removing a person,
  changing a role, or changing app/authentication readiness invalidates
  the approval. A new person added later is not implicitly granted access.
- Granting app administrator access keeps ADR-043's fresh-authentication
  requirement. The daemon enforces it before accepting the installation;
  the ordinary install endpoint must not offer an unprotected admin path.
  A dedicated admin-authorized install action may use ADR-049's fresh
  Authentik step-up on the public dashboard; local/overlay access retains
  the local-password step-up. No local password is sent over the public
  dashboard. Authorization is checked at request time and the daemon then
  applies the recorded, approved choices in its existing setup operation.
- Previously approved or already running setup operations must be read
  compatibly and must not gain administrators or other new access.
  Failures and partial remote grants are visible and auditable; no result
  claims success until the chosen access has actually been applied.

## Consequences
Installs gain a reviewed access step and a recorded decision digest;
stale or unreviewed decisions are rejected before an operation exists.
Unsupported account apps are not silently treated as open.

## Scope and migration
Catalog entries require an evidence-based classification of their account
and permissions behavior. Missing metadata is not proof of an open app.
New integrations are delivered and tested individually. The policy must
not silently convert an unsupported account app to an open app or alter an
existing installation's accounts or permissions during upgrade.

## Amendment (S398): self-managed apps

This is a clarification consistent with the decision above, not a reversal.
S390 implemented "refuse an installation that would otherwise claim these
selections protect the app" too broadly, refusing every app without an
adapter. The refusal applies only when per-person selections would be
claimed. Apps that keep their own accounts install in a `self-managed` mode:
the installer states that the app keeps its own accounts and Homestead does
not control who can sign in, no per-person choices are offered, and any sent
are refused. The mode is bound into the access digest. `unavailable` is kept
for real blockers (unknown adapter, Authentik or people verification
failing).
