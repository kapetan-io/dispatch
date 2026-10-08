# Git Server — Technical Shape

**Status:** Shaping (ENG-93 → ENG-108). Decisions below are agreed in direction; the
**code architecture is shaped (§7)** and the **seam-placement questions are resolved
(D23–D26, quorum-reviewed)**; several implementation threads remain open (see
[Open Threads](#open-threads)).
**Date:** 2026-06-16
**Relationship to prior docs:** Supersedes the unresolved storage fork in
[`storage-architecture-decision-notes.md`](./archive/storage-architecture-decision-notes.md)
and revises [`storage-adr-draft.md`](./archive/storage-adr-draft.md) (ADR-0005): the
decomposed model is adopted as an **architecture from day one** behind a storage
seam, but the MVP runs a **single-node-durable backend** (git pack files + an
embedded KV), not an FDB+Scylla cluster. Conformance and protocol scope are
governed by [`../testing-strategy.md`](../testing-strategy.md) and ADRs 0001–0003.

---

## 1. Purpose & problem domain

git-server is a git wire-protocol server whose **primary consumer is AI coding
agents**, not humans. That single fact drives the shape:

- **Write-heavy (~80/20)** and "chatty": agents run tight
  `fetch → generate → push → fetch` loops, branch heavily, ~2,000 API req/day/seat.
- **CI is the dominant clone consumer** — full clones per PR, often several times
  per PR, all on third-party infra, so we are always on the serving side.
- The git smart-HTTP protocol **forces server-side packfile computation per
  fetch**, so egress is structurally bound to the API server's NIC (no CDN
  offload). Infra premise: flat-bandwidth Hetzner dedicated servers.

**What it is:** a git wire-protocol server tuned for fast checkout/fetch of large
(increasingly mono-) repos plus high-frequency push, with the graph primitives
(diff, merge-base, conflict detection) and an event stream needed to power a PR
UI/UX. Real `git` on both client and server.

**What it is not (now):** a graph-query/random-access product, or the
"materialized POSIX-filesystem view of a repo" (lazy per-file reads instead of
checkout). That view is a **likely separate future product** sharing DNA with
this one; the object-access substrate here should not preclude it, but it is out
of scope.

**Boundary (settled — see §6):** git-server is the git **graph + protocol engine**
— graph operations plus git-native events. The **PR entity, reviews, comments,
check aggregation, and review workflow live in a separate PR-review service** that
consumes git-server; git-server never models a "PR".

### Read / write shape (settled)

- **Checkout/commit is the hot path.** Agents need raw files via real `git`
  checkout; the bet is that does not change. Monorepos are expected to grow, so
  **fast checkout/fetch of large repos is the priority**.
- **Random-access reads (diff, merge-base, file-at-path) are required but
  secondary** — they power the PR surface, not the agent inner loop.
- **`git push` is the primary write**; a **structured mutation API** (apply a
  diff / construct a commit server-side) is required for the PR surface. Both
  write paths converge on the same object store.

---

## 2. Use cases & events

### 2.1 Actors and access patterns

| Actor | Operations | Hot? |
|---|---|---|
| **AI agent** | incremental fetch, push, branch create/delete | hot, read-your-writes |
| **CI** | full clone (per PR, repeated), shallow/partial clone | hot, egress-dominant |
| **PR/Review UI** | list/view PRs, diffs, mergeability, review threads, checks panel, merge action | warm |
| **Control plane / background** | create/archive repo, compaction, main-pack recompute, GC, backup | background |

### 2.2 Eventing — git-server is a *producer* to slip-stream

slip-stream (Kafka-shaped: topics/partitions) is the event-streaming system.
git-server **publishes**; the **SSE gateway is a separate service** that consumes
slip-stream, filters/authorizes per connection, and fans out to agents (SSE) and
the UI (a separate SSE). git-server owns only **event types, content, and
publish semantics**.

**Event taxonomy — split by producer.** git-server emits only **git-native
events**; the PR/review/check/merge-workflow events are produced by the separate
**PR-review service** (§6, §8), which consumes git-server's events + graph API.

*git-server emits:*
- **Ref/Push:** `push.received` is the **atomic envelope** for one push — it carries
  the full set of ref changes (`ref.created` / `ref.updated` (ff / forced) /
  `ref.deleted`, each `{old_oid,new_oid}`) that committed together. All ref changes
  for a push are emitted **after** the ref-CAS commits (Rule 3) and delivered as one
  partition-ordered batch, so a subscriber never observes a partial push. Consumers
  treat the push as the atomic unit; the per-ref line items are for convenience.
- **Lifecycle:** `repo.created/archived/deleted/renamed` (each paired with a
  current-state query — `repo.get` / `repo.list`, Rule 5). **Async job progress is
  *not* a slip-stream event** (D26): a long-running admin/async job (compaction, GC,
  bitmap regen, rehydration) streams its progress over **steve admin-attach**
  (ADR-14) — a global job has no per-repo partition key (Rule 2), so only the job's
  *terminal effect* (a lifecycle or `push.received` event) reaches slip-stream.

*PR-review service emits (consuming git-server):* `pr.*` (opened/synchronized/
base_changed/mergeability_changed/closed/merged/edited), `review.*`, `comment.*`,
`check.*`, `conflict.*`, `merge.*` — built from git-server's ref/push events and its
graph queries (`repo.mergeability`, `repo.diff`, `repo.merge`). git-server never
models a "PR".

The SSE gateway consumes **both** producers' events from slip-stream. Every agent
subscription still reduces to one of seven motivations: stop polling · base moved →
rebase · another writer touched my branch · CI done → merge · PR mergeable →
auto-merge · review feedback → act · long op done → proceed.

**Publish semantics (the rules that make this correct):**

1. **Topics:** a small fixed set by category (e.g. `git.refs`, `git.pr`,
   `git.checks`) — **never per-repo** (topic explosion). Repo selectivity is the
   consumer's filter job.
2. **Partition key = repo id.** This is load-bearing: Kafka orders *within a
   partition*, so per-repo ordering is what makes "the latest event's OID is the
   truth" actually hold. A hot monorepo is one partition (its ordering+throughput
   unit) — fine, because events are tiny and you *want* them serialized.
3. **Emit-after-commit.** Publish only after the authoritative state commits (the
   ref/index transaction). This is what makes the catch-up model below safe.
4. **Payloads are absolute, versioned, idempotent, and self-describing** — they
   carry the state they reflect (e.g. `new_oid`, not "advanced by 3") and their
   routing/authz identity (`{repo, ref?, pr?, actor/visibility}`) so the gateway
   filters and authorizes locally.
5. **Catch-up is snapshot-first, not replay.** Consumers **subscribe first, then
   query current state** to catch up — never replay a cursor (an agent may have
   last listened months ago). Therefore **every event type has a paired
   "current-state" query** owned by its producer (git-server's `ref.updated` ↔
   `repo.refs.list`; the PR service's `pr.mergeability_changed` ↔ its PR query,
   itself backed by git-server's `repo.mergeability`). git-server retains no event
   log for replay; durability of truth lives in its state, queried on demand. The
   subscribe-then-query **race is benign because payloads are absolute and
   idempotent** (Rule 4): the consumer subscribes and buffers first, then takes the
   snapshot; any buffered event already reflected in the snapshot is dropped by
   version/OID comparison and any newer event supersedes it — the snapshot is the
   baseline of truth, so no missed-event replay is ever needed.

---

## 3. Storage architecture

### 3.1 The core decision: disaggregated *architecture* from day one

We build the **disaggregated architecture immediately** — git-server owns the
protocol brain, and storage sits behind a **decomposed-shaped seam** — rather than
building a native-git MVP and re-architecting later. A seam validated only against
a native-git backend is a seam we cannot trust for the decomposed workload,
because that workload never ran through it. Building backends that are all
*decomposed-shaped* makes the seam honest by construction and exercised from day
one.

**Architecture ≠ backend.** "Disaggregated" is the *architecture*; the *backend*
is a per-phase swap over one interface. This is what lets the MVP ship on cheap
single-node ops while the architecture is already the final one.

### 3.2 The seam (JGit DFS prior art)

Draw the line **below the protocol brain, above storage.**

**Above the seam — git-server's permanent, backend-agnostic brain** (built for
real in the MVP, test-first against the conformance oracle):

- HTTP transport, routing, smart-HTTP framing/advertisement, v0/v2 dispatch
- the protocol brain: negotiation (v2 + v0 fallback), shallow, partial-clone
  `filter`, packfile assembly (delta), pack parse/index on ingest
- token validation + the two git-native enforcements (protected-ref, reachability)
- graph/API surface (diff, merge-base, conflict, file-at-path), the mutation API
- event emission to slip-stream

**Below the seam — storage capabilities (the part that swaps):**

- `RefStore` — list / resolve / **transactional CAS** / atomic multi-ref / delete
- `ObjectStore` — exists / get / put by OID
- `GraphIndex` — reachability(oid, ref-set), ancestry walk, merge-base, and a
  **bounded-ancestry-walk primitive** (ancestors-from-roots with a depth/stop bound,
  serving shallow-frontier computation — D24) — all **bulk/batched**, never chatty
  per-object
- `PackEngine` — assemble pack for (wants − haves) → stream; ingest pack
  (**thickening thin packs** against existing objects, D23) → objects + index +
  the **sparse pushed commit→parent/tree edge map** (feeds the connectivity check, D25)

**The discipline that keeps it honest:** design the below-seam interfaces against
the *decomposed* model's constraints now — transactional ref CAS, batched graph
queries, prepared/streamable packs, async pre-compute — even where a single-node
backend could be lazier. The chatty/synchronous pattern that is free on one node
is a perf catastrophe across FDB+Scylla; designing for the high-latency backend
is what prevents a rewrite.

We **own the protocol brain** rather than delegating to git plumbing, because
"pack for free" never exercises (and so never validates) the decomposed seam.

### 3.3 Backend ladder

All backends are *decomposed-shaped* — they differ only in **where bytes/index
live**, never in the data model — so none pollutes the seam.

| Backend | Object bytes | Index + refs + metadata | Role |
|---|---|---|---|
| **In-memory** | in-memory packs | embedded KV, in-memory mode | embedded mode (ADR-3), tests, local dev |
| **MVP single-node** | **git `.pack` files on disk** | **embedded KV on disk** | MVP production — one box, no cluster |
| **Scale** | Scylla (chunked packs) | FoundationDB | scale production |

The **pack format is the stable object encoding across every backend** (it is
also the wire format); only persistence location changes. The scale swap
(`.pack` files → Scylla, KV → FDB) leaves the protocol brain untouched.

**Architecture ≠ backend** (§3.1): every row is the *same disaggregated
architecture*. "Single-node" describes the MVP **deployment** (one box, no cluster),
**not** a native-git design — the seam is decomposed in all three rows.

### 3.4 MVP backend shape (the agreed detail)

**Pack bytes: git `.pack` files on local disk.** Not in the KV. Files win the
paths that matter:

- **Clone streaming (the #1 hot path):** a `.pack` streams via sequential read /
  `sendfile()` (zero-copy, DMA from page cache to NIC). A KV value cannot — it
  copies out through the API, no zero-copy, no sub-value range access.
- **Hot-main-pack cache for free:** the OS page cache *is* the ADR's "local NVMe
  cache" — kernel-managed LRU, no tuning.
- **Byte-range random access:** native `pread(fd, offset, len)` via the index.
- **Clean space reclamation:** delete/compact = delete a file; avoids an embedded
  LSM's value-log GC, whose worst case is exactly large, churning values.
- **Faithful parity baseline:** this is how git/Gitaly serve, so the parity gate
  measures against git's own approach.

**Index + refs + metadata: Badger (selected).** Because pack bytes now live in
files, the KV stores only *small* values (refs, index entries
`{sha → pack-id, offset, len}`, PR/metadata), so the requirement is purely "best
embedded, ordered, **concurrent-writer transactional** KV in Go that maps to FDB."

Rationale (Badger over the field):

1. **It provides the transactions we need out of the box.** The push path is
   "update refs **+** write index entries atomically, with CAS" — an interactive
   multi-key transaction with conflict detection. Badger has it natively
   (ACID/SSI); the workload is write-heavy, so the KV must allow **concurrent**
   write transactions.
2. **It is the closest single-node stand-in for FoundationDB.** FDB is an ordered
   KV with optimistic transactions; Badger is semantically the same on one node,
   so the `RefStore`/`GraphIndex` code written against it ports most directly to
   FDB at scale — reinforcing the seam-honesty principle (exercise the decomposed
   shape now, don't build something FDB later throws away).
3. **In-memory mode collapses the embedded and MVP backends into one
   implementation, two configs** — guaranteeing the embedded backend faithfully
   models production semantics rather than cheating with an easy map.

The field, for the record: **bbolt** and **SQLite** are ruled out because they
serialize writes to one writer transaction at a time, which fights the write-heavy
(80/20) push workload. **Pebble** (CockroachDB's engine) is the credible
**fallback** — better-tested at extreme scale — but it exposes atomic batches +
snapshots rather than interactive transactions, so it would push the CAS/
transaction layer onto us and is a looser match to the FDB target. Named as the
fallback if Badger disappoints operationally.

Moving pack bytes to files also **retires the value-log-GC concern** that
otherwise dogs Badger — that pain is specific to large, churning values, which the
KV no longer holds. The one surviving caveat is **transaction size/memory on a
huge push** (many index entries in one txn) — a constraint FDB shares, so it is a
seam-level concern: the "commit a push" operation must batch the index write while
keeping the ref update atomic. Tracked in [Open Threads](#open-threads). Badger also
opens the DB **single-process exclusive** — one git-server process owns the node's
KV, which fits the node-pinned MVP deployment and scopes read-your-writes to that
process (§6).

**Push crash model (files + KV), which needs no cross-store transaction:**

1. receive pack → temp file → fsync → atomic rename. Objects now on disk,
   **unreferenced (harmless)**.
2. one KV transaction: write index entries `{sha → pack-id, offset, len}` +
   ref CAS → commit.
3. crash between 1–2 → orphan pack file, no index/ref → harmless, GC'd later. No
   corruption.

The atomicity that matters (ref + index) is transactional in the KV; pack bytes
need not be in that transaction because unreferenced objects are safe by git's
design. (Step 2's "one KV transaction" is the common, small-push case. A **large
push batches its index entries across multiple KV txns before the final ref-CAS
txn** — the singular atomic point — which is safe because those entries only make
still-unreferenced objects discoverable, invisible to ref-based negotiation until
the CAS commits. See §3.6 and [Open Threads](#open-threads).) The KV is the **index
of record**; an on-disk `.idx` is optional, emitted only for git-tooling
interop/debugging.

**Reuse vs. replace (do not slide back into the native trap):** reuse git's
*object encoding* (mandatory — it's the wire format) and the *pack container +
`.idx` format* (good MVP storage); **replace** git's repo layout and
ref/index *management* (filesystem `refs/` lockfiles, static `.idx` as the index)
with the transactional `RefStore`/`GraphIndex`. Pack *bytes* are git-format;
*index and refs* are transactional.

### 3.5 Parity gate & invariants

- **Checkout-speed-parity gate (scale).** The scale backend's clone/fetch latency
  must match the MVP filesystem backend's before any cutover; the MVP filesystem
  latency targets git's own (ADR-0005: <2s NVMe-cached, etc.). Measured against
  the Go repo and a synthetic monorepo.
- **Per-repo reachability under cross-fork dedup (invariant).** Cross-fork/cross-
  repo blob dedup is desirable (agent fork-spam), but a shared object pool must be
  **invisible to the permission graph**: object visibility is computed by
  reachability from refs **of the specific repo/fork**, never from the shared
  pool — or fork B can fetch fork A's objects by SHA (the "ghost object" leak).
- **Dedup scope & orphan GC (MVP vs scale).** At MVP, **packs are per-repo — no
  cross-repo pack or object sharing** — so the orphan-pack GC of §3.4 is trivially
  repo-local and safe. Cross-fork dedup is a **scale-phase, object-store-level**
  concern (shared by OID, not by shared pack *files*); when introduced it requires
  **OID-level reference counting across repos** so GC never deletes an object another
  repo still references, and the per-repo reachability invariant above must continue
  to hold independently of physical sharing.

---

### 3.6 Concurrency (hot repo)

When many agents hit one monorepo, the dominant write pattern **parallelizes by
design** and **git's own optimistic ref CAS is the concurrency control** — there
are **no foreground locks**.

**Contention classification:**

| Resource | Contention | Why |
|---|---|---|
| Pack files (bytes) | none | each push writes its own unique, immutable `.pack` (disjoint files) |
| Refs | per-key only | distinct branches = independent CAS; same branch = a race, resolved below |
| Index entries | near-none | mostly disjoint; shared objects are content-addressed → idempotent |
| Per-repo metadata | **hotspot risk** | a single mutable record every push updates → every push conflicts on one key |
| Background jobs | IO, not locks | compaction/recompute compete for disk bandwidth, not locks |

**Write path (concurrency-safe by construction):** (1) receive pack → temp file →
fsync → atomic rename (objects on disk, **unreferenced** — this is git's
quarantine); (2) write index entries in **batched** Badger txns (safe: unreferenced
objects are invisible to ref-based negotiation, entries are idempotent and
order-free — this also satisfies the txn-size limit); (3) **final ref-CAS txn** —
the atomic commit point, after policy + connectivity checks. **Atomic multi-ref
push** = all CASes in that one final txn. There is **no per-repo primary**, so the
single-primary write hotspot of the Spokes model does not exist.

**Same-branch races need no lock.** Concurrent pushes to one ref both upload their
disjoint packs; one wins the final CAS, the other's `old_oid` no longer matches →
**non-fast-forward rejection**, loser's pack orphaned and GC'd. The CAS window is
an instant *after* the (slow) pack upload, so the contention window is tiny.

**The worst contention case is designed away, not solved at the push path.**
Direct pushes to **protected branches (`main`) are restricted** (permission model,
propose-vs-merge): `main` advances via the **merge path**, which the **PR-review
service's merge coordinator serializes** above git-server (calling `repo.merge` on
the DUH-RPC API, §6) using the event stream. So
"many agents racing to push `main`" cannot occur. Residual contention is
collaboration on a shared *feature* branch, handled by optimistic CAS + client
retry. **Held in reserve** (not built now): a **per-ref push queue** for a proven-
hot unprotected ref — serializes pushes to that one ref, still no global lock.

**MVP caveat — `main` contention is *designed* away, not *enforced* away, until the
merge coordinator ships.** The serializing merge coordinator lives in the
**post-MVP PR-review service** (§6, §8). Until it exists, concurrent `repo.merge`
calls targeting a protected ref are **not** serialized above git-server — they fall
back to the same optimistic CAS (one wins, the rest get non-ff and retry). That is
*correct* (no corruption, no lost update), just unsequenced; the "designed away"
guarantee is fully realized only once the coordinator is in place.

**Reads** take a Badger MVCC snapshot → consistent fetch view + read-your-writes;
concurrent monorepo clones share the page-cached main pack (non-contending,
NIC-bound).

**The hotspot to avoid:** no read-modify-write on a single per-repo record in the
push path. Per-repo state is **append-only** (each push writes its own keyed
entry) or **lazy/coarse** (`last_write_at` approximate, staleness tolerated).

**Background jobs** use copy-on-write + atomic index re-point (one txn) + **delayed
unlink** (POSIX keeps an open reader's fd valid after unlink, so deletion never
disrupts in-flight reads) + a **background-only** per-repo compaction advisory lock
(foreground never waits on it) + **foreground IO priority**.

**Branching pattern: mixed/unknown → measure.** Optimistic CAS is the correct base
regardless; the tuning knobs (per-ref queue, compaction aggressiveness) stay
deferred behind **observability**: per-ref non-ff/retry/orphan-pack rates, and
per-repo pack-count / clone read-amplification / compaction lag. Built from day one
so the deferred decisions are data-driven.

**Compaction policy: adaptive/deferred.** Mechanism is fixed (threshold-triggered,
low-traffic windows, foreground-yielding, immutable-COW); aggressiveness is tuned
later against the metrics above.

### 3.7 GraphIndex / reachability

Three consumers converge here — negotiation (including the shallow frontier, D24),
reachability permissions, and merge-base/diff — so the design mirrors git's proven
**two-tier reachability split**. (The push **connectivity check is *not* a GraphIndex
consumer** — it walks the full object graph including trees/blobs, which Tier-1
commit records cannot supply, so it couples to `ObjectStore` via a batched boundary
check — see §3.4 and D25.)

**Tier 1 — Commit-graph (commit-level).** Per-commit `{parents, generation
number, tree-oid}`. Answers commit reachability, merge-base (generation numbers
prune the walk), and ancestry. Small and **fresh**: lives in the transactional
store (Badger → FDB), **updated in the push txn**. Serves the connectivity-check
spine. **Commits-only extraction**: commit records in the KV; **trees and blobs are
read from packs on demand** for diff/file-at-path. (This sits *between* ADR-0005's
two variants — it extracts less than the "primary" (commits+trees) and more than the
"alternative" (refs+pack-index only); see the ADR-0005 reconciliation.) The schema
stays compatible with adding tree extraction later **if benchmarking shows it's
needed**.

**Tier 2 — Reachability bitmaps (object-level).** The per-fetch hot path needs the
full object set reachable from `wants` minus `haves`; walking history per fetch is
O(history) and catastrophic on a deep monorepo. Bitmaps turn it into set-ops:
`OR(bitmaps under W) AND-NOT OR(bitmaps under H)`. This is a **backend-internal
acceleration detail below the seam** (D17): the `local`/`scale` backends own the
object→ordinal bit assignment (append-only at index time; ordered for
compressibility), sampled-commit selection, and generator — likely **Roaring**
bitmaps (Go-native) over EWAH — entirely beneath the `GraphIndex` *query* interface,
while the in-memory backend answers the same queries by naive walk. **"We own this
index" is product-level** — our own reachability index, not git's `.bitmap` files —
**not above-seam generation**: the brain issues reachability *queries* and never sees
a bitmap.

**Freshness model (why async bitmaps survive constant pushes):** bitmaps cover the
**bulk/old history**; a **bounded walk** from the wanted tip down to the nearest
bitmapped ancestor covers the **recent** un-bitmapped commits. Per-fetch cost =
bitmap set-ops + a short recent walk (≈ commits since the last bitmap rebuild).
For a multi-tip fetch (e.g. CI), the walk runs per wanted tip to its nearest
bitmapped ancestor, so "bounded" means **bounded by commits-since-last-regen across
the wanted tips** — not by total history depth, *provided regen keeps pace with the
push rate*. Regen cadence is the knob that holds this — an observability-driven knob
(like compaction), not a fixed policy.

**Consumer mapping:**

| Consumer | Tier | Mechanism |
|---|---|---|
| Negotiation (wants−haves, filtered) | 2 | bitmap set-ops + recent walk |
| Shallow frontier (`deepen`/`deepen-since`, D24) | 1 | bounded-ancestry-walk primitive (depth/committer-time bound), frontier fed downward into pack selection |
| Reachability permission ("can actor see X") | 2 | bit X ∈ `OR(allowed-ref bitmaps)`, **scoped per-repo** (dedup invariant) |
| merge-base / diff | 1 (+ tree reads from packs) | generation-pruned LCA; diff reads the two trees |

The push **connectivity check is deliberately absent** from this table: it couples to
`ObjectStore` (a batched `HasMany` over the boundary of the in-memory-walked pushed
subgraph), **not** to `GraphIndex` — see D25.

**Partial clone:** the object index carries **per-object type and size** (free from
the pack index on ingest), so filtered traversal (`blob:none`, `blob:limit=N`,
`tree:0`, sparse) masks the closure by type/size.

**Storage placement (refines §3.4):**

- **Badger (small, transactional, fresh):** refs · commit-graph records · object
  index `{oid → pack-id, offset, length, type, size, ordinal}` · PR/metadata ·
  append-only pack manifest.
- **Files on disk (large, derived, bulk-regenerated):** pack bytes · **reachability
  bitmap-index files** · optional `.idx` for interop.
- **Scale swap:** Badger → FDB; pack + bitmap files → Scylla/blob store. Behind the
  `GraphIndex`/`PackEngine` seam.

## 4. Permissions

Three layers, two enforcement points. RBAC and identity live in a **separate
identity/authz service** (to be built); git-server owns only the git-native parts.

- **Layer 1 — Coarse RBAC (identity service):** org → repo → ref grants with roles
  (read/write/admin) and protected-branch rules. **Granularity stops at repo+ref**
  — no path/subtree ACLs.
- **Layer 2 — Agent capability attenuation (identity service):** agents present
  **attenuated, task-scoped, short-TTL, signed capability tokens** — scope ⊆ the
  principal's grants, narrowed to a task (e.g. push only to `agent/<task>/*`).
  Ambient identity is the degenerate (un-attenuated) case of the same mechanism,
  used for humans/CI. This structurally yields **propose-vs-merge**: a task token
  can push a work branch but not merge `main`.
- **Layer 3 — Reachability (git-server, git-native):** object read access is
  *derived* — fetch object X iff reachable from a ref you may read. A first-class
  `GraphIndex` traversal; the only place the expensive check lives.

**git-server's entire auth surface (thin contract):** accept a bearer token →
validate its signature against the identity service's public key **locally (no
per-op round-trip on the hot path)** → read scope `{repos, ref-patterns, ops}` →
check a cheap revocation denylist → enforce `op ∈ scope` **plus** protected-ref
(writes) and reachability (reads). Minting, principals, grants, token format,
revocation policy are **identity's**, not git-server's.

**Two enforcement points, cleanly separated:** the **SSE gateway** authorizes
*events* on Layer-1/2 only (events are coarse — ref/PR scoped — so a token-scope
check suffices, no reachability, no round-trip). **git-server's data plane**
enforces Layer-3 reachability on the operations that move objects
(fetch/clone/direct-SHA/file-read).

---

## 5. Protocol scope

The **MVP server targets all conformance phases 1–8** — a protocol-complete smart-
HTTP server (core fetch/push, robustness/errors, auth, shallow & partial clone,
namespaces/hidden-refs/push-options, advanced capabilities, protocol v0 +
fallback). Partial/sparse clone is therefore **core, not fast-follow** — it is the
canonical large-monorepo technique and the ancestor of the materialized-FS future.
Built test-first against the permanently-green `git http-backend` oracle
(ADR-0001). LFS is a separate track (ENG-94).

This **updates the phase framing in `testing-strategy.md` §8**, which scoped the
*MVP* to phases 1–3: those ~55 cases (the blueprint's MVP gate) are the **first
build milestone**; protocol-completeness (phases 1–8) is the MVP server's
**completion target**.

---

## 6. Non-protocol API surface

git-server exposes **two HTTP surfaces on one object store**:

- the git **wire protocol** (`info/refs`, `git-upload-pack`, `git-receive-pack`) —
  raw git, the deliberate **ADR-7 exception**;
- a **DUH-RPC API** (contract-first, `api/v1/openapi.yaml`,
  `POST /v1/<subject>.<action>`, DUH Reply errors) for everything non-protocol.
  Only the wire protocol is exempt from ADR-7.

**Scope: graph + protocol engine only.** git-server owns the graph operations PRs
are built on; the **PR entity, reviews, comments, check aggregation, and review
workflow live in a separate PR-review service** (§8) that consumes this API and
git-server's events. git-server never models a "PR".

**Graph queries** (these *are* the snapshot-first catch-up queries of §2.2):
`repo.refs.list` · `repo.ref.get` · `repo.tree.list` · `repo.file.get` ·
`repo.commit.get` · `repo.commits.list` (log) · `repo.diff` · `repo.mergeBase` ·
`repo.mergeability` (does X merge into Y) · `repo.compare`. **Repo-registry
queries** `repo.get` · `repo.list` are the current-state pair for the lifecycle
events (D26).

**Graph mutations** (server constructs objects; scope = **create-commit + merge**):
`repo.applyDiff` / `repo.commit.create` · `repo.merge` (three-way → merge commit or
conflict result) · lifecycle (`repo.create` / `archive`; no fork at MVP).
rebase / squash / cherry-pick are **deferred** (§8).

**Properties (inherited from earlier decisions):**

- **Mutations are server-originated pushes.** `applyDiff` / `merge` read trees from
  packs on demand (§3.7), write objects to a pack, and ref-CAS through the §3.6
  write path (optimistic CAS, protected-ref, connectivity, event emission). No new
  write primitive; the merge coordinator (the PR-workflow layer above git-server,
  §3.6) is the serialized caller for protected refs — **post-MVP; until it ships,
  protected-ref merges fall back to optimistic CAS (see the §3.6 MVP caveat)**.
- **Reads are MVCC-consistent and read-your-writes** with the wire protocol on the
  same store (within one server process in the single-node MVP — see §3.4's
  single-process Badger note; the scale backend inherits this from FDB's snapshot
  isolation). Monorepo-scale results **page** (DUH pagination: trees, logs, diffs)
  or **stream** (octet-stream: large blobs).
- **Auth is the §4 model** — reads enforce reachability (Layer-3) on the target
  ref; mutations enforce write scope + protected-ref. Same token, same enforcement
  as the wire protocol.

## 7. Code architecture (package & type map)

This is the **ubiquitous language** for the codebase: every concept above names a Go
package and a primary type, so a name in the code maps back to a concept in this document
with no translation step. The visual map lives in the problem-domains explainer (§17); the
rules and table below are authoritative.

**The one dependency rule.** Transports sit over the protocol engine, which sits over the
brain packages, which all **depend inward on the `storage` seam** (interfaces) and *never*
on a concrete backend. Backends implement the seam from below. Nothing above the seam
imports `internal/storage/local` or `…/memory`; the backend is injected at the composition
root (scaffold, ADR-15).

**Package-granularity rule.** A concept earns its own package directory only when it spans
more than one type/file. Single-type concepts live in the root `internal/` package
(`package gitserver`) until they grow a second type. The one exception: a contract other
packages must import stays a small leaf package even as a single type, to keep the import
graph acyclic — which is why the seam is always `internal/storage`. The concrete `Emitter`
(events) and `Registry` (repo substrate) live in the root package and are injected into the
brain through **consumer-defined interfaces** (e.g. the write path declares the small
`emitter` interface it needs; the root provides the concrete).

| Concept (domain) | Package | Primary type(s) | Depends on |
|---|---|---|---|
| A · Transport adapter | `internal/` (root) → `internal/transport/` when ≥2 | `HTTPTransport` (`git://`/SSH later) | `protocol` |
| A · Protocol engine | `internal/protocol` | `UploadPack` · `ReceivePack` · `Advertiser` · pkt-line | `negotiate` · `pack` · `writepath` · `storage` |
| B · Negotiation | `internal/negotiate` | `Negotiator` | `storage` (GraphIndex, RefStore) |
| C · Pack assemble/ingest | `internal/pack` | `Assembler` · `Ingester` | `storage` (PackEngine, ObjectStore) |
| D · Write path | `internal/writepath` | `Committer` (the single commit point) | `storage` · `Emitter` |
| E · Graph query API | `internal/graphapi` | `Service` | `storage` |
| F · Mutation API | `internal/mutate` | `Engine` | `writepath` · `storage` |
| G · Auth enforcement | `internal/auth` | `Enforcer` | `storage` (GraphIndex) · identity (ext) |
| H · Event emission | `internal/` (root) | `Emitter` | slip-stream (ext) |
| **Seam** | `internal/storage` | `RefStore` · `ObjectStore` · `GraphIndex` · `PackEngine` | — |
| Substrate (repo) | `internal/` (root) → `internal/repo/` at scale | `Registry` (+ `Lease`) | `storage` |
| Backend · in-memory | `internal/storage/memory` | `Backend` | implements seam |
| Backend · MVP | `internal/storage/local` | `Backend` | implements seam |
| Backend · scale | `internal/storage/scale` | `Backend` | implements seam (future) |
| Wiring | `internal/` (root) + `cmd/git-server` | `Server` (Start/Shutdown) | everything (composition root) |

**Seam = "what"; backend = "how".** The split is semantic: the brain decides *what* (the
negotiated object set, the pack format/framing, the commit-graph records, the reachability
*query*); the backend decides *how* those are physically stored, streamed, and accelerated
(delta selection, `sendfile`, bitmaps vs. naive walk). See D16–D18.

---

## 8. Open threads

Not yet shaped — the next design passes:

- **Per-package internals** — the §7 package map fixes the boundaries and primary types;
  the internal file/type breakdown within each brain package is settled during the build.
- **Concurrency follow-ons (shaped in §3.6, deferred specifics):** the merge
  coordinator that serializes protected-branch updates above git-server (post-MVP —
  §3.6 MVP caveat); the held-in-reserve per-ref push queue; the contention/compaction
  **metrics surface**. (The connectivity-check shape is now settled — D25, a batched
  `ObjectStore.HasMany` boundary check, **not** a `GraphIndex` consumer.)
- **PR-review service** (separate, consumes git-server) — the PR entity, reviews,
  comments, check aggregation, workflow, and the PR-workflow events. Outside
  git-server scope; its own design effort.
- **API-surface follow-ons (shaped in §6):** `openapi.yaml` contract authoring;
  pagination/streaming specifics for monorepo-scale results; the deferred mutations
  (rebase / squash / cherry-pick).
- **`GraphIndex` follow-ons (shaped in §3.7):** object→ordinal ordering for bitmap
  compressibility; bitmap regen cadence (observability-driven); EWAH-vs-Roaring
  choice — **all backend-internal below the seam (D17)**, invisible to the brain; the
  tree-extraction benchmark trigger (commits-only → +trees). Plus the shape of the
  **bounded-ancestry-walk primitive** the shallow frontier rides (D24), and the
  **`PackEngine.Ingest` return contract** carrying the sparse pushed edge map (D23/D25).
- **Badger validation pass** — pin down transaction size/memory limits on a large
  push (the surviving caveat), in-memory-mode maturity, and the small-value
  value-log-GC story. (Store selected; this validates specifics, not the choice.)
- **Push-commit batching** — the `RefStore` "commit a push" operation must batch
  the index write while keeping the ref update atomic, within Badger's (and later
  FDB's) transaction limits.
- **Identity/authz service** design (separate service; owns Layers 1–2).
- **`.idx` interop** detail; pack **compaction / main-pack pre-compute** jobs.
- **Activity-based storage tiering** (hot/warm/cold, Backblaze B2) — ADR-0005
  specifies this in detail; it is **deferred to post-MVP scale design** and not part
  of the MVP shape.

---

## 9. Decisions log

| # | Decision |
|---|---|
| D1 | Primary consumer is AI agents; checkout/commit is the hot path; monorepo-fast is the priority. |
| D2 | `git push` primary write + a structured mutation API for PR; both converge on one store. |
| D3 | git-server is a slip-stream **producer**; SSE gateway is a **separate** consumer doing filter+authz. |
| D4 | Events: small fixed topics, **partition by repo**, emit-after-commit, absolute/versioned/idempotent/self-describing payloads, **snapshot-first catch-up (no replay)**, a paired current-state query per event. |
| D5 | Build the **disaggregated architecture from day one** behind a decomposed-shaped seam; own the protocol brain (no "pack for free"). |
| D6 | Backend ladder: in-memory (embedded) → single-node-durable (MVP) → FDB+Scylla (scale), one seam. |
| D7 | **MVP backend: git `.pack` files on disk for bytes + Badger (selected) for the transactional index/refs/metadata; Pebble is the named fallback.** Scale swaps bytes→Scylla, KV→FDB. |
| D8 | Permissions: identity service owns RBAC (repo+ref granularity) + attenuated task-scoped capability tokens; git-server enforces only protected-ref + reachability; reachability is per-repo even under cross-fork dedup. |
| D9 | MVP protocol scope = conformance phases 1–8 (protocol-complete); partial clone is core. LFS separate (ENG-94). |
| D10 | Hot-repo concurrency: **no foreground locks** — optimistic per-ref CAS is the concurrency control; disjoint immutable pack files; batched-index-then-ref-CAS commit; MVCC reads. Worst case (`main` contention) designed away via restricted direct push + serialized merges above git-server. Per-ref push queue + compaction aggressiveness deferred behind observability (mixed-branching/adaptive). |
| D11 | GraphIndex is two-tier: **commit-graph** (generation numbers, commits-only extraction, transactional in Badger, defer trees behind a benchmark) + **object-level reachability bitmaps** we **own from day one** (own ordinal assignment + sampled bitmaps + bounded-recent-walk freshness; likely Roaring). Bitmap-index files live on disk like packs (not in Badger). Object index carries type+size for partial-clone filtering. |
| D12 | **Two HTTP surfaces on one store:** the git wire protocol (ADR-7 exception) + a contract-first **DUH-RPC API** (graph queries = the §2.2 catch-up surface; graph mutations; lifecycle). Mutations are server-originated pushes through the §3.6 write path. |
| D13 | **Boundary settled — git-server is the graph + protocol engine.** PR entity / reviews / comments / check aggregation / review workflow + the PR-workflow events live in a **separate PR-review service** consuming git-server; git-server never models a "PR". Mutation scope = **create-commit + merge** (rebase / squash / cherry-pick deferred). |
| D14 | **Horizontal scale = Path A.** MVP is single-node (Badger single-process-exclusive); horizontal scale swaps to the shared-storage backend (FDB + Scylla) and the brain becomes **stateless** — any instance serves any repo, the FDB ref-CAS serializes writes, no data-path leader. A sharded local-disk fleet (repos pinned to nodes) is explicitly ruled out. |
| D15 | **Coordination = per-repo lease.** Background jobs (compaction, GC, bitmap regen) are owned by the holder of a **per-repo lease** (registry/FDB), not a global leader; leader election is reserved only for an optional control-plane. GC waits a **grace TTL > longest push** before reclaiming an orphan pack, so it never races a concurrent push's quarantine. |
| D16 | **Transport seam.** A thin transport adapter (smart-HTTP at MVP; `git://`/SSH later) sits over a transport-agnostic **protocol engine** operating on byte streams (`io.Reader`/`io.Writer`, not `http.Request`), so a new transport is a new adapter, not a brain change. |
| D17 | **GraphIndex seam is a query interface** (`Reachable`/`MergeBase`/`Ancestry`). Tier-1 commit-graph records are computed by the brain and written **transactionally in the push txn**; Tier-2 bitmaps are a **backend-internal acceleration detail**; the in-memory backend answers reachability by naive walk. "We own the index" is product-level (not git's `.bitmap`), not above-seam generation. |
| D18 | **Pack assembly split: object set above the seam, bytes below.** The brain owns *what* (negotiated object set + pack format/framing); `PackEngine` owns *how* (delta selection, byte reads, streaming). Delta selection moves below the seam; the pack format stays the shared encoding. General rule: **the seam splits "what" (brain) from "how it is stored/streamed/accelerated" (backend).** |
| D21 | **Minimal auth token contract now.** Define the token format + local signature verification immediately and stub the issuer; the `scope` claim is **reserved but not enforced granularly** (RBAC shape decided later, non-breaking); protected-ref is config-driven at MVP. Reachability (Layer-3, D8) is git-native and testable now. Refines D8. |
| D22 | **Code architecture / ubiquitous language (§7).** Doc concepts map 1:1 to Go packages + primary types; the brain depends inward on the `internal/storage` seam, backends implement it; single-type top-of-graph concepts live in the root `package gitserver` until they grow. |
| D23 | **Thin-pack thickening is below the seam.** A pushed pack may be *thin* (deltas against bases the client knows the server holds but did not send — `git push` sends thin packs by default). `PackEngine.Ingest` (backend) resolves/thickens against the backend's own stored objects, persists self-contained objects (git's OID-is-content-hash invariant forbids storing unresolved deltas), and returns to the brain **both the ingested OID set and the sparse commit→parent/tree edge map** parsed from the pack (the latter feeds D25). The brain stays OID-only and never touches delta encoding. **Caveats (quorum-reviewed):** the thicken/resolve algorithm lives **once** in `internal/pack` (`Ingester`), parameterized over `ObjectStore` and injected into each backend's `PackEngine.Ingest` — never reimplemented per backend (divergence risk); `Ingest` has read access to the existing object store; it **caps delta-chain depth and detects cycles before resolving** (delta-bomb / circular-ref DoS guard); ingest is **all-or-nothing atomic** (a partial ingest must not leave the store inconsistent). A genuinely-missing base surfaces as an ingest error. Refines D18. |
| D24 | **Shallow / partial-clone split.** The **negotiation** domain (brain) owns the `shallow`/`deepen`/`deepen-since`/`deepen-not`/`deepen-relative`/`unshallow` wire exchange and is **stateless per HTTP request** (the client re-declares its full shallow boundary every request — the correct git-v2 model; no server-side per-client session state, consistent with the stateless scale brain, D14). The depth/time-bounded **frontier computation** is a **GraphIndex query**, not a brain-side DAG walk. **Caveats (quorum-reviewed):** the GraphIndex method is a **general bounded-ancestry-walk primitive** (ancestors-from-roots with a depth/stop bound), not a shallow-only method — it must accept the client-declared shallow set, `deepen-relative`, a `deepen-since` bound using **committer** timestamps (not author), and a `deepen-not` exclusion set; the computed frontier must **propagate downward into pack-object selection** (a shallow boundary disables/limits bitmap acceleration in the backend); the handler **caps `depth` / `deepen-since`** to bound the stateless recompute (DoS). A single protocol-v0 negotiation buffers state for the connection's duration; "stateless" is between requests/connections. |
| D25 | **Connectivity check couples to `ObjectStore`, not `GraphIndex` — via a batched boundary check.** After ingest and before the final ref-CAS, the push path (brain) verifies the pushed objects form a complete subgraph. It needs the **full** object graph (commit→tree→blob), which the commit-DAG-only `GraphIndex` (Tier 1) cannot supply — so it couples to `ObjectStore`, correcting the earlier "couples to GraphIndex" open thread. **It must NOT call `ObjectStore.Has/Get` per object** (the chatty per-object antipattern the seam forbids — O(pack-size) round-trips, ruinous on FDB+Scylla). Instead: the brain walks the **sparse pushed subgraph in-memory** (the edge map returned by `Ingest`, D23), computes the **boundary** (referenced OIDs not in the push-set), and issues a **single batched `ObjectStore.HasMany(boundary)`** — O(1) round-trips regardless of push depth. The check→ref-CAS window is protected from GC by the **D15 grace-TTL/pin** (closes the check-then-CAS TOCTOU). *Promisor note:* if a partial-clone/promisor **backend** is ever supported, `HasMany` must count promised objects as present (else false rejects); MVP origin-server backends hold no promised objects, so this is out of scope now. |
| D26 | **Event taxonomy pinned (git-native + steve split).** git-server's committed slip-stream events are `push.received` (the atomic envelope carrying the `ref.created/updated/deleted` line items, D4) and repo lifecycle (`repo.created/archived/deleted/renamed`), each with a paired current-state query (lifecycle ↔ `repo.get` / `repo.list`, D4 Rule 5). **Job progress is not a slip-stream event** — long-running admin/async jobs stream via **steve admin-attach** (ADR-14), because a global job has no per-repo partition key (Rule 2); only a job's terminal effect reaches slip-stream. Exact event payload field names are pinned during `openapi.yaml` authoring; any event names/payloads in `problem-domains.html` §18/§19 beyond this decision are **explicitly proposals**, not recorded decisions. |
