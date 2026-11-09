# S469 - Manifest `publish` field and the ADR-061 fact table

## Goal and dependencies
First slice of ADR-061 (public apps at the dashboard's subpath). Depends on ADR-044, ADR-060 and the S451 `never_publish` rule. Nothing else changes: no route, plan, edge or dashboard behaviour moves; the field states each package's public form so S470 to S478 can rely on it.

## What changed
- Manifest fields `publish` (`path`, `host`, `never`) and `publish_reason` (`no_subpath`, `own_name_adr044`, `single_origin`, `owner_choice`, only with `host`) in `internal/catalog/schema.go`. `Manifest.DerivedPublish()` gives the form today's fields imply (no endpoint with `allowwider` gives `never`, `serve: path` gives `path`, otherwise `host`); `PublishForm()` is the declared form or the derived one.
- Validation (`validatePublish`, run by `Validate`): `path` needs `serve: path`; `host` needs a known reason and cannot be `serve: path`; `single_origin_sign_in` implies `host` with reason `single_origin` (and the reason implies the flag); `allowwider: false` implies `never` and `never` implies no wider endpoint. Undeclared stays valid and derives, so manifests built in tests keep working.
- All 46 packages declare it: 17 path, 14 never (13 craft apps, Uptime Kuma), 15 host (nine on their own name, six subpath candidates that stay `host`/`no_subpath` until S472 to S477 verify and move each).
- Fact-table fixture `adr061Facts` in `internal/catalog/publish_test.go` must match the manifests exactly; `tests/test_s469_publish_field.py` also checks the manifests against the table in ADR-061.

## Not covered
Nothing reads `publish` yet; plans and Doctor still use `serve`, `allowwider` and `never_publish`. Folding `never_publish` into `publish: never` is deferred.
