# Slip-Stream v1 Blueprint

Single product + technical design for the first version of Slip-Stream. The settled
inputs are `docs/research/decision-ledger.md` (D1–D17),
`docs/research/architecture-inheritance.md`, `docs/research/framework-integration.md`,
and `docs/research/adr-disposition.md`; the domain language is `docs/CONTEXT.md`. This
Blueprint resolves the eight open questions those documents left plus the consumption
model worked out in review, and is the primary input to `plan-from-context`.

> **Note on the consumption model.** The `blueprint-review` discussion replaced the
> research baseline's stream-assigned, client-epoch consumption design with a
> firehose-plus-unary-implicit-ack model. This **revises ledger D4, D5, and D7** (see
> the Decision Log). Where this document and those research notes disagree on
> consumption, this document wins.

## Objective

Build an HTTP event-streaming service: immutable events appended to partitioned,
replayable logs, consumed by offset. Slip-Stream is the **complement** to Querator
(lease-based work queues) — it moves *facts*, Querator moves *work*. The technical goal
is a "dumb broker, simple client": the server sequences writes, serves reads, and owns
all consumption coordination, so clients stay trivial (ADR-0002). v1 targets simplicity
and operational efficiency over raw throughput.

## Core Usage Pattern

Slip-Stream mirrors Kafka's *model* — a partitioned, replayable, offset-addressed log —
while deleting Kafka's *client complexity* and its *operational footprint*. It is built
for teams that want the log abstraction without running Kafka.

**The operational thesis: no new database to deploy.** Standing up Kafka means operating
another stateful distributed system (historically plus ZooKeeper) purely to get a log. In
a small or moderate environment that is a large, dedicated operational burden — often for a
fraction of Kafka's throughput capacity. Slip-Stream instead rides on a database you
*already run*: today MongoDB. You get the Kafka log model without adding Kafka to your
deployment.

**Storage is an interface, so the "already run" backend is not fixed to MongoDB.** The
data plane is defined by two backend-agnostic interfaces (`store.Streams` for control-plane
metadata, `store.PartitionStore`/`Partition` for the append-only log), injected at process
entry. MongoDB is the first production realization (document-per-batch, `deleteMany`
retention); the same interfaces admit a SQL realization (a row per batch keyed by
`(partition, offset)`, range scans, `DELETE … WHERE offset < floor`). Adding **Postgres,
MySQL, or another store** is implementing those interfaces — not a redesign — so an
operator can run Slip-Stream on whatever database their environment already has. (Querator,
the inherited skeleton, already ships InMemory/Badger/Postgres/Mongo backends — D16, ADR-0016.)

### Use it for

- **Internal domain-event distribution within a trusted network** (the bullseye): service
  A emits facts; teams B/C/D each consume at their own pace, in their own language, with
  replay on redeploy — and none of them needs Kafka-client expertise. The consumer is
  `consume → process → repeat`, echoing an opaque token.
- **Audit / activity logs** — append-only, replayable, retention-bounded.
- **Fan-out of facts** — every subscription sees every event (contrast Querator's competing
  consumers, which distribute *work*).
- **Decoupling services via events** — many simple polyglot consumers over plain HTTP, no
  client library to vet, no rebalance protocol to operate.
- **Replacing a Kafka deployment in small/moderate environments** — when you adopted Kafka
  for the log abstraction, not its scale, and the operational cost (a dedicated cluster +
  heavyweight clients) outweighs the throughput you actually use.

### Do not use it for

- **Very high throughput firehoses** (≫ what a single node over a shared database sustains)
  — Slip-Stream accepts a throughput ceiling in exchange for simplicity (ADR-0002); reach
  for Kafka.
- **Exactly-once into transactional sinks** — refused here (D9); use Kafka EOS / a stream
  processor, or the Querator bridge.
- **Per-message work distribution with retries / dead-letter** — that is Querator's job
  (D1); Slip-Stream moves facts, not work.
- **Sub-millisecond delivery latency** — the coordinated path is a long-poll.

### What "simple client" does and does not mean

It means simple *coordination*: the client never picks partitions, tracks offsets, runs an
assignor, handles rebalance callbacks, or maintains a heartbeat separate from its poll. It
does **not** mean *zero correctness obligations* — because delivery is at-least-once,
consumers must be idempotent or deduplicate on the pass-through Event ID (D2, D9). This is
the same obligation Kafka imposes on at-least-once consumers; Slip-Stream removes Kafka's
coordination complexity, not the duplicate-tolerance contract.

## Mental Model

The canonical model is the `docs/CONTEXT.md` glossary; it is authoritative. The
load-bearing relationships:

- A **Stream** is one or more **Partitions**. Users address streams; partitions are an
  implementation detail exposed for operational control (never hidden).
- An **Event** lives at exactly one **(Partition, Offset)** forever, until **Retention**
  deletes it. Offsets are dense per-partition `int64`, assigned by the partition's
  single writer.
- A **Subscription** is durable per-partition offset state attached to one stream. A
  **Consumer** is a client session within a subscription; the server grants it exclusive
  **ownership** of a set of partitions and tracks its progress.
- A **Logical** is the single-writer goroutine that owns all writes for its set of
  partitions. Reads of committed data bypass it.

There are **three read surfaces**, two anonymous and one coordinated:

| Surface | Endpoint | Coordinated? | Shape |
|---|---|---|---|
| **Fetch** | `/events.fetch` | no | unary, stateless read at an offset (curl, polling) |
| **Stream** (tail/replay) | `/events.stream` | no | server-push firehose from a start position |
| **Consume** | `/events.consume` | yes | unary long-poll; server tracks position via implicit ack |

Anonymous surfaces require no subscription and hold no ownership. Coordinated consume is
where assignment, progress, and rebalancing live — and it is *unary*, so liveness is the
poll cadence, not a fragile long-lived connection.

## Core Design Principles

1. **Streams are not queues.** No leases, acks, per-event retries, redelivery, or
   dead-letter machinery. Per-event delivery semantics belong to Querator; the escape
   hatch is the bridge pattern. (D1)
2. **At-least-once everywhere; consumers are duplicate-tolerant by contract** — *and
   duplicates are minimized to failure cases* (see Behavioral Constraints). (D2)
3. **The server holds complexity; clients stay simple.** A consuming client never picks
   partitions, tracks offsets, or coordinates with peers — the server does. (ADR-0002)
4. **Offsets are immutable addresses.** An event never moves. (D10)
5. **Reads never contend on the writer.** Committed data is read concurrently. (D13)
6. **Spec-first.** `openapi.yaml` + `fieldmap.lock` are the source of truth; generated
   client/server/proto are never hand-edited. (D15)
7. **Domain boundaries are opaque.** A problem domain must not contain logic that belongs
   to a domain it calls. The caller depends on the callee's interface contract — never on
   how the callee fulfills it. If a domain needs a guarantee from a dependency, that
   guarantee is a contract of the interface (every implementation provides it); the caller
   never branches on which implementation is active, inspects implementation-specific error
   types, or relies on an implementation-specific property for correctness. The canonical
   case is storage: the Logical (writer domain) calls `store.Partition` and must never
   branch on whether the backend is MongoDB or InMemory. The same rule applies at every
   boundary — service → Stream, Stream → Logical, Daemon → everything. A convenient `if`
   today is a correctness bug when the next implementation ships.

## Correctness Constraints

### State Invariants

Each invariant names what violates it, how it is enforced, and whether enforcement is
**structural** (the design makes violation unrepresentable) or **application logic**
(code must get it right — needs heavier test coverage).

1. **Single-writer per partition.** For any partition, exactly one goroutine — the
   owning Logical's request loop — performs appends, offset advances, ownership
   transitions, and retention truncation. *Enforced* structurally: the only path to a
   write is `requestCh`. Reads do not go through the loop; what they may observe is bounded
   by Invariant 3 (visible head) and Core Design Principle 5 (D13).

2. **Dense offsets, no gaps.** A partition's offsets are a dense `int64` sequence.
   *Enforced* by application logic: the loop owns the counter and advances it only after
   a durable `Append` ack; a failed append leaves the counter unchanged and returns a
   retryable error (duh 454). Offsets are writer-assigned, never user-supplied
   (structural).

3. **Visible head.** A reader may observe any offset in `[retention_floor, visible_head)`,
   never at or above the **visible head**. Offset semantics are exclusive throughout:
   `visible_head` is the next offset to be assigned, `committed` is the next offset to
   deliver, and `retention_floor` is the earliest readable offset — so `lag = visible_head
   − committed` counts unread events. *Enforced* by application logic: the writer publishes
   the visible head (atomic store) only after the durable append; readers bound reads by it
   and never observe partial batches.

4. **Monotonic committed offset.** A subscription's committed offset for a partition
   never decreases except by an explicit administrative set. *Violated by* a stale or
   duplicate progress advance moving a cursor backward. *Enforced* by application logic:
   the commit path (implicit ack, below) rejects any advance ≤ the current committed
   offset. This single rule subsumes the zombie-rewind protection that assignment epochs
   provided in the research baseline.

5. **Ack commits only owned partitions.** A consumer's implicit ack advances offsets
   *only* for partitions that consumer's session still cleanly owns at ack time.
   *Violated by* a consumer advancing a partition it has been rebalanced off of (which
   could mark events processed that the new owner has not confirmed). *Enforced* by
   application logic: the Logical checks the session against the current ownership map
   before advancing; ownership changes and acks are serialized through the same loop, so
   the check is race-free. This is the fence that makes a client-visible epoch
   unnecessary.

6. **Exclusive ownership.** Within a subscription, a partition is owned by at most one
   consumer session at a time. *Enforced* structurally: ownership *release* and
   *acquire* are serialized through the partition's Logical (single-writer), so a clean
   handoff has no overlap window.

7. **Retention-vs-reader safety.** Truncation never yields a torn read. *Enforced*
   structurally by a **universal read-floor**: the single writer raises the read-floor
   (atomic store) *before* it deletes from the backend and evicts from the tail, and every
   read floors against it (resolution step 2). The guarantee is the same for every backend —
   it does not depend on a per-backend atomicity property, so there is no backend conditional
   in the read path.

8. **Tail coherence.** A reader observes a block via the in-memory tail only if its offsets
   lie below the visible head. *Enforced* structurally by publish order: the writer appends
   durably, inserts the block into the tail, *then* advances the visible head; readers bound
   by the head first (Invariant 3), so they can only request offsets the tail already holds.
   No torn read, no "visible but uncached" window. Blocks are immutable (D10), so a cached
   block never needs invalidation — only eviction.

9. **Tail-vs-retention eviction.** A truncated block leaves the tail and the backend with no
   window where a reader sees a freed block. *Enforced* structurally: the same single writer
   owns the read-floor raise, the backend delete, and the tail eviction, serialized through
   the one loop (extends Invariant 7).

10. **Bounded tail memory.** The tail holds at most a per-partition **byte budget**; cold
    history stays in the backend. *Enforced* by application logic: the writer evicts oldest
    blocks once the budget is exceeded (they remain in the backend until retention deletes
    them). Memory is `O(byte_budget × owned_partitions)`, never `O(log_size)`. A byte budget,
    not a block count or time window, is the only bound that caps RAM directly under large
    events or bursts.

### Behavioral Constraints

- **Never deduplicate produces.** The optional Event ID is passed through verbatim. (D9)
- **Duplicates only when a batch was not processed.** A server-driven rebalance, a consumer
  join, and a graceful leave via `/events.release(commit)` all hand partitions off cleanly —
  after the outstanding batch is acked — so they deliver **no duplicates**. Redelivery
  happens only when a batch was *not* processed: consumer **crash/timeout** (force-released
  after the assignment timeout) or an explicit **`/events.release(return)`** (immediate
  hand-back). The server must not redeliver during a clean handoff.
- **Never own a live partition twice.** (Invariant 6)
- **Never drop active firehose streams silently on shutdown.** `BeforeShutdown` emits a
  final frame so clients reconnect elsewhere at their last offset. (T10)
- **Never hide partitions from clients.** Partition identity and per-partition offsets
  are part of the contract. (inherited Querator ADR-0020)
- **Keyed events with the same Stream Key always route to the same partition; in v1 the
  partition count is immutable, so this mapping never changes.** (D8)
- **A subscription whose committed offset falls below the retention floor loses data,
  loudly.** Observable, never silent. (D12)
- **A seek on a partition with a live owner force-releases that owner's session.** The
  seeked offset takes effect on the next consume rather than being silently undone by the
  evicted session's pending implicit ack; the released session sees a stale-token 409 and
  rejoins. Same force-release path as the assignment timeout, same at-least-once
  consequence: the session's unacked batch redelivers.
- **A session ended by a forced delete sees 404 on its next call** — the resource is gone,
  so the response is not-found rather than the stale-token 409 (which would invite a futile
  rejoin). With `IMPLICIT_SUBSCRIPTION_CREATE` enabled, a subsequent token-less consume
  naming the deleted subscription materializes a *new* subscription at `latest`; callers
  should handle the 404 deliberately rather than blind-rejoin.

## Acceptance Criteria

Each is mechanically verifiable through the public API.

1. Producing N events returns N dense, monotonically increasing offsets per target
   partition; a subsequent fetch from offset 0 returns exactly those events in order.
2. Two events with the same Stream Key always land in the same partition; across the v1
   lifetime of a stream (immutable partition count) this never changes.
3. A keyless produce lands only in a non-degraded partition; over many produces, keyless
   events spread across healthy partitions; no produce is rejected solely for lacking a
   key.
4. A produce batch exceeding a server cap is rejected with a 400-class error and writes
   nothing.
5. A failed append advances no offset; the client receives a retryable error and a retry
   succeeds with the next dense offset (no gap).
6. A produce that targets a degraded partition: keyless events route to healthy
   partitions; keyed events are held until the `PRODUCE_TIMEOUT` deadline expires and, if
   the partition is still unavailable, that subset returns a retryable error while other
   partitions' events succeed with assigned offsets.
7. An anonymous fetch or stream returns immutable historical events from any start
   position (earliest / latest / at-offset). A requested offset below the retention floor
   clamps to the earliest available offset, and the response reports the actual start
   offset; no subscription and no server-state change.
8. A consumer's first `/events.consume` returns an opaque `consumer_token` and a batch
   from its server-assigned partitions; the client supplies no partition or offset.
9. Each subsequent `/events.consume` (same token) implicitly acks the prior batch —
   advancing the subscription's committed offsets for partitions the session still owns —
   and returns the next batch.
10. When a second consumer joins a subscription, partitions rebalance toward a balanced,
    sticky assignment; a partition moved between consumers is handed off only after its
    current owner acks, and the joining consumer receives no event the previous owner had
    already acked (no duplicates on normal rebalance).
11. A consumer that stops calling `/events.consume` past the assignment timeout has its
    partitions released and reassigned; its uncommitted outstanding batch is redelivered
    to the new owner.
12. A consumer that calls `/events.release(commit)` acks its outstanding batch and releases
    its partitions; a successor receives no event the leaver processed (clean leave, no
    duplicates).
13. A consumer that calls `/events.release(return)` releases its partitions without
    committing the outstanding batch; that batch is redelivered to a successor immediately,
    not after the assignment timeout.
14. A stale `consumer_token` (session timed out or released) is rejected; the client rejoins
    by calling `/events.consume` with no token.
15. Retention by age and/or size (size = summed raw `Payload` bytes) truncates the oldest
    events; `.stats` reports the floor.
16. `/subscriptions.stats` reports per-partition `visible_head`, `committed`, `lag`, and
    `retention_floor`, and `data_loss_detected` true exactly when `committed <
    retention_floor`.
17. `/subscriptions.seek` administratively repositions a subscription's committed offsets
    for one or more partitions (reset/rewind/skip). Any live session owning a seeked
    partition is force-released — its pending implicit ack is void and its next call gets a
    stale-token 409 — so consumption resumes from the new positions, never silently
    overwritten by an in-flight ack.
18. On graceful shutdown, active firehose streams receive a final frame (not a silent
    EOF) and clients reconnect at their last offset against a restarted instance.
19. Deleting a stream that has active consumer sessions or live subscriptions is refused
    with a conflict unless `force` is set; a forced delete ends those sessions and removes
    the stream and all its data. A stream with no active consumers and no subscriptions
    deletes without `force`. (Translates Querator's `force`-on-active-leases guard to log
    semantics: the guard is "in active use," not "holds data" — a log nearly always holds
    data.)
20. Deleting a subscription that has active consumer sessions is refused with a conflict
    unless `force` is set; a forced delete ends those sessions and removes the subscription
    and its stored offsets. A subscription with no active consumers deletes without `force`.
21. A "dumb" consumer that calls `/events.consume` repeatedly and **never echoes the
    returned `consumer_token`** makes no progress and does not destabilize the server: each
    call is treated as a fresh join, so the committed offset never advances (no implicit ack
    without token continuity) and the same events are redelivered; the orphaned sessions are
    bounded and reaped by the assignment timeout rather than accumulating without limit.
22. Every functional test passes identically against the InMemory and MongoDB backends.
23. A `porcupine` linearizability check over a randomized concurrent produce/fetch/stream
    workload passes against the per-partition append-only-log model: every recorded history
    linearizes to dense, gap-free offsets with reads bounded by the visible head (Invariants
    1–3). (ADR-0021)
24. A `porcupine` linearizability check over a randomized concurrent consume/seek workload
    *with active rebalancing* passes against the subscription commit-register model: the
    committed offset per `(subscription, partition)` advances monotonically and only under
    live ownership, with no partition owned twice (Invariants 4–6). A seek is modeled as an
    administrative set plus a force-release of any live owner; at-least-once redelivery
    of an unacked batch is permitted by the model; ownership convergence is asserted via
    `require.Eventually` on `/subscriptions.stats`, not by the checker. (ADR-0021)
25. With C consumers on a P-partition subscription, ownership converges to within ±1
    partition per consumer; a join moves only the joining consumer's fair share (all other
    partitions keep their owner — no handoff, no duplicate) and a graceful leave or timeout
    moves only the departing session's partitions. (ADR-0022)
26. When the configured storage backend is unreachable, `/readyz` reports not-ready
    (fail-closed) so the instance is pulled from rotation rather than serving data-plane
    errors; `/healthz` still passes (the process is live).

## Success Metrics

- **Consumer lag is observable end-to-end** — operators alert on `lag` and
  `data_loss_detected` per `(subscription, partition)` via Prometheus without writing
  arithmetic. (Headline operational outcome; D12.)
- **Client integration is trivial** — produce is POST + body; coordinated consume is a
  `fetch → process → repeat` loop with no offset, partition, or ack bookkeeping;
  anonymous read is one curl-able POST.

(No throughput/latency target — v1 does not pursue performance parity, ADR-0002. A
benchmark exists only as a regression guard.)

## Scope

### In Scope (v1)

- Streams with an **immutable** partition count, explicit CRUD, `.info`/`.stats`.
- Produce: keyed (Stream Key → fixed partition via bucket routing) and keyless
  (opportunistic, degraded partitions skipped), at-least-once, optional pass-through
  Event ID and opaque `metadata` map, server-wide size caps.
- Three read surfaces: anonymous `/events.fetch` (unary at-offset) and `/events.stream`
  (firehose tail/replay); coordinated `/events.consume` (unary long-poll, implicit ack).
  All three resolve through one uniform read pipeline backed by a per-partition,
  byte-bounded, write-through **tail cache** (the writer's page-cache analog; serves the
  fan-out hot path from memory). Cold/replay misses fall through to a backend range scan;
  cold-read coalescing and a read-through historical cache are deferred.
- Subscriptions: explicit CRUD + implicit-on-default creation; server-stored offsets per
  `(subscription, partition)`; server-managed exclusive ownership with sticky,
  eventually-consistent rebalancing and graceful handoff; start positions earliest /
  latest / at-offset.
- `/subscriptions.seek` management endpoint to reposition stored offsets (current offsets
  are read via `/subscriptions.stats`).
- Retention as the sole deletion mechanism: per-stream `{age, size}` with server default
  and cap. Omitting retention at create applies the server default; an explicit zero size /
  empty age requests an unbounded dimension but is clamped by `RETENTION_MAX_*` when a cap
  is configured — unbounded is achievable only when no cap is set.
- Storage backends: **InMemory** (test matrix + dev) and **MongoDB** (production).
  Metadata and partition data may use different configured backends.
- Observability: stats through the loop, Prometheus via scaffold middleware, consumer
  lag + data-loss flag first-class.
- Spec-first toolchain: `openapi.yaml` → `duh lint`/`duh generate` → `buf generate`.

### Out of Scope / Non-Goals

- **Mutable partition count** — deferred to v2; v1 builds in bucket-indirected routing
  (internal) so adding it later is additive. (T2)
- **At-time start position** — deferred (needs a timestamp index or scan). (T4)
- Multi-tenancy / RBAC / namespaces / API keys — deferred. (D17)
- Scheduled delivery (EnqueueAt) — deferred. (D17)
- Key-shared consumption within a shared subscription — deferred. (D17)
- Clustering / leader election — v1 is single-node, but the consumption model is built to
  generalize to it (see Architecture). (D17)
- Embedded/single-binary durable backend (BadgerDB) — deferred. (D16)
- **Refused, not deferred:** server-side dedup, transactions, exactly-once. (D1, D9)

## Dependencies and Constraints

- `github.com/kapetan-io/scaffold.go` — process shell (lifecycle, bindings, config,
  middleware, shutdown). Owns `main()`. (ADR-15)
- `github.com/duh-rpc/duh.go/v2` — RPC envelope, length-prefixed streaming, retry policy,
  cursor pagination. **v2** — do not copy Querator's v1 transport code.
- `github.com/duh-rpc/duh-cli` — `duh lint` gates `duh generate` (client/server/proto +
  `fieldmap.lock`).
- MongoDB (production): pooled connections keyed by URI with refcounting,
  int64-microsecond timestamps, non-transactional / standalone-compatible.
- `github.com/kapetan-io/tackle/clock` — injectable clock.
- duh streaming constraints: no bidirectional, no client→server streaming; frames capped
  by `MaxFramePayload` (4 MiB default). These constrain only the firehose `/events.stream`
  — coordinated consume is unary, so it is unaffected.

---

## Functional — API Surface

All endpoints POST, body in / body out, paths `/{resource}.{method}` (plural, no `/v1/`
prefix — version in `servers[].url`), snake_case properties, `{Op}Request`/`{Op}Response`
schemas, cursor-only pagination on list endpoints.

```
# Streams — control plane
/streams.create        # name, requested_partitions (immutable), retention {age,size}
/streams.list          # paginated
/streams.update        # retention only (NOT requested_partitions; the spec description's unrealized "mutable metadata" phrase is dropped during the build)
/streams.delete        # force required if active consumers/subscriptions (Acceptance #19); ended sessions see 404 on their next call
/streams.info          # cheap stored metadata
/streams.stats         # cold-request: per-partition head, sizes, failures (= actual partition count)

# Subscriptions — control plane
/subscriptions.create  # name, stream, start_position {earliest|latest|at_offset}
/subscriptions.list    # paginated
/subscriptions.delete  # force required if active consumers (Acceptance #20); ended sessions see 404 on their next call
/subscriptions.info     # stored metadata + config (no .update — config is immutable)
/subscriptions.stats    # per-partition visible_head, committed, lag, retention_floor, data_loss_detected
/subscriptions.seek    # subscription, offsets[{partition, committed}] — admin reposition (reset/rewind/skip); force-releases any live owner of a seeked partition

# Events — produce
/events.produce        # keyed/keyless event batch; per-event {partition, offset} or error; unknown stream → 404 unless IMPLICIT_STREAM_CREATE

# Events — anonymous reads
/events.fetch          # unary stateless read: stream, partition, start_position, max_events, RequestTimeout
/events.stream         # firehose: stream, partition?(filter), start_position (REQUIRED) — server-push framed

# Events — coordinated reads
/events.consume        # subscription, consumer_token?, max_events, RequestTimeout — unary long-poll, implicit ack
/events.release        # subscription, consumer_token, disposition {commit|return} — end the session

```

**`/events.consume`** is the dumb-client coordinated path. First call omits
`consumer_token`; the response carries a server-issued opaque token and a batch from the
session's server-assigned partitions. Every later call echoes the token, which *implicitly
acks the prior batch* (the server advances committed offsets for partitions the session
still owns) and returns the next batch. The client never sends a partition, an offset, or
an epoch. Long-poll via `RequestTimeout`; the call cadence is the liveness signal. A stale
token returns a `rejoin` error (duh 409-class); the client retries with no token. When
`IMPLICIT_SUBSCRIPTION_CREATE` is enabled (the default), a consume naming a subscription
that does not exist materializes it at `latest` and proceeds; when disabled it returns 404
— the spec's consume 404 description gains a note about this gate during the build.
(ADR-0015)

**`/events.release`** ends a consumer session cleanly — the graceful-leave path (scale-down,
shutdown). `disposition: commit` acks the outstanding batch (advances committed offsets for
still-owned partitions) and releases the session: a successor receives nothing the leaver
processed, so leaving produces no duplicates. `disposition: return` releases the session
*without* committing the outstanding batch: that batch is handed back and redelivered to a
successor immediately, rather than waiting for the assignment timeout. Release is
session-level (it ends the whole session and its ownership), never per-event or
per-partition.

**`/events.fetch`** and **`/events.stream`** are anonymous. A requested `at_offset` /
`offset` below the partition's retention floor **clamps** to the earliest available
offset; the response/first-frame reports the actual start offset, so the gap is
self-describing rather than an error.

**Frame types** (`/events.stream` only — coordinated consume is unary): an event-batch
frame `{partition, base_offset, events[]}` and a shutdown/redirect final frame
`{resume_offset_per_partition, retry_after}`, where each resume offset is the position to
read from on reconnect (covers partitions that delivered nothing on the connection). No
assignment or epoch frames exist.

**Produce caps (server-wide, T5):** max per-event payload 1 MiB, max events/batch 1000,
max batch bytes ~3.5 MiB (below duh's 4 MiB frame). Configurable; over-cap rejected
400-class, nothing written.

**Error model:** duh.go v2 envelope and codes. Every non-200 response is a `Reply` with
`{"code": "<string>", "message": "<string>", "details": {...}}`; the "duh code" column below
is the `Reply.code` string value clients inspect programmatically. Client-readable error
messages follow the ADR-18 convention `<const message>;<variable details>` — the prefix
before `;` is stable API contract, asserted in tests with `require.EqualError`.

The v1 code-per-failure contract:

| Condition | duh code | HTTP | Retryable |
|---|---|---|---|
| Invalid request body / params; over-cap produce batch | InvalidOption | 400 | no |
| Unknown stream / subscription / partition | (not found) | 404 | no |
| Resource already exists; stale `consumer_token` (rejoin) | Conflict | 409 | no (rejoin) |
| A produce subset to a degraded partition failed by deadline | RetryRequest | 454 | yes (retry that subset) |
| Backend/internal failure | Internal | 500 | no |

(ADR port of Querator 0006.) A degraded-partition produce failure is the *only* routinely
retryable code on the data plane; the same error is returned for every event of the failed
partition (per-partition all-or-nothing).

**Pagination (list endpoints only):** cursor-only, via duh.go v2's cursor helper — a
`pagination.first` request field (page size) and `pagination.after` cursor field; an
`items` array plus `pagination.end_cursor` and `pagination.has_next_page` in the response;
no offset/limit, no total count. A `pagination.first` above the server max clamps to the
max; `has_next_page` false signals the last page. Applies to `/streams.list` and
`/subscriptions.list`.

## Architecture

The inherited Querator skeleton, translated:

```
HTTP client → duh-cli generated server.go → service.Service (implements ServiceInterface)
  → StreamsManager (registry: create/lazy-get/update/delete/shutdown)
    → Stream (Logical instances + partition index + per-subscription assignment coordinator)
      → Logical (one single-writer goroutine per partition set; requestCh → requestLoop)
        → store.Partition (log-shaped) / store.Streams (metadata)
          → InMemory | MongoDB
```

**Single-writer request loop.** Hot requests (Produce/append, the implicit-ack offset
advance carried by a consume call) drain the channel and batch into one storage write;
cold requests (stats, metadata updates, subscription admin such as seek, and the
ownership acquire/release transitions commanded by the coordinator's desired map) are
handled inline. Ported patterns: non-blocking `requestCh` send with overload→retryable
error, `inFlight` atomic for drain-on-shutdown, batch partial-reset semantics,
shutdown-deadlock prevention. Loop select arms: `requestCh`, `shutdownCh`,
`RetentionTimer`, `NextMaintenanceCh` (data-plane maintenance, e.g. deferred handoff
completion). The loop runs **no session timers** — session liveness is the coordinator's
problem domain. A blocked long-poll waits on a per-partition notification the writer
signals after advancing the visible head (subscribe before the empty-check to avoid a
missed wakeup) and enforces its own `RequestTimeout` deadline; parked reads are never
loop state.

**Read path bypasses the writer (D13).** Through the loop: appends, offset advances,
ownership transitions, retention truncation, metadata updates. Bypassing: fetch, firehose
stream reads, stats of committed data. The implicit-ack offset advance rides through the
loop (it is a write); the *event delivery* it carries is a bypassing read.

**The writer is the cache.** Each partition keeps an in-memory **tail buffer** — a
byte-bounded ring of the most recently appended blocks, ending at the visible head. The
single writer populates it as the *same act* that advances the visible head: on each durable
batch it (1) appends to the backend, (2) inserts the block into the tail, then (3) advances
the visible head (atomic). The cache is therefore **write-through by construction** — nothing
but the writer fills it — and it preserves D13, because the tail is published the same
lock-free way the head is (immutable blocks readers snapshot but never block). The block
insert and head advance are the *same event* that wakes long-poll/firehose waiters, so the
cache and the delivery-wakeup are one mechanism, not two. The cache **unit is the block**
(one produce batch, keyed `(partition, base_offset)`), identical to the storage unit, so the
tail and the backend are interchangeable answers to the same range read.

**Read-resolution pipeline (uniform across backends).** Every read — `fetch`, `stream`, and
the event delivery carried by `consume` — resolves a `(partition, from_offset)` request
through one ordered path. The surfaces differ only in who supplies `from_offset` (client,
stream start position, or the coordinator's committed offset) and pull-vs-push; the pipeline
is the same:

1. **Bound by the visible head** (Invariant 3) — never serve at or above it; a read at or
   beyond `visible_head` parks on the long-poll/firehose wakeup instead of erroring.
2. **Floor against the read-floor** (Invariant 7) — `from < read_floor` ⇒ offset-too-old; no
   backend trip.
3. **Tail covers the whole request?** If `[from, …]` lies within `[tail_floor, visible_head)`,
   serve every block from memory — no backend I/O.
4. **Otherwise → backend range scan.** Any miss sends the *whole* request to the backend (no
   split-and-stitch in v1). Cold/replay reads live here; **lag is what pushes a reader off
   the tail**, so a caught-up reader is free and the cost of deep replay is explicit.

The tail is a layer *above* the storage interface, with **no backend conditional** anywhere:
every backend, InMemory included, takes the identical path (InMemory double-stores, which we
accept as the price of one uniform read path). Steps 1–3 are pure in-memory checks; only step
4 can touch the backend. The pipeline is the **Logical's published read surface**: steps 1–3
consult writer-published state (visible head, read-floor, tail) lock-free without entering the
loop, and step 4 delegates to `store.Partition`. The Stream's consume orchestration and the
service-layer fetch/firehose handlers invoke it; no caller re-implements a step. Deferred
(documented, not built in v1): cold-read coalescing of
concurrent identical scans, a read-through LRU over historical blocks, and straddle-splitting
a part-hot/part-cold request. See `docs/storage-backends.md` for the read-amplification model
under fan-out and `read-path.html` for the rendered walkthrough.

**Consumption: declarative assignment, reconciled ownership.** This is the heart of the
service.

- A per-subscription **assignment coordinator** (in the Stream) is the **session
  authority**: it issues consumer tokens, holds the session registry, tracks each session's
  last-call time, and detects the assignment timeout. From the live sessions it computes a
  **desired** partition→consumer map that is balanced and **sticky** (minimize
  movement between rebalances). Join / graceful-leave / timeout recompute the desired map
  and notify the affected Logicals. The map is a **single-shot recompute** (ADR-0022): each
  consumer's target is `floor(P/C)`/`ceil(P/C)` (balanced within one partition), correctly
  placed partitions are never moved, and only the surplus — a departed consumer's partitions
  and an over-target owner's excess — is reassigned, with a deterministic partition-number /
  session-id tie-break. The **behaviors tests assert** are: ownership balanced within ±1
  partition; a join moves only the newcomer's fair share (others stay → no handoff, no
  duplicate); a leave/timeout moves only the departing session's partitions; cold-start
  placement is reproducible.
- Each Logical drives its partitions' **actual ownership** toward the desired map,
  asynchronously and independently of the assignment decision (eventual consistency). Per
  owned partition it is authoritative for the **ownership binding** (which session holds
  the partition) and the **outstanding watermark** (highest offset delivered but not yet
  committed): delivery records the watermark, the next call's implicit ack consumes it,
  and handoff consults it.
  - **Acquire** a newly-assigned partition at its last-committed offset.
  - **Graceful release**: a partition moving to another consumer is released only at a
    clean point — `committed ≥ watermark`, i.e. the current owner's outstanding batch is
    acked — then the new owner acquires from there. A caught-up idle owner is already at a
    clean point, so its partitions hand off immediately. No uncommitted batch is dropped,
    so a normal handoff delivers no duplicates.
- **Liveness = consume cadence.** Each `/events.consume` renews the session with the
  coordinator. The coordinator detects silence past the assignment timeout, removes the
  session, and publishes a new desired map; each affected Logical *executes* the
  force-release, serialized with acks in its loop (the session's uncommitted batch
  redelivers — the only duplicate path).
- **Fencing without client epochs** (Invariant 5): the implicit ack advances offsets only
  for partitions the session still owns; ownership and acks serialize through the same
  Logical loop.

**Consume orchestration (hosted by the Stream).** A consume call composes the domains in a
fixed sequence, and the Stream is the orchestrator — it alone sees both the coordinator
and all its Logicals without reaching through a boundary (CDP 7): (1) the service layer
resolves the subscription and hands the call to its Stream; (2) the coordinator joins (no
token → new session, recompute, publish) or renews (token → liveness + identity); (3) the
implicit ack advances each owned partition with an outstanding watermark through that
partition's Logical (fenced, Invariant 5); (4) delivery reads the session's owned
partitions via the read-resolution pipeline, parking on their notifications up to
`RequestTimeout` when empty, and each involved Logical records the delivered (outstanding)
watermark; (5) the response carries the batch and the token. `/subscriptions.seek` follows
the same shape (coordinator force-release of live owners + `SetOffset` through the owning
Logicals). The per-call data path (steps 3–5) is coordinator-free — in a cluster it stays
broker-local while the coordinator relocates to the leader (`docs/distributed-design.md`).

This desired-vs-actual reconciliation *pattern* carries over to a cluster unchanged:
"Logicals reconciling toward a desired map" becomes "instances reconciling toward a
cluster-wide map." Clustering (deferred, D17 — a future clustering ADR) is **additive, not
a rewrite**: it adds a coordination plane (a leader-elected per-subscription coordinator, a
broker→leader heartbeat that pulls the assignment, a client redirect to the partition
owner, and a monotonic fencing generation embedded in the already-opaque consumer token)
over an unchanged data plane (storage, the single-writer loop, the produce/consume API).
The client stays token-only; client/wire changes are limited to redirect handling. See
`docs/distributed-design.md` for the evolution path and the ownership-fencing analysis.

**pb ↔ internal seam (T9).** duh-cli generates `pb.*`; the internal layer keeps types for
fields the wire never sees (ownership map, session state, assignment coordinator, failure
counters). Conversion at the **service layer**.

**Shutdown (T10).** Lifecycle: OnStart → listeners open → OnListenReady → BeforeShutdown →
listeners close → OnStop → Cleaner (LIFO). On `BeforeShutdown`, the service layer drains its
active-connection registry so every live firehose stream gets a proactive final frame; new
consume sessions are refused; in-flight consume long-polls return promptly so clients
reconnect.

### Package & interface scopes

Standard mono-repo layout (`cmd/`, `internal/…`). Each interface is defined by the problem
domain it owns, not its methods — concrete method sets are an implementation concern. The
boundaries that matter:

- **`Daemon` / process shell** — owns `main()` via scaffold: lifecycle, bindings, config,
  middleware, shutdown. Its sole logic is wiring; it injects the clock and the selected
  storage backend, and exposes the test-injection surface (below). Problem domain: *process
  existence*, not stream behavior.
- **`service` (pb ↔ internal seam)** — implements the duh-generated `ServiceInterface`,
  translating wire `pb.*` types to internal types and back. It is the *only* layer that
  touches generated types; everything beneath is wire-agnostic. Problem domain: *transport
  translation and request validation*, with no state beyond a request or streaming
  connection. Connection-scoped state is its one ownership: each live firehose's
  per-partition delivery positions (what the final frame's resume offsets report) and the
  process-wide registry of active streaming connections — the registry exists so
  `BeforeShutdown` can drain it with final frames, and it sources the active-stream gauge.
- **`StreamsManager`** — registry of live `Stream`s: create, lazy-get, update, delete,
  shutdown. Problem domain: *stream lifecycle and lookup*, including the create/delete
  guard-rails (force-on-active): the Stream answers "in active use" (live consumer sessions
  via its coordinators, attached subscriptions) and StreamsManager consults that answer on
  delete — it never inspects sessions itself. It holds no per-partition state.
- **`Stream`** — one stream's `Logical` set + partition index + the per-subscription
  **assignment coordinators**. Problem domain: *a single stream's topology, the
  desired-ownership decision (ADR-0022), and consume orchestration*. The coordinator is the
  session authority — registry, liveness, assignment-timeout detection, desired map — and
  decides *who should own what*; the orchestration composes Logical writes and pipeline
  reads per consume call. The assignment decision moves no data, and the orchestration
  holds no event data of its own.
- **`Logical`** — the single-writer goroutine per partition set (`requestCh` → request loop).
  Problem domain: *serialized mutation of its partitions* — appends, offset advances,
  ownership transitions, retention — and driving actual ownership toward the desired map.
  Per owned partition it is authoritative for the ownership binding and the outstanding
  watermark, and it enforces the ack fence (Invariant 5); it *executes* the transitions the
  coordinator commands and runs no session timers. The load-bearing concurrency boundary
  (Invariant 1); its published read surface is the read-resolution pipeline.
- **`store.Streams`** — stream + subscription metadata, ordered/paginated list. Problem
  domain: *durable control-plane metadata*. Separate interface because its access pattern
  (small, listed, rarely written) differs fundamentally from partition data.
- **`store.PartitionStore` / `Partition`** — append-only log data and durable committed
  offsets per partition. Problem domain: *the data plane* — ordered append, range read,
  truncation, offset persistence. `Get()` never errors (lazy init). May bind a different
  backend than `store.Streams`.

The split between `store.Streams` and `store.PartitionStore` is the load-bearing
storage-interface decision (ported from Querator): control-plane metadata and data-plane log
have different shapes, lifetimes, and backend affinity, so they are different problem domains
and different interfaces — not one store with many methods.

**Ephemeral state, by owner.** Sessions and ownership are server memory (ADR-0019), and the
assignment below is deliberate — it is exactly the cluster seam: every coordinator row
relocates to the leader in a future cluster, every Logical row stays with the partition
owner, and nothing else moves (`docs/distributed-design.md`).

| State | Owner | Lifetime |
|---|---|---|
| Session registry (token → session), last-call time, assignment-timeout detection | subscription coordinator (in Stream) | ephemeral |
| Desired partition→consumer map | subscription coordinator (in Stream) | ephemeral, recomputed on membership change |
| Actual ownership binding per partition | Logical | ephemeral |
| Outstanding (delivered-but-uncommitted) watermark per partition | Logical | ephemeral |
| Committed offsets per (subscription, partition) | `store.Partition` | **durable** |
| Firehose delivery positions + active-connection registry | service | per connection |

## Data Design

### Event model

Derived from Querator's `types.Item` minus all lease/lifecycle state:

```
Offset     int64               // writer-assigned at append; never user-supplied
StreamKey  []byte              // optional routing key, wire field `key` (D8)
ID         string              // optional producer-supplied, opaque, passed through (D9)
CreatedAt  time.Time           // server-assigned, UTC
Encoding   string              // user hint, passed through
Kind       string              // user hint, passed through
Metadata   map[string]string   // optional opaque key/values, passed through; ≤1 KiB total
Payload    []byte
```

### Storage format (T6, MongoDB-first)

One document **per produce batch**, `_id = (partition, base_offset)`, events
protobuf-encoded within. Range reads are `_id` range scans; retention is
`deleteMany({_id: {$lt: floor}})` (atomic per document → Invariant 7).
int64-microsecond timestamps, non-transactional, standalone-compatible. Exact BSON layout
is left to the build; the document-per-batch keying is the fixed contract.

### Storage interface

```go
type Partition interface {
    // writes — called only from the request loop (Invariant 1)
    Append(ctx, batch AppendBatch, now clock.Time) error
    AdvanceOffset(ctx, sub string, offset int64) error   // monotonic; rejects offset ≤ committed (Inv 4)
    SetOffset(ctx, sub string, offset int64) error       // admin reposition (/subscriptions.seek); may move backward
    Truncate(ctx, beforeOffset int64) error

    // reads — callable concurrently (D13)
    Read(ctx, events *[]*Event, opts ReadOptions) error
    Offsets(ctx, info *OffsetInfo) error   // visible head, per-sub committed
    Stats(ctx, stats *PartitionStats, now clock.Time) error

    Info() PartitionInfo
    UpdateStreamInfo(info StreamInfo)
    Close(ctx) error
}
```

`AdvanceOffset` is the implicit-ack path (monotonic); `SetOffset` is the admin path.
Ephemeral consumption state is split by owner (see the table under Package & interface
scopes): the session registry lives in the subscription's coordinator, while per-partition
ownership bindings and outstanding watermarks live in the owning Logical. All of it is
rebuilt empty on restart — only committed offsets are durable, in the partition store.

**Metadata vs partition-data split:** `store.Streams` (stream + subscription metadata,
ordered/paginated List) separate from `store.PartitionStore`; may bind different backends;
`PartitionStore.Get()` never errors (lazy init). **Committed offsets live in the partition
store** (T7). **Partition→backend affinity** inherited (named backends + weights) — fix
the known `math.Floor` under-assignment bug in the port.

### Routing (D8, T2, F10)

- **Keyed:** `bucket = hash(StreamKey) % BucketCount`; `partition = bucketMap[bucket]`.
  `BucketCount` is a fixed virtual-bucket space (default 4096) applied from server config at
  stream creation — it is **internal**, not a create parameter on the API. In v1 the
  partition count is immutable, so `bucketMap` divides buckets evenly over partitions and
  never changes (behaviorally identical to `hash % N`). A keyed event whose target
  partition is degraded is held and retried until the `PRODUCE_TIMEOUT` deadline expires
  (batch partial-reset), then fails that subset with a retryable error — it is **never** rerouted (that would
  break per-key ordering).
- **Keyless:** opportunistic least-loaded distribution (Querator ADR-0019, `Failures ASC`
  then load); **degraded partitions are skipped** during assignment, so keyless events are
  written only to healthy partitions.
- **v2 enablement:** the bucket indirection lives entirely server-side; **nothing about
  buckets or a map version is exposed on the v1 wire**. A future `/streams.repartition`
  remaps a minority of buckets and introduces a `partition-map-version` field then — cheap
  to add via append-only `fieldmap.lock`, so reserving it now buys nothing. Across-resize
  ordering semantics are a v2 decision. (ADR 0018)

### Produce response (F10)

`ProduceResponse` reports **one result per event, positionally aligned with the request**
(result *i* describes event *i*): a success carries the event's assigned `{partition,
offset}`, a failure carries `{partition, error}` (retryable) and no offset. Because a
partition's subset is all-or-nothing, every event routed to a partition that could not be
written by the `PRODUCE_TIMEOUT` deadline carries the same error, and the client retries
exactly those events. There is no cross-partition atomicity (MongoDB is non-transactional); each
partition's batch is all-or-nothing (Invariant 2). (This is the Kinesis `PutRecords` shape;
see `produce-response-shape.html` for the rationale.)

### Invariant Preservation

- **Dense offsets / visible head (Inv 2, 3):** the loop is the only appender; it owns the
  counter and atomic visible-head (exclusive next-offset-to-assign) and advances both only
  after `Append` returns.
- **Monotonic + owned-only ack (Inv 4, 5):** `AdvanceOffset` rejects non-increasing
  offsets; the loop checks the acking session against the ownership map before advancing.
  Both run in the loop, race-free.
- **Exclusive ownership (Inv 6):** release-then-acquire is serialized through the Logical.
- **Retention vs readers (Inv 7):** `Truncate` runs through the loop; MongoDB per-doc
  atomic deletes; InMemory read-floor before dropping.

### Illegal State Analysis

Structurally unrepresentable: a user-chosen offset (absent from the produce request); a
consume request that names a partition or offset (the consume schema has neither); a
"leased"/"acked"/"expired" event (no such fields). Application-logic-enforced (heavier
test coverage): no-gap-on-failure, visible-head-after-ack, monotonic offset,
ack-only-owned, exclusive-ownership handoff, retention-vs-reader.

## Observability (T8)

Stats flow through the loop as a cold request; Prometheus via scaffold's streaming-safe
middleware. `/subscriptions.stats` exposes per `(subscription, partition)`: `visible_head`,
`committed`, `lag` (= visible_head − committed), `retention_floor`, and
`data_loss_detected` (= committed < retention_floor) — gauges; operators alert on lag or
the boolean. `/streams.stats` exposes per-partition head offset, sizes, failure state. The
current ownership map (which session owns which partition) is exposed in
`/subscriptions.stats` for operational visibility into rebalancing.

**Metrics set (Prometheus).** Beyond the stats endpoints, the service exports: produce
rate and produce-failure rate (labelled by partition-degraded vs other), consume request
rate and long-poll-timeout rate, per-subscription lag and `data_loss_detected` (mirroring
stats as scrapeable gauges), rebalance count and force-release-on-timeout count, active
consumer-session and active-firehose-stream gauges (from the coordinators' session
registries and the service layer's connection registry), and retention bytes/events
reclaimed.
Storage operations carry a backend label so InMemory and MongoDB are distinguishable.

## Service Surface (Operational)

The operational surface is distinct from the data-plane API:

- **Two bindings.** The data-plane API (`/v1/...`) listens on `API_PORT` (default 2418).
  The admin binding (`/healthz`, `/readyz`, `/metrics`) listens on port **9090** — the
  monorepo-wide convention (ADR-0020), hardcoded in code, not configurable. Keeping them
  separate means operational scraping and health probing never share the API's auth/network
  posture.
- **`/healthz` (liveness)** — process is up and the request loops are not deadlocked.
  Cheap, dependency-free; a pass means "do not restart me."
- **`/readyz` (readiness)** — gates traffic on *dependency reachability and warm state*:
  the configured storage backend(s) reachable (a MongoDB ping; InMemory always ready) and
  the `StreamsManager` initialized. **Readiness fails closed**: if MongoDB is unreachable the
  instance reports not-ready and is pulled from rotation rather than serving errors — there
  is no degrade-and-serve mode in v1.
- **Embedded mode (`MODE=embedded`).** Same image and identical HTTP contract with in-memory
  backends, no MongoDB, instant start, seeded baseline — used for local dev and as an
  in-process test dependency. The embedded instance also exposes the **test-injection
  surface** (see Testing): in-process methods to drive states the data-plane API cannot
  reach, notably forcing a partition's health to *degraded*/*healthy*. These are methods on
  the instance, not wire endpoints — degradation is organically backend-driven in
  production, so no remote "set degraded" API ships in v1 (it would graduate to an admin
  endpoint only if a real drain/maintenance use emerges).

## Configuration

Flat scaffold `ConfigProvider` keys — "keep it boring" (T12): every tunable is a single
flat key with a server default; no nested-YAML storage config. Backend-specific durability
is expressed as **one backend-agnostic key** that each backend maps to its native mechanism
(MongoDB write concern + journal, PostgreSQL `synchronous_commit`, Badger `SyncWrites`), so
the config surface stays flat regardless of `STORAGE_DRIVER`.

**Process & bindings**

| Key | Meaning | Default |
|---|---|---|
| `API_PORT` | data-plane listen port (scaffold binding `api`) | 2418 |
| `MODE` | `production` \| `embedded` (in-memory deps, seeded, instant start) | `production` |
| `IMPLICIT_SUBSCRIPTION_CREATE` | consume naming a missing subscription materializes it at `latest` (ADR-0015); `false` → 404 | `true` |
| `IMPLICIT_STREAM_CREATE` | produce naming a missing stream creates it with default config (ADR-0015); `false` → 404 | `false` |

**Storage**

| Key | Meaning | Default |
|---|---|---|
| `STORAGE_DRIVER` | partition-data backend (`inmemory` \| `mongodb`) | `mongodb` |
| `METADATA_DRIVER` | metadata backend; may differ from partition data | = `STORAGE_DRIVER` |
| `MONGO_URI` | MongoDB connection string (when a Mongo driver is selected) | — |
| `STORAGE_DURABILITY` | `sync` (durable per batch) \| `relaxed` (lazy / replication-based) — the durability lever (see `storage-backends.md`) | `sync` |

**Write path / throughput** (see `storage-backends.md` for the `batch_size / write_latency` model)

| Key | Meaning | Default |
|---|---|---|
| `PRODUCE_LINGER` | request-loop batch linger window; `0` = opportunistic drain. Raising it grows `batch_size` on high-latency backends | `0` |
| `MAX_REQUESTS_PER_STREAM` | in-flight request bound; over it, produce returns a retryable 454 (overload, non-blocking `requestCh`) | *confirm* |
| `PRODUCE_TIMEOUT` | max time a produce holds keyed events for a degraded target partition before failing that subset with a retryable 454 | *confirm* |

**Produce caps** (T5; over-cap rejected 400-class, nothing written)

| Key | Meaning | Default |
|---|---|---|
| `MAX_EVENT_BYTES` | max per-event payload | 1 MiB |
| `MAX_BATCH_EVENTS` | max events per produce batch | 1000 |
| `MAX_BATCH_BYTES` | max batch bytes (below duh's 4 MiB frame) | ~3.5 MiB |

**Consume / assignment**

| Key | Meaning | Default |
|---|---|---|
| `ASSIGNMENT_TIMEOUT` | consume-silence window before a session's partitions are force-released and reassigned | *confirm* |
| `CONSUME_LONGPOLL_MAX` | max long-poll hold per `/events.consume` / `/events.fetch` | *confirm* |
| `CONSUME_MAX_EVENTS` | server cap on a request's `max_events` (clamps above) | 1000 |

**Tail cache** (per-partition write-through buffer; Invariant 10)

| Key | Meaning | Default |
|---|---|---|
| `TAIL_BYTES_PER_PARTITION` | per-partition byte budget for the in-memory tail; the writer evicts oldest blocks past it (they remain in the backend); memory is `O(budget × owned_partitions)` | 4 MiB |

**Retention** (per-stream config overrides the default, bounded by the cap; omitted = server default, explicit zero/empty = unbounded request, clamped by the cap when one is set)

| Key | Meaning | Default |
|---|---|---|
| `RETENTION_DEFAULT_AGE` / `RETENTION_MAX_AGE` | age default and hard cap | *confirm* |
| `RETENTION_DEFAULT_SIZE` / `RETENTION_MAX_SIZE` | size default and hard cap (summed raw `Payload` bytes) | *confirm* |
| `RETENTION_INTERVAL` | RetentionTimer cadence | *confirm* |

**Routing & pagination**

| Key | Meaning | Default |
|---|---|---|
| `BUCKET_COUNT` | virtual bucket space for keyed routing (internal; immutable per stream) | 4096 |
| `LIST_PAGE_SIZE_DEFAULT` / `LIST_PAGE_SIZE_MAX` | list pagination default / max (`pagination.first` clamps to max) | 25 / 100 |

The two throughput-shaping keys — `STORAGE_DURABILITY` and `PRODUCE_LINGER` — are the levers
the storage-backend analysis turns on; the rest are caps, cadences, and bindings. Keys
marked *confirm* have their default **value** settled during implementation (the key itself
ships in v1); see Open Questions.

## Security / PII

- **Security:** v1 has no authn/authz — WireGuard network membership is the trust boundary
  (mono-repo ADR-12); multi-tenancy/RBAC deferred (D17). The admin binding
  (`/healthz`, `/readyz`, `/metrics`) is separate from the API binding.
- **PII:** Slip-Stream is payload-opaque — it stores `Payload`, `Encoding`, `Kind`,
  `StreamKey`, `ID`, and the `metadata` map verbatim and never inspects them. Retention is the only deletion
  mechanism, so the **minimum retention window is the de-facto data lifetime** — operators
  storing PII set retention accordingly. There is no selective per-event deletion.

## Testing

Testing follows the `surface-testing` skill. Every test runs through the public API via the
duh-rpc client against a real scaffold-started HTTP server; no internal functions are called
directly. **Backends are never tested directly** — there are no storage-package unit tests.
Instead, the *entire* surface suite runs once per backend, the way Querator parametrizes
`TestQueue/<Backend>/`: the suite is backend-agnostic and a backend is injected at the
process entry, so InMemory and MongoDB are interchangeable and prove parity by passing the
identical assertions.

Key surfaces:
- **Interchangeable-backend harness.** A top-level `TestStream` ranges over
  `["InMemory","MongoDB"]`; each runs the full suite under a `t.Run(backend, …)` subtree. A
  single helper, given a backend name, spawns the daemon with that `STORAGE_DRIVER` and
  returns a connected client; every test takes the client and is oblivious to which backend
  it hit. The service always runs **in-process** (`scaffold.Start`); only MongoDB itself is a
  testcontainer, so its Go-level injection surface stays reachable in both modes.
- **Entry point:** `scaffold.Start(ctx, &Daemon{Inject: ...}, nil)` → `TestBindings` on
  127.0.0.1:0 → real HTTP against `inst.Addr("api")`.
- **Substitutes:** InMemory backend in-process; MongoDB via testcontainers. No mocks.
- **Fault / state injection (the surface, not the wire).** States the data-plane API cannot
  reach are driven through **methods on the in-process instance**, which still counts as the
  surface (the same instance the test holds), not by reaching into internals. The load-bearing
  one is forcing a partition **degraded**/**healthy** — it drives the same failure-counter
  health signal the writer raises organically — so Acceptance Criteria #3 and #6 (keyless
  skips degraded; keyed held-then-454) are deterministically exercisable without a real
  backend fault and without a speculative remote endpoint.
- **Async observability:** retention truncation, ownership/rebalance changes, and lag are
  asserted via `/streams.stats` and `/subscriptions.stats` with `require.Eventually`.
- **Injectable clock** drives retention cadence, consume long-poll timeouts, and the
  assignment timeout deterministically — essential for testing graceful handoff vs.
  timeout-redelivery without wall-clock waits.
- **Parity:** InMemory and MongoDB run the identical suite (Acceptance #22); a behavior that
  passes on one and not the other is a backend bug, not a test difference.

**Invariant tests** (the `invariant-test` methodology). Starting ledger, each driven
through the public API into the violation: single-writer appends, monotonic committed
offset, ack-commits-only-owned-partitions, exclusive-ownership handoff (no-duplicates on
normal rebalance; duplicates only on timeout), visible-head read safety,
retention-vs-reader safety. The side doors here are the implicit-ack offset advance and
truncation.

**Linearizability checks** (`porcupine`, ADR-0021). Where invariant tests assert
individually-constructed interleavings, porcupine searches for an adversarial schedule that
breaks a sequential model — covering the commit-vs-rebalance races no hand-written test
enumerates. Scoped to the two layers the design claims to be linearizable, each with its own
model:

- **Per-partition log** — concurrent produce/fetch/stream histories must linearize to a
  single append-only log per partition (dense offsets, reads bounded by visible head). Proves
  Invariants 1–3.
- **Subscription commit** — concurrent consume/seek/rebalance histories must linearize to a
  monotonic committed-offset register per `(subscription, partition)`, where an ack advances
  only under live ownership. Proves Invariants 4–6, including exclusivity under concurrent
  rebalance.

The checks run as surface tests: a randomized concurrent workload through the DUH-RPC client,
each operation stamped from a **monotonic wall clock at the HTTP boundary** (distinct from the
injectable logical clock that drives internal cadence), checked with
`porcupine.CheckOperations`. The deliberately non-linearizable layers are **out of model**:
at-least-once redelivery of an unacked batch is *modeled, not flagged* (CDP 2), and ownership
convergence (ADR-0020) is verified with `require.Eventually` over `/subscriptions.stats`, not
by the checker.

## Limitations & Future Work

- **Mutable partition count (v2)** — the internal bucket indirection makes
  `/streams.repartition` additive (the `partition-map-version` field is added then, not
  reserved now); minimal key movement depends on a movement-minimizing reassignment, and
  across-resize ordering is a v2 decision. (ADR 0018)
- **At-time start position** — needs a CreatedAt index or binary-search scan.
- **Clustering / multi-node** — the desired-vs-actual ownership reconciliation pattern
  already has the right shape, and network identity is built into scaffold; clustering
  (deferred, D17) is **additive** — a coordination plane (leader-elected coordinator,
  broker→leader heartbeat, client redirect, fencing generation in the opaque token) over an
  unchanged data plane. Full evolution and fencing analysis in `docs/distributed-design.md`.
- **Durable single-binary backend (BadgerDB)** — deferred.
- **Scheduled delivery, multi-tenancy/RBAC, key-shared consumption** — deferred (D17).

## Open Questions

**Resolved in design** (no longer open): the assignment algorithm (ADR-0022), linearizability
testing (ADR-0021), the interchangeable-backend test harness and degraded-partition injection
surface, package/interface problem-domain scopes, the operational surface (`/healthz`,
`/readyz` fail-closed, metrics set, two-binding split), pagination shape (cursor-only, default
25 / max 100), and the error-code-per-failure contract.

**Tunable defaults to confirm during implementation** (not blocking — the config *keys*
ship in v1 per the Configuration table; only their default *values* are being settled):
virtual `BucketCount` (proposed 4096), produce caps (1 MiB / 1000 / 3.5 MiB), retention
default/cap, RetentionTimer interval, the **assignment timeout** (consume-silence window),
the consume long-poll cap, `MAX_REQUESTS_PER_STREAM`, the `STORAGE_DURABILITY` default
(`sync` vs `relaxed`), `PRODUCE_LINGER` (default `0` / opportunistic), and
`PRODUCE_TIMEOUT` (the degraded-partition hold window).

**Left to implementation by design** (asserted only through surface behavior, never tested
directly): the physical representation of committed offsets within the partition store, the
exact BSON document layout, the `ensureIndexes()` set, the retention-by-size computation
(running aggregate vs scan), the keyed-routing hash function (must be stable across restarts),
and the slow-firehose backpressure policy.

**External dependency:** the three `ENUM_UNSPECIFIED_VARIANT` lint errors are being resolved
upstream in the `duh lint` rule (string enums render as proto `string`, not proto enums); once
that lands, `duh generate` runs clean and produces `fieldmap.lock`. No spec change needed.

## Decision Log → ADR Map

| Decision | Resolution | ADR / Ledger |
|---|---|---|
| Positioning | Querator complement; no acks/retries/DLQ; bridge | 0004 / D1, D2 |
| Consumption model | **Firehose stream + unary long-poll implicit-ack consume; server-issued sessions** | **0019** / revises D7 (D3 server-stored offsets retained) |
| Assignment | **Server-owned, declarative desired-state + reconciled ownership, sticky, graceful handoff** | **0020** / revises D4 |
| Progress fencing | **Monotonic committed offset + ack-only-owned (no client epoch)** | **0020** / revises D5 |
| Read path | Reads bypass the writer; visible-head invariant | 0008 / D13 |
| Offsets | Immutable dense int64 addresses (inverts Querator 0004) | 0009 / D10 |
| Produce routing | Keyed→bucket→partition (held on degrade); keyless opportunistic, skips degraded | 0010 / D8, F10 |
| Produce semantics | At-least-once; no dedup; pass-through Event ID; per-partition partial response | 0011 / D9, F10 |
| Retention | Sole deletion; `{age, size=raw Payload bytes}` + default/cap | 0012 / D12, F11 |
| Anonymous reads | No subscription; below-floor offset clamps to earliest available | 0013 / D6, F7 |
| Consumption wire | Anonymous fetch + firehose stream; coordinated unary consume | 0014 / D7 (reshaped) |
| Resource creation | Explicit + gated implicit; implicit subscription starts at latest | 0015 / D11, F6 |
| Storage backends | InMemory + MongoDB; MongoDB-first | 0016 / D16 |
| Spec-first | openapi.yaml + fieldmap.lock as source of truth | 0017 / D15 |
| Partition count | Immutable v1; internal bucket indirection (map-version deferred to v2) | **0018** (written) |
| Assignment algorithm | Sticky single-shot recompute (balanced ±1, minimal movement, deterministic tie-break) | **0022** / refines 0020 |
| Shutdown | Proactive final frame on firehose streams | port (Querator 0015) |
| Failed-batch offsets | Dense, no gaps, 454 retryable | port (Querator 0014) |
| info/stats | Separate `.stats` endpoints | port (Querator 0020) |
| Linearizability testing | porcupine, scoped to per-partition log + subscription commit | **0021** |

**ADR status.** ADRs `0004–0022` are **written and Accepted** on disk. Numbers `0003`,
`0006`, and `0007` are intentionally absent: `0006` (assignment) and `0007` (epochs) were
superseded by `0020` before they were written and the epoch concept is retired; `0003` was
not carried over from the research baseline. Session-authored decisions of note:
- **0018** — immutable partition count + bucket routing.
- **0019** — implicit-ack consume with server-issued consumer sessions (replaces the
  client-commit half of planned 0014).
- **0020** — eventually-consistent graceful partition handoff (supersedes planned 0006/0007).
- **0021** — porcupine linearizability checking, scoped to the linearizable layers.
- **0022** — sticky single-shot partition assignment (refines 0020's "balanced and sticky").

`adr-disposition.md` reflects this: the retired/reshaped ADRs (0006, 0007, and the
client-commit half of 0014) were not written as originally planned.
