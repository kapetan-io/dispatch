# git-server Auth (Slice 4) Blueprint

> Design artifact for **slice 4 (auth)** of the git-server, per the project's
> design-then-build pattern (D19/D27). This blueprint feeds a separate **build
> ticket**; no server code is written here. It builds on the decisions and prior art
> recorded in [`research.md`](./research.md), which is not re-litigated below.

## Objective

Give the git-server a transport-layer authentication and coarse authorization front
end so that a repository can require a credential for reads, for writes, or for
both — while an unconfigured repository stays fully anonymous. A presented
credential is an **opaque, long-lived API key** that the server resolves to a
**server-side scope record** on every request; there is no self-describing/signed
token in the data plane. This makes revocation first-class (drop the key from the
store and the next request is denied) and keeps scope unbounded and editable in
place.

Slice 4 enforces only the **git-native** part of the permission model — `git:read`
for clone/fetch and `git:write` for push — over HTTP **Basic and Bearer**. It
revises the D21 token contract (local signature verification against identity's
public key) in favor of opaque keys + a per-request scope lookup. The wire stays
`Authorization: Basic`/`Bearer`, so a future OIDC/token-exchange credential source is
a non-breaking change the data plane never observes.

The conformance target is **phase 4** (15 cases: F-08..11/13, P-13/14/15/18,
H-10..15) — all transport mechanics, pinned by the reference oracle. (F-12 is a
phase-3 pack-fault case, not auth, and is excluded.) Ref-level write
scoping, coarse RBAC, capability-token attenuation, the real identity service, and
Layer-3 reachability enforcement are **out of scope** (see below).

## Mental Model

Authorization factors into three orthogonal axes, composed at issue time by the
identity service (out of scope) and enforced here:

```
resource selector   ×   permission category   ×   access level
(which repos)           (git, repo, pr, admin)     (none/read/write/admin)
```

A **principal** (a human, CI job, or agent) holds one or more **opaque keys**. Each
key resolves — server-side, every request — to a **scope record**:

```
scope = { principal,
          repos:  "all" | [repoID…],
          grants: [ { category, level, refs? } ] }
```

- The terse strings `git:read`, `git:write` are a **display/config shorthand**; the
  source of truth is the `{category, level}` struct, so level comparison
  (`write ⊇ read`) is structural, not string parsing.
- **git-server enforces only `git:*`.** `repo:*`, `pr:*`, `admin:*` / `org:*` are
  reserved in the model and *parsed-but-ignored* here; the graph/PR/admin surface (a
  separate enforcement point, **same credential and same scope store**) honors them
  later.
- A repository that requires no credential resolves the request to an implicit
  **anonymous** principal whose scope is exactly what the repo's anonymous policy
  permits.

The one seam that matters: git-server resolves *presented key → current scope, or
unknown/revoked* through **a single per-request interface**. The MVP backs it with an
in-memory store injected at construction; a future identity-service-backed
implementation sits behind the same interface. git-server never mints keys, never
models principals or grants, and never owns revocation policy — it only *reads* the
current scope and enforces `git op ∈ scope`.

## Correctness Constraints

### State Invariants

- **I1 — Fail-closed dispatch.** A request whose resolved scope does not permit the
  required git operation never reaches the protocol engine. *Violation:* dispatching
  upload-pack/receive-pack before the authorization decision. *Enforcement:* the
  transport calls the authorization decision point before any dispatch; a deny
  returns `401`/`403` and the protocol engine is never invoked (structural — the call
  order in the one front-end handler).

- **I2 — Anonymous by default.** An unconfigured repository permits anonymous read
  and write. *Violation:* a repo created without explicit auth policy challenging a
  request. *Enforcement:* the zero value of the per-repo policy is "anonymous allowed"
  for every operation, so the ~55 existing anonymous conformance cases seed and
  operate unchanged (structural — zero-value semantics).

- **I3 — Revocation is immediate.** A key removed from the store authorizes nothing on
  the *next* request. *Violation:* a cached authorization outliving the store
  mutation. *Enforcement:* MVP performs one per-request lookup and caches no
  authorization decision across requests (structural — no cache exists to be stale).
  This is a **contract of the resolution interface** (see Behavioral B3), not a
  property of the in-memory implementation.

- **I4 — git-server enforces only `git:*`.** A reserved-category grant
  (`repo`/`pr`/`admin`) never grants *or* denies a git operation. *Violation:*
  inspecting a non-`git` grant when deciding a clone/fetch/push. *Enforcement:* the
  authorization check consults only `git`-category grants; reserved categories are
  stored verbatim and ignored (application logic — needs test coverage: a scope with
  only `pr:write` is denied `git:read`).

- **I5 — Per-repo confidentiality is unchanged.** A key scoped to repo A cannot read
  repo B. *Enforcement:* the scope's `repos` selector gates the target repo, *and* the
  storage boundary already makes "exists in repo R's store" ≡ "belongs to R" (ADR-0006
  / D8). Auth does not weaken this and does not depend on reachability (which stays
  deferred — see Scope).

### Behavioral Constraints

- **B1 — The protocol engine never sees HTTP auth material.** Credentials are parsed
  and resolved entirely at the transport front end; the engine receives byte streams,
  never an `*http.Request` or an `Authorization` header (D16 opacity, already the
  transport's contract).

- **B2 — Anonymous access requires zero interaction where policy allows it.** An open
  repo is never challenged; `git clone`/no-op fetch of a read-anonymous repo needs no
  password (F-09, F-11, P-15).

- **B3 — Credential resolution is one per-request lookup with no hot-path round
  trip.** The resolution interface returns the *current* scope for a presented key.
  Immediate-revocation (I3) is guaranteed by this contract — a future cached,
  event-invalidated implementation must still return current truth; the caller
  (`Enforcer`) never knows which implementation is behind the interface, never
  branches on it, and never relies on an implementation-specific property (domain
  opacity — a Core Design Principle here because two implementations share this seam).

- **B4 — Auth failures are distinguishable and correct on the wire.** No credential
  where one is required, or a bad/unknown credential → `401` with
  `WWW-Authenticate: Basic realm=…` so `git` retries with credentials (H-10, H-11). A
  *valid* credential with insufficient git scope → `403` (no point re-challenging a
  good credential). Only the `401` paths are oracle-tested in phase 4; the
  `403`-for-insufficient-scope path is our own surface-tested behavior.

## Acceptance Criteria

All phase-4 conformance cases pass against the **`/mvp` adapter** as they already do
against the reference oracle (each is driven by real `git` or raw HTTP through the
public surface):

- **Fetch/auth:** F-08 (Basic-auth clone), F-09 (auth-only-for-push; anon clone
  works), F-10 (auth-only-for-objects: advertisement open, POST needs auth), F-11
  (half-auth no-op fetch needs no password), F-13 (remembers good creds / forgets
  bogus).
- **Push/auth:** P-13 (push over smart-HTTP with auth), P-14 (auth-only-for-push
  repo), P-15 (half-auth clone needs no password; push does), P-18 (password scrubbed
  from push status/reflog — client-side; passes once Basic creds in the URL are
  accepted).
- **HTTP mechanics:** H-10 (`WWW-Authenticate: Basic realm=…` on 401), H-11 (invalid
  creds → 401), H-12 (Bearer accepted), H-13 (proactive/basic auth), H-14 (real `git`
  tolerates WWW-Authenticate header-shape variation — client-side parsing, exercised
  against our single canonical challenge; see Wire behavior), H-15 (Negotiate /
  `http.emptyAuth`).

Additional criteria proven by our own surface tests (no oracle case exists — these
are forge policy, H-16/17/18 = Non-Goal):

- **Opt-in default:** every existing (phases 1–3, 5) anonymous case stays green — a
  repo with no auth policy is never challenged (I2).
- **Instant revocation:** a key valid on request *N* is removed from the store; request
  *N+1* with the same key returns `401` (I3). MVP-only surface test.
- **Reserved categories ignored:** a scope carrying only `pr:write` (no `git` grant) is
  denied `git:read` and `git:write` on a private repo (I4).
- **Basic ⇄ Bearer equivalence:** the same opaque key authorizes as the Basic password
  *and* as the Bearer token (H-12 + F-08 use the one key both ways).

Every criterion is mechanically verifiable through the HTTP surface; none requires
human judgment.

## Scope

### In Scope

- Transport-layer **authentication** over HTTP Basic + Bearer on the three smart-HTTP
  endpoints, with the `WWW-Authenticate: Basic realm=…` challenge on `401`.
- **Coarse git authorization:** `git:read` gates upload-pack (advertise + objects),
  `git:write` gates receive-pack. Repo selector (`repos: all | [id…]`) enforced.
- A **per-repo anonymous-access policy** at operation granularity (advertise / read
  / write), default all-anonymous, expressing the four conformance configs (open,
  auth-only-for-push, auth-only-for-objects, private).
- The **credential→scope resolution interface** (one per-request lookup), its
  in-memory MVP implementation (mutable, to prove revocation), and its injection via
  the `ServerConfig` / `EmbeddedOptions` DI seam (ADR-3, reusing the ENG-124 pattern).
- The `internal/auth` package with the `Enforcer` decision point, wired into the
  transport front end.
- The **shared scope model** structs (`Scope`, `Grant`, level ordering) — the source
  of truth other surfaces will import conceptually — with `git:*` enforced and
  `repo:*`/`pr:*`/`admin:*` parsed-and-reserved.
- Harness: an auth front end on the **reference adapter** (per-path Basic/Bearer gate
  → `REMOTE_USER` to the CGI) and a `ServerConfig`-injected auth mode on `/mvp`, both
  mapping the same policy so cases stay oracle-anchored.
- Two ADRs: opaque-keys-revise-D21, and the reachability-deferral trigger extending
  ADR-0006 (see below).

### Out of Scope / Non-Goals

- **Layer-3 reachability enforcement** — deferred to its own follow-up, gated on
  shared/deduped cross-repo object storage (the CFOR hazard). Existence-based
  want-validation (ADR-0006) is conformant *and* confidential while storage is
  isolated per repo. **Guardrail:** do not ship shared/deduped cross-repo object
  storage without first replacing existence-based want-validation with per-repo,
  per-request reachability scoping. This deferral + trigger is promoted into an ADR
  extending ADR-0006. *(Confirm boundary: if a future ticket adds a shared OID store,
  reachability enforcement is a hard precondition of that ticket — in scope there, not
  here.)*
- **Ref-level write scoping** — the `refs` field on a `git:write` grant is *modeled but
  not enforced* in slice 4. Protected-ref (config-driven, D21) is likewise *modeled but
  not enforced* here. Both are non-breaking to add, have no phase-4 oracle case, and
  land with the identity service that mints task-scoped keys. *(Confirm boundary: a
  key scoped `{git:write, refs:["agent/*"]}` may push to `main` in slice 4 — ref
  patterns are inert until the follow-up.)*
- Layer-1 coarse RBAC and Layer-2 capability-token attenuation (identity service).
- Graph/PR/admin op enforcement (separate surface, same scope store).
- The SSE-gateway event-authorization enforcement point (separate service).
- Short-lived / minted / exchanged tokens; the real identity service and its
  revoke-event subscription; forge-policy authz (H-16/17/18 = Non-Goal).

## Dependencies and Constraints

- **ENG-124 seam** — reuse the `ServerConfig` / `RepoConfigurer` construction-time DI
  seam (ADR-3 struct-field DI) for injecting auth config and per-repo policy.
- **Oracle policy (ADR-0001)** — correctness for every wire behavior (status codes,
  challenge header shape, Basic/Bearer handling) is pinned by `git http-backend` +
  the reference adapter's auth front end. Every phase-4 case runs green against the
  reference by definition.
- **git 2.49.0** is the single pinned oracle version (t5551, t5541, t5563).
- No new external runtime dependency: the MVP credential store is in-memory; the
  identity service is a future implementation behind the resolution interface.

---

## Functional

### Operation → required scope

The transport classifies each request into one of three git operations and maps it to
a required `git` level and to the per-repo anonymous knob that can waive it:

| Endpoint / method | Operation | Required git level | Anonymous knob |
|---|---|---|---|
| `GET info/refs?service=git-upload-pack` | advertise-read | `read` | `AnonAdvertise` |
| `POST git-upload-pack` | read-objects | `read` | `AnonRead` |
| `GET info/refs?service=git-receive-pack` | advertise-write | `write` | `AnonWrite` |
| `POST git-receive-pack` | write | `write` | `AnonWrite` |

Splitting **advertise-read** from **read-objects** is what expresses F-10
(auth-only-for-objects: advertisement open, POST gated); grouping both receive-pack
phases under one `AnonWrite` knob expresses F-09/P-15 (anon clone works, push needs
auth). The four conformance configs are:

| Config | `AnonAdvertise` | `AnonRead` | `AnonWrite` | Cases |
|---|---|---|---|---|
| open (default) | ✓ | ✓ | ✓ | phases 1–3, 5 baseline |
| auth-only-for-push | ✓ | ✓ | ✗ | F-09, F-11, P-14, P-15 |
| auth-only-for-objects | ✓ | ✗ | ✗ | F-10 |
| private | ✗ | ✗ | ✗ | F-08, P-13 |

### Authorization decision (per request)

1. The transport extracts a **presented credential** from the `Authorization` header:
   Basic → the password field (username ignored — PAT-as-password, GitHub-style);
   Bearer → the token. Absence → no credential.
2. The decision point (`Enforcer`, `internal/auth`) receives `(repo, operation,
   presented-or-none)` and the repo's anonymous policy, and returns one of:
   - **Allowed** — anonymous knob permits the operation, *or* the credential resolves
     to a scope granting the required `git` level on this repo.
   - **Unauthenticated** — the operation needs a credential and none/an unknown one was
     presented → `401` + `WWW-Authenticate: Basic realm=…`.
   - **Forbidden** — a *valid* credential lacks the required git scope → `403`.
3. The transport renders the outcome. Only on **Allowed** does it dispatch to the
   protocol engine (I1).

### Wire behavior

- **Basic and Bearer both carry the one opaque key.** The same key is accepted as the
  Basic password and as a Bearer token (H-12). This is the only auth material; there is
  no separate token type.
- **Challenge:** any `401` from an auth decision carries exactly
  `WWW-Authenticate: Basic realm="…"` (H-10). The realm string is not
  protocol-significant; `git` ignores its content. Wire goldens assert the header's
  presence/shape, not byte-identity (ADR-0002).
- **H-14 is satisfied passively.** Our server emits only the one canonical challenge
  above; H-14 confirms real `git` tolerates header-shape variation (mixed case, folded
  continuations, multiple challenges) — a **client-side** parsing property (upstream
  t5563), exercised against our single well-formed header. Neither adapter emits
  non-canonical variants; the "exactly one shape" rule is not relaxed for this case.
- **Negotiate / `http.emptyAuth` (H-15)** and **proactive auth (H-13)** are client
  behaviors; the server only needs to keep issuing the Basic challenge and accept a
  subsequently-filled credential. We do not implement `Negotiate`.
- **Password scrubbing (P-18)** is client-side (`git` scrubs the URL password from its
  own status output and reflog); it passes once the server accepts Basic creds embedded
  in the clone URL.

## Architecture

**`internal/auth` · `Enforcer`.** The single git authorization decision point. It
depends on the credential-resolution interface and a per-repo policy source; for
slice 4 it does **not** depend on `GraphIndex` — the §7-code-map row G lists
`GraphIndex` because that is the *reachability* dependency, which is deferred. The
Enforcer grows the `GraphIndex` dependency when Layer-3 reachability lands. The
Enforcer is HTTP-free: it takes a repo name, an operation, and an opaque presented
credential string, and returns an authorization outcome. It never parses HTTP.

**Transport front end (`internal/transport`).** The existing front end already owns
the HTTP guards (method, content-type, 404, 403-disabled-service, traversal). Auth is
one more guard, evaluated after routing and before dispatch: parse the `Authorization`
header into a presented credential, call `Enforcer`, and render `401`
(+`WWW-Authenticate`) / `403` / proceed. Header parsing (Basic base64, Bearer) lives
here (an HTTP concern); the resolved opaque string crosses into `auth` (B1).

**Resolution seam (drawn as a contract, per the design-ticket "draw the seam"
exception — shapes are illustrative, error categories deliberately open):** a single
per-request interface, conceptually `resolve(presented-key) → (scope, found)`.
`found=false` covers both *unknown* and *revoked* (git-server treats them identically —
both `401`); an implementation may distinguish them internally for logging, but the
caller does not branch on the distinction. The MVP implementation is an in-memory
`key → Scope` map that is **mutable** (supports add/remove) so a test can revoke a key
mid-session. The future implementation caches and drops entries on revoke events
behind the same interface. Injected at construction (ADR-3), never poked into a running
server.

**Per-repo policy.** The anonymous knobs live on the `Registry` alongside
`ServiceEnabled` (per-repo, mutable at construction/test time), read by the transport
(or by the Enforcer) when deciding whether a credential is required. Default
(zero-value) = all anonymous, preserving I2.

**Adapter mapping (harness).**
- `/mvp`: a new `EmbeddedOptions` field carries the credential store; a per-repo
  policy setter (a new optional `Server` capability, e.g. `RepoAuthConfigurer`, probed
  and skip-never-fail like `RepoConfigurer`) maps the policy onto the `Registry`.
- **reference:** the adapter's `route` front end grows the *same* per-path Basic/Bearer
  gate keyed off the same `ServerConfig` credential store + policy, and on success sets
  `REMOTE_USER` for the CGI (git http-backend delegates auth to its front web server —
  exactly the lib-httpd/Apache arrangement t5551/t5563 use). Both adapters honor every
  field, so each phase-4 case stays oracle-anchored (ADR-0001) with no skip.

The revocation test targets the `/mvp` surface only (it proves *our* decision, not a
git-wire behavior), so it may skip the reference — an invariant-style surface test, not
a conformance case.

## Data Design

The scope model is a **shared model** and a design-ticket deliverable, so it is drawn
concretely (the shapes below are the contract other surfaces bind to conceptually; the
implementor may refine field-level details):

```
Scope  = { Principal: string
           Repos:     RepoSelector          // All | explicit set of repo IDs
           Grants:    []Grant }

Grant  = { Category:  string                 // "git" (enforced); "repo"/"pr"/"admin" (reserved)
           Level:     Level                  // None < Read < Write < Admin, ordered
           Refs:      []string }             // ref-patterns; modeled, NOT enforced in slice 4

Level ordering is structural: Write ⊇ Read, so "does this scope satisfy git:read?"
is Level ≥ Read on a git-category grant, never a string comparison.
```

The **credential store** is not a schema (in-memory MVP); its *contract* is: a
mutable, per-request `key → Scope` lookup whose reads always reflect the latest
mutation (I3). There is no on-disk schema, no TTL, and no expiry clock in the MVP —
keys are long-lived and revocation is by removal, which is the entire point of the
opaque-key choice.

### Invariant Preservation

- **I1 (fail-closed):** the transport's single handler evaluates auth before dispatch;
  there is no code path from request to engine that bypasses the Enforcer call.
- **I2 (anon default):** the policy's zero value is all-anonymous; no repo-creation path
  sets a non-default policy, so created-then-unconfigured repos are open.
- **I3 (revocation):** the MVP store holds no per-request-crossing cache; `resolve` is
  called on every request. The interface *contract* (return current scope) — not the
  in-memory representation — carries the guarantee, so the future cached impl preserves
  it (B3, domain opacity).
- **I4 (git-only):** `Enforcer.Authorize` filters grants to `Category == "git"` before
  comparing levels; reserved-category grants are inert. This is **application-logic**
  enforcement (not structural), so it carries an explicit test (reserved-only scope →
  denied).
- **I5 (isolation):** `Repos` gates the target repo above the seam; the storage boundary
  gates object visibility below it. Neither depends on reachability.

### Illegal State Analysis

- **Structurally prevented:** `Level` is an ordered enum, not a free string, so an
  unrepresentable level cannot exist; `write ⊇ read` is a comparison, not a parse.
- **Application-logic (needs tests):** "a reserved-category grant must never affect a
  git decision" (I4) is enforced by filtering, not by the type system — a scope *can*
  hold a `pr:write` grant, and the code must ignore it for git ops. "Anonymous knob
  waives the credential requirement" is likewise logic, not structure. Both are on the
  acceptance-criteria test list.

## Security

- **Fail-closed** (I1) is the primary posture: an auth decision must precede every
  object-moving operation.
- **`401` vs `403`** are used precisely (B4): `401`+challenge invites `git` to supply
  credentials (missing/unknown key); `403` refuses a valid-but-insufficient key
  without re-challenging.
- **Credentials are never logged.** The opaque key is transport-sensitive; it must not
  appear in access logs, error bodies, or event payloads (aligns with git's own P-18
  scrubbing on the client).
- **No cross-repo disclosure** is introduced: auth adds a repo-selector gate on top of
  the existing per-repo storage boundary (I5). The CFOR hazard remains closed because
  storage stays isolated per repo; the reachability guardrail (Scope) prevents a future
  shared-storage change from silently opening it.
- **Bearer over cleartext:** the wire security of Basic/Bearer depends on TLS at the
  deployment boundary (WireGuard membership per ADR-12 / TLS termination); this is a
  deployment property, not a slice-4 code concern, but is noted so it is not assumed
  away.

## PII

The scope record carries a **principal** identifier (an account/agent id, for
authorization and audit). It is an opaque identifier minted by the identity service,
not a name or email, and git-server only reads it — it does not store principal
profiles. The opaque **key** is a secret credential, not PII, and is never persisted
by git-server beyond the injected store (in-memory in the MVP). No other personal data
enters this slice.

## Testing

Testing follows the `surface-testing` skill. Everything is asserted through the public
HTTP surface (real `git` clone/fetch/push, plus raw HTTP for status/header
assertions); no internal function is called directly.

Key surfaces:
- **[integration: conformance suite]** — phase-4 cases (F-08..13, P-13/14/15/18,
  H-10..15) run through `ForEachAdapterWith(t, cfg, …)` against both adapters, driven
  by real `git` and raw HTTP. The reference oracle pins correctness (ADR-0001).
- **[integration: opt-in default]** — the existing anonymous suite (phases 1–3, 5)
  must stay green with the auth code present but no policy configured (I2).
- **[unit/surface: revocation]** — MVP-only: construct a mutable store with one key,
  authorize a request, remove the key, assert the next request `401`s (I3). Observable
  entirely as the wire `401`.
- **[surface: reserved-category]** — a scope with only `pr:write` is denied `git:*` on a
  private repo (I4).
- **[surface: Basic ⇄ Bearer]** — one key authorizes both ways (H-12 + F-08).

DI / substitutes:
- **[fakes needed: credential store]** — the in-memory `key → Scope` store, injected via
  `ServerConfig.Auth` / `EmbeddedOptions`. It is production-real for the MVP (the same
  seam the identity-backed impl will replace), so it is a genuine implementation, not a
  test double.
- **[fakes needed: per-repo policy]** — set via the new `RepoAuthConfigurer` capability,
  mapped by each adapter (MVP → `Registry`; reference → per-path front-end gate +
  `REMOTE_USER`).

No async behavior, no injectable clock: revocation is synchronous and there is no TTL
in the MVP (long-lived keys are the whole point). If the future identity-backed impl
introduces caching, its staleness window becomes a testable property *of that impl*,
behind the same interface — not a slice-4 concern.

## Limitations & Future Work

- **Ref-level scoping and protected-ref** are modeled (`Grant.Refs`, protected-ref
  config) but inert in slice 4; they activate with the identity service.
- **Layer-3 reachability** is deferred with a precise trigger (shared/deduped
  cross-repo object storage); the guardrail is promoted into an ADR extending
  ADR-0006.
- **Token exchange / OIDC** is a non-breaking future: the wire stays Basic/Bearer, so
  the data plane never learns how a credential was obtained.
- **Caching + revoke-event subscription** are properties of the future
  identity-backed resolution implementation, not built here and not foreclosed.

## Open Questions

The three design decisions below were resolved by **recommendation while the author was
away** (the conservative, phase-4-anchored option in each case). None blocks the build;
each is cheap to revisit before implementation:

1. **Ref-level write control** — resolved as *coarse only* (`refs` grants and
   protected-ref modeled but unenforced in slice 4). Alternative: enforce protected-ref
   now, or full propose-vs-merge now.
2. **Anonymous-policy shape** — resolved as *operation-granular flags* (advertise /
   read / write). Alternative: a coarse public/auth-for-push/private enum (cannot
   express F-10 cleanly).
3. **Revocation proof** — resolved as *mutable store + MVP surface test*. Alternative:
   argue instant revocation structurally with no dedicated test.
