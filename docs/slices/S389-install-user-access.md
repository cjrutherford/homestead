# S389 — Access decision contract

## Goal
Define and test a deterministic per-person app access decision contract
under ADR-054. This slice does not connect the contract to installation.

## Scope
- Require exactly one decision for every managed person: no access, user,
  or app administrator when the app supports that mapping.
- Reject disabled-person grants, unknown or duplicate people, unknown roles,
  and administrator choices where the capability is unavailable.
- Bind app, person roster and active state, role choices, administrator
  capability, and a caller-supplied integration fingerprint into an
  order-independent digest. Verify rebuilds it from current inputs. The
  owner is not part of the person roster and always retains full access.
- S390 must derive the fingerprint from authoritative app and identity
  readiness, separately classify open versus account-based applications,
  enforce step-up, connect the plan to both clients, and verify real grants.
  This digest alone neither proves integration readiness nor authorizes
  administrator grants.
