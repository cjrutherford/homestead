# S298 — App sign-in through Authentik

## Goal
Let Immich, Nextcloud and Jellyfin sign people in through the linked Authentik, offered at install and changeable later, always through an approved plan (ADR-039 §3).

## Dependencies
S297. ADR-039, ADR-033, ADR-021, ADR-037.

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

Out of scope: SAML and LDAP, apps other than the three, public app names (S290; this slice re-plans redirects when they exist).

## Acceptance Criteria
- **Manifest:** an OIDC adapter declaration (`immich`, `nextcloud-user-oidc`, `jellyfin-sso-plugin`); Jellyfin's pins the plugin release and its checksum, and any other build is refused.
- **Install:** the wizard offers "Sign in with Authentik" when Authentik is installed and dashboard sign-in is configured, checked by default; the plan lists the Authentik provider and application, the redirect URIs and the app settings changed.
- **Apply:** creates the provider and application, seals the client secret, and configures the app through its adapter; users are created on first sign-in; the app's local admin keeps working. Audited without secret values.
- **App page:** add or remove sign-in through a plan. Removal deletes what Homestead created in Authentik and turns the app's OIDC setting off.
- **Re-plan:** a new app name (local or public) re-plans the provider's redirect URIs for approval.
- Live check on the KVM VM: sign in to each of the three apps through Authentik.
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- `docs/PARITY.md`, `docs/DEFERRED.md`, `docs/DEVELOPMENT.md`, `docs/architecture/DASHBOARD_SSO.md` (app section).

## Handoff
Next: S290 app routes on the tunnel.
