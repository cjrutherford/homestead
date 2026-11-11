# S506 - CI installs test tools and fails on tool skips

## Goal and dependencies
Tests that need an external program must run in CI, so a regression in what they guard cannot merge green. Depends on S275 (the `HOMESTEAD_REQUIRE_AGE` pattern), S421, S493, S498 and S500.

## Problem
The CI `go` job installed only Go. Tests that need node (with `node:sqlite`), minisign, age or nft skipped there, silently: the Kuma, Actual and Audiobookshelf sign-in scripts, the service worker simulations, the real-minisign keygen test, the real-age decrypt and runbook tests, and the `nft --check` tests.

## Fix
- `HOMESTEAD_REQUIRE_TOOLS` is a comma list (or `all`) of tool names. `internal/testtools` (`SkipUnlessTool`, `SkipOrFail`, `Required`) and `tests/_tools.py` (`require_tool`) fail instead of skipping for a listed tool. `HOMESTEAD_REQUIRE_AGE` still works and counts as naming `age`. Every skip in the verified list now goes through them; docker build integration, root ownership, Linux-only discovery and the Chrome acceptance-form test keep skipping.
- The CI `go` job sets up node 24, installs minisign, age, nftables and php-cli with apt, and installs caddy 2.11.7 (the version of `docker.CaddyImage`). Caddy is not in Ubuntu's archive, so the release tarball is downloaded and checked against the release's `caddy_2.11.7_checksums.txt` (sha512) before install. `make test` runs with `HOMESTEAD_REQUIRE_TOOLS=node,minisign,age,caddy,php`.
- nft: `nft --check` needs CAP_NET_ADMIN, which the unprivileged test step lacks, so nft is not in that list. A following step compiles the `internal/firewall` and `internal/server` test binaries as the runner user and runs them under `sudo` with `HOMESTEAD_REQUIRE_TOOLS=nft`, so an unusable nft fails there. (Compiling first avoids running Go as root.) Locally, as a normal user, those nft tests still skip.
- New checks: `TestRenderedEdgeAdaptsInCaddy` (`internal/docker/caddyadapt_test.go`) writes the route files with `ReconcileRoutes` for the shared-gate set (gated path apps, published and unpublished), a plain path set and a fully published set (S498 redirects), and runs `caddy adapt --adapter caddyfile` over them. `TestNextcloudAdminAppIsValidPHP` (`internal/appsso/nextcloud_admin_php_test.go`) runs `php -l` over the PHP files of the embedded `homestead_sso` app.

## Not built
Nothing was pushed, so the workflow changes (apt package names, the sudo nft step, the minisign test against the Ubuntu minisign) are unverified in CI until the owner asks for a push. The caddy test and checksum command were run locally against the downloaded 2.11.7 release.

## Verification
Go: `internal/testtools` tests; the converted tests with the tool present and with `HOMESTEAD_REQUIRE_TOOLS` naming an absent one (they fail). Python suite unchanged and green.
