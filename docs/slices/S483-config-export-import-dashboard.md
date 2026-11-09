# S483 - Config export and import in the dashboard

## Goal and dependencies
Give `GET /api/v1/config/export` and `POST /api/v1/config/import` a dashboard surface (G1, G2) on the Configuration page. Depends on S234 (the API and CLI).

## Owner decisions (2026-11-08)
- Import: upload the YAML, show a validation and diff preview, take any needed secrets in password fields (sent once, never stored, logged, cached or put in browser storage), confirm explicitly, audit it.
- Export: re-authenticate only if the export holds secret material.

## What changed
- `GET /api/v1/config/export?format=yaml` returns the same bytes `config export -f` writes (`desired.MarshalExportYAML`, now shared). The export holds secret names only, never values, so there is no "contains secrets" case and no fresh sign-in; the plain download is direct.
- `POST /api/v1/config/import/preview` (`{"text": ...}`, YAML or JSON): parses strictly, applies the version and schema rules, diffs the settings against the current state and lists the secret names required. It writes nothing. The parsed document comes back so the browser never parses YAML.
- `POST /api/v1/config/import` is unchanged in shape but now audits `config.import` (actor, counts, outcome; never names or values of secrets).
- CLI: `config import --dry-run -f FILE` calls the preview. `config export` and `config import` otherwise behave as before.
- Dashboard: `ConfigTransfer` on the Configuration page. Secret values are read from password inputs at confirm time into one request, then the fields are rebuilt empty; no signal or storage holds them.

## Verification
Go tests (`TestConfigExportYAMLAndPreview`, `TestConfigImportDryRun`), dashboard specs (`config-transfer.spec.ts`), `tests/test_s483_config_transfer.py`, checklist rows `END-PARITY-2` and `END-PARITY-3`, and a live check on VM 2 (export download, preview, round-trip confirm with no setting changes).
