# Mechanical-Change Bot Blueprint

> **Intended path:** `docs/features/<TEAM>-<NUM>-mechanical-change-bot/blueprint.md`
> (Claude chat: produced as an artifact. Create the Linear issue and feature
> directory when you bring this into the repo; this document is the primary
> input to `plan-from-context`.)
>
> **Status:** Design complete for the load-bearing decisions; three Open
> Questions remain (none block the Phase 1 build). Milestone 0 determinism
> spike is **complete (GO)** — see the spike's `FINDINGS.md`.

---

## Objective

Let a developer perform a declared, purely-mechanical refactor across the
monorepo — primarily *moving a type (with its methods) to a different package,
optionally renaming it* — and have the change land through the merge queue
**without blocking on CODEOWNERS review**, because a machine has *proven* the
change is mechanical. Teams are notified after the fact and may file a
disagreement.

The hard problem is not the plumbing (PR creation, CI, merge queue). It is
earning the CODEOWNERS bypass safely: the system's safety rests entirely on a
machine being able to prove a given change really is mechanical. A false
positive auto-merges a semantic change across a team that explicitly walled off
its code. The entire design exists to make that proof sound, and to fail toward
human review whenever it cannot.

This Blueprint covers the **whole product** so the pieces are coherent, but
scopes the **first build (Phase 1)** to a standalone evaluator CLI with no
GitHub integration. Sharding, shimming, and GitHub orchestration are **Phase 2**,
designed here, built later.

---

## Mental Model

Three ideas, in tension, that the design reconciles:

1. **"Mechanical" is a whitelist, not a judgement.** The tool performs a fixed
   *catalog* of known-safe transforms and *proves* it stayed inside it. It never
   classifies an arbitrary diff. The proof is **regenerate-and-compare**:
   regenerate the expected change deterministically from a checked-in *intent
   file*, and compare it (at the normalized-AST level) to what is actually in the
   change. Equal ⇒ "this is exactly transform X and nothing else."

2. **Two tracks, machine-assigned.** A change is **Track A** (auto-merge, no
   human review) only if a machine predicate passes: it is a pure catalog
   transform, it is contained within the type-checked monorepo graph, and it
   touches **zero taint surfaces** (mechanisms where the compiler stops being the
   oracle). Anything failing any gate is **Track B** — the tool still does all
   the mechanical work, but the PR routes to normal CODEOWNERS review. *The human
   can never force a change into Track A; they can only assert it down into
   Track B.* That one-way rule is what keeps "notify after the fact" from
   becoming "break production after the fact."

3. **The shim decouples *packages*, never references.** A million-line move is
   split into many PRs over weeks. A *shim* — a tool-generated forwarding alias
   (`srcPkg.srcName = alias → dstPkg.dstName`) left in the source package — keeps
   the moved symbol resolving during the migration, so a **clean package** (Track A, auto-merges) and a **tainted
   package** (Track B, human review) owned by the same team can migrate
   independently without blocking each other. The shim decouples whole packages
   from one another; it never splits a package. A *team* may be partially
   migrated; a *package* never may.

### Core concepts and their relationships

- **Identity tuple** — the full identity of the moved symbol:
  `(srcPkg, srcName) → (dstPkg, dstName)`. Only `dstName` may differ from
  `srcName` (a rename-on-move); a pure same-name move has `dstName == srcName`.
  When a shim is used, it is **always** `srcPkg.srcName` — the shim name is *not*
  author-controllable and *must not* change, because every not-yet-migrated
  reference still says `srcPkg.srcName` and the shim exists precisely to keep
  those resolving. Renaming the shim would break the exact code it is there to
  support.
- **Package** — the **atomic unit** of migration (Go packages are per-directory).
  Migrated fully or not at all. Never split.
- **Shard** — the **PR / notification / review unit**. A bin-packed group of
  *whole packages*, all within **one ownership domain and one track**,
  soft-bounded by a configurable **impact cap**. A 20-package service is one
  shard (cap permitting), not 20 PRs.
- **Taint** — a reference to the moved symbol through a mechanism that observes
  its *name as data* rather than through the type system: `encoding/gob` and
  name-based reflection (`reflect.TypeOf(x).Name()` / `.String()` /
  `.PkgPath()`). A reflection-keyed registry (a DI container or string registry
  that keys on the type's reflected name) is **not** a separate mechanism — it is
  name-based reflection, caught by the reflection scanner whenever the reflecting
  code is in the corpus. `encoding/json` and protobuf are **not** taint (they key
  on field names/numbers, untouched by a move or rename).
  *[ADR needed: folding reflection-keyed registries into name-based reflection
  rather than modeling them as a first-class taint mechanism — and accepting the
  resulting third-party-dependency false-accept gap (see Limitations) — is a
  load-bearing taint-model decision that should be recorded as an ADR.]*
- **Track-A clearance rate** — the fraction of packages that auto-merge versus
  eject to Track B. The primary measure of whether the tool earns its keep.

---

## Core Design Principles

These shape every downstream decision and are the strongest ADR candidates.

1. **The verifier fails closed; the taint oracle errs broad.** A
   rewriter/verifier limitation must always fail toward **REJECT** (safe — routes
   to human review). The taint oracle has the opposite risk: a mechanism it does
   not model is a false **ACCEPT**, the one direction that breaks the bypass. So
   the taint oracle is deliberately conservative — when in doubt, flag it.
   Over-flagging only sends a safe change to Track B (mild cost); under-flagging
   breaks the bypass (unacceptable).

2. **Nothing merges that a public, logged proof did not pass.** (Phase 2.) The
   bypass is implemented as a bot that is a *legitimate CODEOWNER* approving the
   PR, **plus** an independent `mechanical-proof` required status check.
   GitHub *cannot* bypass CODEOWNERS while the merge queue is enabled (verified),
   so the rule is **satisfied**, not bypassed. The required check means even a
   human admin cannot merge an unproven diff.

3. **No transform outside the catalog.** Including shims. A *tool-generated* shim
   described in the intent is mechanical and Track-A-eligible; a *hand-written*
   shim is an unverifiable diff and ejects to Track B — not because shims are
   dangerous, but because that one wasn't regenerated and can't be regenerated.

4. **The partition is a pure function.** Shard assignment is a deterministic
   function of (package identity, ownership map, cap policy) — never a frozen
   file list, never a fixed count. Codebase drift over the migration window is
   absorbed automatically: a new package appearing mid-migration slots into the
   partition by its identity; existing assignments do not reshuffle.

5. **Stability beats packing.** When growth would push a package out of its bin,
   the bin runs slightly over cap (logged) rather than reshuffle in-flight shard
   assignments. The impact cap is a *soft target at the shard level*. The one
   hard floor: a *single package* exceeding the *whole cap* is a precondition
   violation (see Acceptance Criteria).

6. **A package is migrated fully or not at all.** Enforced structurally: the
   bin-packer treats packages as indivisible items, so no operation can produce a
   half-migrated package on `main`.

---

## Scope

### In Scope — Phase 1 (first build: the evaluator CLI)

- A standalone CLI whose surface is `Evaluate(corpus, intent) → Report`.
- The **mover engine**: AST/type-aware (`go/packages` + `go/types`), performs the
  catalog transform, rewrites all references, fixes imports, emits a canonical
  **Changeset**. The result type-checks — the compiler is the oracle for
  completeness. Compilation is verified by the same `go/packages` + `go/types`
  load the engine already performs: if the rewritten package set loads without
  type errors, the Changeset is considered compile-clean. No separate `go build`
  subprocess is invoked; a working Go toolchain in PATH is not a runtime
  requirement of `Evaluate`.
  *[ADR needed: using the `go/types` load as the compile oracle rather than a
  `go build ./...` subprocess — trading away linker-level failure coverage for a
  zero-runtime-dependency `Evaluate` and a fixed `engine-error:*` taxonomy — is a
  load-bearing completeness-oracle decision that should be recorded as an ADR.]*
- **Both `pure move` and `rename-on-move`** as single atomic transforms (the full
  identity tuple is implemented in the engine and schema from day one).
- The **verifier**: regenerate-and-compare reduced to a normalized-Changeset
  equality check, scoped to the move set's blast radius.
- The **taint oracle**: the two name-reflective mechanisms (gob registration and
  name-based reflection), conservative by default, structured as an extension
  point for additional mechanisms in Phase 2.
- The **experiment harness**: stages a corpus, applies perturbations, measures
  false-reject and true-catch rates (carries the Milestone 0 spike forward,
  extended with a rename-on-move case).
- A **minimal intent file** sufficient to drive a single whole-symbol move,
  with shard/shim/cap fields **reserved** (parsed, validated, but a Phase-1 intent
  that populates them is rejected as unsupported — see Acceptance Criteria).

### In Scope — Phase 2

- **Sharding**: deterministic bin-packing of whole packages into shards within
  (ownership domain × track), soft-bounded by the impact cap.
- **Shimming**: the shim lifecycle as a verifiable sequence of catalog transforms
  (introduce-shim → migrate-reference-set ×N → remove-shim), each independently
  regenerate-and-comparable.
- **Transition-aware taint**: the oracle tracks package path *and* base name
  independently and asserts each taint site observes the identity expected for
  the current shard's state.
- **Move-completion tracking**: a derived property — the partition recomputed
  against current `HEAD` reports zero remaining source references — that gates
  shim-removal eligibility.
- **GitHub orchestration**: bot-as-CODEOWNER approval, `mechanical-proof` required
  check, stale-approval dismissal, `pull_request` + `merge_group` analyzer wiring,
  cross-PR atomicity, notification + disagreement + revert.
- **Pre-migration linter pass** (dependency): one-time taint cleanup + a standing
  guardrail preventing *new* taint during the migration window.

### Out of Scope / Non-Goals

- **Field renames** — change the on-wire shape for JSON and require a
  deploy-aware, backward-compatibility-tracking bot. A different, larger product.
- **Dependency upgrades / import-path rewrites to a new major version** — the new
  version can introduce behavior changes; not purely mechanical.
- **Autonomous change detection** — changes are human-originated.
- **Cross-repo / public-API moves on the no-review path** — escape the monorepo's
  type-checked graph.
- **Splitting a package across shards or tracks** — a package is the indivisible
  atomic unit. (An over-cap package is a precondition violation, not a thing the
  tool sub-divides.)
- **Hand-written shims on the Track-A path** — unverifiable; ejects to Track B.

---

## Dependencies and Constraints

- **Pinned toolchain.** Canonicalization depends on `gofmt` / `go/printer`, whose
  output can shift between Go releases. The engine and the `mechanical-proof`
  check run a **pinned** toolchain; a formatter bump is a deliberate, reviewed
  change, never ambient drift. (Spike pinned `go1.26.4`.)
- **CODEOWNERS as the ownership map.** Ownership resolution uses the *same
  last-match-wins semantics GitHub applies*, so a shard's owner is exactly who
  GitHub would require review from — the notification target and the actual code
  owner are identical by construction.
- **Pre-migration linters** (Phase 2 dependency) — reduce existing taint and
  prevent new taint during the migration window. They do not guarantee zero
  taint; the oracle remains the backstop.
- **GitHub platform constraint** (Phase 2): CODEOWNERS bypass and the merge queue
  are mutually exclusive on current GitHub. The design *satisfies* CODEOWNERS via
  a bot approval rather than bypassing it.

---

## Functional

### The evaluator surface (Phase 1)

`main()` is argument-parsing and rendering only; all logic lives behind one
exported entry point:

```
Evaluate(corpus, intent) → Report
```

- **`corpus`** — a Go module/tree loadable by `go/packages` + `go/types`.
- **`intent`** — the parsed, validated intent file (identity tuple; Phase-1
  intents leave shard/shim/cap fields empty).
- **`Report`** — a *structured value*, never printed-and-grepped, so the
  determinism and fail-closed invariants are testable:
  - `Verdict` — `ACCEPT | REJECT`.
  - `Changeset` — the three normalized parts:
    - `MovedDecls` — the moved type + methods as gofmt-canonical AST text
      (doc comments travel with the declaration and are part of this).
    - `Rewrites` — for each rewritten reference, the gofmt-canonical text of the
      *nearest enclosing statement/field* (the "anchor"), not the whole
      function/file.
    - `ImportEdits` — the import **delta** (added/removed), never a file's full
      import set.
  - `Signals` — the set of gates that fired: `changeset-unequal`, `taint:gob`,
    `taint:reflect-name`, `engine-error:<reason>`. The `<reason>` namespace is
    open, but the Phase-1 taxonomy is fixed: `engine-error:typecheck` (the
    rewritten package set fails the `go/types` load), `engine-error:transform`
    (the AST/import-rewrite step itself returns an error), `engine-error:packages-load`
    (the initial `go/packages` load of the input fails), and `engine-error:internal`
    (any unclassified internal panic). This is the Phase-2 contract field: it is
    reused unchanged by the Phase-2 `merge_group` re-check.
  - `Reason` — a human-readable summary derived from `Signals`, not consumed by
    Phase 2. With one signal present, `Reason` is that signal's display string;
    with multiple, it is the signals joined in the fixed priority order
    `engine-error` > `taint:*` (alphabetical) > `changeset-unequal`. Empty on
    ACCEPT.

Two Changesets are "the same mechanical transform" iff `MovedDecls`, `Rewrites`,
and `ImportEdits` all match. `Evaluate` produces exactly one Changeset for the
corpus it receives. In Phase 1 the experiment harness calls `Evaluate` twice —
once on a clean-base corpus and once on a perturbed corpus — and compares the two
returned Changesets; `Evaluate` itself has no knowledge of the paired run. In
Phase 2 "actual" becomes the PR diff parsed into a Changeset; the comparison
logic is unchanged.

### Sharding & bin-packing (Phase 2)

Pipeline order (this order is itself a contract — taint classification precedes
packing so the poison is contained to the package that earned it):

1. **Resolve references** to the moved symbol across the corpus, grouped by
   owning package.
2. **Taint-classify each package** — clean vs tainted (any tainted reference
   ⇒ the *whole package* is tainted).
3. **Resolve ownership** per package via GitHub last-match-wins; unowned packages
   take a designated fallback owner.
4. **Bin-pack whole packages into shards**, one **(ownership domain × track)** per
   shard, soft-bounded by the impact cap, by a **deterministic, drift-stable**
   algorithm (assignment is a function of stable package identity, not iteration
   order; growth runs a bin slightly over cap rather than reshuffle).

A shard therefore contains only whole packages of one owner and one track. Clean
packages → Track-A shards; tainted packages → Track-B shards. Weight for the cap
is **reference (rewrite) count**, the actual change volume.

### Shim lifecycle (Phase 2)

A sharded move is a verifiable *sequence*, each step a catalog transform with a
deterministic expected Changeset:

1. **introduce-shim** — create `dstPkg.dstName`; leave
   `srcPkg.srcName = alias → dstPkg.dstName`; rewrite zero references.
2. **migrate-reference-set** ×N — rewrite one shard's references from the source
   identity to the destination identity. Each is independently
   regenerate-and-comparable.
3. **remove-shim** — eligible only once completion-tracking reports zero remaining
   source references; the transform regenerates to "shim gone, no dangling refs."

### Completion (Phase 2)

Migration is **complete** when the deterministic partition, recomputed against
current `HEAD`, reports **zero remaining references to the source identity across
all shards**. Shim-removal eligibility is *derived* from this check, not tracked
as separate state. Drift is absorbed: a new package with source references simply
appears as another unmigrated shard until it too is done.

---

## Architecture

Pipeline of pure, separately-testable stages. The Phase-1 CLI is the first three
stages; Phase 2 adds the rest around the same core.

```
intent ─▶ [engine: regenerate] ─▶ Changeset
                                      │
actual ──▶ [parse → Changeset] ──────┤
                                      ▼
                              [verifier: compare] ─▶ changeset signals
corpus ─▶ [taint oracle] ────────────────────────▶ taint signals
                                      ▼
                              [verdict: fail-closed combine] ─▶ Report
                                      │  (Phase 2 ▼)
                       [taint-classify ▶ resolve owners ▶ bin-pack] ─▶ shards
                                      ▼
                       [shim lifecycle ▶ completion ▶ GitHub orchestration]
```

- **Extension points.** The **catalog** (new transform types) and the **taint
  oracle** (new name-reflective mechanisms) are registries: adding a transform or
  a taint mechanism is a single-location addition that does not touch the verdict
  combiner or the verifier. *[NEEDS CLARIFICATION: exact registry interface shape
  — resolve during Phase-1 implementation; the contract is "a transform exposes
  regenerate(corpus,intent)→Changeset; a taint mechanism exposes
  scan(corpus,identity)→[]TaintSite".]*
- **Verdict combiner** is the fail-closed gate: `ACCEPT` iff the transform
  computed, compiled, the Changeset matched, and the taint set is empty;
  any error or any non-empty signal ⇒ `REJECT`.

---

## Data Design

### Intent file (checked in, versioned)

Human-authored fields:

- **Identity**: `srcPkg`, `srcName`, `dstPkg`, `dstName`. Only `dstName` is an
  optional rename; the shim, when used, is always `srcPkg.srcName` and is not an
  author-settable field.
- **Impact cap**: configurable integer (weight = reference count).
- **Ownership policy**: reference to the CODEOWNERS resolution (last-match-wins);
  fallback owner for unowned packages.
- **Reserved (Phase 2, parsed but unused in Phase 1)**: shard/atomicity/shim-lifecycle
  fields. A Phase-1 intent that populates them is rejected as unsupported.

Tool-derived (never hand-authored): the partition, shard assignments, completion
state. *[See Open Question 1: recompute-only vs committed tripwire.]*

### Invariant Preservation

- **Determinism** (same intent + base + pinned toolchain ⇒ byte-identical
  Changeset and verdict): enforced structurally by pure functions + the pinned
  toolchain; verified by a "run twice, assert identical `Report`" test. The
  partition's determinism extends this invariant to the bin-packer.
- **Package atomicity** (no half-migrated package on `main`): enforced
  *structurally* — the bin-packer treats packages as indivisible items, so no
  shard can contain a partial package. No application-logic check can violate it.
- **Fail-closed verdict** (only all-stages-clean ⇒ ACCEPT): enforced by the
  verdict combiner; every stage error maps to an `engine-error:*` signal ⇒ REJECT.
- **Taint soundness** (no tainted reference reaches Track A): enforced by
  **application logic** (the scanner), *not* structurally — therefore the
  highest-priority component for test coverage. The conservative-by-default
  posture is its contract: unknown/ambiguous ⇒ flag.

### Illegal State Analysis

- A *partial package* on `main` is unrepresentable: packages are indivisible
  bin-packing items.
- A *shard spanning two owners or two tracks* is unrepresentable: bin-packing key
  is `(owner × track)`.
- An *over-cap single package* is representable but caught as a precondition
  violation before any PR is created (fail-closed at intent-creation).
- A *hand-written shim passing as mechanical* is caught by regenerate-and-compare
  (the diff is not the output of any catalog transform ⇒ `changeset-unequal`).
- A *shim with a name other than `srcName`* is unrepresentable: the shim name is
  not an author-settable field and is always `srcPkg.srcName`, so unmigrated
  references can never be orphaned by a renamed shim.

---

## Security

The security boundary is the bypass itself (Phase 2). It rests on two independent
signals — a CODEOWNER-legitimate bot approval *and* the `mechanical-proof`
required status check — so that compromising or mis-firing one does not merge an
unproven diff. Stale-approval dismissal on every push is mandatory: the proof is
pinned to an exact commit SHA, and any new commit re-triggers analysis. The
Phase-1 CLI has no privileged capability and no network surface.

## PII

None. The tool processes source code, package paths, and CODEOWNERS identities
(team/user handles, already public within the org). No personal data beyond the
ownership handles already present in the repository.

## Scale

The target case is a single atomic move affecting **~1,000,000 lines** across
hundreds of packages, landing as **100+ PRs over ~a month**. The design absorbs
this through (a) the pure-function partition (drift over the month is a non-event)
and (b) the shim (clean packages auto-merge on Track A while tainted packages
proceed on Track B, so neither blocks the other and the move converges). The
Phase-1 CLI runs offline against a corpus; performance is a Phase-2 concern when
it runs per-PR in CI.

---

## Acceptance Criteria

### Phase 1 (the evaluator CLI)

Each is mechanically verifiable by the experiment harness or unit tests.

1. **Determinism.** `Evaluate` run twice on the same (corpus, intent) returns a
   byte-identical `Report`.
2. **False-reject = 0% on irrelevant base changes.** The four spike perturbations
   (add-unrelated-import, reformat-touched-file, add-unrelated-function,
   rename-unrelated-local) all ⇒ `ACCEPT` with an identical Changeset.
3. **True-catch = 100% on relevant changes.** call-site-edit ⇒ REJECT
   (`changeset-unequal`); gob-register ⇒ REJECT (`taint:gob`); reflect-value ⇒
   REJECT (`taint:reflect-name`). Both taint cases must REJECT *despite an
   unchanged Changeset* — proving the taint oracle is independent.
4. **Rename-on-move.** A `(srcPkg,srcName) → (dstPkg,dstName)` with
   `dstName ≠ srcName` regenerates a Changeset whose moved decl and all rewrites
   use `dstName`; a taint site observing the old base name ⇒ REJECT.
5. **Fail-closed.** A corpus exercising a construct the engine does not handle ⇒
   REJECT with an `engine-error:*` signal (never a crash, never ACCEPT).
6. **Reserved-field rejection.** A Phase-1 intent populating any shard/shim/cap
   field ⇒ rejected as unsupported (not silently ignored).

### Phase 2 (sharding, shimming, orchestration)

1. **Deterministic partition.** Same (intent, base, policy) ⇒ identical shard
   assignment; a re-run after adding an unrelated package leaves existing shard
   assignments unchanged.
2. **Bin-packing.** A 20-tiny-package single-owner service packs into one shard
   (cap permitting); a single package exceeding the whole cap ⇒ precondition
   violation surfaced at intent-creation, migration blocked.
3. **Track purity.** No shard contains both clean and tainted packages; no shard
   spans two owners.
4. **Shim sequence.** A worked multi-shard move (introduce → migrate ×N → remove)
   is `ACCEPT`ed at every step; shim-removal is rejected while any source
   reference remains and accepted once the partition reports zero.
5. **Completion is derived.** With all reference shards merged, the completion
   check reports zero remaining source references and only then is shim-removal
   eligible.
6. **Bypass integrity.** A PR whose diff fails `mechanical-proof` cannot merge
   even with the bot approval present; a new push dismisses a prior bot approval.

---

## Success Metrics

- **Track-A clearance rate** (primary) — fraction of packages that auto-merge vs
  eject to Track B, measured per migration. This is what the shim-decoupling and
  the linter pass are ultimately optimizing; it predicts how much human review
  the system actually saves.
- **False-reject rate on real moves** — fraction of clean packages wrongly
  ejected to Track B. (Cannot be measured on company data yet — see Limitations.)
- **Time-to-converge** — wall-clock from intent creation to verified completion
  for a large move.

---

## Testing

Testing follows the `surface-testing` skill.

Key surfaces:
- **integration:** `Evaluate(corpus, intent) → Report` against staged test
  corpora (the spike's experiment harness, generalized to take any corpus dir).
- **unit:** the taint oracle's per-mechanism scanners (highest-priority coverage,
  since taint soundness is enforced by application logic, not structurally); the
  Changeset normalizer (import-set equality, anchor scoping, gofmt canonicality);
  the verdict combiner's fail-closed mapping.
- **determinism:** run-twice byte-identical `Report` assertion; (Phase 2) re-run
  partition after corpus drift and assert stable assignments.
- **fakes needed:** none external in Phase 1 (pure function over a corpus dir).
  Phase 2 needs a fake CODEOWNERS resolver and, for orchestration, a fake
  GitHub surface; the cross-PR completion state needs an observable status API
  (it is not visible through `Evaluate`).

---

## Limitations & Future Work

- **Coverage is unmeasured on real data.** The spike's 0% / 100% are an existence
  proof on a small synthetic corpus, not a distributional estimate. We have no
  legally-runnable company data, so the real-corpus false-reject distribution and
  the full taint-mechanism inventory remain **known open gaps** to close once the
  tool can run on real history (its own dogfooding output is a candidate source).
- **Engine generality.** Phase 1 handles the constructs the test corpus
  exercises; the long tail (dot-imports, aliased imports, embedded fields,
  generics, `go:` directives) is incremental. Anything unhandled fails the
  equality check and ejects to Track B — the safe direction.
- **Taint inventory.** Two mechanisms are modeled (gob registration and
  name-based reflection). Two known *false-accept* risks remain: an unmodeled
  name-reflective mechanism, and a **reflection-keyed registry reached through a
  third-party dependency** — the reflecting code lives outside the corpus, so the
  corpus-only scanner cannot see it and the call site looks like an ordinary
  type-checked reference. The Phase-2 linter audit and the conservative-by-default
  oracle are the mitigations, not a proof of completeness.

---

## Open Questions

1. **Persisted partition vs pure recomputation.** Should the derived partition be
   recomputed fresh on every read (simplest; the pure function guarantees
   agreement) or computed once and committed back as a lock file that CI verifies
   its recomputation against (an audit artifact + a determinism tripwire)? The
   tripwire catches an accidental non-determinism that pure recomputation would
   silently absorb into a different sharding. Leaning pure-recomputation; the
   tripwire is the argument for the alternative. Does not block Phase 1.
2. **Weight metric edge.** Weight = reference (rewrite) count is settled, but a
   package with few references but enormous individual rewrite anchors (huge
   statements) could still produce a large PR. Is reference count alone the right
   cap weight, or does it need a secondary byte/line bound? Phase 2.
3. **Engine generality cutline.** Which long-tail Go constructs (generics,
   embedded fields, aliased/dot imports) are in Phase 1 vs deferred? Each
   deferred construct is *safe* (ejects to Track B) but lowers clearance rate.
   Resolve against the actual corpus when one is available.
