# S462 - Doctor reports a hand-stopped app container

## Goal and dependencies
Owner acceptance PLAT-DOCTOR-2. Depends on S316 (Doctor live probes). With `docker stop memos-memos` the Doctor showed `app-containers:memos: ok - 0 service(s) running` (only the reachability probes failed), because the probe treated any `Exited (0)` container as a finished one-shot job. `docker stop` also exits 0.

## What changed
- `probeAppContainers` no longer excuses `Exited (0)`: every generated service restarts unless stopped, so an exited service is a fault ("<service> is exited", with the existing restart guidance). Doctor still does not restart anything.

## Tests
`internal/server/doctorlive_test.go` (exited 0 now fails), `tests/test_s462_doctor_stopped.py`.
