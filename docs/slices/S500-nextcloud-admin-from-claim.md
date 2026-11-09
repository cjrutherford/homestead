# S500 - Nextcloud administrators follow the admins group

## Goal and dependencies
The owner, and members of the Nextcloud admins group, must be Nextcloud administrators (ADR-043). Depends on S499.

## Problem
user_oidc 8.13 hashes every group id it provisions (sha256 of `<provider>_0_<name>`) while unique ids are on, and has no setting that maps a claim value to a real group. The claim's `admin` alias therefore became a hashed group whose display name is `admin`, never Nextcloud's `admin` group, and the owner reached `settings/admin` as "Access forbidden". Turning unique ids off would rename every account that already signed in, so it is not an option.

## Fix
- Enabling Nextcloud sign-in writes a small first-party app, `homestead_sso`, into `custom_apps/` (no network, no app store) and enables it.
- Its listener runs on `UserLoggedInEvent`, which user_oidc dispatches after it provisioned the person's groups. For an account whose backend is `user_oidc` it puts the person in the real `admin` group when they hold the hashed alias group (display name `admin`) and removes them when they do not.
- Removal: taking a person out of the Nextcloud admins group removes their administrator rights at their next sign-in. The local `admin` account (another backend) is never touched.
- Plan text, role note and the off-settings list name the app. Apply sign-in again on existing installs.

## Not built
Background sync (rights change at sign-in only), removing the app files on sign-in off. See DEFERRED S500.

## Verification
Go: `internal/appsso/appsso_test.go` (TestNextcloudAdapterUsesOccAndStdinForTheSecret). `tests/test_s500_nextcloud_admin.py`. Live on VM 2: see the S500 commit report.
