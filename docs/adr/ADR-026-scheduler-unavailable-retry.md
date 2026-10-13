# Scheduler Unavailable Retry

## Status
Accepted

Amends ADR-023 (Daemon Scheduler).

## Decision
A scheduled job whose build fails because its inputs are not there right
now (the backup destination mount is not accessible, read-only or absent
from discovery, or the package source directory has not appeared) is
unavailable, not failed. The build marks this by wrapping
`scheduler.ErrUnavailable`. An unavailable job does not spend its window:
`last_run_at` stays put, the job stays due, and the scheduler retries with
exponential backoff (5 minutes doubling, capped at the job interval), so a
late-mounting disk still gets its scheduled run. Configuration faults (no
class assignment, unknown package, unreadable secret, and so on) are never
unavailable: they fail through the ordinary path and spend the window, as
ADR-023 describes.

The outage is audited on its first attempt and then periodically: at least
once every 24 hours and at every tenth attempt, so an unbounded outage
stays visible without one audit row per retry. Scheduler status reports
the attempt count and the time the outage began (in memory; a daemon
restart retries immediately and resets both).

## Consequences
- Only inputs that can appear on their own are wrapped as unavailable.
- The overlap, skip and missed-run policies of ADR-023 are unchanged.
- Changes require a superseding ADR.
