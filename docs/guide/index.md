# Homestead owner guide

This is the guide for the person who owns a Homestead appliance. It matches the version you are running, ships inside the appliance and works without an internet connection.

Homestead turns one ordinary Debian or Ubuntu machine at home into a private cloud you own: apps, one sign-in, local names, optional public access through a Cloudflare tunnel, nightly backups and signed updates. If Homestead ever disappears, your apps and their data stay usable with plain Docker Compose.

## Contents

| Section | What it covers |
|---|---|
| [Getting started](getting-started.md) | Install, claim the appliance, first sign-in and the setup wizard |
| [Apps and the catalog](apps.md) | Installing apps, signing in, the apps page, app groups, craft apps |
| [People and sign-in](people.md) | Adding people, choosing who can open which app, recovery |
| [Names, tunnel and public access](exposure.md) | Local names, the Cloudflare tunnel, publishing an app |
| [Storage and backups](storage-backups.md) | Mounted storage, nightly backups, the recovery key, restore |
| [Updates and releases](updates.md) | Updating apps and the platform, trusted keys |
| [Troubleshooting](troubleshooting.md) | The Doctor page and common problems |
| [Command and page reference](reference.md) | Every `homesteadctl` command and its dashboard page |
| [How this guide is built](how-this-guide-is-built.md) | Where the source lives and how it is served |

## Two ways to do everything

Every setting can be changed from the dashboard or from `homesteadctl`; both use the same API, so the result is identical. Changes that touch the system are shown as a plan first. You review the plan and approve it, and the change is recorded in the audit log.

## Scope

One appliance, one owner. Homestead uses disks you have already mounted and apps from its own curated catalog. It does not format disks, manage a fleet or run a cluster.
