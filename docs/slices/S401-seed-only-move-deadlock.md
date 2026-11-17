# S401: seed-only moves no longer deadlock

## Problem
S400 added a restart step for moves that write seed files but change no
environment (Jellyfin's `BaseUrl`). `catalog.StartStack` took the
project's Compose lock and then called `composeUp`, which takes the same
lock. That lock is not re-entrant, so the move hung at its `up` step,
with Jellyfin stopped. This was seen on VM 2 (op 160).

## Change
`StartStack` no longer takes the lock; `composeUp` holds it, as it does
for every other caller.

## Tests
`TestStartStackDoesNotDeadlock` runs `StartStack` against a stubbed
Compose and fails if it does not return within five seconds.

## Out of scope
- A move that fails between its stop and up steps leaves the stack
  stopped (S400 review); see DEFERRED.
