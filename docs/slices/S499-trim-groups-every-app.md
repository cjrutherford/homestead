# S499 - Trimmed groups claim for every OIDC app; Komga denial

## Goal and dependencies
Make S496's trimmed groups claim apply to every OIDC app and explain Komga's silent denial. Depends on S496 and S497.

## Part 1: every app trimmed
- Every app's Homestead claim mapping now lists only the app's own groups (`homestead-<app>-users`, `-admins` and the shared owner group) plus the adapter's admin alias, and answers to the `profile` scope. It carries the profile claims itself, so Authentik's standard `profile` mapping (which lists every group a person holds, about 90 for the owner) is no longer bound to any app provider.
- Every OIDC app asks for `profile`, and an app that also asks for `groups` still receives the claim from the same mapping, so no manifest changes. The `trimgroups` manifest flag (S496) is gone (`EnvOIDC.TrimGroups`, `RoleMapping.TrimGroups`); FreshRSS no longer sets it.
- Group and role checks reviewed against the trimmed claim: Gitea admin group, Nextcloud group provisioning, Jellyfin plugin roles, Immich `immich_role`, Paperless default group, Mealie, BookStack, Audiobookshelf, Calibre-Web, Memos, Vikunja, Vaultwarden, Linkding, Komga (no role mapping). All read only the app's own groups or admin alias; none needs a group outside the set.
- Existing installs: plan and apply each app's sign-in again. The plan names the mapping change (the scope mapping answers to the profile scope in place of the standard one) and the provider diff shows the mapping list change; the Authentik scope mapping object reports a `scope` and `expression` update.

## Part 2: Komga denial
Live finding (VM 2, public name): when Casey (no grant) signs in, Authentik's authorize page in Komga's popup shows "Permission denied ... Request has been denied". Komga's login page itself never changes because Komga's front end opens the OAuth flow in a popup and has no handler for a refusal. Not a Homestead defect and not changeable without patching Komga; documented in the troubleshooting guide and SRV-KOMGA-5.

## Not built
A custom Authentik denial message; patching Komga. See DEFERRED S499.

## Verification
Go: `internal/appsso/trimgroups_all_test.go`. `tests/test_s499_trim_groups_every_app.py`. Live on VM 2: see the S499 commit report.
