# S406: re-applying sign-in removes a stale trust variable

## Problem
S405 stopped setting `OIDC_TLS_CACERTFILE` when an env-oidc app (Mealie) signs in
through the public issuer. An install wired before S405 still had the variable,
and re-applying the same issuer never removed it: wiring only adds what the
current issuer wants, and the issuer had not moved, so no `ReplacedIssuer`
cleanup ran. Mealie kept the appliance root as its whole trust store and failed
the public issuer's Let's Encrypt certificate (VM 2, `/api/auth/oauth` 500).

## Change
The apply's wiring step removes any adapter trust variable that the current
wiring does not set. Removal is value-checked, as in S404, so a value the owner
changed stays.

## Tests
`TestEnvOIDCOwnerValues` adds the stale variable back on a public issuer and
re-applies; the variable must be gone. This fails without the change.

## Out of scope
The plan still reads "Nothing changes" for such a re-apply, because the plan
compares names and the issuer, not the override file. Recorded in DEFERRED.
