# Project Constitution

## Product statement
Homestead turns an ordinary Debian or Ubuntu Server machine into a customer-owned private cloud with friendly applications, secure DNS/TLS, local and remote access, backups, updates, and guided configuration.

## Product promise
Homestead makes ownership easier; it must not make ownership dependent on HAI.

## Ownership rule
The customer owns the appliance, its configuration, and its data. Homestead must not introduce runtime lock-in: no hosted coordination/control service, no license-gated operation, and no opaque state that cannot be inspected or recovered with standard Linux, Docker, and Compose tooling.

## UX rule
Every supported configuration operation must be available from both `homesteadctl` and the Angular dashboard. Simple settings may be schema-rendered; complex/confusing operations use guided wizards.

## Network rule
Local first. Explicit exposure. Least privilege. Owner authority.

## Recovery rule
If Homestead disappears, applications and their data remain operable through Linux, Docker, and Compose.

## Conformance
Each principle above is binding on every slice. Conformance is checked automatically by `tests/test_s000_constitution.py`:

- Product statement → `docs/ARCHITECTURE.md` core components
- Ownership rule → ADR-005 (Compose runtime), ADR-012 (single appliance)
- UX rule → ADR-002 (Angular dashboard), ADR-004 (CLI/dashboard parity)
- Network rule → ADR-006 (local-first exposure), ADR-008 (firewall boundary)
- Recovery rule → ADR-001 (native Go daemon), ADR-003 (SQLite and YAML), ADR-005 (Compose runtime)

Architectural changes require a superseding ADR before implementation. Out-of-scope discoveries are recorded in `docs/DEFERRED.md`.
