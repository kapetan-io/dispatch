# git-server Graph API (Slice 5) Blueprint

> Design artifact for **slice 5 (graph-API)** of the git-server, per the project's
> design-then-build pattern (D19/D27). The build follows on this same branch,
> mirroring ENG-126. This slice bootstraps git-server's **first contract-first
> DUH-RPC surface** — the service has no `api/` directory, no `openapi.yaml`, and no
> Taskfile today; all three land here.

## Objective

Give git-server its second HTTP surface (D12): a contract-first DUH-RPC API for
**read-only graph queries** over the same store the wire protocol serves. The wire
protocol moves packs; this surface answers questions — refs, trees, file contents,
commits, logs, diffs, merge-bases, comparisons — so that agents and the future
PR-review service can inspect repository state without cloning. These queries are
the **snapshot-first catch-up queries** of the eventing model (§2.2, D4 Rule 5):
every event consumer that subscribes-then-snapshots reads its baseline truth from
this API.

Mutations (`repo.applyDiff` / `repo.commit.create` / `repo.merge` / lifecycle) are
**slice 6**. `repo.mergeability` is also deferred to slice 6 (see Scope).

## Mental Model

Two HTTP surfaces, one store, one auth model:

- The **wire protocol** (`info/refs`, `git-upload-pack`, `git-receive-pack`) is the
  deliberate ADR-7 exception — raw git for moving objects.
- The **graph API** (`POST /v1/<subject>.<action>`, DUH Reply errors) is the standard
  house surface for reading state. It lives in `internal/graphapi` (`Service`, §7
  row E) and depends only on the `storage` seam.

The consistency model is **anchor-then-read**: a query resolves its mutable input
(a ref name, `HEAD`) to an OID once, and every subsequent read under that anchor is
of immutable, content-addressed state. Pagination cursors embed the anchor, so a
multi-page read is a consistent snapshot by construction — concurrent pushes cannot
tear it. Reads are **read-your-writes** with the wire protocol: a push whose ref-CAS
committed is visible to the next graph query (§6, one process in the single-node
MVP).

Correctness is defined by **reference git** (ADR-0001): every query with a
git-plumbing counterpart must return data equivalent to what real `git` produces for
the same repository state. ADR-0001's runtime oracle (`git http-backend`) has no
graph surface, so the differential harness (Testing) adds a *new* runtime oracle —
reference-git plumbing — to enforce that rule; same reference-git authority,
different instrument.

## Correctness Constraints

### State Invariants

- **I1 — Visibility mirrors the wire protocol.** For the same `(credential, repo,
  operation-class)`, the graph API and the wire protocol reach the same
  allow/challenge/deny decision: a repo readable over the wire is readable here; a
  repo hidden there is hidden here. *Violation:* a graph endpoint reachable with a
  credential the wire transport would reject, or vice versa. *Enforcement:* the same
  `auth.Enforcer` and the same per-repo anonymous `Policy` knobs decide both
  surfaces; the graph surface introduces no policy of its own (application logic —
  every endpoint carries an authorization test; see Architecture for the decorator
  that centralizes the check). The *decision* is shared, the *rendering* is
  per-surface: the wire renders a scope-hidden repo (`auth.Hidden`) as `403`, the
  graph API as `404 REPO_NOT_FOUND` (Security). Wire nuance: the advertise-only
  class is observable only under git protocol v0/v1 — under v2 even `ls-remote`
  fetches refs via a `command=ls-refs` POST to upload-pack, the same gated read as
  a clone.

- **I2 — The graph surface is read-only.** No graph query mutates any state below
  the seam. *Violation:* a query path reaching a write method. *Enforcement:*
  structural — `graphapi` consumes narrow, consumer-defined read interfaces
  (ref listing/resolution, object reads, graph queries); it is never handed
  `RefStore.CompareAndSwap`, `PackEngine`, or `Backend.CreateRepo`.

- **I3 — Anchored reads are immutable.** All data returned under a resolved anchor
  OID (including later pages of the same query) reflects exactly that
  content-addressed snapshot. *Violation:* a page mixing state from two anchors.
  *Enforcement:* structural — cursors carry the anchor OID; content-addressed
  objects cannot change under it. (Name-anchored listings — `repos.list`,
  `refs.list` — are the documented exception: their cursor anchors a *name*
  position, and entries created or deleted between pages may appear or be missed,
  standard cursor semantics.)

- **I4 — Read-your-writes with the wire protocol.** A push whose ref-CAS has
  committed (the client saw `report-status` success) is visible to every subsequent
  graph query on the same server. *Violation:* a fetch-after-push observing the new
  ref while `refs.list` still returns the old OID. *Enforcement:* both surfaces read
  the same store through the same seam in one process (§3.4 single-process Badger /
  the in-memory backend); MVCC snapshots are taken per request. Carries a surface
  test (push via real git → immediately query).

### Behavioral Constraints

- **B1 — Differential fidelity.** Every query with a git-plumbing counterpart
  returns data equivalent to reference git's answer for the same repository state
  (equivalence per endpoint defined in Testing). This is the correctness definition,
  not a nice-to-have: a divergence is a bug even if the output "looks reasonable".

- **B2 — git-server never models a PR** (D13). No endpoint, schema, or field in this
  contract names a PR, review, comment, or check. The graph primitives are the
  vocabulary the separate PR-review service composes.

- **B3 — Bounded work per request.** Every collection result pages (DUH cursor
  pagination, `first` ≤ 100); no endpoint walks unbounded history or an unbounded
  tree in one request. Seam crossings stay batched/bounded (the log walk is bounded
  by the page size; the diff walk descends only differing subtrees) — the chatty
  per-object antipattern remains forbidden.

- **B4 — Streaming errors precede bytes.** `files.get` (octet-stream) resolves the
  repo, rev, path, and authorization — and surfaces any error as a DUH Reply —
  before the first content byte is written (DUH unstructured-stream rule).

- **B5 — Credentials never leak.** The opaque key appears in no response body, log
  line, or error message (inherited from ENG-126; the new surface adds new places to
  get this wrong).

Concurrency: two clients querying while a push lands each see a consistent
anchor-resolved snapshot (I3) and the push becomes visible atomically (I4).
Reversibility: not applicable — the surface is read-only (I2). Partial failure: a
query that fails mid-computation returns a Reply and no partial result; the one
exception is a mid-stream `files.get` disconnect, which is an infrastructure error
the client retries from the start (DUH rule).

## Acceptance Criteria

All are mechanically verifiable through the public HTTP surface:

1. **Differential equivalence** — for each endpoint, the differential harness's
   cases pass: the API answer is equivalent to the pinned reference git's answer
   (git 2.49.0) for the same seeded repository state, per the equivalence
   definitions in Testing. Cases cover: empty repo, linear history, branched +
   merged history, annotated tags, symlinks/executable modes, binary files,
   no-newline-at-EOF files, nested trees, and multi-page results.
2. **Contract pipeline green** — `duh lint api/v1/openapi.yaml` passes with zero
   errors; `task git-server:duh-gen` and `proto-gen` produce the stubs;
   `bazel build //services/git-server/...` compiles the generated code hermetically;
   the checks-lane lint list includes git-server's spec.
3. **Auth surface** — with no policy configured, every graph endpoint is anonymous
   (ENG-126 I2 extends to this surface). With a private-repo policy: no credential →
   `401` + `WWW-Authenticate`; valid credential without `git:read` (e.g. only
   `pr:write`) → `403`; credential scoped to other repos → `404 REPO_NOT_FOUND`,
   indistinguishable from an absent repo (Security). `repos.list` returns exactly
   the repos the caller may see, never `403`.
4. **Read-your-writes** — a test pushes via real `git` and immediately observes the
   new ref/commit through `refs.list` / `commits.get` (I4).
5. **Pagination stability** — a test starts a multi-page `commits.list`, pushes new
   commits to the same branch mid-pagination, and the remaining pages reflect the
   original anchor (I3).
6. **Streaming contract** — `files.get` on a missing path returns a `404` Reply and
   zero content bytes; on a large blob it streams the exact blob bytes (hash-checked
   against `git cat-file`).
7. **Existing suite unchanged** — all wire-protocol conformance phases stay green;
   the graph surface adds no wire-visible behavior.

## Scope

### In Scope

- The **11 read-only endpoints** (Functional): `repos.list` · `repos.get` ·
  `refs.list` · `refs.get` · `trees.list` · `files.get` · `commits.get` ·
  `commits.list` · `commits.diff` · `commits.merge-base` · `commits.compare`.
- **Contract pipeline bootstrap** for git-server: `api/v1/openapi.yaml` +
  `fieldmap.lock`, the service's first `Taskfile.yml`, the hand-authored
  `api/v1/BUILD.bazel` codegen (slip-stream pattern), root `BUILD.bazel` gazelle
  directives + `stage-generated` entry, CI checks-lane lint entry.
- `internal/graphapi` (`Service`) and its wiring: `AddRPC` ahead of the wire mux on
  the one `api` binding; the auth decorator.
- **GraphIndex seam growth** (contract altitude): merge-base and an ordered,
  resumable ancestry-listing query (Architecture).
- The **differential-vs-real-git harness** as `internal/graphapi` surface tests —
  the D19 runtime-differential pattern's first implementation.

### Out of Scope / Non-Goals

- **All mutations** — `repo.applyDiff`, `repo.commit.create`, `repo.merge`,
  lifecycle create/archive (slice 6, D19).
- **`repo.mergeability`** — deferred to slice 6 with its contract. Matching git's
  answer requires the content-level three-way merge (git reports a conflict only
  when the *content* merge fails), which is the same machinery `repo.merge` needs.
  Slice 6 should build it **read-only-first** — mergeability proven against the
  `git merge-tree` oracle before the write path is wired — recovering the risk
  isolation without splitting the machinery across slices.
- **Rename/copy detection in diffs** — MVP diffs report delete+add pairs
  (`git diff-tree` plumbing default). A `detect_renames` request flag is the named,
  non-breaking extension path (Limitations).
- **Rich revision syntax** — `rev` parameters accept a hex OID, `HEAD`, a full ref
  name (`refs/...`), or a short branch/tag name resolved in git's DWIM order
  (Functional). Expression syntax (`~`, `^`, `A..B`, `:path`) is not parsed.
- **Layer-3 reachability enforcement** — stays deferred per ADR-0009 (gated on
  shared cross-repo object storage, not on any API slice).
- **Activating `repo:*`/`pr:*` scope categories** — graph reads gate on coarse
  `git:read` + repo visibility (decision below); the reserved categories stay
  parsed-but-ignored until the identity service exists to mint them.
- **Webhooks/eventing** — slice 6 (this slice ships the query half of the D26
  pairing; the lifecycle events arrive with the mutations that cause them).
- **Range requests / resumable streaming** on `files.get`.

## Dependencies and Constraints

- **ENG-126 auth** — the `Enforcer`, `Resolver`, `Scope` model, and per-repo
  anonymous `Policy` are reused as-is; this surface adds enforcement call sites, not
  policy. Same credential, same scope store.
- **duh-cli v0.7.0+** (the `go.mod` tool version) — validated during this design:
  `duh generate` fully supports unstructured octet-stream endpoints (streaming
  server method + `io.ReadCloser` client), so `files.get` is generated, not
  hand-authored. The endpoint naming below was validated against `duh lint`
  empirically (0 errors).
- **Oracle policy (ADR-0001)** — reference git (pinned 2.49.0, hermetic in Bazel) is
  the singular privileged oracle. The wire oracle (`git http-backend`) has no graph
  surface, so the differential harness gates against reference-git *plumbing*
  instead — a different runtime instrument, same reference-git authority. Third-party
  APIs (GitHub's, Gitea's) inform contract *shape*, never correctness.
- **Scaffold binding model** — `Binding.AddRPC` handlers run before the `SetMux`
  fallthrough, which is what lets `/v1/*` and the wire paths share one port with no
  transport change.
- **@pierre/diffs compatibility** (consumer requirement) — the diff contract feeds
  `parsePatchFiles` (git-standard unified patch text per file) and preserves
  no-newline-at-EOF markers; `files.get` supplies old/new blobs for its
  expand-unchanged mode.

---

## Functional

### The HTTP contract (pinned — consumers and tests bind to it)

All endpoints: `POST /v1/<subject>.<action>`, JSON and protobuf content
negotiation, DUH Reply errors. Naming validated against `duh lint`: flat plural
subjects, hyphenated actions, `<Subject><Action>Request/Response` schemas. The
§6 conceptual names map as: `repo.refs.list` → `/v1/refs.list`, `repo.mergeBase` →
`/v1/commits.merge-base`, etc. Repo scoping is a `repo` field in every request body
(no path parameters — DUH paths are static).

| Endpoint | Request (beyond `repo`) | Response (essence) | Oracle |
|---|---|---|---|
| `/v1/repos.list` | `pagination` | `items: [Repo]`, `pagination` | registry state |
| `/v1/repos.get` | — | `Repo{name, head, head_oid?}` | `git symbolic-ref HEAD` |
| `/v1/refs.list` | `prefixes?`, `pagination` | `items: [Ref{name, oid, target?}]` | `git for-each-ref` |
| `/v1/refs.get` | `name` (full ref, `HEAD`, or short name — DWIM) | `Ref` | `git rev-parse --symbolic-full-name` + resolve |
| `/v1/trees.list` | `rev`, `path?`, `recursive?`, `pagination` | `items: [TreeEntry{mode, type, oid, size?, path}]` | `git ls-tree [-r] -l` |
| `/v1/files.get` | `rev`, `path` | raw blob bytes (`application/octet-stream`) | `git cat-file blob` |
| `/v1/commits.get` | `rev` | `Commit` (below) | `git cat-file commit` |
| `/v1/commits.list` | `rev`, `pagination` | `items: [Commit]`, `pagination` | `git rev-list --format` |
| `/v1/commits.diff` | `base`, `head`, `pagination` | `items: [FileDiff]`, `pagination` | `git diff-tree -r -p --no-renames` |
| `/v1/commits.merge-base` | `a`, `b` | `bases: [oid]` (empty = none, still `200`) | `git merge-base --all` |
| `/v1/commits.compare` | `base`, `head`, `pagination` | `merge_bases, ahead_count, behind_count, items: [Commit]`, `pagination` | `git rev-list --left-right --count` + `base..head` |

Schema shapes (the request/response schemas are pinned in full during
`openapi.yaml` authoring; the fields below are the contract):

- **`Repo`** — `name`, `head` (the ref HEAD points at), `head_oid` (absent for an
  unborn branch). Deliberately minimal: lifecycle metadata (created/archived state,
  timestamps) arrives in slice 6 with the D26 lifecycle events this query pairs
  with.
- **`Ref`** — `name` (full, e.g. `refs/heads/main`), `oid`, `target` (present only
  for symbolic refs). `refs.list` covers the `refs/` namespace; `HEAD` is reachable
  via `refs.get {name: "HEAD"}` and `repos.get`.
- **`Commit`** — `oid`, `tree`, `parents[]`, `author{name, email, time, tz_offset}`,
  `committer{...}`, `message` (full, unabridged). Field values match the raw commit
  object (differential-comparable with `git cat-file commit`).
- **`TreeEntry`** — `mode` (octal string, e.g. `"100644"`), `type`
  (`blob`/`tree`/`commit` for submodules), `oid`, `size` (int64, blobs only),
  `path` (entry name; full path when `recursive`). Entries in git tree order.
- **`FileDiff`** — `status` (git letter: `A`/`M`/`D`/`T`), `old_path`, `new_path`,
  `old_mode`, `new_mode`, `old_oid`, `new_oid`, `binary` (bool), `additions`,
  `deletions`, `patch` (git-standard unified diff text for the file — `---`/`+++`
  headers plus `@@` hunks, including the `\ No newline at end of file` marker;
  omitted when `binary`). One entry per changed file, paginated by file, stable
  path order. `commits.diff` is two-dot (tree-to-tree between the resolved revs);
  triple-dot reads are composed by the client. Because `commits.compare` already
  returns `merge_bases`, the GitHub-style flow is two calls —
  `commits.compare{base, head}` then `commits.diff{base: <merge_bases[0]>, head}`
  — with `commits.merge-base` available when the ahead/behind counts aren't
  needed. `merge_bases[0]` reproduces git's three-dot base in the single-base
  case (the overwhelming common case); when `merge_bases` has more than one entry
  the histories criss-cross and there is no unique three-dot base — pick per your
  policy, as git's own `base...head` picks one and does not document which. The
  spec documents this recipe on both endpoints: the intuition study found
  composing three-dot wrongly (diffing `base..head` directly, or assuming the diff
  is inherently three-dot) was the most common semantic error.

**Revision (`rev`) contract:** a 40/64-char hex OID, `HEAD`, a full ref name
(`refs/...`), or a short name resolved in git's standard DWIM order
(`refs/<name>` → `refs/tags/<name>` → `refs/heads/<name>` → …, matching
`gitrevisions(7)`), so `main` and `v1.0` work the way git users expect. No
expression syntax. `refs.get {name}` accepts the same forms minus the hex OID —
a full ref name, `HEAD`, or a short name resolved in the same DWIM order — and
the response always carries the full name. (The API-intuition study found every
blindfolded agent sends `"main"`; requiring the full form while `rev` DWIMs
would be an internal inconsistency — see `api-intuition-study.md`.)

**Ordering contract:** `commits.list` and `commits.compare` return commits in
`git rev-list` default order (commit-date descending with git's tie-breaking); the
harness asserts exact sequences. `trees.list` returns git tree order. `refs.list`
and `repos.list` return lexicographic name order.

**Pagination:** house shape (`pagination{first(1–100, default 25), after}` /
`pagination{end_cursor, has_next_page}`). Cursors are opaque strings encoding the
anchor (resolved OID for content queries; last name for registry/ref listings) plus
a resume position (commit position for logs, path for tree walks and diffs). A
cursor is not a capability: every page request re-runs authorization; a cursor
whose anchor has been garbage-collected returns `404`.

### Error → Reply mapping

| Condition | HTTP | `code` | Notes |
|---|---|---|---|
| Unknown repo (or repo hidden by scope — see Security) | 404 | `REPO_NOT_FOUND` | same code whether absent or hidden — must not leak existence (Security) |
| Unknown ref / unresolvable rev / no such path | 404 | `REF_NOT_FOUND` / `REV_NOT_FOUND` / `PATH_NOT_FOUND` | the code is the discriminator; `details` carries the offending value |
| Cursor anchor garbage-collected | 404 | `CURSOR_EXPIRED` | re-issue the first-page query; distinct from a *malformed* cursor (400) |
| Malformed request (bad OID syntax, empty rev, path with `..`, bad cursor, `first` out of range, unknown field) | 400 | `"400"` (default) | teaching message + `details` carry the fix (agent-legibility) |
| `rev` resolves to a non-commit where a commit is required; `path`'s terminal entry is the wrong type (a tree or gitlink in `files.get`, a blob or gitlink in `trees.list`) | 400 | `WRONG_OBJECT_TYPE` | wrong-type, not missing — terminal entry only: a path descending *through* a non-tree does not exist and is `PATH_NOT_FOUND` (404) on both endpoints |
| Credential required and absent/unknown | 401 | `"401"` (default) | + `WWW-Authenticate: Basic realm=…`, exactly as the wire transport |
| Valid credential covering the repo, insufficient `git` level | 403 | `"403"` (default) | single condition — status identifies it; a credential scoped to *other* repos is `REPO_NOT_FOUND` (first row), never `403` |
| No merge base / empty diff / empty history | 200 | — | empty collections are data, not errors (matches git exit-1-no-output semantics as data) |
| Unknown `POST /v1/*` path (no RPC handler) | 404 | `"404"` (framework default) | generated catch-all; the bare numeric code is itself the signal the request never reached service code. Reply lists the valid graph endpoints (agent legibility; see Architecture) |
| Storage fault (missing object mid-walk, corrupt) | 500 | `"500"` (default) | Reply present → service error, retryable per DUH |

**The Reply `code` is the service discriminator** (see `docs/duh-rpc-cheatsheet.md`,
"Setting `code`"). Three layers, distinct jobs: the **HTTP status** governs
retry/routing; the **`code`** is the machine-branch a consumer switches on;
**`message` + `details`** teach the human/agent the fix. Where several conditions
share a status — the 404 family — each carries a distinct semantic code so a
consumer branches on `code`, never on `message` text. Where a status already
identifies its single condition (400 syntax, 401, 403, 500) the numeric default
(`NewServiceError`) is kept. Two deliberate constraints: hidden and absent repos
**share** `REPO_NOT_FOUND` (confidentiality — Security §), and the
unmatched-endpoint 404 keeps the framework-generated bare `"404"` — a
service-specific code there would be a lie, since the request never reached this
service's code. Codes are emitted with `duh.NewServiceErrorWithCode`; they are API
contract and the surface tests assert them.

Two **agent-legibility rules** shape the error bodies (evidence:
`api-intuition-study.md` — blindfolded agents miss the pagination envelope
10/10, `prefixes` 10/10, and the merge-base `a`/`b` fields 8/8; a teaching
error converts each miss into a one-retry recovery):

- **400s teach the schema.** A validation failure names what was received *and*
  what was expected: an unknown field names its closest expected field with its
  type (`unknown field "prefix"; expected "prefixes" ([]string)`); a malformed
  or flat pagination shape spells the expected envelope
  (`pagination{first int 1–100, after string}`). Error messages are API
  contract (house rule) — the surface tests assert the strings.
- **Endpoint misses self-correct.** The unmatched-`/v1/*` 404 above carries the
  full endpoint list in `details` plus a closest-match `did_you_mean` hint
  (`/v1/diffs.get` → `did_you_mean: "/v1/commits.diff"`), so a near-miss guess
  (`diffs.get`, `files.download`, `commits.mergeBase`) recovers on the next
  call. The hint matters: the recovery simulation showed a bare list leaves
  diff-vs-compare ambiguous for a "files changed" intent.

## Architecture

**`internal/graphapi` · `Service`** (§7 row E). The query engine: resolves revs,
walks trees, computes diffs, formats commits. It depends inward on the `storage`
seam only, through **consumer-defined read interfaces** (the §7 package-granularity
convention): the ref reads, object reads, and graph queries it needs — never the
write half of the seam (I2 structural). It is HTTP-free below the generated
handler and never parses `Authorization` material (the ENG-126 B1 discipline
extended): the transport-level middleware parses the header into the opaque
presented credential and passes it via context; `graphapi` hands it to the
`Enforcer`.

**Mounting.** `Daemon.OnStart` gains `api.AddRPC(generated.NewHandler(<service>))`
alongside the existing `api.SetMux(transport.New(...))`. RPC handlers match their
exact `/v1/...` paths first; everything else falls through to the wire transport
unchanged — except the `/v1/` prefix itself, which is reserved for the RPC
surface: a `POST /v1/*` matching no handler returns the 404-with-endpoint-list
Reply (Error → Reply mapping) rather than falling through. Reserving the prefix
makes a repo named `v1` (or `v1/...`) unaddressable over the wire, so repo-name
validation must reject a leading `v1` segment — the catch-all then costs the
wire protocol nothing. One binding, one port, zero wire-transport changes.

**Authorization decorator.** A wrapper implementing the generated service
interface authorizes, then delegates to the query implementation — the single
place the graph surface's auth semantics live (I1's application-logic enforcement,
kept out of every query method). Endpoint classes map onto the existing ENG-126
operations and anonymous knobs:

| Class | Endpoints | Operation ≈ | Anonymous knob |
|---|---|---|---|
| repo/ref metadata | `repos.get`, `refs.list`, `refs.get` | advertise-read | `AnonAdvertise` |
| content | `trees.list`, `files.get`, `commits.get`, `commits.list`, `commits.diff`, `commits.merge-base`, `commits.compare` | read-objects | `AnonRead` |
| registry listing | `repos.list` | — | filters (below) |

`repos.list` never denies: it returns the repos the caller may see — for an
anonymous caller, repos whose policy allows anonymous advertisement; for a
credentialed caller, additionally the repos its scope's selector covers with a
`git:read` grant. Credentialed checks require `git` category `Level ≥ Read` and the
scope's repo selector covering the target (coarse model — decision log).

**GraphIndex growth (internal seam — contracts, not signatures).** Two additions,
both commit-level Tier-1 queries answered identically by a naive walk (in-memory
backend) and an accelerated walk (scale backend, generation numbers) per D17
opacity:

- **Merge-base query** — the lowest common ancestors of two commits
  (`git merge-base --all` semantics: all best common ancestors, none an ancestor of
  another). Empty result for disjoint histories is data, not an error. Named by D17
  from the start; made live here.
- **Ordered ancestry listing** — commits reachable from a root, minus those
  reachable from an exclusion set (`git rev-list <root> ^<exclude>` semantics), in
  rev-list default order, resumable: bounded by a page size and a resume position
  so one page costs one bounded walk (B3). Serves `commits.list` (no exclusion),
  `commits.compare`'s ahead/behind lists and counts (exclusion = the other tip),
  and composes with the existing `Reachable`/`BoundedAncestry` family — a sibling
  consumer of the same Tier-1 records, not a re-cut of the seam.

**Diff computation lives in `graphapi`, above the seam** (§3.7: "diff reads the two
trees"). The tree-diff walk loads the two root trees, descends only into subtrees
whose OIDs differ (content-addressing prunes identical subtrees — the walk is
O(changed paths), not O(tree size)), classifies entries (A/M/D/T), and runs a
Myers-style line diff over the two blob payloads to emit unified hunks matching
git's default diff algorithm. Binary detection follows git's heuristic (NUL byte in
the first 8000 bytes). Blob loads use `ObjectStore.Get` — the §3.7-sanctioned
on-demand read; a batched multi-get is a named seam follow-on if the scale backend
needs it (§8), not this slice.

**Commit/tree parsing** reuses `internal/object` (`ParseCommitInfo`, `ParseTree`) —
extended, not duplicated, where the graph API needs fields the wire path did not
(author/committer identity lines, messages).

## Data Design

There is no new persistent state: every response is derived from the existing
seam state (refs, objects, commit-graph records). The pinned surfaces are the
OpenAPI schemas (Functional) and the `fieldmap.lock` proto numbering the pipeline
maintains.

Schema conventions (differential comparability drives all of them): OIDs are
lowercase hex strings; times are int64 Unix seconds with the git timezone offset
carried separately (`tz_offset`, e.g. `"-0700"`); modes are octal strings; diff
statuses are git's letters; messages and author identities are byte-faithful to
the commit object (UTF-8 assumed, invalid sequences replaced — noted in the spec).

### Invariant Preservation

- **I1** — one decorator owns every allow/deny; it delegates to the same `Enforcer`
  the wire transport calls, with the same policy source (`Registry`). A new endpoint
  cannot ship without a decorator method (the generated interface forces
  exhaustiveness at compile time). Per-endpoint auth tests (anonymous / denied /
  insufficient-category) cover the mapping table.
- **I2** — `graphapi`'s constructor accepts only read interfaces; the write methods
  are not in its dependency surface (compile-time).
- **I3** — cursors embed the anchor OID; the handler resolves names once, at page 1.
  Content-addressed immutability does the rest. The pagination-stability acceptance
  test (AC-5) proves it end to end.
- **I4** — no caching layer exists between the graph handlers and the seam; each
  request reads current MVCC state. The RYW acceptance test (AC-4) proves it over
  the wire.

### Illegal State Analysis

- **Structural:** an unresolvable anchor cannot produce a page (the cursor carries
  the OID; a bad cursor fails validation); the generated stubs make request shapes
  (types, required fields) unrepresentable-if-wrong on the wire; `files.get` cannot
  emit both a Reply and bytes (the error path returns before the stream starts).
- **Application logic (tested):** OID syntax validation, `rev` resolution order
  (DWIM), path normalization (reject `..`, empty segments), the auth mapping table
  (a `pr:write`-only scope denied `git:read`-class queries — the ENG-126 I4 test
  extended to this surface), and cursor opacity (a tampered cursor → 400, never a
  panic or a leak).

## Security

- **Same posture as the wire protocol, by construction** (I1): the graph API is a
  second door into the same rooms, guarded by the same lock. Anonymous policy,
  coarse `git:read`, repo-selector scoping, and the 401-challenge/403-insufficient
  distinction (ENG-126 B4) all carry over — with one deliberate rendering
  difference for scope-hidden repos (next bullet).
- **404 vs 403 and repo existence:** an unknown repo is `404 REPO_NOT_FOUND` — and
  so is an existing repo the caller's credential is not scoped to (`auth.Hidden`):
  absent and scope-hidden repos return byte-identical Replies, so a scope-excluded
  prober cannot learn existence. The `401` challenge remains where a credential
  could help (anonymous caller, private repo), and `403` is reserved for a
  credential whose scope covers the repo but lacks `git:read` — that caller already
  holds a grant naming the repo, so its existence is not a secret. The wire
  transport renders the same `Hidden` decision as `403` (its contract is parity
  with real-git front ends); the decision point is shared, the rendering is
  per-surface. `repos.list` filters rather than erroring, so it discloses only
  visible repos.
- **Path traversal:** `path` and `prefixes` inputs are validated (no `..`, no
  leading `/`) before any storage access — though the storage seam is
  repo-scoped and content-addressed, so traversal has no filesystem to reach; the
  validation is defense-in-depth and a `400` contract.
- **Cursor safety:** cursors are opaque but untrusted input — validated on parse,
  and never a substitute for per-request authorization (a cursor minted under
  credential A grants nothing to credential B; the decorator re-checks).
- **DoS bounds:** `first ≤ 100`, one bounded walk per page (B3), the generated
  request-size cap (5 MiB), and the existing transport limits. `commits.diff` on a
  pathological blob pair is bounded by blob size; blob loads are per-file and the
  page bounds the file count.
- **Layer-3 reachability** stays deferred (ADR-0009): storage is isolated per repo,
  so repo-visibility is the confidentiality boundary; nothing in this slice weakens
  the ADR's trigger/guardrail.

## PII

The graph API serves **commit author/committer names and emails and commit
messages** — repository content that the wire protocol already serves to any
cloner. This slice adds a finer-grained read path to the same data under the same
visibility rules (I1); it stores nothing new, aggregates nothing, and adds no
profile of any person. The auth principal handling is unchanged from ENG-126 (an
opaque identifier, never logged with credentials). No other personal data enters
this slice.

## Scale

- Monorepo-scale results page (B3); the anchor model keeps deep pagination cheap
  (resume-from-position, never re-walk-and-skip beyond one page's bound).
- The read surface shares the process with the NIC-bound wire hot path; graph reads
  are MVCC snapshot reads with no locks (§3.6), so they contend only for CPU/IO —
  and every request's work is bounded, so a graph-query storm degrades gracefully
  rather than wedging pushes.
- The scale backend inherits the surface unchanged: merge-base and ancestry-listing
  accelerate below the seam (generation numbers, D17); tree/blob reads move to
  Scylla behind the same `Get` contract, with the named batched-read follow-on if
  latency demands it.

## Testing

Testing follows the `surface-testing` skill. Everything is asserted through the
public HTTP surface: the generated client (JSON and protobuf negotiated by it)
against `gitserver.NewEmbedded`, with real `git` seeding state over the wire
protocol.

**The differential harness** (the D19 runtime-differential pattern, first
implementation) lives as `internal/graphapi` external-package surface tests
(`package graphapi_test`), per the design decision — not in the adapter-swapping
conformance suite, whose reference oracle has no graph API. Its loop, per case:

1. Build a local workspace repo with deterministic fixtures (pinned
   author/committer dates and identity, the conformance suite's convention) —
   this local repo **is** the oracle's state.
2. `git push` it to the embedded server (protocol-only provisioning, ADR-0001).
3. Run the oracle's plumbing command against the local repo (`for-each-ref`,
   `ls-tree -l`, `cat-file`, `rev-list`, `diff-tree -r -p --no-renames`,
   `merge-base --all`).
4. Call the graph API via the generated client.
5. Assert equivalence.

**Equivalence per endpoint:** exact-sequence for ordered results (`commits.list`,
`rev-list` order); set-equality for `merge-base --all`; field-by-field for parsed
objects (`commits.get` vs the raw commit object); byte-equality for `files.get`
content and for each `FileDiff.patch` against the corresponding per-file section of
`git diff-tree -p` output; count-equality for compare's ahead/behind against
`rev-list --left-right --count`.

**Oracle version:** the same pinned git 2.49.0, resolved the same way as the
conformance suite (Bazel runfiles hermetically; host `git` for plain `go test`
iteration). The harness reuses the suite's git-binary resolution and fixture-seeding
helpers rather than duplicating them.

**Red-then-green discipline:** differential cases are written against the oracle
before the endpoint exists and observed red; the reference-git side of each case is
self-validating (it exercises the local repo, not our server), so a case that can't
fail is structurally impossible — the assertion compares two independently-produced
answers.

Key surfaces:
- **[surface: differential]** — the harness above, per endpoint × fixture matrix
  (AC-1).
- **[surface: auth]** — per-endpoint anonymous/401/403/filtering tests via raw HTTP
  + the generated client against a policy-configured embedded server
  (`EmbeddedOptions.Auth` + `SetAuthPolicy`, the existing ENG-126 seams; AC-3).
- **[surface: consistency]** — the RYW (AC-4) and pagination-stability (AC-5) tests.
- **[surface: streaming]** — `files.get` error-before-bytes and byte-fidelity
  (AC-6).
- **[integration: conformance]** — the existing wire suite unchanged (AC-7).
- **[fakes needed: none]** — the embedded server is production-real (in-memory
  backend); real `git` is the test-side oracle, not a fake; no new external
  dependency, no async behavior, no clock.

When the `local` (Badger + pack files) backend lands, the same suite re-runs
against it unchanged — the harness binds to the HTTP surface and the
`EmbeddedOptions` seam, not to a backend.

## Limitations & Future Work

- **`repo.mergeability`** lands in slice 6, read-only-first against the
  `git merge-tree` oracle, then `repo.merge` wires the same machinery to the write
  path.
- **Rename/copy detection** — `detect_renames` request flag on `commits.diff`
  (adds git similarity scoring; @pierre/diffs renders renames once patches carry
  rename headers). Non-breaking addition.
- **Rich revision syntax** (`~`, `^`, `A...B`, `:path`) — extend the `rev`
  contract if consumers need it; the DWIM subset ships first.
- **Batched object reads** (`GetMany`-shaped seam addition) — named follow-on if
  scale-backend diff/tree latency demands it (§8 of the technical shape).
- **Lifecycle metadata** on `Repo` (created/archived, timestamps) — slice 6, with
  the D26 lifecycle events.
- **Range/resume on `files.get`** — DUH supports standard `Range` headers; add
  when a consumer needs partial blob reads.
- **Commit-graph acceleration** — the ancestry-listing and merge-base queries run
  as naive walks in the in-memory backend today; generation-number pruning arrives
  with the scale backend below the seam (D17), no contract change.

## Open Questions

- Whether `repos.list` visibility should later incorporate identity-service
  repo-listing semantics (Layer-1 RBAC) when that service exists — today's
  anonymous-knob + selector filtering is the coarse-model answer, revisited when
  `repo:*` categories activate.
- Whether the diff `patch` field should also be offered as a whole-diff octet
  stream (a `commits.diff` streaming variant) for very large diffs — deferred until
  a consumer demonstrates the need; pagination is the MVP answer.
