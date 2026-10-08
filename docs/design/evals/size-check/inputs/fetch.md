# Git-Server Slice 2 (Fetch) Implementation Blueprint

> **Status:** Design (ENG-117). The reviewed plan a follow-up build ticket (the ENG-111 analogue) implements against. No server code is written here.
> **Sources of truth this blueprint derives from:** [`technical-shape.md`](../../design/technical-shape.md) (decisions **D1–D27**, esp. D5/D9/D11/D17/D18/D19/D24/D25/D27, §3.7 GraphIndex/reachability, §5 protocol scope, §7 package map), the [slice 0+1 blueprint](../eng-112-git-server-slice-01-implementation-blueprint/blueprint.md) (BP-1…BP-8, the structural template), the conformance suite under [`services/git-server/conformance/`](../../../conformance/), [`testing-strategy.md`](../../testing-strategy.md), [`conformance-inventory.md`](../../conformance-inventory.md), and ADRs [0001](../../adr/0001-git-server-conformance-via-real-git-interop.md)–[0004](../../adr/0004-format-aware-object-id-value-type.md).
> **Relationship to slice 0+1:** slice 0 (repo substrate) + slice 1 (push) are landed on `main`. This blueprint designs **slice 2 (fetch)**, the next step in the push-first build order (D19). It builds the parts the ENG-112 blueprint *drew but did not exercise*: `internal/negotiate`, `PackEngine` assembly, and the `GraphIndex` reachability path.

> **⚠️ Correction (post-build, ENG-117).** **BP-13's want-policy "correction" was itself wrong and is retired.** BP-13 asserted that validating `want`s against advertised ref tips only "matches git's default `upload-pack`." That is true of git **protocol v0**, but this slice is **v2** — and git's v2 `upload-pack` (`parse_want`) accepts a `want` for **any object the server holds** (existence check only; the `allowReachableSHA1InWant`/`allowAnySHA1InWant` knobs gate only the v0 path). Verified empirically against the pinned oracle (`git 2.49.0`), including a reachable non-tip *and* a dangling unreachable object — both served over v2. Slice 1's `ObjectStore.HasMany` (any in-store object) therefore **already matched git v2**; ref-tips-only would have made the server *stricter* than git, contradicting the compatibility goal. **The build keeps the slice-1 `HasMany` want-validation.** Ref-tips-only is git's *v0* default and is folded into the deferred v0-fetch follow-up ticket (it becomes correct there). See [`ref-tips-only-explainer.html`](./ref-tips-only-explainer.html) and **ADR-0006**. Wherever this document says "ref tips only / `RefStore.List` / BP-13 want policy / Invariant 2 (want is a ref tip)," read it as superseded by this note. The *other* parse-policy correction — **BP-17, rejecting unrecognized fetch-argument lines — stands** (it matches git v2's `unexpected line: …`).

## Objective

Take the git-server from *"basic clone/fetch happens to work"* to *"fetch is a real, seam-honest negotiation path."* Slice 1 left behind an upload-pack handler in `internal/protocol/fetch.go` that **already implements the full v2 wire state machine** — stateless `want`/`have`/`done` parsing, ACK/NAK lines, the `ready`→`0001`-DELIM / not-ready→`0000`-flush framing, and side-band-64k packfile output. What it does *not* do is honor the storage seam: it walks `ObjectStore` directly (`objectsToSend` + inline `pack.Write`), so `GraphIndex` is dead code, `PackEngine` has no assembly method, and `internal/negotiate` does not exist. It also accepts **any in-store object** as a `want` (an `ObjectStore.HasMany` check), which diverges from git's default ref-tips-only policy (BP-13) — a divergence the existing conformance cases do not catch (`E-04` uses a non-existent SHA, not an in-store non-tip).

So this slice is **not** about building negotiation wire behavior (that exists and passes interop). It is three structural moves plus one policy correction, fixed here **on paper and at contract altitude** so the build does not re-decide them mid-flight:

1. **The negotiation engine** — extracting the *existing* v2 stateless have/want/ACK/NAK/ready/done wire logic out of `internal/protocol/fetch.go` into `internal/negotiate` (the `Negotiator`), with `internal/protocol` reduced to a thin upload-pack handler. The wire behavior is preserved as-is. *(The original plan also "corrected" want-validation to advertised-ref-tips-only — **that correction was itself wrong and is retired**; want-validation keeps slice-1's "any in-store object" `ObjectStore.HasMany`, which is what git v2 does. See the correction note at top. The surviving parse-policy fix is **BP-17**: rejecting unrecognized fetch-argument lines, which git v2 also does.)*
2. **The seam evolution** — `PackEngine` grows an **`Assemble`** responsibility (object enumeration + byte streaming below the seam, D18), and `GraphIndex.Reachable` goes **live** (consumed for real, closing the RS-009 duplication).
3. **The scope line** — exactly which fetch conformance cases gate this slice, and which deferred work (shallow, partial-clone `filter`, v0 fetch fallback, the streaming-OFS resolver) is pushed to named follow-up tickets that **must** be created.

**Altitude.** Same exception as ENG-112: the storage seam is an internal surface that this ticket's explicit deliverable is to *draw* so the build does not redraw it. The seam's method **shapes** are drawn — but as **contracts with headroom** (conceptual inputs/outputs, open error categories, internals described not frozen), never as frozen Go. Everything above the seam (the `Negotiator`, the handler) is described as responsibilities and collaborations. The exact Go signatures are the implementor's.

## Mental Model

git-server is a permanent **brain** over a swappable **backend**, divided by the **seam** (`internal/storage`). Slice 1 established this for push. Slice 2 extends it to fetch, and in doing so makes the seam honest on the read path for the first time.

**The fetch path, end to end (slice 2 target):**

1. **Transport** hands the upload-pack request body to the protocol engine as a byte stream (D16, unchanged from slice 1).
2. **`internal/protocol`** parses the v2 command. `ls-refs` is handled as today; `fetch` is delegated to the **`Negotiator`**.
3. **`internal/negotiate` (the `Negotiator`)** runs one stateless negotiation round (D24): parse `want`/`have`/`done`, validate wants against the repo's advertised ref tips (`RefStore`), ACK the haves the server holds (`ObjectStore.HasMany`), and decide the response shape — more rounds (`NAK`/flush), or `ready` + pack. The negotiation carries **no server-side session**; each HTTP POST re-derives its state from the client's re-sent haves.
4. **`PackEngine.Assemble(wants, haves)` (backend, below the seam)** enumerates the object closure reachable from `wants` but not from `haves` and streams it as a side-band-64k packfile. *How* the closure is enumerated (in-memory naive walk; scale-backend Tier-2 bitmaps) is the backend's concern, invisible to the brain.

**What changes vs. slice 1.** The wire state machine (steps 1–3 above) already exists in `fetch.go` and is preserved through the refactor — it moves into `internal/negotiate` unchanged except for the want-policy correction (step 3, ref-tips-only). The substantive change is step 4: the object-enumeration walk and pack writing move **out of the brain** (`protocol/fetch.go`'s `objectsToSend` + inline `pack.Write`) and **below the seam** (into `PackEngine.Assemble`). The brain keeps *what* (the negotiated wants/haves frontier, the pack framing); the backend owns *how* (object enumeration, delta selection later, byte streaming) — this is D18 applied to fetch.

**Why now, and why this way (D5).** A seam validated only against push is a seam half-trusted. Fetch is where object-level reachability and pack assembly first run through the seam; designing `Assemble` against the decomposed model's constraints now — a single below-seam enumeration the bitmap backend can accelerate, not an O(objects) brain-driven loop — is what keeps the seam honest and prevents a scale-backend rewrite.

### Package skeleton (the §7 map, scoped to this slice)

| Package | Primary type / role | Side of seam | In this slice? |
|---|---|---|---|
| `internal/transport` | `HTTPTransport` — HTTP endpoints + routing guards | brain | unchanged from slice 1 |
| `internal/protocol` | upload-pack handler (ls-refs + delegate fetch), pkt-line, side-band framing | brain | **modified** — fetch logic extracted to `negotiate` |
| `internal/negotiate` | `Negotiator` — the v2 fetch negotiation state machine | brain | **new — the slice's centerpiece** |
| `internal/pack` | `Ingester` (slice 1) + the whole-object pack writer the backend's `Assemble` delegates to | seam-level shared algorithm | writer reused below the seam |
| `internal/writepath` | `Committer` — gains a `GraphIndex` dependency (RS-009) | brain | **modified** — `fastForward` routes through `GraphIndex` |
| `internal/storage` | the seam: `RefStore`, `ObjectStore`, `GraphIndex` (+`Assemble` on `PackEngine`) | **seam** | **modified** — `PackEngine.Assemble` added; `GraphIndex` goes live |
| `internal/storage/memory` | `Backend` — implements `Assemble` (naive closure walk) | backend | **modified** |
| `cmd/git-server` | thin `main` | wiring | unchanged |

Packages not listed (`graphapi`, `mutate`, `auth` beyond config-driven protected-ref) belong to later slices (D19).

## Scope

This blueprint covers **slice 2 (fetch)** of the push-first build order (D19). It is bundled as its own blueprint (not ridden along with another slice) because fetch is substantial and seam-touching — it first exercises `internal/negotiate`, `PackEngine` assembly, and `GraphIndex` (D27).

### In Scope

- **Real v2 fetch negotiation** — the `Negotiator`: `want`/`have`/`done` parsing, want-validation against ref tips, ACK/NAK of haves, the `ready` decision, and the stateless-between-requests model (D24). Hardens and restructures slice 1's minimal handler.
- **`PackEngine.Assemble`** — the new seam method: given the negotiated `wants`/`haves` frontier, enumerate the object closure below the seam and stream a side-band-64k packfile (D18). The in-memory backend implements it as a naive commit→tree→blob walk (reusing the shared `object.References` switch, RS-007, and the slice-1 `pack` writer).
- **`GraphIndex.Reachable` goes live** via the **RS-009** unification: `Committer.fastForward` routes its commit-ancestry question through `GraphIndex.Reachable`, removing the duplicated walk. The `Committer` gains a `GraphIndex` dependency.
- **The D24 bounded-ancestry-walk primitive — drawn at contract altitude, not exercised.** The shallow-frontier primitive's contract is fixed in `GraphIndex` so the seam does not need re-cutting when shallow lands, but no shallow wire handling is built.
- **New acceptance coverage that is red against slice 1's code** (the proof the slice did real work, since the v2 wire cases already pass — see *Acceptance Criteria*): a backend-agnostic `PackEngine.Assemble` seam-contract test, an RS-009 routing test (`fastForward` through `GraphIndex.Reachable`), a rewritten `E-04` that wants an in-store non-tip object (BP-13), a new `V2-16` case rejecting an unrecognized fetch-argument line (BP-17), and a new `F-21` ManyHavesNegotiation regression guard for the restructured batched have/ACK path. The existing wire-framing/robustness cases (`V2-27`/`V2-19`/`E-03`/`E-06`/`V2-20`/`V2-21`) are kept green as regression guards through the refactor.

### Out of Scope / Non-Goals (deferred — follow-up tickets **required**, see *Deferred Work*)

- **Shallow clone/fetch** (`--depth`, `--shallow-since`, `--shallow-exclude`, `deepen*`/`unshallow`; cases `N-01`…`N-07`) — the wire exchange and the *live* bounded-walk frontier. Only the primitive's **contract** is drawn here. → phase-5 follow-up ticket.
- **Partial clone / `filter`** (`blob:none`, `blob:limit`, `tree:0`; `N-08`…`N-17`) — the object type/size masking. → same or sibling phase-5 follow-up ticket. (D9 holds partial clone is core for the MVP completion target — this is a sequencing decision, not a descope of the MVP.)
- **Protocol v0 fetch / v2→v0 fallback** (`F-05`, `F-14`, `F-28`; phase 8) — slice 2 is v2-focused. The existing v0 empty-clone (`F-15`) must not regress, but no new v0 negotiation is built. → phase-8 follow-up ticket.
- **The streaming-OFS resolver** — a true streaming OFS-delta resolver for packs above the `receive.maxInputSize` cap (RS-008); `pack.Ingest` still `io.ReadAll`s its bounded input. This is a push-side perf item with no fetch conformance case. → follow-up ticket.
- **Want-acceptance beyond ref tips** — `allowReachableSHA1InWant` / `allowAnySHA1InWant` (`F-22`/`F-23`/`N-18`/`N-19`; phase 7). MVP accepts only advertised ref tips.
- **Delta-compressed fetch packs** — the `Assemble` writer emits whole (undeltified) objects, as slice 1 does; delta selection is a below-seam optimization for a later backend, not a slice-2 correctness requirement.
- **Tier-1 commit-graph *record* persistence** — generation numbers and a persisted commit-graph are a backend-internal acceleration for the local/scale backends; the in-memory backend walks objects on demand and persists nothing (see *Tier-1 write-timing*, resolving the ENG-112 open question).

## Correctness Constraints

### State Invariants

1. **Fetch reachability isolation (D8).** A fetch against repo B never enumerates or transfers an object belonging to repo A. *Violation:* a `want` resolves to an object only present in another repo (the "ghost object" leak). *Enforcement:* `Assemble` and want-validation operate within the `Registry`-scoped stores of the target repo; at MVP objects are per-repo with no cross-repo sharing — structural. Surface-guarded by `TestInvariant_PerRepoIsolation`.
2. **Want must resolve to an object the server holds (git-v2 parity).** ~~(Originally stated as "want must be an advertised ref tip, BP-13" — retired; see the correction note at top.)~~ A `fetch` whose `want` is an OID the server does not hold is rejected with a clean error (`ERR ... not our ref`), never hung on; a `want` for any object the server **does** hold — tip, reachable non-tip, or otherwise — is served, exactly as git's v2 `upload-pack` does. *Violation:* hanging on, or 5xx-ing, a `want` for an unheld OID; or refusing a `want` for a held object git would serve (a compatibility regression). *Enforcement:* the `Negotiator` validates each `want` with one batched `ObjectStore.HasMany` (existence) before assembly. Surface coverage: `E-04`/`E-03` (unheld want → clean `ERR`), `F-24` (unknown SHA), and `F-21` (held ancestors negotiated). The advertised-ref-tips restriction is git's **v0** policy, deferred to the v0-fetch follow-up ticket.
3. **Pack completeness for the client's view.** The packfile `Assemble` produces is self-contained given the client's `haves`: every object reachable from `wants` and not reachable from `haves` is present, and no delta references a base the client lacks. *Violation:* omitting a reachable tree/blob, producing a pack the client cannot index. *Enforcement:* `Assemble` enumerates the full commit→tree→blob closure (shared `object.References`) and emits whole objects (no thin-to-client deltas at MVP) — **contract-enforced**, needs a seam-contract test.

### Behavioral Constraints

- **Negotiation always terminates (no hang).** The handler must never block waiting for input that will not come: EOF after `want`s with no `done` is tolerated and answered, not hung on (`E-06`); a single POST runs one bounded round. This rules out any design that blocks on a second client message within one stateless request.
- **Stateless between requests (D24).** The negotiation holds **no server-side session**. Each HTTP POST re-derives its state from the client's re-sent `have` set. This rules out a server-side per-client negotiation cache and is what keeps the brain consistent with the stateless scale deployment (D14). (A single v0 connection may buffer state for its duration — out of scope this slice.)
- **No chatty per-object storage on the read path (D25, §3.2).** Object enumeration for the pack is a single below-seam operation (`Assemble`), not a brain-driven `for each oid: ObjectStore.Get` loop. The ACK probe is one batched `ObjectStore.HasMany`. This rules out the per-object shape that is free in-memory and ruinous on FDB+Scylla, and is the reason enumeration lives below the seam (Reading B, BP-10).
- **Domain-boundary opacity.** The `Negotiator` switches only on seam-defined contracts and error categories — never on which backend is active, never on how `Assemble` enumerates (naive walk vs. bitmaps), never on a backend-specific error type. `GraphIndex.Reachable` returns the same answer regardless of backend acceleration.

<!-- Architectural constraint (no user-facing story): stateless-between-requests; no chatty per-object reads; enumeration below the seam; opacity — verified by code structure and seam-contract tests, not interop. -->

## Acceptance Criteria

Slice 2 is done when the Go server, registered as the `mvp` conformance adapter, passes the following **and** they remain green against the reference oracle (ADR-0001), all driven through the public surface — real `git` interop plus raw HTTP for wire/framing assertions (ADR-0009).

**Kept green through the refactor** (already passing in slice 1; must not regress as fetch moves into `negotiate` + `Assemble`):

| Case | Asserts |
|---|---|
| `F-01` CloneBareRepo | full `git clone` over smart HTTP v2 succeeds; HEAD + files correct |
| `F-02` IncrementalFetch | push a second commit, `git fetch`, `origin/main` advances |
| `F-15` CloneEmptyRepo | clone an empty repo (v0 and v2) succeeds with no commits (the v0 path must not regress) |
| `F-20` ChunkedFetchPackRequest | a fetch request via chunked transfer-encoding yields the packfile |
| `F-24` WantUnreachableShaRejected | a `want` for an unknown OID is rejected, client sees a clean error |

**Also kept green — wire-framing regression guards** (these already pass against `mvp` today because slice 1's handler already implements the full v2 wire state machine; they are regression guards for the `negotiate` extraction, **not** proof the slice did new work):

| Case | Asserts |
|---|---|
| `V2-27` ReadyFraming | when the server sends `ready`, a packfile section follows after a `0001` DELIM; when it is not ready and the client is not `done`, the acknowledgments section ends with a `0000` flush and **no** packfile follows |
| `V2-19` FetchOverHttpV2 | clone/fetch driven explicitly over protocol v2 (distinct, asserted v2 negotiation, not only the implicit `F-01` path) |
| `E-03` BadWantMissingObject | a `want` for a non-existent object → clean error, no hang |
| `E-06` TolerateEofAfterWants | EOF after `want`s with no `done` → no hang, no 5xx |
| `V2-20`/`V2-21` Robustness | incomplete pkt-line / invalid parameters handled without hang or crash (the negotiation-request parse surface the extraction touches) |

**New — the must-fail-today gates that prove the three deliverables.** Because the wire behavior above already passes, the proof the slice did real work rests on tests that are **red against slice 1's code and green only after the deliverables land**:

| Gate | Asserts | Red today because |
|---|---|---|
| `Assemble` seam-contract test | backend-agnostic test against `internal/storage`: for given `wants`/`haves`, the assembled pack is the complete commit→tree→blob closure minus the `haves` closure; whole objects only; a missing/corrupt object surfaces the recognizable error category (this last is the seam-level form of `E-05` pack-enumeration-error — the interop `E-05`/`E-01`/`F-12` fault cases skip for `mvp`, so the seam test is where enumeration errors are asserted) | `PackEngine` has no `Assemble` method (only `Ingest`); enumeration lives in the brain |
| RS-009 routing test | `Committer.fastForward` answers its ancestry question through `GraphIndex.Reachable` (direct seam-level reachability test); `P-06` non-fast-forward + push-atomicity stay green | `Committer` has no `GraphIndex` dependency; `fastForward` runs its own duplicated BFS walk |
| ~~`E-04` BadWantNotATip (rewritten)~~ → **`E-04` BadWantNotATip (unchanged)** | a `want` for an OID the server **does not hold** → clean `ERR ... not our ref` (git-v2 parity; an in-store non-tip is *served*, as git v2 does — the rewrite is retired with BP-13, see correction note) | this is not a must-fail gate — it passes today and stays green dual-adapter; the slice's must-fail gates are the `Assemble` seam test, the RS-009 routing test, and `V2-16` |
| `V2-16` UnexpectedFetchLine (new case) | an **unrecognized argument line** in a v2 `fetch` request → rejected (no packfile), matching the reference (BP-17) | verified: `mvp` silently ignores the line and serves a pack (HTTP 200, packfile), where `git` upload-pack rejects it with "unexpected line: …" |

> **Note (tests to author):** `F-21` ManyHavesNegotiation and `V2-16` UnexpectedFetchLine **do not exist yet** — both are **authored as part of this slice** (per the conformance README they were deferred-P2 hardening cases). `F-21` is a new regression guard for the restructured batched have/ACK path (D25) — it has no must-fail role, but it must pass once written. `V2-16` is the new red-today gate for BP-17; its redness was confirmed by **probing** the `mvp` and reference adapters during review (no test exists today — the divergence was observed behaviorally, not by running a case).

> Note: the existing "negotiation ACK" behavior (an incremental fetch ACKing a common commit) is already covered by `V2-27`'s ready path and `F-02`; it is a regression guard, not a new gate.

> Shallow/partial (`N-*`), v0 fetch fallback (`F-05`/`F-14`/`F-28`), and want-by-reachable-SHA (`F-22`/`F-23`) are **not** gated here — they are the deferred follow-up tickets below. They continue to run green against the reference and skip for the `mvp` adapter via the capability probe (`fetch` advertises no `shallow`/`filter` sub-caps).

**Adapter integration:** unchanged — the Go server remains registered alongside `ReferenceAdapter`, `Caps{Auth:false}`, `MODE=embedded`, no external dependencies.

## Success Metrics

None for this slice — it is protocol-engine infrastructure with no post-ship user-facing outcome to measure. (The §3.6 observability surface — per-fetch object-count, pack-bytes, negotiation rounds — is a candidate to seed when the local backend lands and clone latency becomes a parity gate; it is not an acceptance gate here.)

---

## Architecture

### The protocol / negotiate split (D16, D22)

`internal/protocol` keeps the v2 command dispatch (`ls-refs` | `fetch`) and the pkt-line / side-band framing primitives. The `fetch` branch becomes a **thin delegation** to `internal/negotiate`. Negotiation earns its own package because it spans more than one type and is the first read-path brain concept with real internal structure (§7 package-granularity rule). The dependency direction is `protocol → negotiate → storage` (seam); `negotiate` never imports a concrete backend.

*`Negotiator` responsibility (contract, not signature):* given one upload-pack `fetch` request's argument lines and the repo's stores, run **one stateless negotiation round** and produce the response body. It:
- parses `want`/`have`/`done` and the *recognized* fetch arguments (`thin-pack`, `no-progress`, `ofs-delta`, `include-tag`, … — accepted, no effect on a whole-object pack this slice). An **unrecognized** argument line is **rejected, not silently ignored** — matching git's upload-pack ("unexpected line: …"), per BP-17 and `V2-16`. (Slice 1's handler ignores all unknown lines via a permissive `default` branch; this is the second parse-policy correction alongside the BP-13 want fix.)
- validates each `want` against the advertised ref tips (`RefStore.List`) — rejecting non-tips per BP-13. *Edge:* peeled tag advertisements (`refs/tags/x^{}`) are **not** themselves valid want tips (`N-20`) — validate against ref OIDs, not their peeled values;
- determines the common haves via one batched `ObjectStore.HasMany`, emitting `ACK <oid>` lines;
- decides the response shape per the v2 framing rule (below): keep negotiating (`NAK`/flush, no pack) or terminate (`ready`/`done` → invoke `PackEngine.Assemble` and frame the packfile section).

The `Negotiator` holds the **wire state machine and the policy** (want validation, ACK/ready decision, framing); it does **not** enumerate objects or write pack bytes — that is `Assemble`'s job below the seam.

#### The v2 framing rule (V2-27 — a byte-exact, protocol-mandated assertion, ADR-0002)

The acknowledgments section lists `ACK <oid>` for recognized common haves (or `NAK`). Two terminal shapes, and the framing distinguishes them:

- **Ready** (server has enough common history but client did *not* send `done`): the acknowledgments section is followed by a `0001` **DELIM**, then the `packfile` section (side-band-64k).
- **Not ready** (more rounds needed): the acknowledgments section ends with a `0000` **flush** and **no** packfile follows; the client will POST again with more haves.
- **Done** (client sent `done`): slice 1 emits **no** `acknowledgments` section at all — it writes the `packfile` section directly. This is what `F-01`/`F-20` (full clone, which always sends `done`) exercise and it interoperates with real `git`. The `Negotiator` extraction **must preserve this done-path shape exactly**; it is not a behavior to "fix" under this slice, and `V2-27` deliberately tests only the non-done paths.

This is protocol-mandated exactness (ADR-0002); `V2-27` is the regression guard that the ready/not-ready framing survives the extraction (it already passes — see Acceptance Criteria).

### The seam evolution

#### `PackEngine` grows `Assemble` (BP-10, Reading B — D18 reconciled with D11/§3.7)

*Responsibility (new):* given the negotiated **`wants` and `haves` frontier**, enumerate the object closure reachable from `wants` but not from `haves`, and stream it as a packfile.

*What flows across the seam:* **in** — the `wants` set, the `haves` set (the negotiated frontier, **not** a pre-materialized flat OID list), and the **destination writer** (`io.Writer`-shaped) the pack is emitted into. **out** — an error only; the packfile bytes flow out through the writer, not a returned byte slice. The writer-sink shape (not a `([]byte, error)` return) is the contract-with-headroom that keeps assembly *streaming* — it lets a scale backend emit side-band-64k frames without first buffering the whole pack in memory. A recognizable error category surfaces on a malformed/missing object.

*Why the frontier and not a materialized OID set (the D18-vs-D11 reconciliation):* D18 says "object set above the seam, bytes below," but D11/§3.7 place **object-level reachability in Tier-2 bitmaps, below the seam** ("the full object set reachable from wants minus haves — bitmaps turn it into set-ops," backend-internal). Object enumeration is *exactly* what bitmaps accelerate, so it must live below the seam to keep the seam honest (D5); materializing the closure above the seam would force an O(objects) `ObjectStore` loop in the brain that no bitmap backend could replace — a guaranteed scale-backend re-cut. So "object set above the seam" (D18) is read as **the brain owns the *negotiated logical set* (wants minus haves — the protocol outcome) and the pack framing**; the backend owns **materializing and serializing** it. The in-memory backend enumerates by naive walk (commit→tree→blob via the shared `object.References`, then excludes everything reachable from `haves`); a future backend swaps in Tier-2 bitmap set-ops behind the identical contract.

*Invariants:* pack completeness (Invariant 3) — the closure is complete for the client's `haves`; whole objects only (no thin-to-client deltas at MVP). Enumeration is **one below-seam operation**, never a brain-driven per-object loop (Behavioral Constraint).

*Error contract:* a missing/corrupt object encountered during enumeration surfaces as a recognizable seam category that the handler renders to the client cleanly (no hang) — the `E-01`/`E-03`/`F-12` "server-side error surfaced, not hung" contract. Reuses the slice-1 open/extensible error vocabulary (BP-4); no new category is forced unless enumeration surfaces one this design did not foresee.

#### `GraphIndex` goes live, stays commit-level (BP-11)

*Status change:* slice 1 declared `GraphIndex.Reachable(tips, target)` but **nothing consumed it** (BP-8). Slice 2 makes it **live** via RS-009 (below). It stays a **commit-level Tier-1 query interface** (D17): it answers commit reachability/ancestry; it does **not** enumerate trees/blobs (that is `Assemble`'s object-level closure, below the seam). The in-memory backend answers by naive commit-parent walk — the stand-in for a scale backend's generation-number-pruned walk.

*The D24 bounded-ancestry-walk primitive — drawn, not exercised.* `GraphIndex`'s contract is extended to describe a **general bounded-ancestry-walk primitive**: ancestors-from-roots with a stop-set and an optional depth / committer-timestamp bound and exclusion set (D24). Negotiation is the unbounded degenerate case (roots = `wants`, stop-set = `haves`, no bound); the shallow frontier (deferred) is the bounded case. Drawing the contract now means the shallow follow-up adds a *consumer*, not a seam re-cut. **No shallow code is written this slice**; the primitive's shape is fixed so it does not have to be guessed later.

> *Consequence worth stating plainly:* because object enumeration lives in `Assemble` (Reading B) and wants are ref-tips-only (BP-13), the **live consumer of the `GraphIndex.Reachable` seam method this slice is RS-009 (`fastForward`)**, not the negotiation closure (which is the backend's internal walk inside `Assemble`). This is the honest synthesis of the two scope decisions: the *accelerable wants−haves reachability* runs below the seam in `Assemble` (where bitmaps will accelerate it), and the *commit-ancestry boolean* runs through `GraphIndex.Reachable` (where `fastForward` consumes it). Both are the same backend walk family; both are now exercised.

#### RS-009 — unify the duplicated ancestry walks (BP-11)

Slice 1 left two structurally identical commit-ancestry BFS walks: `writepath.Committer.fastForward` (push fast-forward check, via `ObjectStore.Get`) and `memory.graphIndex.Reachable` (via the backend's internal object map). The breadcrumb on `storage/memory/memory.go` deferred unification to "once fetch negotiation makes `GraphIndex` a live consumer."

*Resolution:* route `fastForward`'s question through `GraphIndex.Reachable` — "is `old` reachable from `new`" is `Reachable(tips:[new], target:old)`. The `Committer` gains a `GraphIndex` dependency (a brain→seam dependency). *Injection point (so the build does not re-decide it):* today `internal/daemon.go` wires `NewCommitter(storage.SHA1, NoopEmitter{})`; the per-repo `GraphIndex` comes from `Backend.Graph(repo)`. Because a repo is known only per request, add the per-repo `GraphIndex` (`Backend.Graph(repo)`) as a parameter to the `Commit` call rather than freezing it at construction — matching how `Refs(repo)`/`Objects(repo)` are already passed per request. (`NewCommitter` stays construction-time for `format`/`emit`; the engine call site passes `Backend.Graph(repo)` alongside the `refs`/`objs` it already obtains — not a per-request `Committer` reconstruction.) The duplicated walk in `fastForward` is removed; `GraphIndex.Reachable` becomes the single commit-ancestry definition for both the push ff-check and any future negotiation consumer.

*Correctness note (visibility):* the push ff-check runs *after* ingest and *before* the ref-CAS. In the in-memory backend, `Ingest` stores objects into the same map `Reachable` walks, so the just-pushed commits are visible to `Reachable` before the CAS — preserving slice-1 behavior exactly (slice 1's `fastForward` read the same just-ingested objects via `ObjectStore.Get`). This is consistent with the unreferenced-object-safety invariant (objects exist before the ref points at them). No push conformance case changes; `P-06` (non-fast-forward) and the push-atomicity tests must stay green as the proof.

### Tier-1 commit-graph write-timing — the ENG-112 open question, resolved (BP-14)

ENG-112 left open whether slice 1 persists Tier-1 commit-graph records "in the push txn" (per D17/§3.7) or defers it to slice 2. **Resolution:** for the **in-memory backend**, there is *nothing to persist* — `GraphIndex.Reachable` and `Assemble` walk the stored objects directly, parsing commit parents on demand. A persisted commit-graph (generation numbers, a packed record table) is a **backend-internal acceleration** (D17, like the Tier-2 bitmaps) that belongs to the **local/scale backends**, where walking objects per query is too slow. It is therefore deferred to those backend slices, not built here. This closes the open question without a seam change: the `GraphIndex` *query* contract is identical whether the backend walks objects (in-memory) or reads a commit-graph table (local/scale).

### v0 fetch and the streaming-OFS resolver — deferred (BP-15)

Both are explicitly out of scope (see *Deferred Work*). v0 fetch fallback is a phase-8 concern; the existing v0 empty-clone (`F-15`) must stay green but no new v0 negotiation is built. The streaming-OFS resolver (unbounded packs above the `receive.maxInputSize` cap) is a push-side perf item orthogonal to fetch — `pack.Ingest` keeps its bounded `io.ReadAll`. Designing unbounded packs while shaping the fetch negotiation would balloon the slice and couple two unrelated changes.

## Data Design

### Invariant Preservation

| Invariant | Operations that touch it | Why it holds in this slice |
|---|---|---|
| Fetch reachability isolation (1) | want-validation, `Assemble` | stores are `Registry`-scoped per repo; no cross-repo objects at MVP — structural (guarded by `TestInvariant_PerRepoIsolation`) |
| Want is a ref tip (2) | `Negotiator` want-validation | each `want` checked against `RefStore.List` before assembly (replacing slice-1's `ObjectStore.HasMany` check, which accepted any in-store object); non-tip → clean `ERR` — application logic (needs rewritten `E-04`/`F-24`) |
| Pack completeness (3) | `PackEngine.Assemble` | full commit→tree→blob closure via shared `object.References`, minus the `haves` closure; whole objects only — contract-enforced (needs seam-contract test) |
| Negotiation terminates (behavioral) | `Negotiator` round | one bounded stateless round; EOF-after-wants answered not awaited — contract-enforced (needs `E-06`) |
| Push ff-check unchanged (RS-009) | `Committer.fastForward` → `GraphIndex.Reachable` | same commit-ancestry walk, now via the seam method, over the same pre-CAS-visible objects — behavior-preserving (proven by `P-06` + push-atomicity staying green) |

### Illegal State Analysis

- **Want for a non-tip / non-existent object** — *not* structurally preventable; validated at negotiation time against live ref state. **Application logic**, covered by `E-03`/`E-04`/`F-24`.
- **Incomplete pack (missing reachable object)** — unrepresentable *if* `Assemble` enumerates the full closure; since that is contract- not type-enforced, it needs a seam-contract test plus the interop clone cases (`F-01`/`F-02`).
- **Ready-without-packfile / not-ready-with-packfile framing mismatch** — a wire-shape error the type system cannot prevent; pinned by the `V2-27` framing assertion.

## Testing

Testing follows the `surface-testing` skill and ADR-0009: tests exercise the running server through its public surface — the git wire protocol via real `git` (interop, primary) and raw HTTP for wire/framing assertions — never internal functions. **The surface is the conformance `Adapter`**, unchanged from slice 1; no new harness.

**The surface-testability risk, named honestly.** This is the slice's central testing hazard: basic clone/fetch *and the full v2 framing* already pass in slice 1 (`F-01`, `F-02`, `V2-27`, `E-03`, `E-06`, `V2-19` are all green against `mvp` today), so a naive refactor that moves enumeration below the seam — or that quietly keeps the "any in-store object" want policy — would leave *every conformance case still green and notice nothing*. The interop suite cannot, by itself, prove this slice did its work. The three must-fail-today gates above (the `Assemble` seam-contract test, the RS-009 routing test, and the rewritten `E-04`) are therefore the load-bearing proof, each chosen because it is **red against slice 1's code**:
- the `Assemble` seam-contract test is red because `PackEngine` has no `Assemble` method (per ADR-0005, enumeration belongs below the seam).
- the RS-009 routing test is red because `Committer` has no `GraphIndex` dependency and `fastForward` walks ancestry itself.
- the rewritten `E-04` is red because slice 1 *serves* an in-store non-tip want; it passes only once want-validation moves to `RefStore.List` (BP-13).
- the new `V2-16` case is red because slice 1's parser silently ignores an unrecognized fetch-argument line and serves a pack; it passes only once the Negotiator rejects unexpected lines (BP-17). (Both `E-04` and `V2-16` were confirmed red-today by probing the `mvp` and reference adapters during review.)

Key surfaces and substitutes:
- *[integration / interop]* the upload-pack endpoint driven by real `git clone` / `git fetch`, plus raw HTTP + hand-crafted pkt-line for the `V2-27` framing and the negotiation ACK/NAK/ready assertions. This is the existing conformance suite.
- *[in-process startup]* unchanged — `MODE=embedded`, in-memory backend, fresh repo per case seeded over the protocol via `git push` (protocol-only provisioning, ADR-0001).
- *[seam-contract tests]* `PackEngine.Assemble` gets a **backend-agnostic** seam-contract test (closure completeness for given wants/haves; whole-object output; missing-object error category), written against the `internal/storage` interface so it is **reused unchanged** when the local backend lands — the parity model ENG-112 established. `GraphIndex.Reachable`'s contract is covered transitively by the push ff-check cases (`P-06`) now routing through it, plus a direct seam-level reachability test.
- *[fakes needed]* none beyond the in-memory backend; the reference oracle (`git http-backend`) is the always-registered reference adapter.

**No injectable clock** is needed — fetch has no time-dependent behavior. (The deferred shallow `--shallow-since` / `deepen-since` frontier uses **committer** timestamps from the commit objects themselves, not wall-clock — so even that follow-up needs no clock injection.)

**Regression golden:** the advertisement golden is unchanged this slice — slice 2 advertises the same `fetch` token with **no new sub-capabilities** (no `shallow`/`filter`), because those are deferred. If the build adds any advertised capability it must update the owned golden (`testdata/mvp/advertisement.golden`) as a reviewed change.

## Deferred Work & Required Follow-up Tickets

Per the ENG-117 design decision, deferred scope is **not silently dropped** — each item below **MUST** become a Linear follow-up ticket. Tickets are created **after** slice-2 implementation lands (so their scope reflects what the build actually leaves), but the **need is recorded now and is a hard requirement**:

1. **Shallow clone/fetch (phase 5)** — the `shallow`/`deepen`/`deepen-since`/`deepen-exclude`/`unshallow` wire exchange and the *live* bounded-ancestry-walk frontier (D24), making the drawn primitive a real consumer. Cases `N-01`…`N-07`.
2. **Partial clone / `filter` (phase 5)** — `blob:none`, `blob:limit=N`, `tree:0`, sparse; the object type/size masking on enumeration (the object index carries type+size, §3.7). Cases `N-08`…`N-17`. (May be the same ticket as #1 or a sibling, decided at creation.)
3. **Protocol v0 fetch / v2→v0 fallback (phase 8)** — `F-05`, `F-14`, `F-28`; the v0 negotiation buffering for a connection's duration (D24). **This ticket also owns v0 want-validation: git's v0 `upload-pack` is advertised-ref-tips-only by default** (`uploadpack.allowTipSHA1InWant`/`allowReachableSHA1InWant`/`allowAnySHA1InWant` relax it) — so the retired BP-13 "ref-tips-only" policy resurfaces *here*, scoped to the v0 path, where it is the git-compatible behavior (ADR-0006). v2 want-validation stays existence-based.
4. **Streaming-OFS resolver** — a true streaming OFS-delta resolver for packs above the `receive.maxInputSize` cap (RS-008); replaces `pack.Ingest`'s bounded `io.ReadAll`. Push-side perf; no fetch conformance case.

A handoff/close-out step for the slice-2 build ticket must verify these four tickets exist before the slice is considered complete.

## Limitations & Future Work

- **Fetch packs are undeltified.** `Assemble` emits whole objects (slice-1 `pack` writer reused). Delta selection is a below-seam optimization for the local/scale backend (D18) and a clone-bandwidth win, not a slice-2 correctness requirement.
- **`GraphIndex` is in-memory naive walk only.** Generation-number pruning and persisted commit-graph records arrive with the local backend (BP-14); the Tier-2 reachability bitmaps that accelerate `Assemble`'s enumeration are a scale-backend internal (D11/D17). The seam contracts are designed for all three; only in-memory semantics are exercised now.
- **Negotiation is single-round-per-request by construction (D24).** Multi-round negotiation happens across multiple client POSTs (the stateless model); the server holds no session. This is correct for v2 stateless-RPC and is not a limitation to fix, but it bounds how clever the `ready` heuristic can be (it sees only the current request's haves).

### Multi-tenancy readiness

Auth and tenant identity are out of scope for this slice (and the MVP), but the fetch design is deliberately **tenancy-friendly by construction** even though it is **tenancy-neutral by intent** — nothing here implies or builds multi-tenancy, yet nothing precludes it. Recorded so the eventual tenancy slice knows where the seams already are and what must not be broken before then.

- **What already supports it.** Per-repo isolation is *structural*, not best-effort (Invariant 1 / D8, surface-guarded by `TestInvariant_PerRepoIsolation`): every storage access is `Registry`-scoped and reaches the backend through a single repo key (`Backend.Refs/Objects/Packs/Graph(repo)`). Tenant isolation is that same property at a coarser grain. The stateless-between-requests model (D24/BP-16) means no server affinity (a horizontally-scaled fleet works), and domain-boundary opacity means a tenant-aware backend (per-tenant keyspace / placement on the scale backend) drops in behind the identical seam. The `Registry` already names this as its eventual role (leases D15, placement D14, not yet implemented).
- **Where the work concentrates when tenancy is added.** Three predictable spots, with the protocol brain largely untouched: (1) **repo identity** — `repo` is a flat global string today (`Registry.Create` enforces global uniqueness); tenancy means deciding whether the tenant is encoded into a structured repo key or threaded as a separate dimension through `Registry`/`Backend`. (2) **Access control** — an auth/authorization layer above the seam (ref-tips-only `want` validation governs *what* is fetchable within a repo, never *who* may reach the repo). (3) **Fairness + metering** — there are no per-tenant quotas, rate limits, or concurrency bounds (pack assembly is an unbounded per-request walk), and the deferred observability surface is per-fetch, not tenant-labeled for billing/SLOs.
- **The invariant that must stay sacred.** Isolation holds today *because* of an explicit choice: at MVP objects are per-repo with **no cross-repo object sharing**. The most tempting future optimization — cross-repo object deduplication (shared object pools) — is exactly what would make a tenant boundary load-bearing rather than structural and reopen the "ghost object" leak (Invariant 1). If cross-repo sharing is ever introduced, Invariant 1 must first be re-derived at the *tenant* grain. Until tenancy is explicitly designed, keep objects per-repo.

## Open Questions

- **Seed the fetch observability surface now or with the local backend?** Per-fetch object-count / pack-bytes / negotiation-round metrics become a parity-gate input when clone latency starts mattering (local backend). They could be seeded minimally now or deferred. Not an acceptance gate either way; flagged for the build to decide on cost. *(Mirrors the slice-1 §3.6-observability open question; not a blocker.)*

---

## Decision Log

Decisions made in this blueprint continue the `BP-` sequence from ENG-112 (BP-1…BP-8) so numbers are unique across the git-server slice blueprints. The `D-` entries are the inherited `technical-shape.md` decisions that are load-bearing here.

| # | Decision | Status |
|---|---|---|
| **BP-9** | **Slice 2 scope = core v2 fetch**: harden negotiation, make `GraphIndex` live, land `PackEngine.Assemble`. Shallow, partial-clone `filter`, v0 fetch, and the streaming-OFS resolver are **deferred to required follow-up tickets**; only the D24 bounded-walk primitive's *contract* is drawn. | decided |
| **BP-10** | **`PackEngine.Assemble` takes the negotiated `wants`/`haves` frontier (Reading B), not a materialized OID set.** Object enumeration + serialization live **below the seam** (in-memory naive walk; scale Tier-2 bitmaps), reconciling D18 ("object set above") with D11/§3.7 (object reachability is Tier-2, below the seam) toward the accelerable-below-seam reading. The brain owns the negotiated logical set + framing. Settled by **deliberation** (3 advocates: above-seam / below-seam / hybrid — the decisive fact: reachability bitmaps consume `wants`/`haves` directly, so a flat OID set or a pre-computed commit set both leave the bitmap backend nothing to consume); recorded as **ADR-0005**. | decided |
| **BP-11** | **`GraphIndex` stays commit-level (Tier-1) and goes live via RS-009**: `Committer.fastForward` routes through `GraphIndex.Reachable` (the `Committer` gains a `GraphIndex` dependency), removing the duplicated ancestry walk. The D24 bounded-ancestry-walk primitive is drawn at contract altitude, not exercised. | decided |
| **BP-12** | **Negotiation extracted into `internal/negotiate` (`Negotiator`)** per §7/D22; `internal/protocol`'s `fetch` branch becomes a thin delegation. `protocol → negotiate → storage`. | auto-resolved from §7 |
| **BP-13** | ~~**MVP want-acceptance = advertised ref tips only**~~ → **RETIRED, see correction note at top.** The premise ("matches git's default `upload-pack`") describes git **v0**; this slice is **v2**, whose `upload-pack` accepts a `want` for **any in-store object** (existence-only `parse_want`; the `allow*SHA1InWant` knobs gate only v0). Empirically confirmed against `git 2.49.0` (reachable non-tip *and* dangling object both served over v2). **The build keeps slice-1's `ObjectStore.HasMany` want-validation — that is the git-v2-compatible behavior, not a divergence.** `E-04` stays the unheld-want rejection (a want the server does not hold → `ERR ... not our ref`), green against both adapters. Ref-tips-only is git's **v0** default and moves into the deferred v0-fetch follow-up ticket, where it is correct. Recorded as **ADR-0006**. | superseded |
| **BP-14** | **Tier-1 commit-graph record persistence is deferred to the local/scale backend slices** — the in-memory backend walks objects on demand and persists no records; the `GraphIndex` query contract is identical either way. Resolves the ENG-112 open question. | decided |
| **BP-15** | **v0 fetch fallback and the streaming-OFS resolver are deferred**; follow-up tickets are **required** (created post-implementation, recorded now). The existing v0 empty-clone (`F-15`) must not regress. | decided |
| **BP-16** | **Stateless-between-requests negotiation (D24)** — no server session; each POST re-derives state from the client's re-sent haves. The v2 ready→DELIM / not-ready→flush framing (`V2-27`) is a protocol-mandated assertion. | auto-resolved from D24 |
| **BP-17** | **The Negotiator rejects unrecognized fetch-argument lines** (matching git upload-pack's "unexpected line: …"), rather than silently ignoring them. **Corrects a slice-1 divergence** confirmed by probing both adapters during review: the current permissive `default` branch ignores unknown lines and serves a pack where the reference rejects the request. Recognized-but-unhandled arguments (`thin-pack`, `no-progress`, `ofs-delta`, `include-tag`) are still accepted with no effect. Gated by new case `V2-16`. | decided |

Inherited and load-bearing here: **D5** (decomposed seam from day one → BP-10), **D9** (protocol-complete MVP; partial clone is core but sequenced later → BP-9), **D11** (two-tier GraphIndex; object reachability is Tier-2 below the seam → BP-10), **D17** (GraphIndex is a commit-level query interface; bitmaps backend-internal → BP-10/BP-11/BP-14), **D18** (seam splits what/how → BP-10), **D19** (push-first build order; fetch is slice 2), **D24** (shallow/partial split; stateless-per-request; bounded-ancestry-walk primitive → BP-11/BP-16), **D25** (no chatty per-object reads), **D27** (fetch gets its own blueprint). Plus **BP-1** (format-aware OID, ADR-0004), **BP-2** (shared `pack` algorithm, reused by `Assemble`), **RS-007** (one shared `object.References` reachability switch — `Assemble` reuses it), **RS-009** (the deferred walk unification, now resolved → BP-11), and ADRs **0001** (oracle), **0002** (interop-not-byte-identity → the `V2-27` framing classification), **0003** (status matrix), **0004** (format-aware OID), **0005** (fetch object enumeration below the seam → BP-10/BP-11).
