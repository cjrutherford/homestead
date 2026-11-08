# S464 - Narrow CSP allowances for Parametric and API Client

## Goal and dependencies
Acceptance run on VM 2 (EV-PARAMETRIC-6/7, EV-API-CLIENT-10). Depends on S375/S376 (WebAssembly policy), S436 (API Client), ADR-057.

## Findings
- Parametric: the page already allows `'wasm-unsafe-eval'`. The failure was not WebAssembly: the geometry worker (`assets/worker-*.js`, emscripten embind) builds its call stubs with `new Function(...)`, which is JavaScript eval. It stays refused on the page.
- API Client: the script sandbox is a `srcdoc` iframe. A srcdoc frame inherits the embedding page's policy, so the page policy (no inline script, `worker-src 'self'`, no eval, no `frame-src`) blocked the frame's loader, its Blob workers and the eval that runs a script. The frame's own meta policy could only narrow, never widen.

## What changed (no ADR; ADR-057's policy stays fail closed by default, the allowances are per app and declared in the manifest)
- `HOMESTEAD_EVERYTHING_WORKER_EVAL=1` (`catalog/parametric.yaml`): `'unsafe-eval'` is added only to the response for a Worker script (`Sec-Fetch-Dest: worker` or `sharedworker`). The page policy and every other response are unchanged.
- `HOMESTEAD_EVERYTHING_SANDBOX_PAGE=homestead-sandbox.html` (`catalog/api-client.yaml`): the bridge serves that one file with its own policy (`script-src 'self' 'unsafe-eval' blob:`, `worker-src blob:`, `connect-src 'none'`, nothing else) and the app page gains `frame-src 'self'`. The frame keeps `sandbox="allow-scripts"` (opaque origin: no app DOM, storage or cookies) and no network. A patch makes the app frame that page instead of `srcdoc`; the loader moved verbatim into `homestead-sandbox.js` (added files, a copy to re-check against upstream on a version bump).
- Images `everything-api-client` and `everything-parametric` must be rebuilt (`homesteadctl craft build`) and the apps reinstalled to pick up the new environment; installed copies keep the old environment until then.

## Tests
`internal/everything/csp_test.go` (worker eval only for worker scripts of apps that ask; sandbox page policy; page never gets eval), `tests/test_s464_everything_csp.py`; S375/S376/S377/S436 pytest pins updated to look at the page policy function and the new patch count.
