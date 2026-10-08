# git-server masked missing object under blob:none

A `--filter=blob:none` partial clone is supposed to mask blobs entirely — a blob the server no longer holds should be silently omitted, exactly as it is when the server *does* hold it. Today the clone instead **fails** with a `*storage.PackError` when a masked blob is missing, so a caller cloning with `blob:none` against a repo with a corrupt/absent blob sees an error for an object they never asked for. Reference git tolerates this; we should too.

## Fix

The MVP's `wants` walk loads the masked blob before honoring the mask. The fix is structural: thread the expected object type through the `Assemble` walk queue so the mask predicate runs **before** the missing-object check, and the masked object is never enumerated.

## Proof / where it's tracked

* Skipped conformance test: `services/git-server/conformance/partial_clone_test.go` → `TestPartialClone_MaskedMissingObjectTolerated` (currently `t.Skip`, RS-003). Un-skip it as the acceptance gate.
* Review-suite report: `services/git-server/docs/features/eng-120-git-server-partial-clone/review-suite.html` (finding RS-003).

## Context

Loose end from ENG-120 (partial clone) — the only deferred item from that ticket's review-suite pass. Distinct from ENG-123 RS-003 (the sideband PackError fix).
