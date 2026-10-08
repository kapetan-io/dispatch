# git-server Partial Clone (`filter`) Blueprint

**Linear:** ENG-120 (parent epic ENG-91, project git-server) · **Branch:** `thrawn01/eng-120-git-server-partial-clone`

Phase-5 follow-up #2 from the slice-2 (fetch) blueprint's *Deferred Work* section. Slices 0–3 (push, fetch, robustness) are merged; this adds the v2 partial-clone `filter` capability on top of the existing fetch path.

## Objective

Let a git client reduce what a clone/fetch transfers by sending `git clone|fetch --filter=<spec>`, and have the server omit the masked objects from the assembled pack while leaving them lazily fetchable later. Per **D9**, partial clone is *core* for the MVP protocol-completion target — this was deferred from slice 2 only to keep that slice bounded, not descoped.

Concretely: advertise the `fetch=filter` sub-capability (gated on config), parse and validate the `filter` argument line in negotiation, and apply **object type/size masking during enumeration below the storage seam** (ADR-0005, §3.7) so the assembled pack contains the negotiated closure minus the masked objects. The conformance cases **N-08…N-18** turn green against both the from-scratch `mvp` adapter and the reference git oracle.

## Mental Model

A `--filter` does not change *which commits/trees* the client is asking for — it changes *which reachable objects the server bothers to send*. The server still computes the same `wants − haves` reachability closure; the filter is a **mask applied as that closure is enumerated**, dropping objects by type or size. The dropped objects are not "missing" in the broken sense — they are **promised**: the client records the pack as coming from a promisor remote and lazily fetches a dropped object (by explicit OID) the moment it actually needs it (N-15).

This maps cleanly onto the existing architecture:

- **Brain (`internal/negotiate`, `internal/protocol`)** decides *what was requested and whether it is allowed*: it parses the wire `filter` line, validates it against policy (is filter enabled for this repo? is this kind supported? is the tree depth within bounds?), and either rejects it with an in-band `ERR` or hands a **well-formed, permitted `storage.Filter` value** across the seam. It also gates the advertisement.
- **Backend (`PackEngine.Assemble`, below the seam)** decides *how the mask is applied during enumeration* — it walks the closure and skips objects the filter masks, using the type+size the in-memory index already has (§3.7). It never parses wire syntax and never re-checks policy; it trusts the validated filter (domain-boundary opacity).

This is the same "what above the seam / how below the seam" split the fetch path already uses (D18, ADR-0005); partial clone adds one validated value to the down-call, not a new responsibility above the seam.

## Correctness Constraints

### State Invariants

1. **Filtered-closure identity.** The set of objects in the assembled pack equals `closure(wants) − closure(haves)`, *minus* exactly the objects the filter masks — no more, no fewer. The haves-exclusion still runs first and unchanged; the filter is a second predicate layered on the surviving want-closure.
   - *Violation:* sending a masked object, or dropping an object the filter does not mask.
   - *Enforcement:* the mask is a predicate evaluated inside the existing closure walk; it governs both **inclusion** (whether an object is added to the send set) and **descent** (whether the walk recurses through it). See *Invariant Preservation*.

2. **Explicit wants are never masked.** An OID named directly in a `want` line is always present in the pack, even when the active filter would mask an object of its type/size. The filter prunes only objects *discovered by traversal* from the want roots, never the roots themselves. This is what makes lazy fetch (N-15) work: a client refetching a previously-masked blob names it as a `want`, and the server must return it.
   - *Enforcement:* the want roots seed the send set before the mask predicate is consulted; the mask is applied only to objects reached *through* a root. (Verify the exact rule against the oracle — see *Open Questions*.)

3. **Pack self-containment, restated for partial clone.** The pack remains self-contained *for the objects it contains*: every commit and tree present resolves, and no emitted object is a delta against a base the client lacks (we emit whole, undeltified objects, unchanged from slice 2). A `blob:none` pack contains a tree that references an omitted blob — that dangling reference is the **promisor contract**, not a broken pack. "Complete for the client's haves" from the slice-2 `Assemble` contract is therefore narrowed under a filter to "complete modulo promised objects."

4. **Masked objects stay fetchable.** The server will serve any object the client later requests by explicit OID. This is *already true* and needs no new code: v2 want-validation is existence-based (ADR-0006), so a `want <blob-oid>` for a previously-masked blob is accepted and `Assemble` returns it. N-15's *server side* is free; the work is only ensuring the filter does not mask an explicit want (Invariant 2).

### Behavioral Constraints

1. **Never silently ignore a filter.** An unsupported, malformed, or banned filter spec is **rejected with an in-band `ERR`** (the `ERR upload-pack: …` line on the negotiation stream), never accepted-and-ignored. Covers N-10 (filter sent to a server that did not advertise it), N-12 (bogus spec), N-13 (banned `sparse:`/`combine:` and out-of-scope `tree:<n>` for n ≥ 1).
2. **Never advertise filter unless enabled.** The `fetch=filter` sub-cap appears in the v2 advertisement only when `uploadpack.allowFilter=true` for the repo (N-11). Default is *not advertised* (git's default `allowFilter=false`).
3. **Stateless per request.** Filter handling adds no per-client server state; each fetch POST re-declares its filter (D24, unchanged).
4. **Opacity.** The brain never branches on which backend applies the mask; the backend never parses wire syntax or re-checks policy. <!-- Architectural constraint (no user-facing story): the validated storage.Filter value is the entire contract between the two. -->

## Acceptance Criteria

Each is mechanically verifiable through the public surface (real `git` over HTTP against both adapters), mapped to its conformance case:

- **N-08 (P0):** `git clone --filter=blob:none` over HTTP succeeds; the resulting clone contains all commits and trees and **zero blobs** from history; a subsequent operation that needs a blob lazily fetches it and succeeds.
- **N-09 (P1):** `git clone --filter=blob:limit=<n>` returns blobs strictly smaller than `<n>` and omits blobs `≥ <n>`; boundary at exactly `<n>` bytes matches the oracle.
- **N-10 (P1):** a client sending a `filter` line to a repo with `allowFilter` unset receives a clean `ERR` (filter not supported), not a silently-unfiltered full pack and not a hang.
- **N-11 (P1):** the `fetch` capability advertises `filter` iff `uploadpack.allowFilter=true`; with it unset, the token is absent.
- **N-12 (P2):** a bogus filter spec (e.g. `blob:bogus`, `blob:limit=` with no number) is rejected with `ERR`.
- **N-13 (P2):** banned/out-of-scope filters — `combine:`, `sparse:*`, and `tree:<n>` for n ≥ 1 (`tree:0` is supported; see the grammar table) — are rejected with `ERR`, matching the oracle's rejection (exact text need not match; see ADR-0002).
- **N-14 (P2):** with only `tree:0` supported, `uploadpack.filterMaxTreeDepth` is satisfied trivially (no accepted filter exceeds depth 0); the case asserts no regression and that a depth-bearing tree filter is rejected (folds into N-13).
- **N-15 (P1):** after a `blob:none` clone, a `git cat-file`/checkout that needs a missing blob triggers a lazy fetch that the server serves correctly.
- **N-16 (P2):** the client tolerates the server not sending a tag's target object on a partial clone (server behavior consistent with the oracle for `include-tag` under a filter).
- **N-17 (P2):** `in_vain` is not triggered before the first ACK / resets on ACK across multi-round negotiation with a filter present — verified to match the oracle's round behavior.
- **N-18 (P1):** `git clone --filter=tree:0` over HTTP succeeds; the resulting clone contains all reachable commits (and tags) and **zero trees and blobs** from history; a subsequent operation that needs a tree or blob lazily fetches it and succeeds (the promisor contract, exactly as N-08/N-15 establish for blobs). This is the positive-acceptance counterpart to the `tree:0` support listed in scope — N-13/N-14 only assert the rejection of depth-bearing tree filters.

The umbrella criterion: **every N-08…N-18 subtest passes against both the `mvp` and `reference` adapters with no skip on the MVP surface.**

## Scope

### In Scope

- Advertise `fetch=filter` gated on `uploadpack.allowFilter` (per repo).
- Parse + validate the `filter` argument line in `internal/negotiate`.
- Three filter kinds: **`blob:none`**, **`blob:limit=<n>`**, **`tree:0`** — the object type/size masks the ticket emphasizes.
- Apply the mask during below-seam enumeration in `PackEngine.Assemble` via a validated `storage.Filter` value, carried in a new `AssembleRequest` struct (the seam-shape decision below).
- Reject unsupported/malformed/banned filters with in-band `ERR`.
- `uploadpack.filterMaxTreeDepth` honored to the extent `tree:0`-only requires (reject any `tree:<n>` with n ≥ 1).
- Conformance cases N-08…N-18 turned green against both adapters; the per-repo config (`allowFilter`, `filterMaxTreeDepth`) driven through the `RepoConfigurer.SetConfig` seam.

### Out of Scope / Non-Goals

- **`sparse:oid` and general `tree:<n>` (n ≥ 1)** — deferred. `sparse` needs a pathspec/pattern-blob matcher against tree paths; general `tree:<depth>` needs depth-aware tree traversal. No N-08…N-18 case functionally gates either, and the ticket's emphasis is type/size masking. Both are rejected via the N-13 "unsupported/banned" path today; either becomes a follow-up ticket if a consumer needs it.
- **`combine:` filters** — rejected (N-13), consistent with not yet supporting filter composition.
- **Shallow clone (`--depth`, `deepen*`)** — sibling ticket **ENG-119**, a separate blueprint. *Shared-seam note:* ENG-119 will feed enumeration a depth/frontier bound (D24: the shallow frontier propagates downward into pack-object selection). The `AssembleRequest` struct introduced here is shaped to absorb that as an added field, so ENG-119 is an additive struct field, not a second seam re-cut. ENG-119 also adds `fetch` sub-caps (`shallow`/`deepen-*`) to the same advertisement line — coordinate the advertisement-assembly code so both tickets append cleanly.
- **Delta-compressed packs** — `Assemble` still emits whole objects (slice-2 decision, unchanged); a filtered pack is whole-object too.
- **Bitmap-accelerated filtered enumeration** — the in-memory backend walks; a future scale backend applies the same filter behind the same `Assemble` contract (ADR-0005). No bitmap work here.

## Dependencies and Constraints

- Builds on the merged slice-2 fetch path: `internal/negotiate.Negotiator`, `storage.PackEngine.Assemble`, the in-memory backend's closure walk, existence-based v2 want-validation (ADR-0006).
- Object type+size must be available during enumeration. The in-memory backend has both directly (`storage.Object.Type`, `len(Object.Data)`), per §3.7's "object index carries type+size." No index change is needed for the in-memory backend; the *contract* note that a backend's object enumeration must expose type+size to the mask is recorded for the future scale backend.
- Oracle-anchoring (ADR-0001): both adapters must honor `uploadpack.allowFilter` and `uploadpack.filterMaxTreeDepth`. The reference sets them via real `git config`; the MVP maps the same keys onto its per-repo `Registry` config through `RepoConfigurer.SetConfig`. Both keys have git-native equivalents, so no skip is needed.

---

## Functional

### Wire contracts (pinned — clients and the conformance suite bind to these)

**Advertisement.** `AdvertiseUploadPack` (v2) currently emits a bare `fetch\n` capability line. When `uploadpack.allowFilter=true` for the repo, it emits the value form listing the supported sub-features:

```
fetch=filter
```

When `allowFilter` is unset/false, the line stays bare `fetch`. (When ENG-119 lands, the value list grows to e.g. `fetch=shallow filter` — the assembly must build the space-separated value from the set of enabled sub-features, not hard-code the string.)

**`filter` argument line.** Within a `git-upload-pack` v2 `fetch` command, the client may send one argument line:

```
filter <filter-spec>
```

Supported `<filter-spec>` grammar for this ticket:

| Spec | Meaning | Masks |
|---|---|---|
| `blob:none` | omit all blobs reached by traversal | every blob not an explicit want |
| `blob:limit=<n>` | omit blobs of size `≥ n` (`<n>` accepts the `k`/`m`/`g` suffixes git uses) | blobs whose payload length `≥ n` |
| `tree:0` | omit all trees and blobs (commits/tags only) | every tree and blob not an explicit want |

Any other spec (`sparse:*`, `combine:*`, `tree:<n>` for n ≥ 1, malformed) → in-band `ERR upload-pack: …`.

**Rejection framing.** A rejected filter is reported the same way other negotiation rejections are (the `ERR upload-pack: <reason>` data line the existing `not our ref` path uses), so the transport does not turn it into an HTTP 5xx (ADR-0003). Exact reason text need not byte-match git (ADR-0002); the suite asserts the failure mode and that the clone fails cleanly, not the wording.

### Negotiation flow changes (`internal/negotiate`)

The `recognizedArg` comment that says "filter … args are deferred and unadvertised" is reversed for `filter`. `Negotiate` gains:

- Recognition of the `filter <spec>` line and parsing into a `storage.Filter` value.
- A **policy gate**: the Negotiator must know, for the repo, whether filter is allowed and the max tree depth. If filter is not allowed and a `filter` line is present → `ERR` (N-10). If the spec is malformed/unsupported/banned → `ErrBadRequest`-equivalent in-band `ERR` (N-12/N-13).
- Passing the validated `storage.Filter` into the down-call to `Assemble` (via `AssembleRequest`).

The Negotiator currently holds only `format`; it gains access to per-repo filter policy. *Contract, not signature:* the negotiation layer needs a way to ask "is filter allowed for this repo, and what is the max tree depth" — supplied by the same per-repo config source the disabled-service gate uses (`Registry`), threaded in at construction or per call. The implementor draws whether that is a small policy interface, a value passed by the protocol `Engine`, or a field — the constraint is only that policy is resolved **above** the seam and the backend receives an already-permitted filter.

## Architecture

### The seam change — `AssembleRequest`

`PackEngine.Assemble` changes from positional `wants, haves` to a request struct carrying the negotiated inputs:

```
Assemble(ctx, w PackWriter, req AssembleRequest) error
```

where `AssembleRequest` bundles `Wants`, `Haves`, and `Filter` (a `storage.Filter`). Rationale (the *Seam shape* decision): ENG-119 will add a shallow frontier/depth bound to the *same* enumeration, so a struct absorbs that as an added field rather than forcing a second breaking signature change across two coupled phase-5 tickets. The zero-value `Filter` means "unfiltered," so today's full-clone path is `AssembleRequest{Wants, Haves}` with no behavior change.

This is a deliberate "draw the seam" change (per the blueprint exception): the struct *shape* is drawn concretely because it is the contract two tickets bind to, but with headroom — its fields are described, not frozen, and it is expected to grow a shallow field.

### The filter value type — `storage.Filter`

Lives in `internal/storage` (the leaf seam package), because it crosses the seam. It is a small validated value, not the wire string. Shape (contract, with illegal-state headroom):

- A **kind** discriminator with a zero value meaning "no filter" (so `AssembleRequest{}` is unfiltered). Kinds: none, blob-none, blob-limit, tree-depth.
- A **size limit** (used by blob-limit) and a **tree depth** (used by tree-depth; only `0` is accepted this ticket).

Making kind an enum with a meaningful zero keeps an invalid kind unrepresentable; the parse/validation layer is the only producer, so a backend never receives a half-formed filter. *Contract, not signature:* the exact Go type (enum const set vs. a tagged struct) is the implementor's to draw against real parse code; the constraint is that the zero value is "unfiltered" and that the kinds enumerated above are the only ones a backend must handle.

### Mask application — the closure walk (`internal/storage/memory`)

`packEngine.closure`/`walk` gain filter awareness. The mask is a predicate consulted at two points in the existing walk:

- **Inclusion:** before adding a loaded object to the send set, the mask decides whether this object's type/size is omitted. (`blob:none` omits any blob; `blob:limit=n` omits a blob with `len(Data) ≥ n`; `tree:0` omits any tree or blob.)
- **Descent:** `tree:0` additionally stops the walk from recursing through the root tree (no trees/blobs are enumerated at all); `blob:none`/`blob:limit` still descend through trees (the trees are sent) but skip the blob leaves. Commits and tags are never masked by these kinds.

The want roots are exempt from the mask (Invariant 2): a root OID is added to the send set regardless of the predicate. The haves-exclusion pass is unchanged and runs first; the filter never causes a `*PackError` (a masked object is intentionally absent, not missing-broken), so the slice-2 missing-object error semantics are untouched for unmasked objects.

The size/type data the predicate needs is already on the in-memory `storage.Object`. *Contract for future backends:* a backend's enumeration must be able to evaluate the mask without a per-object round trip — for the scale backend the object index (§3.7) carries type+size precisely so a bitmap-driven enumeration can mask without fetching blob bytes. Recorded as a seam contract, not built here.

### Config plumbing

`uploadpack.allowFilter` and `uploadpack.filterMaxTreeDepth` are per-repo settings (git models them as repo config). Backing them on the MVP is **new work** — none of the three pieces exist today:

1. **`Registry`** (`internal/registry.go`) currently stores only the per-repo disabled-service gate (ENG-124, a `disabled` map); it gains per-repo storage and accessors for `allowFilter` and `filterMaxTreeDepth` (e.g. a `FilterConfig(repo) (allowFilter bool, maxDepth int)` accessor).
2. **`Daemon.SetConfig`** (`internal/daemon.go`) currently maps only `http.uploadpack`/`http.receivepack` onto `registry.SetServiceDisabled` and no-ops every other key; its dispatch extends to recognize the two new `uploadpack.*` keys and write them onto `Registry`.
3. The protocol **`Engine`** gains a per-repo policy source (the `Registry`, or a small interface extracted from it) so it can read `allowFilter` when assembling the advertisement and supply the Negotiator's policy gate.

The conformance `RepoConfigurer.SetConfig(repo, key, value)` signature already takes arbitrary key/value strings, so no new `ServerConfig` field is needed on the suite side; the reference adapter maps the two keys onto real `git config`, the MVP onto the extended `Registry` above.

### Invariant Preservation

- **Filtered-closure identity (Inv 1):** the only new logic is a predicate inside the established `walk`. Objects excluded by haves are pruned before the predicate (unchanged); objects surviving haves-exclusion are added iff the predicate permits. Because the predicate is a pure function of object type+size (and the want-root exemption), the send set is deterministically `want-closure − haves-closure − masked`.
- **Explicit-want exemption (Inv 2):** enforced structurally by seeding the send set with the want roots before applying the predicate to traversed children. An implementation that instead applied the predicate uniformly would drop an explicitly-wanted blob under `blob:none` — the bug N-15 guards against; the build must verify the root-exemption against the oracle.
- **Self-containment / promisor (Inv 3):** unchanged emit path (whole objects); the filter only *removes* objects, never adds an unresolved delta. A tree referencing an omitted blob is valid promisor output.
- **Fetchability (Inv 4):** no code path restricts serving an object by OID; existence-based want-validation (ADR-0006) already accepts a `want` for any held object.
- **Opacity:** the backend receives a `storage.Filter` and applies it; it never inspects wire syntax or `allowFilter`. The policy gate lives entirely above the seam. No invariant relies on an implementation-specific property of a backend.

### Illegal State Analysis

- `storage.Filter` kind is an enum with a "none" zero value → an unfiltered request and an invalid kind are distinct and a garbage kind is unrepresentable from the parse layer.
- `blob:limit` without a numeric argument, or `tree:<non-zero>`, never produce a `Filter` — they fail validation in the brain and become `ERR`, so the backend only ever sees `{none | blob-none | blob-limit(n) | tree-depth(0)}`.
- The advertisement string is built from the set of enabled sub-features, so "advertised but unsupported" and "supported but unadvertised" cannot drift: the same `allowFilter` gate drives both the advertised token and the negotiation accept/reject.

## Security

- **DoS surface:** a filter spec is small and bounded; parsing is O(spec length). `filterMaxTreeDepth` is the git-native guard against a pathological deep-tree filter; with `tree:0`-only it is satisfied by construction, but the gate is wired so raising scope later inherits the bound. `blob:limit=<n>` size parsing must reject overflow/garbage (→ `ERR`), not panic.
- No new authn/authz: filter availability is a per-repo capability gate (`allowFilter`), orthogonal to the WireGuard trust boundary (ADR-12) and to reachability permissions. A filtered fetch sees exactly what an unfiltered fetch of the same wants/haves would, minus masked objects — never more.

## PII

None. The feature transports git objects the client is already authorized to fetch; it only reduces what is sent. No new data is logged or stored.

## Scale

- The in-memory mask is O(closure size), same order as the unfiltered walk, minus the masked objects' byte reads (a `blob:none` walk never loads blob payloads beyond what type/size inspection needs — and in-memory both are free).
- For the future scale backend, masking by type/size from the object index (§3.7) is *cheaper* than full enumeration: a `blob:none` clone can answer from commit+tree bitmaps without touching blob storage. This is why ADR-0005 put enumeration (and thus masking) below the seam — recorded here as the payoff, not built.

## Testing

Testing follows the `surface-testing` skill. All cases run through the public git wire surface (real `git` over HTTP) against both the `mvp` and `reference` adapters via `ForEachAdapter`/`ForEachAdapterWith`; the reference is the oracle (ADR-0001) and exact byte-identity is not required (ADR-0002).

Key surfaces:

- **integration (conformance):** a new partial-clone case group (mirroring git's `t5616`/`t5500`) under `conformance/`, one subtest per N-08…N-18, each `t.Run(adapter.Name())`. Drives real `git clone --filter=…`, asserts object presence/absence in the resulting clone (including the `tree:0` commits/tags-only shape, N-18) and clean rejection where required, and asserts the advertisement token via `ProbeV2`.
- **config seam:** cases needing `allowFilter`/`filterMaxTreeDepth` use the `conformance.SetRepoConfig(t, srv, repo, key, value)` helper (which type-asserts to `RepoConfigurer` and calls its `SetConfig` method — probe-and-skip-never-fail for an adapter that cannot honor it; both adapters can). N-11 sets `uploadpack.allowFilter=true` then probes the advertisement; N-10 leaves it unset and asserts the `filter` line sent by a handcrafted/`-c` client is rejected.
- **unit (negotiate):** filter-spec parse + validation table (valid kinds → `storage.Filter`; malformed/banned → reject) as a focused test on the negotiation parse, complementing the end-to-end conformance cases (the spec grammar is brain-side and cheap to table-test directly at the package's exported surface).
- **fakes needed:** none new — the in-memory backend is the substitute; no external dependency, no async behavior, no time dependence.

No new observability API is required: filter behavior is fully observable at the wire surface (advertised token, pack contents, rejection line, lazy-fetch success).

## Limitations & Future Work

- `sparse:oid`, general `tree:<n>` (n ≥ 1), and `combine:` filters are rejected, not supported (follow-up tickets if a consumer needs them).
- Filtered enumeration is unaccelerated in the only backend that exists today (in-memory walk); bitmap-driven masking arrives with the scale backend behind the unchanged `Assemble` contract.
- Filtered packs are whole-object (no delta compression), consistent with the rest of the MVP fetch path.

## Open Questions

- **Explicit-want exemption under an active filter (Inv 2).** git's exact rule for whether an explicitly-`want`ed blob is included when a `blob:none`/`blob:limit` filter is *also* present on the same request — versus the common lazy-fetch path where the refetch sends no filter — should be confirmed against the reference oracle during the build rather than assumed. The design assumes "explicit wants are never masked"; the N-15 case (and a targeted want+filter case) is the oracle check. Not a build blocker — the oracle resolves it — but flagged so the implementor verifies rather than guesses.
- **`filterMaxTreeDepth` exact semantics under `tree:0`-only (N-14).** Confirm the oracle's behavior when a depth-bearing tree filter exceeds the configured max vs. when only `tree:0` is offered, so the MVP's rejection matches the matrix expectation rather than over- or under-rejecting.
