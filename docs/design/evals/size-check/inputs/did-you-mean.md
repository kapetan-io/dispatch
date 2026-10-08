# git-server generated did-you-mean 404s

Agents that guess a wrong /v1/ endpoint on git-server self-corrected via hand-rolled teaching 404s (`did_you_mean` + an endpoint list in the error details). That behavior should come from the contract toolchain, not custom mux code — one less bespoke subsystem to maintain.

**Original scope — teaching 404s onto the toolchain**

duh-cli v0.8.0's `x-duh-did-you-mean` path-item extension generates matched routes that answer teaching 404s. Declare the three near-misses the surface tests pin (`/v1/diffs.get`→`/v1/commits.diff`, `/v1/files.download`→`/v1/files.get`, `/v1/commits.mergeBase`→`/v1/commits.merge-base`) and delete the runtime `closestEndpoint`/`tokenize` matcher.

The migration is not behavior-neutral: the generated arm emits only `did_you_mean` (no endpoint list) and covers only declared guesses. Evaluated against the existing surface tests — the declared list is sufficient for the guesses that must self-correct; the endpoint census moves onto the contract test, which reads the spec (the source of truth) rather than a runtime 404 body.

**Expanded scope — dissolve the bespoke graph mux**

Reviewing the mux showed the rest of it was hand-rolled transport machinery that only existed because we were experimenting to find the shape of the teaching APIs. With the shape settled, it moves onto the toolchain/framework seams:

* Remove the `prevalidate` teaching-400 pass (unknown-field / pagination-envelope hints) and its duplicated per-endpoint field-schema table — a second source of truth for the contract, and a double-unmarshal of every request body. An unknown JSON field is then refused by the generated decoder as a DUH 455 (Client Content Error); value-level 400s (empty rev, bad OID, `..` in path, out-of-range `first`, bad cursor) stay in the service.
* Bump scaffold v0.0.5 → v0.0.6, which defaults terminal 404/500 to a DUH-shaped JSON reply and adds `SetErrorHandler`.
* Drop the bespoke `/v1` prefix reservation. The wire transport participates in scaffold's RPC chain as a decline-handler (`ServeHTTP(w, r) bool`, declining non-git paths) instead of `SetMux`, so an unrouted path — an unmatched `/v1/*` or otherwise — falls through to scaffold's terminal JSON 404. No hand-rolled catch-all.
* `graphapi.Mux` → `graphapi.Handler`: a thin adapter that only parses the credential into context, sets the files.get Accept shim, and adds the 401 challenge, then delegates to the generated dispatcher. Routing, teaching-404s, and the terminal 404 are owned by duh + scaffold.

Net: the graph surface's `mux.go` (~275 lines) collapses to an ~80-line adapter; routing, teaching, error-shaping, and field validation all move to the toolchain and framework.

Note for spec hygiene: `x-duh-did-you-mean` entries are generator input, not consumer-facing prose, so they don't violate the "docs must not describe the self-correction machinery" rule.

Enabled by the duh-cli v0.8.0 bump in ENG-132.
