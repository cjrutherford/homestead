# CLI flag parity (S486; ADR-004)

Owner rule (2026-11-08): every `homesteadctl` action that goes through the
API has a real dashboard control. Only `claim token`, `login --print-token`
and the root-socket `admins/recover` stay CLI-only. This table lists every
flag in `cmd/homesteadctl`: the dashboard field that sends the same request
field, or `n/a` with one reason. `tests/test_s486_parity_flags.py` parses the
flags out of the Go sources and fails on any that is missing, and checks that
every named dashboard field exists in non-spec dashboard code.

Reasons for `n/a`: `output` (shapes output or the process, such as `--follow`,
`--json`, `-f`, `--out`, `--format`), `plan-digest` (the page carries the
reviewed plan's digest itself), `actor` (the signed-in owner, `--actor`
only), `route` (one route per app, S488, `--route` only), `session` (the
dashboard uses the session cookie), `retired` (the daemon rejects the flag's
request field), `cli-only` (the owner's exceptions above).

| Source | Flag | Command | Request field | Dashboard field |
|--------|------|---------|---------------|-----------------|
| `admin.go` | `--current-password-file` | passwd | current_password | `current_password` |
| `admin.go` | `--new-password-file` | passwd | new_password | `new_password` |
| `admin.go` | `--password-file` | setup, login | password | `password` |
| `admin.go` | `--print-token` | login | (none) | n/a (`cli-only`) |
| `admin.go` | `--token` | any | Authorization header | n/a (`session`) |
| `admin.go` | `--username` | setup, login | username | `username` |
| `apppublic.go` | `--digest` | apps public apply | digest | n/a (`plan-digest`) |
| `apppublic.go` | `--follow` | apps public apply | (none) | n/a (`output`) |
| `apppublic.go` | `--hostname` | apps public plan/apply | hostname | `hostname` |
| `apppublic.go` | `--off` | apps public plan/apply | enabled | `enabled` |
| `apppublic.go` | `--route` | apps public plan/apply | route | n/a (`route`) |
| `apps.go` | `--abandon-volumes` | apps uninstall | abandon_volumes | `abandon_volumes` |
| `apps.go` | `--adopt-secrets` | apps secrets | adopt_secrets | `adoptSecrets` |
| `apps.go` | `--approve-access` | apps install | setup.access approval | `accessRoles` |
| `apps.go` | `--approve-public` | apps install | setup.public.digest | `digest` |
| `apps.go` | `--approve-sign-in` | apps install | setup.sign_in.digest | `digest` |
| `apps.go` | `--data-dir` | apps uninstall | data_dirs | `data_dirs` |
| `apps.go` | `--follow` | apps install/uninstall/setup | (none) | n/a (`output`) |
| `apps.go` | `--force` | apps secrets set | force | `force` |
| `apps.go` | `--leave-sign-in` | apps uninstall | leave_sign_in | `leave_sign_in` |
| `apps.go` | `--person` | apps install | setup.people | `people` |
| `apps.go` | `--public` | apps install | setup.public | `publicName` |
| `apps.go` | `--reauth-file` | apps install | reauth | `reauth` |
| `apps.go` | `--remove-data` | apps uninstall | remove_data | `remove_data` |
| `apps.go` | `--rotate-secret` | apps secrets | rotate_secrets | `rotateSecret` |
| `apps.go` | `--service` | apps config | service | `service` |
| `apps.go` | `--set` | apps config | changes | `changes` |
| `apps.go` | `--skip-verify` | apps secrets set | skip_verify | `skipVerify` |
| `apps.go` | `--unset` | apps config | changes | `changes` |
| `apps.go` | `--value-file` | apps secrets set | value | `value` |
| `appsetup.go` | `--digest` | apps setup resume | digest | n/a (`plan-digest`) |
| `appsetup.go` | `--follow` | apps setup resume | (none) | n/a (`output`) |
| `appsetup.go` | `--reauth-file` | apps setup resume | reauth | `reauth` |
| `appsetup.go` | `--token-file` | apps setup resume | token | `token` |
| `appsso.go` | `--digest` | apps sign-in apply | digest | n/a (`plan-digest`) |
| `appsso.go` | `--follow` | apps sign-in apply | (none) | n/a (`output`) |
| `appsso.go` | `--off` | apps sign-in plan/apply | off | `off` |
| `appsso.go` | `--token-file` | apps sign-in plan/apply/access | token | `token` |
| `audit.go` | `--limit` | audit | limit query | `LIMITS` |
| `audit.go` | `-n` | audit | limit query | `LIMITS` |
| `backup.go` | `--actor` | backup schedule | actor | n/a (`actor`) |
| `backup.go` | `--enabled` | backup schedule | enabled | `enabled` |
| `backup.go` | `--interval` | backup schedule | interval | `interval` |
| `backup.go` | `--package` | backup run/schedule | package | `package` |
| `backup.go` | `--retain` | backup schedule | retain | `retain` |
| `backuppolicy.go` | `--digest` | backup policy apply | digest | n/a (`plan-digest`) |
| `backuppolicy.go` | `--off` | backup policy | enabled | `enabled` |
| `backuppolicy.go` | `--retain` | backup policy | retain | `retain` |
| `backuppolicy.go` | `--time` | backup policy | time_of_day | `time_of_day` |
| `backuprecipients.go` | `--ack` | backup recipients export | acknowledge | `ack` |
| `backuprecipients.go` | `--fingerprint` | backup recipients remove | fingerprint | `fingerprint` |
| `backuprecipients.go` | `--key` | backup recipients add | key | `key` |
| `backuprecipients.go` | `--label` | backup recipients add | label | `label` |
| `backuprecipients.go` | `--out` | backup recipients export | (none) | n/a (`output`) |
| `backuprecipients.go` | `--stdout` | backup recipients export | (none) | n/a (`output`) |
| `claim.go` | `--file` | claim token | (none) | n/a (`cli-only`) |
| `config.go` | `--dry-run` | config import | preview route | `previewConfigImport` |
| `config.go` | `-f` | config | (none) | n/a (`output`) |
| `config.go` | `--file` | config | (none) | n/a (`output`) |
| `config.go` | `--secrets-file` | config import | secrets | `secrets` |
| `craft.go` | `--allow-older` | craft plan/build | allow_older | `allow_older` |
| `craft.go` | `--artifact` | craft plan/build | artifact | `artifact` |
| `craft.go` | `--follow` | craft build | (none) | n/a (`output`) |
| `craft.go` | `--manifest` | craft plan/build | manifest | `manifest` |
| `craft.go` | `--signature` | craft plan/build | signature | `signature` |
| `craft.go` | `--version` | craft plan/build | version | `version` |
| `craftfiles.go` | `--account` | craft files link | account | `account` |
| `craftfiles.go` | `--allow-insecure-http` | craft files link | allow_insecure_http | `allow_insecure_http` |
| `craftfiles.go` | `--appliance` | craft files link | appliance | `appliance` |
| `craftfiles.go` | `--create-app-password` | craft files link | create_app_password | `create_app_password` |
| `craftfiles.go` | `--login-password-file` | craft files link | account_password | `account_password` |
| `craftfiles.go` | `--password-file` | craft files link | app_password | `app_password` |
| `craftfiles.go` | `--root` | craft files link | root | `root` |
| `craftfiles.go` | `--url` | craft files link | base_url | `base_url` |
| `craftfiles.go` | `--yes` | craft files link | (none) | n/a (`output`) |
| `dashboard.go` | `--exposure` | dashboard plan | exposure | `setDashboardAddress` |
| `dashboard.go` | `--follow` | dashboard apply | (none) | n/a (`output`) |
| `data.go` | `--acknowledge-no-backup` | data migrate | acknowledge_no_backup | `acknowledge_no_backup` |
| `data.go` | `--digest` | data migrate | digest | n/a (`plan-digest`) |
| `data.go` | `--follow` | data migrate | (none) | n/a (`output`) |
| `doctor.go` | `--live` | doctor | live query | `checkAgain` |
| `drift.go` | `--follow` | drift scan | (none) | n/a (`output`) |
| `driftschedule.go` | `--enabled` | drift schedule | enabled | `enabled` |
| `driftschedule.go` | `--interval` | drift schedule | interval | `interval` |
| `edge.go` | `--follow` | edge apply | (none) | n/a (`output`) |
| `exposure.go` | `--actor` | exposure | actor | n/a (`actor`) |
| `exposure.go` | `--follow` | exposure approve | (none) | n/a (`output`) |
| `exposure.go` | `--network` | exposure | network | `networksRaw` |
| `myapps.go` | `--json` | myapps | (none) | n/a (`output`) |
| `names.go` | `--digest` | names apply | digest | n/a (`plan-digest`) |
| `names.go` | `--off` | names | enabled | `enabled` |
| `names.go` | `--on` | names | enabled | `enabled` |
| `people.go` | `--admin` | people grant | role admin | `admin` |
| `people.go` | `--email` | people add/edit | email | `email` |
| `people.go` | `--generate` | people add/reset-password/recreate | method generated | `generated` |
| `people.go` | `--link` | people add/reset-password/recreate | method link | `link` |
| `people.go` | `--name` | people add/edit | display_name | `display_name` |
| `people.go` | `--password-file` | people add/reset-password | password | `password` |
| `people.go` | `--password-stdin` | people add/reset-password | password | `password` |
| `people.go` | `--username` | people add/edit | username | `username` |
| `platform.go` | `--allow-older` | platform apply/verify/rollback | allow_older | `allow_older` |
| `platform.go` | `--artifact` | platform apply/verify | artifact | `artifact` |
| `platform.go` | `--checksums` | platform apply/verify | checksums | `checksums` |
| `platform.go` | `--component` | platform apply/verify/rollback | component | `component` |
| `platform.go` | `--follow` | platform apply/rollback | (none) | n/a (`output`) |
| `platform.go` | `--key-file` | platform keys add | key_file | `key_file` |
| `platform.go` | `--label` | platform keys add | label | `label` |
| `platform.go` | `--signature` | platform apply/verify | signature | `signature` |
| `readlinks.go` | `--digest` | read-links apply | digest | n/a (`plan-digest`) |
| `readlinks.go` | `--follow` | read-links apply | (none) | n/a (`output`) |
| `readlinks.go` | `--off` | read-links | enabled | `enabled` |
| `recover.go` | `--socket` | admins recover | (none) | n/a (`cli-only`) |
| `secrets.go` | `--confirm` | secrets | confirm | `confirm` |
| `secrets.go` | `--follow` | secrets | (none) | n/a (`output`) |
| `sso.go` | `--authentik-url` | sso plan | authentik_url | `authentik_url` |
| `sso.go` | `--digest` | sso apply | digest | n/a (`plan-digest`) |
| `sso.go` | `--follow` | sso apply | (none) | n/a (`output`) |
| `sso.go` | `--keep-token` | sso apply | keep_token | `keepToken` |
| `sso.go` | `--origin` | sso plan/login | public_origins | `origin` |
| `sso.go` | `--password-file` | sso login/link | password | `reauth` |
| `sso.go` | `--signing-key` | sso plan | signing_key | `signing_key` |
| `sso.go` | `--token-file` | sso plan/apply | api_token | `api_token` |
| `storage.go` | `--acknowledge-impact` | storage assign | acknowledge_impact | `acknowledge_impact` |
| `storage.go` | `--actor` | storage | actor | n/a (`actor`) |
| `storage.go` | `--class` | storage assign/unassign | class | `class` |
| `storage.go` | `--label` | storage register | label | `label` |
| `storage.go` | `--mountpoint` | storage register/assign/unregister | mountpoint | `mountpoint` |
| `tlsissuance.go` | `--agree-terms` | tls issuance | agree_terms | `agree_terms` |
| `tlsissuance.go` | `--confirm-production` | tls issuance | confirm_production | `confirm_production` |
| `tlsissuance.go` | `--digest` | tls issuance | digest | n/a (`plan-digest`) |
| `tlsissuance.go` | `--follow` | tls issuance | (none) | n/a (`output`) |
| `tlsissuance.go` | `--renewal` | tls issuance | renewal_mode | `renewal` |
| `tlslocal.go` | `--digest` | tls local | digest | n/a (`plan-digest`) |
| `tlslocal.go` | `--follow` | tls local | (none) | n/a (`output`) |
| `tlslocal.go` | `--format` | tls local ca | (none) | n/a (`output`) |
| `tlslocal.go` | `--out` | tls local ca | (none) | n/a (`output`) |
| `tunnel.go` | `--account-id` | tunnel setup | (retired, rejected) | n/a (`retired`) |
| `tunnel.go` | `--account-id-file` | tunnel setup | (retired, rejected) | n/a (`retired`) |
| `tunnel.go` | `--adopt` | tunnel setup | adopt | `TunnelRequest` |
| `tunnel.go` | `--agree-terms` | tunnel setup | agree_terms | `agree_terms` |
| `tunnel.go` | `--api-token` | tunnel setup | (retired, rejected) | n/a (`retired`) |
| `tunnel.go` | `--api-token-file` | tunnel setup | (retired, rejected) | n/a (`retired`) |
| `tunnel.go` | `--auth-hostname` | tunnel setup | auth_hostname | `auth_hostname` |
| `tunnel.go` | `--confirm-production` | tunnel setup | confirm_production | `confirm_production` |
| `tunnel.go` | `--dashboard-hostname` | tunnel setup | dashboard_hostname | `dashboard_hostname` |
| `tunnel.go` | `--digest` | tunnel setup | digest | n/a (`plan-digest`) |
| `tunnel.go` | `--follow` | tunnel setup | (none) | n/a (`output`) |
| `tunnel.go` | `--hostname` | tunnel setup | (retired, rejected) | n/a (`retired`) |
| `tunnel.go` | `--name` | tunnel setup | name | `name` |
| `tunnel.go` | `--service` | tunnel setup | (retired, rejected) | n/a (`retired`) |
| `updates.go` | `--backup` | updates apply | backup | `backup` |
| `updates.go` | `--craft-allow-older` | updates apply | craft.allow_older | `allow_older` |
| `updates.go` | `--craft-artifact` | updates apply | craft.artifact | `artifact` |
| `updates.go` | `--craft-manifest` | updates apply | craft.manifest | `manifest` |
| `updates.go` | `--craft-signature` | updates apply | craft.signature | `signature` |
| `updates.go` | `--db-password-file` | updates apply | database_password | `database_password` |
| `updates.go` | `--follow` | updates apply | (none) | n/a (`output`) |
