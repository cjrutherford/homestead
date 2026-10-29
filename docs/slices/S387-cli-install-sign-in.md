# S387 — Review app sign-in and people in CLI installs

## Goal
Expose the existing install-time Authentik plan and person grants in the
CLI for apps with a Homestead sign-in adapter, using the same reviewed
setup operation as the dashboard.

## Behavior
- `apps plan` prints the sign-in offer, its changes, redirects, app
  settings, role mapping, warnings and digest; `apps install --public`
  prints the offer for the proposed public name.
- `apps install --approve-sign-in DIGEST` approves the matching current
  plan. Repeat `--person ID` to grant selected people user access after
  sign-in setup. The CLI verifies the selected IDs are distinct active
  people before starting the install. It requires a retained Authentik
  API token and refuses a missing, unavailable or stale plan.
- Public installs accept both approval digests, and preserve any other
  setup approvals supplied in the input. Setup remains subject to the
  server's independent re-plan and failure handling.

## Verification and limits
CLI tests cover local and public approval, person selection, stale and
unavailable plans, missing retained tokens, and the request body. These
tests use a simulated server; they do not prove real Authentik sign-in or
provision adapters for additional apps. Those remain separate work.
People can change between this review and the later server grant step;
the current server grants sequentially and can stop after a partial set.
