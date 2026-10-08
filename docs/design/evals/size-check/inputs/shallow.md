# git-server Shallow Clone/Fetch Blueprint

**Linear:** ENG-119 (parent epic ENG-91, project git-server) · **Branch:** `thrawn01/eng-119-git-server-shallow-fetch`

Phase-5 follow-up #1 from the slice-2 (fetch) blueprint's *Deferred Work* section. Slices 0–3
(push, fetch, robustness) and the sibling partial-clone follow-up (ENG-120) are merged; this adds
the v2 **shallow** capability on top of the existing fetch path. It makes the D24
**bounded-ancestry-walk primitive** — drawn into `GraphIndex` at contract altitude in slice 2 but
never exercised — a real consumer, and adds the `shallow`/`deepen*`/`unshallow` wire exchange and a
below-seam server-shallow-repo capability.

> **Relationship to ENG-120 (partial clone).** ENG-120 landed the seam headroom this ticket spends:
> `PackEngine.Assemble` already takes an `AssembleRequest` struct whose doc comment reserves room
> for "a shallow-clone frontier/depth bound", `GraphIndex.Reachable`'s doc comment names itself "the
> unbounded degenerate of a general bounded-ancestry primitive (D24) … adding shallow support is a
> new consumer, not a seam re-cut", and `Engine.fetchSubFeatures` already builds the `fetch=…`
> advertisement from a set of enabled sub-features. Shallow is therefore **additive**: a new
> `AssembleRequest` field, a new `GraphIndex` method, one appended advertisement token — no seam
> re-cut. ENG-120 is the structural template (a validated seam-crossing value produced above the
> seam in `negotiate`, passed opaque through `protocol`, consumed below the seam in
> `storage`/`memory`).

## Objective

Let a git client bound how much history a clone/fetch transfers — `git clone --depth=N`,
`--shallow-since=<date>`, `--shallow-exclude=<ref>`, and deepen an existing shallow clone with
`git fetch --depth`/`--deepen`/`--shallow-since`/`--shallow-exclude` — and let the server both
**serve** such bounded fetches and **hold** a shallow repo (push to/from one). Per D9 this is core
MVP protocol-completion work, deferred from slice 2 only to keep that slice bounded.

Concretely, four additive moves:

1. **Advertise `shallow`** as a `fetch` sub-capability (unconditionally — git has no `allowShallow`
   config), coordinated with ENG-120's `filter` token on the same line.
2. **Parse the shallow/deepen wire exchange** in `internal/negotiate`: the client's `shallow <oid>`
   boundary declaration and the `deepen <n>` / `deepen-relative` / `deepen-since <ts>` /
   `deepen-not <ref>` argument lines; emit the response `shallow-info` section
   (`shallow <oid>` / `unshallow <oid>`).
3. **Make the D24 bounded-ancestry primitive live** on `GraphIndex`: a commit-level walk from
   `roots = wants` bounded by depth and/or committer-timestamp, excluding a `deepen-not` set,
   relative to the client's declared shallow set, producing the **frontier** (new boundary commits)
   and the **unshallow** set. The frontier propagates downward into `PackEngine.Assemble` as a new
   `AssembleRequest.Shallow` stop-set (ADR-0005 unchanged: object enumeration stays below the seam;
   the frontier is a *bound*, like `haves`, not a materialized object set).
4. **A server-shallow-repo capability** below the seam: a per-repo recorded shallow boundary, so the
   MVP backend can hold a repo with genuinely-absent deep history — bounding what `Assemble` serves,
   admitting a push that builds on the boundary, and reporting the boundary in `shallow-info`. A
   conformance provisioning seam (`MakeRepoShallow`) creates one on the MVP, mirroring the
   reference's `git clone --depth`.

Conformance cases **N-01…N-07** turn green against both the `mvp` adapter and the reference oracle.

> **Altitude.** This is a "draw the seam" ticket for the D24 primitive (its *explicit* deliverable
> is to fix the bounded-ancestry method so no downstream re-decides it). The primitive's method
> **shape** and the `AssembleRequest.Shallow` field are therefore drawn more concretely than the
> default rule — but as **contracts with headroom** (conceptual inputs/outputs, open error
> categories, struct internals described not frozen), never as frozen Go. The exact signatures are
> the implementor's, drawn against real parse code and real commit-graph types.

## Mental Model

A shallow clone is a **grafted** history: the client holds commits down to a **boundary**, and the
boundary commits are treated locally as having no parents (their real parents are absent but not
"missing-broken"). Everything about shallow is computing and communicating **that boundary**.

Three distinct roles, all riding the same boundary concept:

- **Client-driven shallow fetch from a complete server** (N-01, N-03, N-04, N-05). The client asks
  for a bounded slice (`--depth`, `--shallow-since`, `--shallow-exclude`, `--deepen`). The server
  computes the **frontier** — the deepest commits it will send, whose parents it withholds — sends
  a `shallow-info` section naming them, and assembles a pack containing exactly the commits from the
  `want` tips down to (and including) the frontier, with their trees and blobs, but not the withheld
  parents. This is the heart of the feature.

- **Deepening an existing shallow clone** (N-05). The client re-declares its current boundary with
  `shallow <oid>` lines and asks to go deeper (`--depth`/`--deepen`/`--shallow-since`). Commits that
  were the client's boundary but whose parents are now being sent are named in `unshallow <oid>`
  lines; a new, deeper frontier is named in `shallow <oid>` lines.

- **The server holds a shallow repo** (N-02 fetch-from-shallow-server, N-06 push-to-shallow-server).
  The server repo itself has a boundary — deep history is genuinely absent. A fetch from it is capped
  at the server's boundary (a full clone of a shallow server yields a shallow clone); a push that
  builds on the server's boundary tip succeeds without demanding the absent parents. This is a new
  **backend** capability, orthogonal to the negotiation above.

The architecture split is the same "what above the seam / how below the seam" the fetch path already
uses (D18, ADR-0005):

- **Brain (`internal/negotiate`, `internal/protocol`)** parses the wire shallow/deepen lines,
  invokes the `GraphIndex` bounded-ancestry primitive to compute the frontier + unshallow set, frames
  the `shallow-info` section, and hands the frontier down as a validated stop-set. It never walks
  objects.
- **Backend (`GraphIndex` bounded walk + `PackEngine.Assemble`, below the seam)** computes the
  commit-level frontier (Tier-1) and bounds object enumeration at it (Tier-2 territory). It also owns
  the recorded server-shallow boundary. It never parses wire syntax.

The frontier is the entire contract between the two: a small set of boundary commit OIDs.

## Correctness Constraints

### State Invariants

1. **Bounded-closure identity.** The assembled pack contains exactly the object closure reachable
   from `wants` but not from `haves`, **pruned at the shallow frontier**: every commit from a `want`
   tip down to and including a frontier commit is present with its full tree and blobs; no commit
   strictly beyond the frontier (a withheld parent) and none of its exclusive objects is present.
   - *Violation:* sending a withheld parent commit (leaking deeper history than `--depth` asked for),
     or omitting a tree/blob of an included commit (an unusable shallow clone).
   - *Enforcement:* the frontier is a **stop-set** carried in `AssembleRequest.Shallow`; the closure
     walk includes a frontier commit and descends its tree, but does **not** enqueue its parents.
     Layered on the existing haves-exclusion (which runs first, unchanged) and the ENG-120 filter mask
     (independent predicate). See *Invariant Preservation*.

2. **`shallow-info` accuracy.** The response's `shallow <oid>` lines name exactly the new boundary
   commits whose parents this response withholds; the `unshallow <oid>` lines name exactly the
   commits in the client's declared `shallow` set whose parents this response now sends (deepening).
   - *Violation:* naming a commit shallow whose parents are in the pack (client grafts a false
     boundary), or failing to unshallow a now-completed commit (client keeps a stale graft).
   - *Enforcement:* the bounded-ancestry primitive computes both sets from the same walk that
     produces the frontier, comparing the new frontier against the client-declared shallow set.

3. **Stateless between requests (D24).** Shallow negotiation holds **no** server-side session. Each
   fetch POST carries the client's **full** current shallow boundary (`shallow <oid>` lines) and its
   deepen arguments; the frontier is recomputed from that request alone.
   - *Enforcement:* structural — the `Negotiator` threads no shallow state across requests; the
     primitive is a pure function of one request's roots/bounds/declared-shallow. (A single protocol
     v0 connection may buffer state for its duration; v0 fetch is out of scope, deferred.)

4. **Server-shallow honesty (capability B).** A shallow **server** repo never claims to serve, and
   never is asked by the connectivity check to hold, objects beyond its own recorded boundary. A
   fetch from a shallow server is capped at that boundary (unioned into the frontier); a push onto the
   boundary tip does not demand the absent parents.
   - *Violation:* `Assemble` walking past the server's boundary into absent objects (a crash or a
     `*PackError` on legitimately-absent history), or the push connectivity check rejecting a valid
     push because it demands a boundary commit's absent parent.
   - *Enforcement:* the server's recorded boundary is a stop-set unioned into the frontier for
     `Assemble`, and is added to the connectivity check's root set (boundary commits are treated as
     present roots, their parents not required — the D25 batched `HasMany` boundary excludes them).

### Behavioral Constraints

1. **Never leak history past the requested boundary.** A `--depth=1` clone transfers only the tip
   commit(s) and their trees/blobs — never an ancestor. This rules out any design that assembles the
   full closure and trims afterward at the wire (the frontier must bound *enumeration*, not framing).
2. **`deepen-since` bounds on committer timestamps, not author** (D24). The cutoff compares each
   commit's **committer** date. This rules out reusing an author-date parse and is why
   `object.ParseCommit` gains committer-timestamp extraction (it returns only tree+parents today).
3. **`shallow` advertised only when it can be honored, but not config-gated.** Git advertises
   `shallow` in the v2 `fetch` capabilities **unconditionally** (there is no `uploadpack.allowShallow`
   knob). So the token is always present — no per-repo policy gate, unlike ENG-120's `allowFilter`.
   <!-- Architectural constraint (no user-facing story): the advertisement set drives capability
   gating; the shallow token's presence is what makes the conformance suite run N-01…N-07 against the
   mvp adapter. -->
4. **Opacity.** The `Negotiator` switches only on the `GraphIndex` primitive's contract and the
   frontier it returns — never on which backend computed it (naive walk vs. generation-number pruning
   vs. bitmaps), never on a backend-specific error type. `Assemble` receives the frontier as an opaque
   OID stop-set and never learns whether the bound was depth, timestamp, exclusion, or the server's own
   boundary. <!-- Architectural constraint: the frontier OID set is the entire contract; no invariant
   relies on how it was derived. -->
5. **No chatty per-object reads (D25).** The frontier is one seam call (`GraphIndex` bounded walk);
   enumeration is one below-seam `Assemble`. No brain-driven per-commit or per-object loop.

## Acceptance Criteria

Each is mechanically verifiable through the public surface — real `git` over HTTP against both the
`mvp` and `reference` adapters via `ForEachAdapter`/`ForEachAdapterWith`, plus raw HTTP + hand-crafted
pkt-line for the framing assertion — the reference is the oracle (ADR-0001); byte-identity is not
required except where the protocol mandates framing (ADR-0002).

- **N-01 (P0):** `git clone --depth=N` over HTTP against a complete server succeeds; the resulting
  clone contains exactly the N most-recent commits per tip (their trees/blobs), is marked shallow at
  the frontier, and `git fsck` passes. Deepening it later (N-05) completes cleanly.
- **N-02 (P1):** the `shallow-info` framing is correct and appears **once** — no `shallow`/`unshallow`
  lines are emitted after the acknowledgments section resolves to "ACK … ready". Asserted at the raw
  wire surface. *(t5539 #3 forces protocol v0; the case is authored to assert the v2-equivalent
  shallow-info placement, with the v0-specific negotiation gated/skipped for the v2 mvp per the
  capability probe.)*
- **N-03 (P1):** `git clone --shallow-since=<date>` and a follow-up `git fetch --shallow-since=<date>`
  return exactly the commits whose **committer** date is at/after the cutoff; the boundary matches the
  oracle.
- **N-04 (P1):** `git clone --shallow-exclude=<ref>` and `git fetch --shallow-exclude=<ref>` return
  exactly the commits not reachable from the excluded ref; the boundary matches the oracle.
- **N-05 (P1):** deepen an existing shallow clone — `git clone --depth 1` then `git fetch --deepen=1`
  (relative) — yields the correct deeper history, with the previously-shallow tip **unshallowed** and
  a new frontier one commit deeper. Also asserts the no-op case: from that same shallow clone, a
  plain `git fetch` (no `--deepen`/`--depth`/`--shallow-since`/`--shallow-exclude`) returns an empty
  `shallow-info` body — no `unshallow` lines, boundary unchanged, repo stays shallow.
- **N-06 (P2):** `git push` from a complete client onto a **shallow server repo** (provisioned with a
  recorded boundary) succeeds; the pushed commit is added, the server stays shallow at its boundary,
  and a clone-back + `fsck` confirms the expected shallow slice (the pushed commit + the server's
  boundary history, no deeper).
- **N-07 (P2):** `git push` **from a shallow client** to a complete server succeeds; the server
  accepts the push (its connectivity check finds the client's shallow-boundary parents already
  present) and ends with complete history.

The umbrella criterion: **every N-01…N-07 subtest passes against both the `mvp` and `reference`
adapters with no skip on the mvp surface**, and the pre-shallow fetch/push cases (F-*, P-*, V2-*,
N-08…N-18) stay green (no regression from the `AssembleRequest`/advertisement/negotiation changes).

## Success Metrics

None — protocol-engine infrastructure with no post-ship user-facing outcome to measure. (The deferred
per-fetch observability surface — object count, pack bytes, negotiation rounds — would gain a
"shallow depth / frontier size" dimension when it is seeded with the local backend; not an acceptance
gate here.)

## Scope

### In Scope

- Advertise `fetch=shallow` unconditionally, coordinated with `filter` on the same line.
- Parse `shallow <oid>`, `deepen <n>`, `deepen-relative`, `deepen-since <ts>`, `deepen-not <ref>` in
  `internal/negotiate`; reject a malformed shallow/deepen line cleanly (in-band `ERR`, never a hang or
  5xx).
- The **live** D24 bounded-ancestry primitive on `GraphIndex` (depth + committer-timestamp bound,
  `deepen-not` exclusion, client-declared shallow set, `deepen-relative`), producing frontier +
  unshallow. The in-memory backend implements it as a bounded commit-parent walk; a bitmap/generation
  backend substitutes behind the same contract.
- Frame the `shallow-info` response section (`shallow`/`unshallow` lines) in the correct v2 position.
- `AssembleRequest.Shallow` frontier stop-set; the in-memory closure walk bounds enumeration at it.
- **Server-shallow-repo capability (B):** a per-repo recorded shallow boundary in the backend; it
  bounds `Assemble`, feeds the push connectivity check, and is reported in `shallow-info`. A
  `MakeRepoShallow` provisioning seam (backend + conformance `Server` capability + `conformance`
  helper). The reference maps it to a real shallow clone; the mvp records the boundary.
- Push-side shallow: `internal/protocol` receive-pack tolerates and applies the client's `shallow
  <oid>` lines (N-07); the connectivity check treats client-declared and server-recorded boundary
  commits as roots (N-06/N-07).
- `object.ParseCommit` (or a sibling) gains committer-timestamp extraction for `deepen-since`.
- Conformance cases N-01…N-07 green against both adapters; two backend-agnostic seam-contract tests
  (bounded-ancestry primitive; `Assemble` with a frontier).

### Out of Scope / Non-Goals

- **DoS depth/`deepen-since` caps** (D24's "handler caps depth to bound the stateless recompute").
  Deferred: shallow adds no unbounded surface worse than the already-uncapped full-clone `Assemble`
  walk (a huge `--depth` degenerates to a full clone), so capping shallow in isolation is inconsistent
  and premature. Implement exact git-parity (no cap) now; record the cap as future hardening
  (*Limitations*). → follow-up ticket if a fairness/tenancy requirement forces it.
- **Protocol v0 shallow negotiation.** v0 fetch is a separately-deferred phase-8 concern (slice-2
  BP-15). N-02's v0-specific negotiation is gated/skipped for the v2 mvp; the shallow work here is v2.
- **`packfile-uris` / `ref-in-want` interaction with shallow.** Those capabilities are unbuilt
  (phase 6/7); shallow is designed not to preclude them but adds no handling.
- **Delta-compressed shallow packs.** `Assemble` still emits whole objects (slice-2 decision); a
  shallow pack is whole-object too.
- **Bitmap/generation-number-accelerated bounded walk.** The in-memory backend walks commit parents;
  a future scale backend substitutes generation-number pruning behind the same `GraphIndex` primitive
  (D11/D17). No acceleration work here.
- **Reachability-permission or tenancy interaction with the shallow boundary.** Auth is unbuilt; a
  shallow fetch sees exactly what an unbounded fetch of the same wants/haves would, minus the withheld
  deep history — never more.

## Dependencies and Constraints

- Builds on merged slice-2 fetch + ENG-120: `negotiate.Negotiator`, `storage.PackEngine.Assemble`
  (the `AssembleRequest` struct), `storage.GraphIndex.Reachable`, existence-based v2 want-validation
  (ADR-0006), `Engine.fetchSubFeatures`/`fetchCapability`, the `RepoConfigurer` conformance seam and
  `ServerConfig` DI pattern.
- Oracle-anchoring (ADR-0001): both adapters must reach the same shallow boundaries. The reference
  uses real `git` (`--depth`/`--shallow-since`/`--shallow-exclude`/`--deepen`, and `git clone --depth`
  to provision a shallow server repo); the mvp must match. `deepen-since` compares committer dates, so
  the conformance fixtures set `GIT_COMMITTER_DATE` deterministically (t5539 does).
- No injectable clock (slice-2 decision holds): `deepen-since` reads committer timestamps from the
  commit objects themselves, not wall-clock. No time-dependent server behavior is introduced.

---

## Functional

### Wire contracts (pinned — clients and the conformance suite bind to these)

**Advertisement.** `AdvertiseUploadPack` builds the fetch line from `fetchSubFeatures(repo)` via
`fetchCapability`. Shallow appends the `shallow` token **unconditionally**:

```
fetch=shallow                 # shallow enabled (always), filter off
fetch=shallow filter          # shallow + ENG-120 filter (allowFilter=true)
```

The `fetchSubFeatures` set must build the space-separated value from the enabled sub-features (it
already does for `filter`); the two tickets append cleanly. The owned advertisement golden
(`testdata/mvp/advertisement.golden`) is updated as a reviewed change — the wire-golden asserts the
capability **set/shape**, not order (per the CONTEXT.md wire-golden rule), so token order is free.

**Client shallow/deepen argument lines** (within a v2 `fetch` command). The Negotiator recognizes:

| Line | Source | Meaning |
|---|---|---|
| `shallow <oid>` | client's current boundary | the client already has `<oid>` as a shallow (parentless) root; declared every request (stateless) |
| `deepen <n>` | `--depth=N` | absolute: keep commits within `n` of the want tips |
| `deepen-relative` | `--deepen=N` | modifies `deepen <n>` to count from the client's current shallow boundary, not the tips |
| `deepen-since <ts>` | `--shallow-since` | keep commits with **committer** time ≥ `<ts>` (unix seconds) |
| `deepen-not <ref>` | `--shallow-exclude` | keep only commits **not** reachable from `<ref>` (a ref name; a bare OID is rejected — reference git requires a ref); may repeat |

A malformed shallow/deepen line (bad OID, non-numeric `deepen`, unresolvable `deepen-not` ref) →
in-band `ERR upload-pack: …` (the existing `not our ref` rejection framing, so the transport does not
turn it into HTTP 5xx — ADR-0003). Exact reason text need not byte-match git (ADR-0002).

**Response `shallow-info` section** (v2). When the request carried any shallow/deepen line (or a full
clone hits a shallow **server** repo's boundary), the fetch response includes a `shallow-info`
section, positioned **after `acknowledgments` and before `packfile`**, introduced by its section
header and delimited like the other v2 fetch sections:

```
shallow-info
shallow <oid>        # a new boundary commit; its parents are withheld
unshallow <oid>      # a previously-shallow commit now completed; its parents follow
```

`shallow` lines name the frontier; `unshallow` lines name client-declared shallow commits now
deepened past. The section is emitted **once** per response and never after the acknowledgments
resolve to "ready" (N-02). When no boundary applies, the section is absent entirely.

### Negotiation flow changes (`internal/negotiate`)

The `recognizedArg` switch (which today handles `thin-pack`/`no-progress`/`ofs-delta`/`include-tag`
with no effect, and whose comment declares "shallow/ref-in-want args remain deferred and
unadvertised") gains real handling for the five shallow/deepen lines above. `Negotiate`:

1. Parses the `shallow <oid>` set and the deepen arguments into a validated request for the
   bounded-ancestry primitive (contract, not signature — see *Architecture*).
2. Calls `GraphIndex`'s bounded-ancestry primitive with `roots = wants`, the parsed bound, the
   `deepen-not` exclusion set, the client's declared shallow set, and the `deepen-relative` flag —
   receiving the **frontier** and **unshallow** sets. The server's own recorded shallow boundary
   (capability B) is unioned into the inputs so a shallow server caps the frontier.
3. Frames the `shallow-info` section from those two sets.
4. Passes the frontier into the down-call as `AssembleRequest.Shallow`.

The Negotiator holds `format` today and gains access to the per-repo shallow boundary source (the same
`Registry`/backend the filter policy threads through) — resolved **above** the seam so the backend
receives an already-computed frontier (opacity).

### Push-side shallow (`internal/protocol` receive-pack, `internal/writepath`)

- **N-07 (push from a shallow client):** git's `git-receive-pack` request from a shallow clone carries
  `shallow <oid>` lines declaring the client's boundary. The receive-pack handler parses them and adds
  them to the connectivity check's root set (the D25 in-memory subgraph walk + batched
  `ObjectStore.HasMany(boundary)`), so a boundary commit's absent parent is not demanded. The server
  is complete, so the parents are in fact present — the case largely exercises that the handler
  *tolerates and applies* the client's `shallow` lines rather than rejecting the request.
- **N-06 (push to a shallow server):** the target repo has a **server-recorded** boundary; those
  boundary commits are added to the connectivity check's root set exactly as client-declared ones are.
  A push that builds on the boundary tip (parent present) succeeds; the boundary is preserved (the push
  does not un-shallow the server).

## Architecture

### The D24 bounded-ancestry primitive — drawn live on `GraphIndex`

This is the ticket's centerpiece and the "draw the seam" deliverable. `GraphIndex` gains a second
method beside `Reachable`. Its **contract** (shape drawn concretely per the draw-the-seam exception,
internals left to the implementor):

- *Responsibility:* compute the shallow **frontier** and **unshallow** set for one fetch, from
  commit-level ancestry only (Tier-1). It never enumerates trees or blobs (that stays in `Assemble`,
  ADR-0005).
- *Conceptual inputs:* `roots` (the `want` commits); a **bound** that is any combination of a depth
  `n` and/or a committer-timestamp cutoff; an **exclusion set** (`deepen-not` commits and their
  ancestors are pruned); the client's **declared shallow set**; a **relative** flag (depth counts
  from the declared shallow set, not the roots); and the repo's **server-recorded boundary**
  (capability B), which also stops the walk. **Bound-absent is two cases, keyed on the declared
  shallow set, not one:** with an **empty** declared shallow set (a non-shallow client's ordinary
  fetch), bound-absent is genuinely unbounded — the degenerate `Reachable` case. With a
  **non-empty** declared shallow set and no depth/`deepen-since`/`deepen-not`/relative argument (an
  already-shallow client's plain incremental fetch), bound-absent means *hold the boundary*: the
  frontier equals the declared shallow set, the unshallow set is empty, and the walk does not
  proceed past it — matching real git, which never auto-unshallows on a plain fetch.
- *Conceptual outputs:* the **frontier** — the set of commits at the boundary of what will be sent
  (their parents withheld) — and the **unshallow** set — declared-shallow commits whose parents are
  now within the bound. An error category for a malformed input (surfaced to the handler as a clean
  `ERR`).
- *Error/edge contract:* a `deepen-not` ref that resolves to nothing, or a bound that excludes the
  roots themselves, degenerates predictably (empty frontier / empty pack) rather than erroring; a
  genuinely absent commit encountered mid-walk (should not happen within a consistent repo) surfaces
  the recognizable seam error category. A bound-absent request against a non-empty declared shallow
  set is not the unbounded case (see conceptual inputs) and must never be treated as a request to
  walk full ancestry — doing so fabricates `unshallow` lines for history the response never sent.

*Contract, not signature:* the exact Go shape — a `BoundedAncestryRequest`-style input struct
mirroring `AssembleRequest`, returning a small result struct — is the implementor's to draw against
the real commit-graph types. The constraint is only that the inputs and outputs above are all present
and that the unbounded case coincides with the existing `Reachable` (so the two share one walk
family, per the slice-2 RS-009 synthesis).

*In-memory implementation:* a bounded commit-parent BFS/DFS from `roots`, tracking depth (or reading
committer timestamps via the extended `object.ParseCommit`), pruning at the exclusion set and the
server boundary, collecting the deepest-reached commits as the frontier. This is the stand-in for a
scale backend's generation-number-pruned walk; the query contract is identical either way (D17).

### `AssembleRequest.Shallow` — the frontier fed downward (ADR-0005 preserved)

`AssembleRequest` gains a `Shallow` field: the frontier as a **stop-set** of commit OIDs.

```
type AssembleRequest struct {
    Wants   []OID
    Haves   []OID
    Filter  Filter
    Shallow []OID   // frontier: boundary commits whose parents are withheld; nil = not shallow
}
```

This is the additive field the ENG-120 doc comment reserved — no breaking signature change, no seam
re-cut. The zero value (nil) means "not shallow", so today's full/filtered clones are unchanged.

*Why a stop-set, not a materialized commit set (the ADR-0005 line):* ADR-0005 rejects handing the
backend a precomputed object or commit set — bitmaps compute commits+trees+blobs together and consume
`wants`/`haves`, not a precomputed set. The frontier is not that set; it is a **bound**, the same
shape as `haves` (a small set of commit OIDs the walk stops at). `Assemble` still enumerates the
closure below the seam; the frontier just adds a second stop condition: at a frontier commit, include
the commit and descend its tree, but do **not** enqueue its parents. A bitmap backend intersects the
same frontier as a boundary on its reachability set-ops. So enumeration stays below the seam and the
seam stays honest.

*Composition with the ENG-120 filter:* `Shallow` and `Filter` are independent predicates on the same
walk — the frontier bounds which **commits** are reached; the filter masks which **objects** (by
type/size) among the reached closure are sent. `git clone --depth=1 --filter=blob:none` applies both.
The want-root exemption (ENG-120 Invariant 2) and the haves-exclusion are unchanged and run as before.

### Mask/bound application — the closure walk (`internal/storage/memory`)

`packEngine.closure`/`walk` (which already do haves-exclusion then the filter-masked wants pass) gain
one more rule: when the walk reaches a commit in `req.Shallow`, it **includes** that commit and
descends its tree (so the client gets the boundary commit's full snapshot), but does **not** enqueue
the commit's parents. The server-recorded boundary is unioned into `req.Shallow` by the brain before
the call, so a shallow server naturally stops there too — the walk never reaches an absent object, so
a legitimately-shallow boundary produces **no** `*PackError` (distinct from the missing-object error
path, which stays for genuine corruption).

### Server-shallow-repo capability (B) — the backend boundary + provisioning seam

A shallow **server** repo needs three things the MVP backend lacks today:

1. **A recorded per-repo shallow boundary** — a set of commit OIDs treated as grafted roots (their
   parents are absent from the store). Stored like the existing `Registry` per-repo maps
   (`disabled`, `allowFilter`) or on the backend's repo record. It is read by the negotiation
   (unioned into the frontier), the connectivity check (added to the root set), and the shallow-info
   framing (reported on a full clone of a shallow server).

2. **Genuinely-absent deep history** — the objects for commits beyond the boundary are simply never
   stored. `Assemble` and the bounded-ancestry walk stop at the boundary, so they never look for them.

3. **A provisioning seam** — because there is no wire path to make an *origin* server shallow (git
   makes one via `git clone --depth`, which copies a `.git/shallow` file), the conformance suite needs
   an out-of-band way to create one. A new optional `Server` capability, sibling to `RepoConfigurer`:

   ```
   // conformance seam (skip-never-fail; both adapters implement it, so no skip in practice)
   MakeRepoShallow(repo string, roots []OID)
   ```

   The harness seeds full history via push, then calls `MakeRepoShallow(repo, roots)` to record the
   boundary and drop objects unreachable within it. The **reference** adapter maps it to a real
   shallow clone (or writing `.git/shallow` + repack); the **mvp** records the boundary set in the
   backend and prunes below it. Wired through a `conformance.MakeRepoShallow(t, srv, repo, roots)`
   helper (type-asserts the capability, like `SetRepoConfig`).

   *Contract, not signature:* the exact backend method name/shape and the pruning mechanics are the
   implementor's; the constraint is that after the call the repo behaves as a shallow repo at `roots`
   — fetches cap there, pushes onto the boundary tip succeed, and a clone-back yields the shallow slice.

### No config plumbing (contrast with ENG-120)

Because `shallow` is advertised unconditionally, there is **no** `uploadpack.allowShallow` config key,
no `Registry` policy field, no `Daemon.SetConfig` dispatch case, and no `RepoConfigurer` key for
enabling shallow. The only new per-repo state is the **shallow boundary** (capability B), set via the
`MakeRepoShallow` admin seam, not via git-config. This is a deliberately smaller surface than ENG-120's
filter-config plumbing.

### Invariant Preservation

| Invariant | Operations that touch it | Why it holds |
|---|---|---|
| Bounded-closure identity (1) | bounded-ancestry primitive → `AssembleRequest.Shallow` → `Assemble` walk | the frontier is a stop-set; the walk includes a frontier commit + its tree but not its parents; haves-exclusion (first) and filter mask (independent) are unchanged — contract-enforced, needs the `Assemble`-with-frontier seam test |
| `shallow-info` accuracy (2) | bounded-ancestry primitive; `shallow-info` framing | frontier and unshallow come from one walk comparing the new frontier to the client-declared shallow set — application logic, needs the primitive seam test + N-01/N-05 interop |
| Stateless between requests (3) | `Negotiator` per request | no shallow state threaded across requests; the primitive is pure over one request's inputs — structural (proven by N-05 deepen working across separate POSTs) |
| Server-shallow honesty (4) | server boundary → frontier union; connectivity check root set | the boundary is a stop-set for `Assemble` and a root for the connectivity check; the walk never reaches an absent object — contract-enforced, needs N-06 + a boundary seam test |

### Illegal State Analysis

- **A frontier commit whose parent is in the pack** — an internally inconsistent shallow-info/pack
  pair the client would graft wrongly. Not type-preventable; enforced by computing the frontier and
  the pack bound from the **same** stop-set (one source of truth), verified by N-01/N-05 interop
  (`git fsck` on the shallow clone) and the seam tests.
- **`Assemble` walking into absent server history** — prevented by unioning the server boundary into
  the stop-set *before* the walk; a boundary commit's parents are never enqueued, so absence is never
  observed (no spurious `*PackError`).
- **Advertised-but-unhonored / honored-but-unadvertised shallow** — cannot drift: `shallow` is
  unconditionally advertised and unconditionally handled; there is no gate to fall out of sync.
- **Malformed deepen argument** — never produces a frontier; it fails validation in the brain and
  becomes an in-band `ERR`, so the backend only ever sees a well-formed stop-set.

## Security

- **DoS surface:** a shallow/deepen argument set is small and bounded; parsing is O(lines). The
  bounded-ancestry walk and `Assemble` are O(commits within the bound) ≤ O(history) — the **same**
  order as the already-uncapped full-clone walk, so shallow introduces no new unbounded surface (a
  pathological huge `--depth` degenerates to a full clone). The D24 depth/`deepen-since` cap is
  deferred (see *Limitations*); when a fairness/tenancy requirement lands, the cap belongs beside the
  existing full-clone assembly bound, not as a shallow-only special case.
- `deepen-not <ref>` resolution and `deepen-since <ts>` parsing must reject garbage/overflow with an
  in-band `ERR`, never a panic.
- No new authn/authz: shallow availability is unconditional and orthogonal to the WireGuard trust
  boundary (ADR-12) and reachability permissions. A shallow fetch sees a **subset** of what an
  unbounded fetch of the same wants/haves would — never more.

## PII

None. The feature transports git objects the client is already authorized to fetch (a bounded subset)
and lets the server hold a bounded repo. No new data is logged or stored; the recorded shallow
boundary is a set of commit OIDs already public within the repo.

## Scale

- The in-memory bounded walk is O(commits within the bound), cheaper than the unbounded walk it
  specializes. `Assemble` with a frontier enumerates fewer objects than a full clone.
- For the future scale backend, the bounded-ancestry primitive is where **generation numbers** pay
  off (D11/D17): the walk prunes by generation without touching every ancestor, and the frontier
  bounds the Tier-2 reachability set-ops. This is exactly why D24 placed the frontier computation in
  `GraphIndex` (Tier-1) and fed it downward into pack selection — recorded here as the payoff, not
  built. A shallow boundary "disables/limits bitmap acceleration" (D24) at the boundary edge; the
  contract absorbs that behind the seam.

## Testing

Testing follows the `surface-testing` skill and ADR-0009: cases exercise the running server through
its public surface — the git wire protocol via real `git` (interop, primary) and raw HTTP for the
`shallow-info` framing (N-02) — never internal functions. **The surface is the conformance `Adapter`**,
unchanged; no new harness.

**The surface-testability risk, named.** As in slice 2, some behavior is invisible to a naive interop
pass: a bug that assembles the full closure and lets `git` discard the deep history client-side would
still produce a correct-looking shallow clone while leaking bytes over the wire. So the load-bearing
proof is split between interop (does the client end up with the right history?) and **seam-contract
tests** (does the server bound *enumeration*, not just framing?):

Key surfaces and substitutes:

- *[integration / interop]* the upload-pack and receive-pack endpoints driven by real `git clone
  --depth/--shallow-since/--shallow-exclude`, `git fetch --deepen`, and `git push` from/to shallow
  repos, against both adapters via `ForEachAdapter`; a new shallow case group under `conformance/`
  (mirroring t5539 for fetch, t5542 for push), one subtest per N-01…N-07, each `t.Run(adapter.Name())`.
  Asserts the resulting clone's commit set + shallow marks (`git fsck`, `git rev-parse --is-shallow-repository`)
  and, for N-06/N-07, clone-back + `fsck` of the server state.
- *[raw wire]* N-02: raw HTTP + hand-crafted pkt-line asserting the `shallow-info` section position and
  that no shallow lines follow "ACK … ready".
- *[provisioning seam]* N-02/N-06 (and any fetch-from-shallow-server case) use
  `conformance.MakeRepoShallow(t, srv, repo, roots)` — probe-and-skip-never-fail; both adapters
  implement it (reference via real shallow clone, mvp via the recorded boundary), so no skip.
- *[seam-contract tests]* backend-agnostic tests against `internal/storage`, reused unchanged when the
  local backend lands (the ENG-112 parity model):
  1. **Bounded-ancestry primitive** — for given roots + bound (depth / committer-time) + `deepen-not`
     exclusion + client-declared shallow set + relative flag, the returned frontier and unshallow sets
     are correct; the unbounded case coincides with `Reachable`.
  2. **`Assemble` with a frontier** — given a `Shallow` stop-set, the assembled pack is exactly the
     bounded closure (frontier commits + their trees/blobs, no withheld parents), and composes with
     `Haves` and `Filter`.
- *[fakes needed]* none beyond the in-memory backend; the reference oracle (`git http-backend`) is the
  always-registered reference adapter.

**No injectable clock** — `deepen-since` reads committer timestamps from commit objects; fixtures set
`GIT_COMMITTER_DATE` deterministically (as t5539 does). **Regression golden:** the advertisement golden
(`testdata/mvp/advertisement.golden`) is updated once to include the `shallow` token (a reviewed
change); no other golden changes.

## Limitations & Future Work

- **No DoS depth/`deepen-since` cap.** Exact git-parity today; a pathological deep request costs the
  same as a full clone. When per-tenant fairness/quotas land, the cap (D24) belongs beside the
  full-clone assembly bound, not as a shallow-only special case.
- **In-memory bounded walk only.** Generation-number-pruned bounded ancestry and bitmap-bounded
  enumeration arrive with the local/scale backends behind the unchanged `GraphIndex` primitive and
  `Assemble` contract; only in-memory semantics are exercised now.
- **v0 shallow negotiation deferred.** The v2 path is built; v0 fetch (including its shallow
  negotiation and connection-lifetime state) is the separately-deferred phase-8 follow-up.
- **Shallow packs are undeltified** (slice-2 decision, unchanged); a shallow pack is whole-object.
- **`deepen-not` resolves refs, not arbitrary revs.** `--shallow-exclude` takes a ref or OID; extended
  rev syntax is out of scope (matches the conformance cases, which pass tag/ref names).

## Open Questions

- **Exact `shallow-info` position and framing against the v2 oracle.** The section's placement
  relative to `acknowledgments`/`wanted-refs`/`packfile` and the delim/flush framing must be confirmed
  byte-wise against `git upload-pack` with `GIT_TRACE_PACKET=1` during the build (the design fixes the
  order from the protocol spec; the build pins the exact bytes). Not a blocker — the oracle resolves
  it — but flagged so the implementor verifies rather than guesses.
- **`git fsck` expectations on a shallow clone against the mvp.** N-01/N-06 assert `git fsck` passes on
  the client's shallow clone; confirm the mvp-produced pack + `shallow-info` yields a clone `fsck`
  accepts identically to the reference (the shallow graft must be recorded such that missing parents
  are legitimate). Oracle-resolved during the build.
- **Whether N-02 is authored as v2-adapted or gated v0.** t5539 #3 forces protocol v0; decide at
  authoring time whether to assert the v2-equivalent shallow-info-once placement (preferred) or keep
  the v0 specifics gated/skipped for the v2 mvp. Not a design blocker.

---

## Decision Log

Ticket-local `SH-` prefix (ENG-120 used inline decisions, not a `BP-` table; the slice blueprints'
`BP-`/`D-` numbering is referenced, not extended, to avoid collision).

| # | Decision | Status |
|---|---|---|
| **SH-1** | **Shallow is an additive consumer, not a seam re-cut** — spend the headroom ENG-120/slice-2 left: a new `AssembleRequest.Shallow` field, a new `GraphIndex` bounded-ancestry method, one appended advertisement token. | decided |
| **SH-2** | **The D24 bounded-ancestry primitive lives on `GraphIndex` (commit-level, Tier-1)** and produces the frontier + unshallow set; `Reachable` is its unbounded degenerate (one walk family, the slice-2 RS-009 synthesis). Drawn concretely as a contract-with-headroom (draw-the-seam deliverable); exact Go signature is the implementor's. | decided |
| **SH-3** | **The frontier is fed to `Assemble` as a stop-set (`AssembleRequest.Shallow`), not a materialized commit set** — preserving ADR-0005 (enumeration stays below the seam; the frontier is a bound like `haves`). Composes independently with the ENG-120 `Filter`. | decided |
| **SH-4** | **`shallow` is advertised unconditionally** (git has no `allowShallow`), so no config plumbing — the only new per-repo state is the shallow boundary, set via an admin seam, not git-config. Contrast with ENG-120's `allowFilter` gate. | decided |
| **SH-5** | **`deepen-relative` is implemented now** (not just contract-drawn) — confirmed exercised by N-05 (t5539 #8: `git clone --depth 1` then `git fetch --deepen=1`). The primitive accepts the client's declared shallow set as the relative origin. | decided (user) |
| **SH-6** | **Full push-side shallow (N-06/N-07) is in scope**, which requires the server-shallow-repo capability (B): a per-repo recorded boundary, absent deep history, connectivity-check root-set inclusion, and a `MakeRepoShallow` provisioning seam. | decided (user) |
| **SH-7** | **A shallow server repo is provisioned on the mvp via a per-repo admin seam** (`MakeRepoShallow`, sibling to `RepoConfigurer`), not construction-time config or wire — symmetric with ENG-120's filter-config seam; reference maps it to a real shallow clone. | decided (user) |
| **SH-8** | **No DoS depth/`deepen-since` cap now** — exact git-parity; shallow adds no unbounded surface worse than the already-uncapped full clone. The D24 cap is deferred to future fairness/tenancy hardening. | decided (user) |
| **SH-9** | **`object.ParseCommit` gains committer-timestamp extraction** for `deepen-since` (it returns only tree+parents today); the cutoff compares committer (not author) date, per D24. | auto-resolved from D24 |

Inherited and load-bearing here: **D24** (shallow/partial split; stateless-per-request; the
bounded-ancestry primitive → SH-2/SH-3/SH-9), **D17** (`GraphIndex` is a query interface, bitmaps
backend-internal → SH-2), **D18 / ADR-0005** (enumeration below the seam → SH-3), **D25** (batched
connectivity check, no chatty per-object reads → push-side SH-6), **D11** (two-tier reachability;
generation numbers/bitmaps accelerate the bounded walk → *Scale*), **BP-9/BP-11/BP-16** (slice-2:
`GraphIndex` live via RS-009, stateless negotiation, the primitive drawn-not-exercised — now
exercised), ADR-**0006** (existence-based v2 want-validation, unchanged), and the ENG-120 seam
template (`AssembleRequest` extension, `fetchSubFeatures` advertisement assembly, the `RepoConfigurer`
provisioning-seam pattern).
