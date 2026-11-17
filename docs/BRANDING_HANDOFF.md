# Homestead branding and iconography handoff

This brief records product facts and the supporting identity direction.
S510 integrates the supplied Fieldwork navigation and catalog illustrations
without replacing the primary mark or redesigning the established theme.

## Product in brief

Homestead is a customer-owned, single-machine private-cloud control plane. It
turns a Debian or Ubuntu Server host into a place to install and manage a
curated set of self-hosted applications through a native Go daemon, a CLI,
and an Angular dashboard. The CLI and dashboard use the same API. Operational
state lives in SQLite, desired and generated configuration is inspectable
YAML, and apps run as ordinary Docker Compose stacks. If Homestead is removed,
the customer can keep using the Compose files and app data.

The owner can create people, choose who can sign into an app, and apply those
choices to apps installed later. Public exposure is explicit: the owner plans
and reviews the route, and changes are recorded. Management stays local or on
the private overlay by default. The public management path, when configured,
is the dashboard through the tunnel behind Authentik sign-in.

## Audience and jobs to be done

The core audience is a technically comfortable household or individual who
wants self-hosted services on hardware they control, while preferring a
guided control surface for setup and ongoing administration.

- Install and update supported household apps from one curated catalog.
- Create local people and select the apps each person may use, including for
  apps installed after the people were created.
- Keep management local by default, then deliberately expose selected apps
  through a reviewed plan.
- Use a browser or CLI for the same supported configuration tasks.
- Keep access to app configuration and data through standard Linux, Docker,
  and Compose tools if Homestead is no longer used.

Owner-reported review evidence (2026-11-17): initial catalog installation,
person creation and permissions, and permission choices for apps installed
later worked as expected on the reviewed appliance. This is one owner's
observation, not a broad compatibility study or a published testimonial.

## Why it works and why it matters

**Verified product foundations:** one native service owns host changes;
dashboard and CLI share the API; catalog packages have an explicit lifecycle;
person and app access is managed centrally; app stacks remain ordinary
Compose; storage is mounted rather than formatted by Homestead; and public
routes are planned, reviewed, and audited.

**Proposed positioning:** Homestead brings installation, people, and app
access into one owner-controlled workflow while leaving the services and data
on the customer's machine. That can make a private cloud easier to operate
without making its owner dependent on Homestead or a hosted HAI control
service.

The value is practical: fewer unrelated setup steps, a clearer way to manage
who can use household apps, and a path back to familiar Compose files and
Linux storage. Do not translate these points into absolute privacy, security,
availability, or compatibility promises.

## Boundaries and nonclaims

- One appliance only; no clustering, high availability, or fleet management.
- Storage must already be mounted. Homestead does not partition, format, or
  create a RAID/ZFS/Btrfs pool.
- The catalog is first-party and curated, with the pinned Jellyfin SSO plugin
  as the documented exception; Homestead is not a general-purpose container
  marketplace.
- Public access requires owner setup and external account/domain prerequisites.
- App behavior, resource needs, and upstream support still vary by application
  and CPU architecture.
- The product is not an operating system, cloud provider, backup guarantee, or
  substitute for the owner's network and recovery decisions.

## Current name, visual system, and assets

- Product name: **Homestead**. The admin shell and apps header pair the name
  with the existing mark (`dashboard/src/app/app.html`,
  `dashboard/src/app/home/home.html`).
- Working emblem/wordmark: [`assets/homestead-logo.png`](../assets/homestead-logo.png),
  a 1254×1254 transparent PNG with emerald, cream, and gold corn/network
  imagery. [`assets/README.md`](../assets/README.md) calls it a working asset
  and asks that favicon, app-icon, monochrome, and SVG variants come from a
  deliberate later branding pass.
- The dashboard has a token-based visual system described in
  [`docs/architecture/DASHBOARD_DESIGN.md`](architecture/DASHBOARD_DESIGN.md):
  Lamplight is dark by default, with light, system, and high-contrast choices.
- Bundled UI typefaces are Atkinson Hyperlegible Next, Atkinson Hyperlegible
  Mono, and Barlow Semi Condensed for display use (`dashboard/src/fonts.css`).
- Catalog entries use monochrome Fieldwork illustrations for five verified
  package IDs and keep manifest-keyed Material Design Icons for other
  packages (`dashboard/src/app/ui/app-icon.ts`). The labels remain visible;
  neither illustration family is used as the Homestead brand mark. Bundle
  provenance and mapping are documented in
  [`assets/brand/fieldwork/README.md`](../assets/brand/fieldwork/README.md).

## Remaining identity work

The supplied bundle contains no primary vector source or favicon. The
1254×1254 logo PNG remains the primary mark; `dashboard/public/favicon.ico`
is unchanged. The shell clips the supplied raster's lower wordmark area in a
fixed-size display window and renders the product name as separate text; the
source image is not edited. A later asset pass can create a vector master,
small-size favicon/app-icon exports, and use guidance after the original logo
source is available. The bundle's marketing landing-page concept is not
deployed. Do not turn its proposed language into compatibility, privacy,
security, or availability guarantees.

## Source references

- Product and ownership principles: [`docs/PROJECT.md`](PROJECT.md),
  [`docs/ARCHITECTURE.md`](ARCHITECTURE.md), ADR-001, ADR-003, ADR-005,
  ADR-006, ADR-037, and ADR-057.
- Current product and release scope: [`CHANGELOG.md`](../CHANGELOG.md),
  [`docs/INSTALL.md`](INSTALL.md), and [`docs/DEFERRED.md`](DEFERRED.md).
