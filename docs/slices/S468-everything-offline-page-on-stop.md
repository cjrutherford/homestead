# S468 - Stopped container shows the offline page, not a Caddy 502

## Goal and dependencies
Acceptance run on VM 2 (EV-CONTACTS-9, EV-INVENTORY-9, EV-TIME-TRACKING-9, EV-SOLITAIRE-7, EV-SUDOKU-7, EV-EXPENSE-7; Notes EV-NOTES-10 the same). Depends on S367-S380 (offline page workers).

## Findings
The workers sent a page navigation to the network and served the offline page only when `fetch` threw. With the app container stopped the edge (Caddy) is still up and answers 502, so `fetch` succeeds and the bare gateway error was shown.

## What changed
Narrowest shared fix, in the worker and nothing in the edge or the daemon: in all eight Everything workers (Tasks, Projects, Contacts, Inventory, Time tracking, Solitaire, Expense, Sudoku) a navigation answered 502, 503 or 504 counts as offline and gets the same fixed "You are offline" page. Any other status (200, 404, 500, sign-in redirects) passes through untouched. A Caddy `handle_errors` block was rejected: routes are generated per exposure mode (LAN, tunnel entry, paths, gated), so it would touch every shape and every non-Everything app too.
Images must be rebuilt (`homesteadctl craft build APP --follow`) and refreshed to carry the new worker; the browser picks it up on its next visit (the worker version is the source digest, so the file change alone is detected by byte comparison).

## Not covered
Notes, Circuits, Parametric, API Client, Records and 3D ship no service worker, so a stopped container still shows the 502 there (DEFERRED). First visit after a stop with no worker installed also shows it.

## Tests
`scripts/everything/test_edge_down_sw.cjs` (run by `tests/test_s468_everything_edge_down.py`) exercises all eight workers: 502/503/504 give the offline page; 200/404/500 pass through.
