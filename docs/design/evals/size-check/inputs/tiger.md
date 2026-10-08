# Tiger Go

**A Go dialect where the specification lives in the source and a machine checks that the code matches
it.**

Adapted from [TigerStyle](https://github.com/tigerbeetle/tigerbeetle/blob/main/docs/TIGER_STYLE.md).
Tiger Go is not a style guide. It is a restriction of Go chosen so that questions a reviewer normally
answers by reading become questions a tool answers by checking. The formatting rules are a side
effect, not the point.

This document is the whole specification. Every rule has an ID, a reason that traces to a named
benefit, and an enforcement mechanism. Where a rule cannot be mechanically enforced, it says so and
says what to do instead.

---

# Part I: Why

## The premise

One economic claim sits above everything.

> The cost of a defect grows super-linearly with the distance between the moment it is introduced and
> the moment it is detected. Distance means time, stack frames, call sites, service hops, and
> releases.

Every rule here is one of three strategies against that curve. Shrink the population of possible
defects, shorten the distance to detection, or bound the damage while the distance is being
travelled.

This also explains the goal order, safety then performance then developer experience. A safety defect
is detected by a customer, a performance defect by a dashboard, a developer experience defect
immediately by the person typing. The ordering is a statement about detection distance, not about
which one matters more.

The corollary is the part teams skip. Design is the cheapest place to fix anything, and it is
precisely where nothing can be measured or profiled, which is why the sketch and the mental model come
before the code rather than after the benchmark.

## Enforceability is a property of a rule in a language

This is the insight the rest of the document is built on, and getting it backwards hides most of the
available wins.

> The analyses that are undecidable in general Go are almost all decidable in Go restricted by the
> other rules. The analysis does not get smarter. The language gets smaller.

Alias analysis is the clearest case. In general Go it is hopeless. In a package with no package-level
mutable state, no `unsafe`, no reflection, no escaping internal pointers, and no single-implementation
interfaces, the ways a pointer can reach a piece of state are small, enumerable, and decidable.

The restrictions compound, and they compound in a specific order:

```
no globals + no unsafe + no reflection
  → the heap graph is reachable only through parameters and receivers
    → points-to analysis is exact for the fragment
      → frame conditions are checkable
        → "what can this function change" is machine-answerable
          → mechanical refactoring is provably behavior-preserving
            → refactoring diffs need no review
```

Each link is worthless alone. No globals, by itself, is mild hygiene. In the chain it is load-bearing
for a guarantee five steps away.

The practical consequence: **you do not get sixty percent of the value from sixty percent of the
rules.** You get very little until a chain completes, and then a lot at once. Adopt by chain, not by
ease. The sequencing section at the end gives the order.

## Judgment is conserved

One thing cannot be designed away, and pretending otherwise is how verification efforts fail.

> Judgment is conserved. You can relocate it, you cannot delete it. Every rule promoted from review to
> lint moves a judgment out of the code and into a declaration, and someone still has to be right
> about the declaration.

`TS-S22` does not verify that `BatchMax` is correct. It verifies that `BatchMax` equals the expression
written next to it. The judgment moved from a number in a file to a formula in a comment. That is a
spectacular trade and it is not a proof.

It is a good trade for three measurable reasons. The declaration layer is small, typically two to five
percent of lines. It is stable, changing an order of magnitude less often than the code it governs.
And it is dense, so attention spent there is spent where being wrong is most expensive, which is the
opposite of how review attention is usually distributed.

## How review divides

The split is not by topic and not by directory. Every concern has two parts, and the parts get
different treatment.

**Machines check the correspondence.** Does the code satisfy the invariant it names, stay inside its
declared frame, terminate, cover every state, match its declared effect set, route its nondeterminism
through an injectable surface, respect the declared layering.

**Humans and AI review the declarations.** Is `SequenceMonotonic` the invariant this protocol actually
needs. Should this function be permitted to touch the disk at all. Is 512-byte sectors true of the
hardware. Can the `Storage` interface express a torn write.

**The declarations are a layer, not a region.** They cut across every file. There is no part of the
tree that is "the reviewed part", because the effect set of a function determines which checks apply
to it, and the effect set is computed rather than decreed.

The policy that falls out:

> A change touching no declaration and passing CI merges without a human reading it. A change touching
> a declaration goes to someone who knows the domain, and nothing else about it is discussed, because
> everything else has already been checked.

Track one number and make it the headline: **the fraction of merged lines that no human read.**

## The nine benefits

"Safety, performance, developer experience" is the right goal hierarchy and useless as a rationale,
because every rule anyone has ever wanted can be argued to serve safety. These nine sit between the
goals and the rules. Every rule cites at least one. A rule that cannot complete the chain from rule to
mechanism to benefit to goal is a preference, and is either labelled as such or deleted.

Each benefit is stated as a claim you could argue with, plus its cost. A benefit with no stated cost
is a slogan.

**B0 Uniformity.** For a class of decisions, the cost of debating it exceeds the cost of any
particular answer, so the value is entirely in everyone doing the same thing. Prevents formatting
diffs that hide semantic diffs, and the same argument every quarter with a new hire. Costs: you will
sometimes follow a convention you think is worse. *Honesty requirement: a B0 rule may never be dressed
up as a safety rule. The correct answer to "why 100 columns" is "because it is 100 everywhere and the
agreement matters more than the number".*

**B1 Bounded execution.** Everything in reality has a limit. Code that does not declare its limit has
one anyway, chosen by whatever runs out first, discovered at the worst time. Prevents infinite loops,
unbounded queues, stack exhaustion, OOM kills, retry storms. Costs: you have to pick the number, and a
bound set wrong is an outage of a different shape. An unbounded system fails globally, late, and far
from the cause; a bounded one fails locally, early, and at a boundary you wrote.

**B2 Short defect distance.** Detection belongs as close to the cause as the language allows, and the
ladder from cheap to expensive is compile error, assertion, test, production. Prevents silent
corruption, swapped argument pairs, truncating conversions, an error surfacing three services from its
origin. Costs: assertions cost cycles and cost availability *on purpose*, trading a correctness bug for
a liveness bug. Sharpest form is the type system, because it moves detection to compile time and the
cost is paid once at the declaration.

**B3 Local reasoning.** Correctness review is bounded by human working memory, which holds a handful
of items. Code requiring more than that will not be reliably verified regardless of who reviews it.
Prevents reviewed-but-wrong code, the compound condition with one unhandled case, the abstraction
whose behaviour lives in four files. Costs: more functions, more names, and a real risk of making
things worse by splitting badly. *This is the benefit most often mistaken for aesthetics. It is a
claim about a cognitive limit, and it predicts that a reviewer will miss a case in a five-clause
condition.*

**B4 Determinism.** A failure you can reproduce on demand is a bug; one you cannot is a rumour.
Reproducibility is the precondition for every other debugging technique. Prevents heisenbugs, flaky
tests, incidents that close as "could not reproduce". Costs: clocks, seeds, and IDs have to be plumbed
through code that has no other use for them.

**B5 Defect discovery leverage.** An assertion is an oracle that every future execution reuses for
free. Bugs found equals assertion density times input diversity, so investing in both multiplies
rather than adds. Costs: density is real work, and a wrong assertion is a false crash in production.

**B6 Predictable cost.** For systems with tail latency or safety requirements, variance is worse than
the mean. Code whose cost is visible in the source can be budgeted; code whose cost depends on the
allocator, the GC, or the branch predictor cannot. Costs: less convenient code, fewer abstractions,
restrictions on the parts of Go that are pleasant precisely because they hide cost.

**B7 Change safety.** Most code is read and modified by someone who did not write it, and the
assumptions in the author's head are not in the file. Rules that make an invalidated assumption fail
loudly at build time protect the majority author, who is the second one. Costs: rigidity. Some changes
now require touching more places, which is the mechanism working and will still feel like friction.

**B8 Blast radius control.** A defect's cost is proportional to how much of the system could have
caused it and how much it can reach. Confinement shrinks the suspect list and the damage together.
Costs: plumbing instead of convenient globals, and reimplementing what a dependency would have given
you free.

## The five questions

A rule is legitimate when it answers all five. Which one it fails tells you what kind of bad rule it
is.

| Question | If it cannot answer |
| --- | --- |
| Which benefit, by ID | **Preference.** Delete it, or admit it is B0 |
| What failure mode, concretely enough to write a test for | **Preference.** A rule with no bug behind it is taste |
| What is the chain from rule to mechanism to benefit, with no step assumed | **Folklore.** Possibly right, with the reason lost, which is dangerous because nobody can extend it to a case it does not literally cover |
| What does following it cost | **Dogma.** A rule claiming zero cost has not been examined |
| What observation would retire it | **Religion.** Unfalsifiable, therefore unimprovable |

A worked example of the folklore failure, from the development of this document. An earlier draft
split the codebase into a verified core and an unverified shell, on the grounds that the core could
then be tested in isolation. When it became clear that testing the core in isolation leaves the bugs
in the shell and at the boundary, the split was restructured into three tiers rather than retired.
The conclusion had outlived its rationale and was being defended on inertia. The correct response was
to delete it, which is what the scoping model below does.

## Turning judgment into checks

Many rules here look like proxies for a property rather than the property itself. That is deliberate,
and there are seven techniques, worth knowing because you will need them when adding rules.

1. **Nominalize the property.** An analyzer cannot know two assertions concern the same invariant.
   Name the invariant, declare it once, reference it at both sites, and the check becomes reference
   counting.
2. **Move it into the type system.** The compiler is the cheapest analyzer you will ever run.
3. **Require the artifact of the thinking.** You cannot check that someone did the arithmetic. You can
   require the arithmetic in the source and then evaluate it.
4. **Shrink the domain until the relationship is syntactic.** Relatedness between two names anywhere is
   unknowable; between two parameters in one list it is obvious.
5. **Ban the anti-pattern instead of requiring the virtue.** You cannot require a good name; you can
   forbid `data`, `manager`, and `helper`.
6. **Replace the predicate with a budget and a ratchet.** A per-package number that may only decrease
   beats a principle everyone agrees with.
7. **Measure it at run time.** Some properties are decidable, just not statically.

**The test a proxy must pass:** satisfying it must require the same thinking the original demanded.
Otherwise you have built a ceremony and trained the team to satisfy the ceremony instead of the
property. A doc comment on every function fails this test, because it produces `// increment i`. An
exact assertion count per function fails, because it produces `assert.Ok(true, "")`. Error-path
coverage and mutation score pass, because neither can be raised without exercising a path that was
previously unexercised.

---

# Part II: Structure

## The scoping model

> **Status: confirmed 2026-08-12**, after an adversarial review of this section against the rest
> of the specification. It replaces an earlier core/shell split, rejected twice. Confirmation
> brought amendments, chiefly: `TS-P01` gained per-axis defaults so the absence of a declaration
> is never an error (mirroring the pin model one level up), and `TS-P03`'s surface-diff
> obligation now also covers drops in the computed `TS-P02` bound.

Restrictions divide along two axes, and conflating them is what produced the rejected split.

**Function scope, computed.** Effects, frames, contracts, and variants are properties of a single
function, derived from its body and its callees. A function's declared effect set determines which
checks apply to it. There is no boundary here and no region to defend, only a gradient. A function
with an empty effect set gets contracts, variants, frames, and totality checking. A function that
declares `io(disk)` gets the same checks modulo the declared effect, plus the obligation that the
effect reach an injectable surface.

**Package scope, declared.** Import allowlists, dependency bans, dispatch closure, and layering can
only be package-scoped, because Go's import granularity is the package. A file cannot import a
package differently from its neighbour. The layering axis is not a second declaration: it is the
`TS-X03` checked-in layer file, which a restriction set references rather than duplicates.

**The relationship: package restrictions are enablers, not tiers.** They determine the *precision* of
the function-level checks, not whether those checks run. A function in a package that permits dynamic
dispatch still gets a computed effect set; the analyzer uses a conservative over-approximation and
reports lower confidence. The result is a precision gradient rather than a wall, which matters because
a wall is a thing that gets argued about, eroded, and exempted, and a computed property is not.

**The ratchet.** Track the fraction of functions with empty effect sets and the fraction of packages
with closed dispatch. These are descriptive metrics that should rise over time. Pure functions will
cluster in practice; do not decree the cluster in advance and then defend its border. The metrics
track voluntary claims: `TS-P01` never requires a package to claim closure — an undeclared axis
takes its default — so the cluster emerges instead of being decreed.

**`TS-P01` A package's restriction set is declared; undeclared axes take stated defaults.**
Why. Import allowlists, dispatch closure, and dependency bans are package-granular by language
constraint, so they must be stated somewhere — and a per-package declaration lets a reviewer answer
"how precise are the checks on this function" from the package clause instead of walking the import
graph. Without a declared or defaulted input, `TS-P02`'s transitive bound is undefined. Absence of
a declaration is never an error — each axis has a default, mirroring the pin model one level up
("unpinned is fine, a wrong pin fails"): imports default to `TS-D01`'s stdlib-only baseline,
dispatch defaults to open (no precision claim; conservative over-approximation applies), and
layering defaults to the `TS-X03` layer file, referenced rather than duplicated. External test
packages (`foo_test`) need no declaration and make no claim; nothing imports them, so they never
enter a bound.

The form is a directive in the package doc comment, at most one per package:

```go
// Package ledger applies entries to the account state machine.
//
//tiger:restrict closed-dispatch, no-reflect, imports(internal/domain/...)
package ledger
```

Enforce. `restrictions` (custom) plus `depguard` (auto). Blocking on a contradiction between a
declaration and observed code, never on the absence of a declaration.

**`TS-P02` A package's effective precision is bounded by the weakest package it transitively imports.**
Why. Closed dispatch buys nothing if a dependency reintroduces an unknown callee. Computing and
reporting the bound stops a package from claiming a guarantee its dependencies do not support. The
standard library does not degrade the bound: the analyzer carries a curated fact table for it (the
same table the built-in `io` qualifiers come from), so stdlib internals are semantic ground rather
than an unknown. A third-party dependency with no declaration counts as weakest on every axis,
which is honest and points the incentive the same direction `TS-D01` already does.
Enforce. `restrictions` (custom). Reported, not blocking. The number belongs on the dashboard — and
a drop in the bound's value additionally appears in the release surface diff, per `TS-P03`.

**`TS-P03` Weakening a package's restriction set requires a directive and appears in the surface diff.**
Why. Erosion happens one convenient exception at a time. Making each one visible in the release
surface diff is what stops the drift. The obligation covers both doors. Editing your own
declaration is a choice: blocking, directive required, visible in the diff. A drop in the computed
`TS-P02` bound caused by a dependency change is a consequence: nobody made a choice a directive
could justify, so it does not block — but the drop appears in the release surface diff, so
dependency-caused erosion gets the same guaranteed human moment. A diff entry fires only when the
bound's value actually changes, never on every transitive dependency touch; and it is
annotation-only — it does not participate in `TS-R02`'s semantic-version decision, because the
package's own surface did not change, only its neighbourhood.
Enforce. `restrictions` plus `surfacediff` (custom). Blocking for declaration edits;
reported-in-diff for computed-bound drops.

## Pins: the analyzer infers, a declaration locks

> **Status: adopted 2026-08-12**, replacing the original mandatory-declaration model for
> function-scoped facts. `TS-F01` and `TS-F02` are rewritten in its terms. Chosen by deliberation
> against declarations-first and computed-only-first alternatives; the deciding evidence was the
> history of annotation systems — the survivors (Rust lifetimes, TypeScript, NullAway) all let
> inference do the bulk of the work and required annotation only at boundaries, while the systems
> that front-loaded annotation cost before the tool gave value (checked exceptions, JML, .NET Code
> Contracts) died of it.

Function-scoped facts — effect sets, frames, variants — have a computed baseline: the analyzer
derives them from the code and its callees with no annotation required, for every function, from
day one. The declaration for such a fact is therefore optional, and its meaning changes: it is a
**pin**. A pin freezes the computed fact into a blocking contract.

The lifecycle:

1. **Unpinned.** When a pull request changes a computed fact, the analyzer reports it in the PR —
   printed in pin syntax, the exact line you would paste above the function — and the merge
   continues. Report output and pin are one format; adopting a pin is a paste.
2. **Pinned.** The analyzer compares pin and code on every pull request. Agreement is silence.
   Disagreement fails the CI check before merge, showing the pin and the computed line in the same
   format. The two exits are to change the code or change the pin, and a pin edit is visible in
   the diff, so a reviewer rules on the new promise. A pin cannot go stale: a wrong pin is a
   failed check, not a lie in a comment.

Four rules govern pins:

- **Function-scoped pins attach to exported functions and methods only.** Unexported helpers are
  exactly the region mechanical refactoring (`TS-R01`) must be free to reshape; a pin there fights
  the prover and invites keeping dead code alive to keep CI quiet. Nothing is lost, because of the
  next rule. (`main` has almost no exports; `main` itself is pinnable. Loop variants are the
  placement exception: a variant pin attaches to its loop, wherever the loop lives, because the
  termination obligation belongs to the loop and cannot be checked from an enclosing signature.)
- **A pin bounds its entire callee subtree.** Effects propagate upward, so exported pins constrain
  unexported internals transitively, and the failure names the introducing call. Sparse pins give
  dense enforcement.
- **Pins are exact.** Undeclared effects fail and declared-but-absent effects fail, which stops a
  pin drifting into a defensive superset that means nothing. Purity is a positive claim with a
  positive spelling: `//tiger:effects none`. Absence of a pin never means purity; it means
  unpinned.
- **The trigger.** In a package that declares closed dispatch (`TS-K03` under `TS-P01`), pins are
  required on every exported function. Pins become mandatory exactly where the computed facts are
  exact — a defined event, not a mood. This is what keeps "optional" from decaying into "never".

One asymmetry inside the pinnable set: an effect set or frame always exists to be computed, but a
variant must be *found*. Where the analyzer synthesizes one, the usual lifecycle applies; where it
cannot, the variant pin is required (`TS-V01`) — mandatory because the fact is missing, not because
precision is claimed. The trigger and this are the only two ways a pin stops being optional, and
both are defined events.

What is not pinnable: invariants, surface fault sets, package restriction sets, `hot`, `wire`,
`owner`. These have no computed baseline; they are intent, not observation. You state them first
and the analyzer enforces their consequences. Facts become machine; intent stays human.

## The prerequisite: an assert package

Go has no `assert`. This is the single largest gap between TigerStyle and idiomatic Go, and nothing
else works until you close it. Go gives you `panic`, which the community treats as a smell, and
`error`, which is for something else entirely.

Draw the line clearly and keep it drawn.

> An error is an expected operating condition the caller must handle. An assertion failure means the
> code is wrong, and the only correct response to wrong code is to crash.

A disk returning `ErrNotExist` is an error. A function receiving a negative length is a bug, and
returning an error for it launders the bug into a caller who can do nothing about it either. Assert it
and die.

The companion `assert.go` is the whole package: about a hundred lines, no dependencies, enabled in
production. That last part is the point. Assertions you compile out are assertions that only ever
protected your test suite.

Two Go-specific traps. Variadic arguments are boxed at the call site before the callee tests anything,
so a formatted assertion allocates on every call including the ones that pass; the package therefore
provides no formatted variant, and formatting lives behind an `if` that only runs on failure. And
inlining has a budget, so the failure path is a separate `//go:noinline` function.

## Declarations at a glance

The full declaration vocabulary, gathered in one place. This is the layer that gets reviewed.

Source directives share one namespace, `//tiger:<verb>`, following Go's own directive convention
(`//go:build`, `//go:generate`); the prefix matches the analyzer binary, `tiger`, so seeing a
directive tells you what to run. (The prefix was renamed from `//tigerstyle:` — the project is
defined as not-a-style-guide, and the word `style` does not belong inside every declaration.)
Three kinds with different lifecycles: **pins** (computed by default, blocking once written, see
above), **intent declarations** (nothing to infer; stated first, enforced after), and **escape
hatches** (loosen one rule at one site, always with a reason, counted by `TS-D06`).

| Declaration | Form | Kind | Rule |
| --- | --- | --- | --- |
| Invariant | `inv.HeaderChecksum`, declared in one package | intent | TS-A07 |
| Limit derivation | `// BatchMax = (WriteBufferSize - HeaderSizeBytes) / EntrySize` | intent | TS-S22 |
| Effect set | `//tiger:effects io(disk), mutate(r.log)` — `none` for purity | pin | TS-F01 |
| Frame | `//tiger:frame r.log, r.checkpoint` | pin | TS-F07 |
| Precondition | `//tiger:requires len(target) == HeaderSizeBytes` | pin | TS-V03 |
| Postcondition | `//tiger:ensures result.Checksum == checksum(result.Payload)` | pin | TS-V03 |
| Loop variant | `//tiger:variant len(pending)` | pin | TS-V01 |
| Surface fault set | fault cases enumerated in the interface | intent | TS-I05 |
| Package restrictions | `//tiger:restrict closed-dispatch, imports(...)` in the package doc comment | intent | TS-P01 |
| Layer graph | checked-in layer file | intent | TS-X03 |
| Hot path | `//tiger:hot` | intent | TS-F03 |
| Wire type | `//tiger:wire` | intent | TS-K06 |
| Batched IO | `//tiger:batched <reason>` | escape | TS-M10 |
| Deviation | `//tiger:<rule-id> <reason>` | escape (gated, does not ship before the first heuristic rules + TS-D06 ratchet) | TS-L09 |

> **Status: amended 2026-08-12.** Escape hatches are gated by an **admission test**: a per-site
> escape exists only where the restricted dialect eliminates something reality requires — where
> no in-dialect rewrite can comply. Inconvenience never qualifies, because the primary consumer
> is an AI agent, and an agent offered a cheaper path than conforming will take it. A directive's
> reason is shape-checkable (verb known, reason present) but never truth-checkable, so an escape
> whose legitimate cases have in-dialect rewrites is a suppression in costume.
> `//tiger:bounded` fails the test and is withdrawn (see `TS-S02`): every terminating loop can
> state an explicit cap and assert on exhaustion, and forever-loops have the `TS-S03` shape.
> `//tiger:batched` passes: an external system that only accepts per-item IO is a fact of the
> world no rewrite removes. The deviation form ships, if ever, together with the first heuristic
> rules and the `TS-D06` ratchet — mechanism and control in one release. Every escape directive
> in a tree additionally surfaces as a standing **advisory** finding on every run: shape is
> machine-checked, truth is human-reviewed, and unverified claims never leave the report.

---

# Part III: The rules

Enforcement levels: **auto** (off-the-shelf linter), **custom** (an analyzer you write, all specified
in Part V), **partial** (heuristic, expect false positives), **runtime** (test or CI gate), **review**
(human or AI judgment, and the reason is always that the declaration is a claim about the world).

Severity is one of three words, used precisely:

- **Blocking.** The CI check fails on the pull request. The default unless stated.
- **Advisory.** Reported and counted against the per-package ratchet (`TS-D06`); does not fail the
  check. The on-ramp for structural rules on a legacy codebase: start advisory, ratchet per package.
- **Reported.** A PR annotation and a dashboard number only. Used where the fact is computed and no
  one made a choice a directive could justify: unpinned effect changes (`TS-F01`), the transitive
  precision bound (`TS-P02`).

## Safety and control flow

**`TS-S01` No recursion.**
Why. Bounded execution. A recursive function's stack depth depends on data, and data comes from
outside. Go grows stacks to 1GB by default, so you do not get a clean early crash, you get a process
that quietly eats a gigabyte and dies far from the cause.
Enforce. `norecursion` (custom, SSA callgraph, catches mutual recursion).

**`TS-S02` Every loop has an upper bound.**
Why. Everything in reality has a limit. A loop that does not state its limit is asserting that some
input will always be well-formed. Bound it and an infinite hang becomes a fast, loud failure at the
boundary. See `TS-V01` for the stronger form.
Enforce. `boundedloop` (custom). A `for` with no condition, or a condition not derived from a constant,
a `len`, or a counter, is a finding; no directive waives it. The compliant forms are in-dialect:
an explicit iteration cap with an assert on exhaustion, or the `TS-S03` event-loop shape.
*(Amended 2026-08-12: the `//tiger:bounded <reason>` escape is withdrawn — it fails the escape
admission test, since a reason is shape-checkable but not truth-checkable and every terminating
loop can state a cap. See "Declarations at a glance".)*

**`TS-S03` An unbounded event loop selects on `ctx.Done()` and asserts progress.**
Why. Some loops genuinely run forever. Those get an explicit termination path and an assertion that
they are still making progress, so a stuck loop reports itself instead of pinning a core in silence.
Enforce. `boundedloop` (custom) for the select shape; the progress assertion is `review`.

**`TS-S04` Hard limit of 70 lines per function.**
Why. The discontinuity between a function you can see and one you must scroll is real. Go functions
run longer than Zig ones because `if err != nil` costs three lines each time, so 70 is tighter here
than it looks. Push the `if`s up into a parent that owns control flow and the `for`s down into leaves
that own none. The bound is B3; the number is B0.
Enforce. `funlen` (auto).

**`TS-S05` Cyclomatic complexity at most 10, cognitive at most 15, nesting depth at most 3.**
Why. Length alone can be gamed. These catch the wide flat function and the deeply nested one.
Enforce. `cyclop`, `gocognit`, `nestif` (auto).

**`TS-S06` One logical operator per condition.**
Why. `if a && b || c` asks the reader to verify a truth table in their head. Nested `if`/`else` makes
each case visible and gives you somewhere to put the `else` you were about to forget.
Enforce. `compoundcond` (custom). Forbids mixing `&&` with `||`.

**`TS-S07` Split compound assertions.**
Why. `assert.Ok(a && b, ...)` tells you the pair failed. Two assertions tell you which one.
Enforce. `compoundcond` (custom).

**`TS-S08` Every switch over a closed set is exhaustive and ends in `assert.Unreachable`.**
Why. Adding a variant should break every switch that has not thought about it. Go's compiler will not
do this, because `iota` constants are just integers. The default arm covers the value that came off
the wire and was never in your enum.
Enforce. `exhaustive` (auto) plus `compoundcond` for the default arm (custom).

**`TS-S09` No `goto`, no labeled break or continue.**
Why. Simple explicit control flow. A labeled break is usually a loop that wanted to be a function.
Enforce. `nogoto` (custom).

**`TS-S10` No `init()` functions.**
Why. Hidden control flow that runs before `main`, in an order no reader tracks. Initialization you
cannot see is initialization you cannot assert on.
Enforce. `gochecknoinits` (auto).

**`TS-S11` No package-level mutable state.**
Why. A global with extra steps. No owner, defeats deterministic testing, makes parallel tests share
state they never agreed to share. Load-bearing for the points-to chain.
Enforce. `gochecknoglobals` (auto). Constants and `var Err... = errors.New(...)` allowed.

**`TS-S12` No `unsafe`, no `cgo`, no reflection.**
Why. `unsafe` removes the one memory guarantee Go gives you. `cgo` costs cross-compilation, a
predictable stack, and half your tooling. Reflection moves errors to run time and allocates doing it.
All three break the points-to chain. The sanctioned exception is `unsafe.Sizeof` in a compile-time
assertion, which executes nothing and reads no memory.
Enforce. `depguard` plus `gocritic` (auto), scoped by `TS-P01`.

**`TS-S13` Declare variables at the smallest scope, at the point of first use.**
Why. A variable declared thirty lines before use is thirty lines in which the wrong variable can be
picked up. Distance in space is distance in time, and that gap is where check-then-use bugs live. See
`TS-Q02` for the structural fix.
Enforce. `declusedistance` (custom, over 10 lines), `ineffassign`, `wastedassign` (auto). Partial,
advisory.

**`TS-S14` No shadowing.**
Why. `err` shadowed inside an `if` is the most common way a Go program silently discards a failure.
Enforce. `govet` with `shadow` (auto). See `TS-K05` for the stricter form.

**`TS-S15` Struct literals name every field.**
Why. Positional literals break silently when a field is inserted. Naming every field, including ones
set to their zero value, is the Go form of passing options explicitly. A zero value you typed is a
decision; one you omitted is an accident waiting for someone to change the struct.
Enforce. `exhaustruct` for config and wire types, `govet composites` everywhere (auto).

**`TS-S16` No magic numbers. Every limit is a named constant with a `Max` or `Min` suffix.**
Why. A limit you can find is a limit you can change, assert on, and reason about. A `4096` buried in a
slice expression is none of those.
Enforce. `mnd` (auto).

**`TS-S17` Assert relationships between constants at compile time.**
Why. A design invariant checked before the program runs is worth ten checked at runtime. Go has no
`comptime`, but it has constant overflow, and that is enough.

```go
// Fails to compile unless the header is exactly 128 bytes.
const _ = uint(unsafe.Sizeof(Header{}) - HeaderSize)
const _ = uint(HeaderSize - unsafe.Sizeof(Header{}))

// Fails to compile unless *Replica satisfies Member.
var _ Member = (*Replica)(nil)
```

Enforce. `wiretypes` (custom) for `//tiger:wire` types; `review` elsewhere.

**`TS-S18` No naked `panic` outside the assert package.**
Why. One crash path, one message format, one place to change the behaviour.
Enforce. `forbidigo` on `^panic$` (auto) plus `paniccheck` (custom) for the assert exemption. `revive`
`deep-exit` covers `os.Exit` and `log.Fatal`.

**`TS-S19` Integer conversions are checked or provably safe.**
Why. `int32(n)` where `n` came from `len()` is a truncation waiting for a large input. Go converts
silently, which is what made `int` versus `usize` a rule in the original.
Enforce. `gosec` G115 plus `unconvert` (auto).

**`TS-S20` Type assertions are always checked.**
Why. The one-result form panics on failure, far from whoever produced the wrong type.
Enforce. `forcetypeassert` and `errcheck` with `check-type-assertions` (auto).

**`TS-S21` Every limit constant participates in a compile-time relational assertion.**
Why. Whether `BatchMax = 8189` is correct is arithmetic about your hardware and no analyzer will know.
Whether you believe it relates to anything else is checkable, and a limit that relates to nothing is a
limit nobody reasoned about.
Enforce. `limitrelate` (custom). Every `Max`/`Min` constant must appear in at least one `const _ =
uint(...)` expression in its package.

**`TS-S22` Every limit constant carries a derivation, and the analyzer evaluates it.**
Why. This is the back-of-the-envelope sketch, moved into the source and then checked. State where the
number came from as an expression, not prose, and the tool confirms the arithmetic still holds after
someone changes an input.

```go
// WriteBufferSize = SectorSize * SectorsPerWrite.
const WriteBufferSize = 32768

// BatchMax = (WriteBufferSize - HeaderSizeBytes) / EntrySize.
const BatchMax = 8189
```

Enforce. `derivation` (custom). Highest value per line of any analyzer here, because it is the only one
that catches a *stale* number rather than a missing one.

## Assertions and invariants

**`TS-A01` Average at least two assertions per function.**
Why. This is the rule that carries most of the safety, and the one teams quietly drop. The density
target stops assertions clustering in the three functions someone was worried about that week.
Enforce. `assertdensity` (custom), per package and per file. Blocking at package level, advisory per
function.

**`TS-A02` Assert every precondition on entry and every postcondition before return.**
Why. A function must not operate blindly on data it has not checked. Its purpose is to raise the
probability the program is correct, which it cannot do if it trusts its inputs. See `TS-V03` for the
modular form that moves this to compile time.
Enforce. `assertdensity` (custom). Partial.

**`TS-A03` Assert the invariant on both sides of every boundary.**
Why. Validate before writing to disk and again after reading back. Two independent checks on the same
property catch the case where one of them was wrong. Enforced through the invariant vocabulary below,
because "the same property" is not a thing an analyzer can see in two boolean expressions.
Enforce. via `TS-A07` and `TS-A08`.

**`TS-A04` Assert the negative space.**
Why. Bugs live where data crosses from valid to invalid. Asserting only what you expect leaves the
transition unguarded.
Enforce. via `TS-A09`, `TS-T03`, and `TS-T09`.

**`TS-A05` Assertions never have side effects.**
Why. They must be removable in principle even though you never remove them. An assertion that mutates
means the program behaves differently under test than in production.
Enforce. `effects` (custom, `TS-F04`). Exact, not heuristic.

**`TS-A06` Distinguish index, count, and size.**
Why. The usual suspects. An index is 0-based, a count is 1-based, a size is a count times a unit, and
every conversion between them is an off-by-one waiting to happen.
Enforce. via `TS-Q01`, which makes it a compile error.

### The invariant vocabulary

Stop passing strings to assertions. Declare the invariant, and the properties an analyzer could never
see become reference counting and set equality.

```go
// Package inv declares every invariant the storage engine enforces.
package inv

type ID string

const (
	HeaderChecksum    ID = "header-checksum"    // Header checksum matches the payload.
	HeaderSize        ID = "header-size"        // Header occupies exactly HeaderSizeBytes.
	SequenceMonotonic ID = "sequence-monotonic" // Sequence numbers never decrease within a term.
	BatchFitsBuffer   ID = "batch-fits-buffer"  // Batch length never exceeds the write buffer.
)
```

```go
func encodeHeader(target []byte, h Header) {
	assert.Invariant(inv.HeaderSize, len(target) == HeaderSizeBytes)
	assert.Invariant(inv.HeaderChecksum, h.Checksum == checksum(h.Payload))
}

func decodeHeader(source []byte) Header {
	assert.Invariant(inv.HeaderSize, len(source) == HeaderSizeBytes)
	h := ...
	assert.Invariant(inv.HeaderChecksum, h.Checksum == checksum(h.Payload))
	return h
}
```

The ID's value is entirely static — it is an address the analyzer counts (`TS-A07`), compares
(`TS-A08`), and matches to a violating test (`TS-A09`); at run time `assert.Invariant` behaves
exactly like `assert.Ok`. The underlying type is a string rather than an int for two reasons: the
failure message names the invariant with no generated code, and the assert package accepts any
`~string` ID via generics — coupled by shape, not by import — so `assert` keeps zero dependencies
and `inv` stays project-owned.

**`TS-A07` Every declared invariant is asserted in at least two distinct functions.**
Why. Pair assertion, restated as something countable. An invariant asserted once is either misfiled or
under-defended, and both are worth knowing.
Enforce. `invariantrefs` (custom). Also fails on a declared invariant with zero references.

**`TS-A08` Symmetric boundary functions assert the same invariant set.**
Why. Once invariants are named, "the same property on both sides" is set equality.
Enforce. `invariantsymmetry` (custom). Pairs by naming convention (`encodeX`/`decodeX`,
`marshalX`/`unmarshalX`, `writeX`/`readX`, `putX`/`getX`).

**`TS-A09` Every invariant has a test that violates it.**
Why. The negative space made concrete. If no test can make the invariant fail, either the invariant is
unreachable or the assertion is wrong, and you want to know which.
Enforce. `invariantnegative` (custom). Requires `assert.Violates(inv.X, func(){...})`.

Three things fall out that the string version never gave you. Grep an invariant and get every site
defending it. The invariant list becomes a design document that cannot go stale, because a deleted
assertion fails `TS-A07`. And a fake invariant costs a declaration, two call sites, a symmetric
counterpart, and a negative test, which is more work than thinking of a real one.

## Quantities and types

**`TS-Q01` Domain quantities are named types. Conversions live in one package and are named.**
Why. The original treats index, count, and size as distinct types and relies on naming discipline to
keep them apart, because Zig will not. Go will. A named type cannot be assigned to another without a
conversion, so the discipline becomes a compile error instead of a convention.

```go
type Index int   // 0-based position.
type Count int   // 1-based quantity.
type Bytes int64 // Size in bytes.

// CountFromIndex converts a 0-based position to a 1-based quantity.
// This is the one place the plus one lives.
func CountFromIndex(i Index) Count {
	assert.Ok(i >= 0, "negative index")
	return Count(i) + 1
}
```

Enforce. Compiler, plus `quantitycast` (custom) forbidding direct conversions outside the quantity
package.

**`TS-Q02` Parse, do not validate. Exported functions take domain types, not bare primitives.**
Why. The mechanizable form of "check variables close to where they are used". The check-to-use gap
exists because a checked value and an unchecked value have the same type, so the check cannot travel
with the value. Give the checked value its own type whose only constructor asserts, and the gap closes
structurally. A function taking an `AccountID` cannot be called with an unvalidated string no matter
how far apart the lines are.
Enforce. `domaintypes` (custom). Exported functions may not take `string`, `[]byte`, or unsized
integers except through an allowlist.

**`TS-Q03` Units are types, not name suffixes.**
Why. `latencyMsMax` is a good name and `TS-N03` still applies, but a `Millis` type means the compiler
catches the nanosecond you passed. `time.Duration` is the proof this works.
Enforce. Compiler, plus `quantitycast` (custom).

## Errors

**`TS-E01` Every error is handled. No exceptions.**
Why. The USENIX analysis of catastrophic failures in distributed data-intensive systems found the
overwhelming majority were caused by incorrect handling of non-fatal errors the software had already
explicitly signalled. Go hands you every error as a value, which makes this the one rule the language
is on your side for. Best-evidenced rule in the document.
Enforce. `errcheck` with `check-blank: true` (auto).

**`TS-E02` Discarding a result with `_` requires a justification comment.**
Why. `_ = f.Close()` is sometimes right, often enough that banning it outright gets you `//nolint`
instead, which is worse. Make the reason visible.
Enforce. `errignore` (custom).

**`TS-E03` Wrap once with `%w`, compare with `errors.Is` and `errors.As`.**
Why. String comparison breaks the first time someone reformats a message. Double wrapping produces
messages nobody can read.
Enforce. `errorlint`, `wrapcheck` (auto).

**`TS-E04` Sentinel errors are `ErrFoo`, error types are `FooError`, messages lowercase and unpunctuated.**
Why. Consistency, and because wrapped messages concatenate; a capitalized message reads wrong mid-chain.
Enforce. `errname`, `staticcheck` ST1005 (auto).

**`TS-E05` Never return both a nil value and a nil error.**
Why. It forces every caller to invent a third case, and half of them get it wrong.
Enforce. `nilnil` (auto).

**`TS-E06` Minimize return arity.**
Why. Every return value is a branch at the call site, and branches propagate up the chain. Preference
order: nothing, `T`, `(T, bool)`, `(T, error)`. Never `(T, bool, error)`.
Enforce. `returnarity` (custom, at most two results, at most one non-error) plus `ireturn` (auto).

**`TS-E07` Close errors on writable resources are checked.**
Why. On a writer, `Close` is where the flush happens, so `defer f.Close()` on a file you wrote to
discards the one error that mattered.
Enforce. `errcheck` (auto) plus `review` for deferred-close-with-named-return.

**`TS-E08` No naked returns.**
Why. A named result returned implicitly is a value the reader must reconstruct from the whole function.
Enforce. `nakedret` with `max-func-lines: 0` (auto).

## Concurrency

This area has no counterpart in the original, because Zig gave TigerBeetle a single-threaded event
loop and static allocation. Go gives you a scheduler and a `go` keyword that is two characters long,
which is the most dangerous ergonomics in the language.

**`TS-C01` Prefer a single owner goroutine over shared memory with locks.**
Why. The same instinct as the control plane and data plane split. One goroutine owns a piece of state,
everyone else sends it messages, and the whole class of data race disappears rather than being
defended against.
Enforce. via `TS-C10`, `TS-C11`, `TS-C13`, and `TS-F07`, with `-race` as the runtime backstop.

**`TS-C02` Every goroutine has an owner, a documented exit condition, and a lifetime tied to a context.**
Why. A goroutine with no owner is a leak you will find in production, at a memory number nobody can
explain. Start them through `errgroup` or a supervisor, never with a bare `go` mid-function.
Enforce. `nogoroutine` (custom) plus the `spawn` effect (`TS-F08`).

**`TS-C03` Goroutine leaks fail the test suite.**
Why. Checkable at run time and not at compile time, so check it at run time.
Enforce. `goleak` in `TestMain` for every package (runtime).

**`TS-C04` Every channel has an explicit capacity, and the capacity is a named constant.**
Why. Put a limit on everything. An unbuffered channel is a bound of zero and that is a fine answer; an
unbounded queue is not an answer at all. Go will not let you build an unbounded channel, which is a
gift, so do not rebuild one out of a slice and a mutex.
Enforce. `mnd` (auto), `queuebound` (custom) for slice-backed queues.

**`TS-C05` Every blocking receive or send has a `ctx.Done()` case.**
Why. Shutdown that hangs is shutdown that gets `SIGKILL`ed, and a process killed mid-write is a
process that lost data.
Enforce. `selectctx` (custom).

**`TS-C06` No mutex held across a channel operation or a call that can block.**
Why. It is how you build a deadlock without noticing.
Enforce. `effects` (custom, `TS-F05`). The critical section's effect set must exclude `block`. Exact,
not heuristic.

**`TS-C07` `context.Context` is the first parameter, never stored in a struct, and `context.TODO` never ships.**
Why. Cancellation has to propagate or the bounds everywhere else are decorative.
Enforce. `containedctx`, `contextcheck`, `noctx`, `revive` (auto).

**`TS-C08` No `time.Sleep` in production code.**
Why. Sleep is a guess about someone else's timing. Use a channel, a timer you own, or a condition you
can assert on.
Enforce. `forbidigo` (auto) plus the `time` effect (`TS-F01`), which catches the helper three frames
down that a name-based ban misses.

**`TS-C09` Run at your own pace. Do not spawn work in direct reaction to an external event.**
Why. Straight from the original, doubly true in Go where the natural shape is one goroutine per
request. Accept into a bounded queue, then drain at a rate you control. Batching, back pressure, and a
bound on work per unit time all fall out of the same decision.
Enforce. `nogoroutine` (custom) structurally; the design is `review`.

**`TS-C10` A type marked `//tiger:owner` has no exported fields, no `sync` primitives, and cannot be copied.**
Why. If nobody outside the package can reach the state and there is no mutex, then either the state
has one owner goroutine or the race detector is about to tell you otherwise. The absence of a mutex is
the interesting part: a lock is evidence you chose shared memory, so banning it forces the
message-passing design instead of recommending it.
Enforce. `ownership` (custom). Zero exported fields, no `sync.*` field types, `noCopy` embed.

**`TS-C11` No exported method returns a pointer, slice, or map that aliases an internal field.**
Why. This is where single ownership actually leaks in Go. `func (s *State) Entries() []Entry` hands
out a mutable view of your state and nothing in the type system says so. Returning a pointer to a
field is the syntactic form of aliasing, and unlike general alias analysis it is a five-line check.
Load-bearing for the points-to chain.
Enforce. `escapecheck` (custom).

**`TS-C12` Channel types used across goroutines are declared in one file per package.**
Why. Centralizing the communication surface makes the topology reviewable in one place and puts the
capacity constants next to each other, where a mismatched pair is visible.
Enforce. `chandecl` (custom).

**`TS-C13` One concurrency paradigm per package.**
Why. A package sharing state through channels *and* mutexes has two mental models, and every reader
must work out which applies to the field in front of them. This is the checkable half of `TS-C01`,
because "which paradigm" is set membership even though "is this correct" is not.
Enforce. `paradigm` (custom). A package with a `sync.Mutex` field may not also have a channel field,
absent a directive.

## Effects and frames

The highest-leverage rules here, and the reason several older ones could settle for a heuristic. An
effect declaration turns a local syntactic ban into a transitive whole-program guarantee.

```go
// applyBatch writes entries to the log and updates the checkpoint.
//
//tiger:effects mutate(r.log, r.checkpoint), io(disk)
//tiger:frame r.log, r.checkpoint
func (r *Replica) applyBatch(ctx context.Context, batch []Entry) error {
```

The effect lattice is closed and small: `alloc`, `io`, `block`, `panic`, `rand`, `time`, `mutate(x)`,
`spawn`. An empty effect set is purity, and in a pin it is spelled `none`.

`io` carries a qualifier from a two-tier vocabulary, and both tiers are closed. The **built-in
tier** is computed from standard-library facts with no configuration: `io(disk)`, `io(net)`,
`io(exec)`, `io(env)`. The **declared tier** is project-defined and anchored to surfaces: a surface
declaration (`TS-I01`) may name a logical qualifier — a `Store` surface declaring `io(database)` —
and the analyzer infers that qualifier for any function whose call graph reaches the surface, which
makes detection exact rather than heuristic. A pin may only use qualifiers that exist, built-in or
declared, so a misspelled qualifier is an error instead of a silently meaningless string. The
declared tier is visibility; the hard wall for "only the store touches the database" is the import
restriction (`TS-P01`), and the two are designed to be used together.

**`TS-F01` Every function's effect set is computed; a pin makes it a contract.**
Why. `TS-C08` bans `time.Sleep` by name, which catches the direct call and misses the helper three
frames down. An effect is transitive by construction, so a ban expressed as an effect cannot be
laundered through indirection. This is the difference between a rule about syntax and a rule about
behaviour. The analyzer computes the set for every function with no annotation required; changes to
unpinned sets are reported in the pull request, printed in pin syntax. A pin — permitted on
exported functions and methods only, spelled `//tiger:effects ...`, with `none` for purity — is
exact and blocking, and is required on every exported function of a closed-dispatch package
(`TS-K03`). *Which* effect sets are acceptable for a given function is a declaration, and therefore
`review`. See Part II, "Pins".
Enforce. `effects` (custom, `buildssa`). On a pinned function, undeclared effects fail and so do
declared-but-absent effects, which is what stops a pin drifting into a defensive superset that
means nothing. Unpinned changes are reported, not blocking.

**`TS-F02` A pin bounds the entire subtree beneath it.**
Why. Effects propagate upward, so a pinned function's computed set is a true summary of everything
reachable below it, and a change anywhere in that subtree which widens the set past the pin fails
at the pin, with the introducing call named. Sparse pins therefore give dense enforcement: pinning
a package's exported surface constrains every unexported helper under it. Where a pinned function
calls another pinned function, the callee's pin is checked against the caller's from signatures
alone, which keeps the checking modular exactly where pins are dense.
Enforce. `effects` (custom).

**`TS-F03` A function marked `//tiger:hot` has effect set at most `{mutate}`.**
Why. Zero-allocation hot paths needed a benchmark gate because allocation is a runtime fact. With
effects it becomes static, since `alloc` propagates. Keep the benchmark as a cross-check on escape
analysis, which belongs to the compiler and changes between releases, but the first line of defence
moves to compile time. Also subsumes the older rules about extracting hot loops: no receiver, no
interface parameters, no `defer`, no closures.
Enforce. `effects` plus `hotpath` (custom) plus an `AllocsPerRun` gate (runtime). A CI job profiles
the benchmark suite and fails if any of the top twenty functions by self time is unmarked, which is
what keeps the annotation honest.

**`TS-F04` Assertion arguments are pure.**
Why. Enforces `TS-A05` exactly rather than heuristically, because effects are the interprocedural
purity analysis that rule always wanted.
Enforce. `effects` (custom).

**`TS-F05` A critical section has effect set excluding `block`.**
Why. Enforces `TS-C06`. As a reachability heuristic this was a week of work and a false positive
generator; as an effect check it is three lines.
Enforce. `effects` (custom).

**`TS-F06` Every core function excludes `rand` and `time`.**
Status. **Deleted.** Fails the five-question test: it is `TS-F01` plus a review judgment about which
effect sets are acceptable, and it was scoped by the rejected core/shell split. Determinism is
enforced by `TS-F01` plus `TS-T01`. ID retired, not reused.

**`TS-F07` Every function's frame is computed; writes outside a pinned frame fail.**
Why. This is the answer to "do not duplicate or alias state", which is undecidable in general Go.
Given `TS-S11`, `TS-S12`, and `TS-C11`, the set of locations a function can write is exactly the set
reachable from its parameters and receiver, which is finite and computable. This rule is what makes
"which function could have written this value" answerable in one query.
Enforce. `frames` (custom, `buildssa` points-to over the restricted fragment). Precision bounded by
`TS-P02`.

**`TS-F08` `spawn` is a declared effect, and only a supervisor may declare it.**
Why. `TS-C02` restricts `go` by file path, which is a proxy for ownership. `spawn` as a transitive
effect makes a function's goroutine behaviour visible in its signature to callers several frames up,
which is where the ownership decision actually gets made.
Enforce. `effects` (custom).

## Termination and contracts

**`TS-V01` Every unbounded loop has a verified variant: synthesized where the analyzer can, pinned where it cannot.**
Why. `TS-S02` checks a loop has a bound and cannot check the bound is ever reached, so a `for` with a
decreasing condition and a body that never decrements passes. A variant is a ranking expression
required to strictly decrease on every back edge and be bounded below, which is the standard
termination argument. Variants follow the pin lifecycle with one asymmetry: an effect set always
exists to be computed, but a variant must be *found*. Where the analyzer synthesizes one — linear
integer expressions over locals, which covers nearly every real loop — the loop is proven with no
annotation, the synthesized variant is reported like any computed fact, and pinning it is a paste;
the pin attaches to the loop, wherever the loop lives. Where synthesis fails, the termination
argument exists only in the author's head, so the pin is required: mandatory because the fact is
missing, not because precision is claimed. This is the artifact-of-the-thinking technique — the
analyzer cannot find your ranking, so you write it down and the analyzer checks it.

```go
// The analyzer synthesizes len(pending) for this loop unaided; the pin locks it.
//tiger:variant len(pending)
for len(pending) > 0 {
	entry := pending[0]
	pending = pending[1:] // Analyzer verifies the variant strictly decreases here.
}
```

Enforce. `variant` (custom, `buildssa`). Synthesis and verification are feasible for linear integer
expressions over locals. Blocking when a loop has neither a synthesized nor a pinned variant, and
when a pinned variant fails to decrease. A ranking beyond the analyzer's predicate language needs a
deviation with a reason (`TS-L09`).

**`TS-V02` Functions are total.**
Why. Total means it returns for all inputs satisfying its preconditions. With `TS-S01`, `TS-V01`, and
effects excluding `panic` and unbounded `block`, totality is compositional and cheap to check. A total
function is one whose worst case you can state.
Enforce. `effects` plus `variant` (custom). Applies wherever the effect set permits it.

**`TS-V03` Preconditions are declared and discharged at call sites.**
Why. `TS-A02` asserts preconditions inside the callee, which is a runtime check. Declaring them lets
the analyzer verify at each call site that the condition is established by a dominating assertion, by
a type, or by a preceding check, which moves detection a rung up the B2 ladder. This is what makes the
assertion layer modular rather than local.

```go
//tiger:requires len(target) == HeaderSizeBytes
//tiger:ensures result.Checksum == checksum(result.Payload)
func decodeHeader(target []byte) Header
```

Enforce. `contracts` (custom, abstract interpretation over a restricted predicate language: nil-ness,
integer ranges, length relations, invariant IDs). Partial by nature. **Unproven obligations degrade to
a runtime assertion rather than a build failure**, which is the right default because it never blocks
on the analyzer's incompleteness.

## Surfaces and injectability

Co-equal with the rule catalogue, not a testing detail. The rules above make properties checkable;
these make the checked properties describe the running system rather than a model of it.

The distinction that matters is between injection and description. Injection means the code takes a
`Storage` interface and calls `s.Write(...)`; IO is swappable but still performed, the effect set
still contains `io`, and every test needs a fake whose fidelity you must also be right about.
Description means the code returns commands as values and something else interprets them. Prefer
description where the shape allows it. Require injection everywhere else.

**`TS-I01` Every nondeterministic effect reaches the system through a declared surface.**
Why. Time, randomness, IO, scheduling, and identity are the inputs that make a run unrepeatable. A
function calling `time.Now()` cannot be replayed, and a system with one such call cannot be simulated.
The effect set names them; this rule says where they must go.
Enforce. `effects` plus `surfaces` (custom). Any function with `io`, `rand`, or `time` in its effect
set must obtain it from a declared surface parameter.

**`TS-I02` Every surface has a production implementation and a simulated one.**
Status. **Deleted.** Fails the five-question test: its failure mode (a surface with one
implementation) is exactly the condition under which `TS-I03`'s conformance obligation cannot run,
its enforcement is the same implementation counting `surfaces` performs for `TS-I03`, and its
marginal cost is zero — every cost it has is `TS-I03`'s cost. Two IDs, one check. The existence
requirement is folded into `TS-I03`; the default-fidelity claim moved to `TS-I07`. ID retired, not
reused.

**`TS-I03` Every surface has a production and a simulated implementation, and both pass one conformance suite.**
Why. A surface with one implementation is a surface in name only. If the fake and the real diverge,
you have verified a world that does not exist. One suite run against both is the only mechanism that
keeps them interchangeable.
Enforce. `surfaces` (custom) requires both implementations and the suite; CI runs the suite against
both (runtime).

**`TS-I04` The simulated implementation is at least as adversarial as the specification permits.**
Why. A fake that behaves better than reality hides the bugs you built the simulator to find. Inject
every fault the spec allows, not the ones you have happened to observe.
Enforce. `review` of the fault set, plus a CI check that every fault case in the interface is exercised
by the suite (runtime).

**`TS-I05` A surface interface must be able to express every fault its specification permits.**
Why. **This is the rule that decides whether any of the rest is worth anything.** If a real disk can
produce a torn write, a misdirected write, or an `fsync` that lies, and your `Storage` interface cannot
represent those, the simulator is structurally blind to an entire bug class and will run green
forever. Interfaces get designed around their failure modes rather than their happy-path ergonomics,
which makes them uglier and is the point. Whether the fault set matches reality is irreducibly
`review`.
Enforce. `surfaces` (custom) checks every declared fault has a case in the interface and a test.

**`TS-I06` Adapters contain no logic.**
Why. The adapter is the one place determinism is unobtainable, so it must be the one place nothing
interesting happens. A branch in an adapter is a decision the simulator never sees.
Enforce. `adapters` (custom). Cyclomatic complexity of 1 beyond error mapping.

**`TS-I07` Fidelity is configuration, not code.**
Why. The same suite must run fully simulated, partially simulated, and fully real, with the difference
being injection config. The default configuration is fully simulated. This is what turns fidelity
into a dial and stops the simulated and real paths diverging into two test suites that check
different things.
Enforce. `surfaces` (custom). Test code may not reference concrete surface implementation types.

## Memory and performance

Go's GC means "no dynamic allocation after startup" cannot be stated, let alone enforced. What
survives is the reason behind it: allocation is unpredictable latency, and unpredictable latency is
what you were trying to avoid.

**`TS-M01` Preallocate with a known capacity.**
Why. `append` into a nil slice in a loop is a growth curve, a copy per doubling, and garbage. When you
know the size, say it.
Enforce. `prealloc`, `makezero` (auto).

**`TS-M02` Hot paths allocate zero times.**
Why. See `TS-F03`, which makes this static via the `alloc` effect. The benchmark gate remains as a
cross-check on escape analysis.
Enforce. `effects` (custom) plus `AllocsPerRun` (runtime).

**`TS-M03` Pass structs larger than 64 bytes by pointer.**
Why. A by-value parameter copies, and a copy in a loop is a copy per iteration. Go has no `const`, so
the immutability the original got for free is simply lost; the copy avoidance is the part that
survives.
Enforce. `gocritic` `hugeParam` and `rangeValCopy` (auto).

**`TS-M04` Initialize large structs in place through an out pointer.**
Why. Same as the original minus the pointer stability argument, which Go's non-moving GC already
gives you. `func (r *Replica) Init(...) error` beats returning a large value, and it is mandatory for
anything containing a mutex, which cannot be copied at all.
Enforce. `govet copylocks` (auto) for locks, `outptr` (custom) for size.

**`TS-M05` Pooled types implement `Reset`, and `Put` is preceded by a reset.**
Why. Buffer bleeds. Go zeroes every `make`, so the classic case is gone, but `sync.Pool` and `buf[:0]`
reuse bring it straight back. Zero on release, not on acquire, so the leak cannot outlive the owner
who knew how much was sensitive.
Enforce. `poolzero` (custom). Partial.

**`TS-M06` Wire and hot structs are field-aligned with explicit padding.**
Why. Padding you did not declare is padding you did not zero, and on a serialized struct that is both
a leak and a determinism break.
Enforce. `govet fieldalignment` (auto), `wiretypes` (custom) for explicit padding fields.

**`TS-M07` No `any`, no reflection, no `fmt.Sprintf` in hot paths.**
Why. Each is an allocation, a dynamic dispatch, or both. `strconv` over `fmt` is usually a straight win.
Enforce. `perfsprint` (auto), plus the `alloc` effect.

**`TS-M08` Extract hot loops into standalone functions with primitive parameters and no receiver.**
Why. Without a receiver the compiler does not have to prove it can cache struct fields in registers,
and the reader can see the redundant computation.
Enforce. via `TS-F03`.

**`TS-M09` Batch across every boundary you cross.**
Why. Amortize network, disk, and syscall costs. A design decision made at design time, when you cannot
yet measure. Sketch the four resources and their bandwidth and latency before writing the code, because
after implementation the cheap wins are gone.
Enforce. via `TS-M10` for the anti-pattern; the design is `review`.

**`TS-M10` No IO inside a loop body.**
Why. You cannot check that a design batches, but you can check for its opposite, which has a shape. A
`Write`, `Query`, or syscall inside a `for` is the anti-batching pattern, and also every N plus one
query ever written.
Enforce. `ioinloop` (custom), unless annotated `//tiger:batched <reason>` — the annotation itself
surfaces as a standing advisory finding (escapes are never silent).

**`TS-M11` Hot paths are declared and cross-checked against the profiler.**
Why. See `TS-F03`, which is the enforcing rule. Kept as a separate ID because the profiler
cross-check is a CI gate rather than an analyzer.
Enforce. `effects` (custom) plus profile gate (runtime).

## Abstraction

**`TS-X01` No interface with exactly one implementation.**
Why. Abstractions are never zero cost and every one adds a leak risk. An interface with one
implementation has paid the cost and bought nothing, and it is the exact fingerprint of speculative
abstraction. Whole-program implementation counting is a solved analysis. Surface implementations under
`TS-I03` are exempt by construction, because they have two.
Enforce. `singleimpl` (custom, `types` plus `packages`, test doubles excluded).

**`TS-X02` No pass-through methods.**
Why. A method whose body is one forwarding call with unchanged arguments is indirection that exists to
be counted, not used. Each one lengthens the chain a reader must follow to find code that does
something.
Enforce. `passthrough` (custom). Budget of zero.

**`TS-X03` The package import graph is declared and enforced.**
Why. The control plane and data plane split cannot be verified, but its main consequence can. The
direction of every dependency is a fact you write down once and check forever. Layering is the
enforceable residue of architecture.
Enforce. `depguard` path rules driven by a checked-in layer file (auto).

## Canonical form

An intermediate representation has one spelling per meaning. Go has several for most things, and every
alternative spelling is a diff a human must read to confirm it means nothing.

**`TS-K01` One spelling per construct.**
Why. If a construct has one legal form, any diff touching it is semantic by construction, and a
reviewer never spends attention confirming a rewrite was cosmetic.
Rules. No `else` after a terminating branch. Error checks in the `if err != nil` form only, never
inverted. `for` over a range where a range is possible. Composite literals always keyed. No
parentheses beyond what precedence requires. Single-form nil checks.
Enforce. `canonical` (custom) plus existing auto-fixers. Auto-fixed on commit.

**`TS-K02` One import path per package, one alias per import path, module-wide.**
Why. Aliasing the same package differently across files defeats cross-file mechanical transforms and
makes grep lie.
Enforce. `canonical` (custom). Auto-fixed.

**`TS-K03` No dynamic dispatch where precision is claimed.**
Why. This is the enabling rule for everything upstream. An exact callgraph rather than a conservative
over-approximation is what makes effects, frames, and contracts precise instead of noisy. It cannot be
scoped by effect set, because dispatch precision is what makes effect sets computable; it is therefore
package-scoped under `TS-P01`, and a package that permits dispatch gets lower-confidence results
rather than an exemption.
Enforce. `closedworld` (custom). Interface method calls fail unless the receiver devirtualizes to a
single type.

**`TS-K04` Declaration order is total and deterministic.**
Why. A partial order leaves choices. A total order makes file layout a function of its contents, so a
tool can regenerate it and two implementations of one interface are diffable line by line.
Enforce. `canonical` (custom). Auto-fixed. Subsumes the older const/var/type/func ordering.

**`TS-K05` No shadowed identifier anywhere in a function, including across scopes.**
Why. `TS-S14` bans shadowing in nested scopes. Unique names within a function body make every
identifier occurrence resolvable without scope analysis, which is what lets a rename or an
extract-function transform be textual and still sound.
Enforce. `canonical` (custom).

**`TS-K06` Struct field order is semantic and declared.**
Why. `TS-M06` orders fields by alignment. For wire types the order is also the format, so it must be
stable against a tool that would otherwise reorder for packing. Declare which one governs.
Enforce. `wiretypes` (custom).

## Mechanical refactoring

The cash-out. Everything above exists so these can be true.

**`TS-R01` Extract-function, inline-function, and rename are proven behavior-preserving.**
Why. Given exact callgraphs, declared effects, declared frames, and unique identifiers, a tool can
prove a proposed extraction preserves behaviour by checking that the region's effect and frame sets
are unchanged and no identifier capture occurs. A proven transform does not need review.
Enforce. `refactor` (custom tool, not a linter). Commits produced entirely by proven transforms carry
a `Refactor-Proof:` trailer, and CI re-runs the proof rather than trusting the trailer.

**`TS-R02` Public surface changes are diffed against the previous release.**
Why. Every exported signature, effect set, contract, invariant, and package restriction forms a
surface. A machine-readable diff tells you the semantic version, decides whether downstream must act,
and catches the accidental widening nobody meant to publish.
Enforce. `surfacediff` (custom, checked-in surface file, CI diff).

**`TS-R03` Generated and hand-written code are indistinguishable and interchangeable.**
Why. The property that matters most in the near future, and the one a conventional style guide never
has to think about. If the dialect is canonical, effect-checked, and contract-checked, a
machine-generated diff faces exactly the same gate as a human one, and "who wrote this" stops being
the basis on which trust is assigned. Trust attaches to what the artifact provably satisfies.
Enforce. Follows from `TS-K01` through `TS-K06`, plus a CI check that generated files carry no
weakened directives.

## Naming

**`TS-N01` `MixedCaps`. Acronyms keep their case.**
Why. `snake_case` does not survive contact with Go. The standard library, struct tags, and every tool
assume `MixedCaps`, and exported-ness is encoded in the first letter. `VSRState`, `HTTPServer`, `id`
never `Id`.
Enforce. `staticcheck` ST1003, `revive` var-naming (auto).

**`TS-N02` No invented abbreviations. A short allowlist and nothing else.**
Why. The original bans abbreviations outright; Go's own convention wants short names in short scopes.
The compromise that holds is a closed vocabulary. `i`, `n`, `ok`, `err`, `ctx`, `buf`, `r`, `w`, `b`,
`t` are allowed because every Go reader knows them. `cfg`, `mgr`, `svc`, `tmp` are not, because you
invented them this morning.
Enforce. `varnamelen` with an explicit allowlist plus `noabbrev` (custom).

**`TS-N03` Units and qualifiers go last, sorted by descending significance.**
Why. `latencyMsMax`, not `maxLatencyMs`. Then `latencyMsMin` lines up under it and everything about
latency sorts together. Big-endian naming, so alphabetical order becomes semantic order.
Enforce. `unitsuffix` (custom).

**`TS-N04` Infuse names with meaning.**
Why. Name the lifetime and the ownership, not the type. `alloc Allocator` is fine; `gpa` and `arena`
tell the reader whether cleanup is their problem.
Enforce. via `TS-N12` and `TS-N13`, which ban the failures rather than requiring the virtue.

**`TS-N05` Related names have the same length.**
Why. `source` and `target` beat `src` and `dst` because `sourceOffset` and `targetOffset` line up in
the slice expression underneath, and the eye catches an asymmetry the brain would skim past.
Enforce. via `TS-N15`.

**`TS-N06` A helper is prefixed with the name of its caller.**
Why. The name carries the call history, so `readSector` and `readSectorRetry` sort together and read
in order.
Enforce. `declorder` (custom). Partial, detectable for single-caller unexported functions.

**`TS-N07` Two or more parameters of the same type means you need an options struct.**
Why. Go has no named arguments, so `Copy(src, dst string, retries, timeout int)` is a call site where
the compiler cannot help you and a swapped pair is a silent bug.
Enforce. `sametypeparams` (custom). Flags adjacent identical types and functions over four parameters.

**`TS-N08` A boolean parameter is a lie. Use a named type.**
Why. `Save(true)` means nothing at the call site. `Save(SyncImmediate)` means something.
Enforce. `sametypeparams` (custom).

**`TS-N09` Prefer nouns to participles.**
Why. `replica.pipeline` can be a heading in a design doc. `replica.preparing` must be rephrased first,
and derived names like `pipelineMax` compose where `preparingMax` does not.
Enforce. via `TS-N14`.

**`TS-N10` No stutter, no context-dependent overloading.**
Why. `replica.ReplicaState` reads badly. Worse, reusing a term that already means something else in
your domain costs a whole team a whole meeting.
Enforce. `revive` (auto) for stutter; overloading is `review` against a domain glossary.

**`TS-N11` Money and other exact quantities are integers, never floats.**
Why. Binary floating point cannot represent a tenth.
Enforce. `nofloat` (custom), scoped by `TS-P01`.

**`TS-N12` Semantically empty name tokens are forbidden.**
Why. `data`, `info`, `value`, `object`, `item`, `manager`, `handler`, `helper`, `util`, `process`,
`temp`, `misc`, `common`, `base`, `impl`, and `wrapper` are placeholders that survived into the commit.
None tells the reader what the thing is, which is the entire job of the name.
Enforce. `namedeny` (custom, token dictionary). Per-repo allowlist requires a reason. The escalation,
once a team is ready, is to invert the list: a committed glossary that every token must appear in,
which turns naming from a taste argument into a pull request against the glossary.

**`TS-N13` No type echo in names.**
Why. `userStr`, `cfgStruct`, `idsSlice`, and `errList` restate what the declaration already says and go
stale the moment the type changes.
Enforce. `namedeny` (custom).

**`TS-N14` Exported identifiers do not end in a present participle.**
Why. Enforces `TS-N09`. A trailing `-ing` is a suffix check, so nouns-over-participles turns out to be
almost free.
Enforce. `participle` (custom), allowlisting genuine nouns like `Encoding` and `Logging`.

**`TS-N15` Known name pairs use the approved half.**
Why. Enforces `TS-N05` without measuring anything. Commit a table of known pairs and ban the wrong
half: `src`/`dst` becomes `source`/`target`, `in`/`output` becomes `input`/`output`. The equal length
is why the table is built, not what the analyzer checks.
Enforce. `namepairs` (custom, table lookup). No false positives, because it is a lookup.

## Layout and comments

**`TS-L01` `gofmt` is not negotiable. `gofumpt` on top.**
Why. Zero-config formatting is the single best thing about Go tooling. Do not fight it, configure it,
or discuss it.
Enforce. `gofumpt` (auto).

**`TS-L02` Lines at most 100 columns, tab counted as 4.**
Why. Two files side by side, nothing behind a scrollbar. Pure B0; the number is a Schelling point and
arguing about it is the most expensive thing you can do with it.
Enforce. `lll` (auto).

**`TS-L03` Declaration order is `const`, `var`, `type`, `func`.**
Why. A file read top-down should introduce its vocabulary before using it.
Enforce. `decorder` (auto). Subsumed by `TS-K04` where canonical form is adopted.

**`TS-L04` Important things near the top. Entry points first.**
Why. A file is read top-down on the first pass. `main` first, then the type the file is about, then
its constructor, then its methods, exported before unexported.
Enforce. `declorder` (custom). Subsumed by `TS-K04`.

**`TS-L05` Struct order is fields, nested types, constructor, methods.**
Why. Same reason, one level down. Complex nested types get promoted to top level.
Enforce. `declorder` (custom). Advisory.

**`TS-L06` Comments are sentences.**
Why. Comments are prose describing the code, not scribbling in the margin. Trailing comments may be
fragments.
Enforce. `godot` (auto).

**`TS-L07` Every exported identifier has a doc comment starting with its name.**
Why. It is what `go doc` prints, and the only documentation most callers read.
Enforce. `revive` exported rule (auto).

**`TS-L08` Always say why.**
Why. Code says what. A comment repeating the what is noise; one giving the reason gives the reader the
criteria to evaluate your decision and to change it correctly later. The most important rule here and
the least mechanizable.
Enforce. `review`, plus `TS-L12` and `TS-L13` for the checkable halves.

**`TS-L09` Every escape-hatch directive carries a reason.**
Why. An escape without a reason is a rule silently deleted. Applies to `//nolint`,
`//tiger:batched`, and the `//tiger:<rule-id> <reason>` deviation form when that ships. Pins and
intent declarations are exempt: they carry expressions, not excuses, and tightening a contract
needs no justification. *(Amended 2026-08-12: `//tiger:bounded` removed from the list — the
escape itself is withdrawn under the admission test; see `TS-S02`.)* Beyond the reason check,
every escape directive surfaces as a standing advisory finding on every run, so an escape is
counted and visible for as long as it exists.
Enforce. `nolintlint` with `require-explanation` (auto) plus `directives` (custom): blocking on
a missing reason or an unknown verb, advisory on each escape present.

**`TS-L10` Blank lines group acquisition with its `defer`, and no `defer` inside a loop.**
Why. Put acquire and release next to each other and a leak becomes visible. A `defer` in a loop is an
unbounded queue of cleanup, which violates `TS-S02`.
Enforce. `deferdistance` (custom). Blocking on the loop case, advisory on distance.

**`TS-L11` Imports grouped standard, external, internal.**
Why. Mechanical, so let the machine do it.
Enforce. `gci` (auto).

**`TS-L12` A doc comment may not restate its identifier.**
Why. You cannot require a comment to say why; you can reject the one that only says what, which is the
exact output a comment-presence linter produces. `// Count returns the count.` is worse than nothing,
because it satisfies the tooling and teaches the team the tooling is stupid.
Enforce. `restatement` (custom). Advisory; expect to tune.

**`TS-L13` Commit bodies contain a `Why:` section.**
Why. A pull request description is not in the repository and is invisible in `git blame`, so the
reason must be in the commit or it is gone.
Enforce. CI check on message shape (runtime).

## Dependencies and tooling

**`TS-D01` The standard library, and nothing else, where restrictions permit.**
Why. Every dependency is a supply chain, a safety surface, a performance unknown, and an install-time
cost, amplified through everything built on top. Go's standard library makes this genuinely
achievable, which is not true of most languages.
Enforce. `depguard` with per-package allowlists from `TS-P01` (auto).

**`TS-D02` Dependencies are vendored and pinned, including the toolchain.**
Why. Reproducible builds. `go.mod` pins the toolchain, `vendor/` pins the rest, CI verifies both.
Enforce. `go mod verify` and a vendor diff check (runtime).

**`TS-D03` Scripts are Go programs, not shell.**
Why. Cross-platform, type-checked, and it runs the same on everyone's machine instead of hitting a
Bash version difference. Write `cmd/ci/main.go`, not `scripts/ci.sh`.
Enforce. CI check for `*.sh` outside an allowlist (runtime).

**`TS-D04` Generated code is committed and verified.**
Why. If `go generate` output is not in the tree, the build depends on a tool version nobody pinned.
Enforce. CI regenerates and diffs (runtime).

**`TS-D05` `TODO` and `FIXME` reference a tracker ID that exists and is open.**
Why. A dated comment is a decision to defer. Deferring is allowed; forgetting is not.
Enforce. `godox` plus a CI call to the tracker (runtime).

**`TS-D06` Per-package budgets may only decrease.**
Why. What a zero-technical-debt policy looks like on a codebase that already has some. Check in a file
of numbers per package: total complexity, directive count, skipped tests, assertion density,
allocation counts on hot benchmarks. A global threshold gets raised at 2am before a release; a ratchet
cannot be raised without a commit that says so. **Track the directive count as a first-class metric**,
because forced directives are how a dialect degrades into a style guide with extra steps.
Enforce. CI, with the budget file in review (runtime).

**`TS-D07` Skipped tests are reported on every run.**
Why. A skipped test is a test that passes. Visibility beats paperwork: a report recomputed from
the code on every run cannot go stale, while a tracker reference can outlive its issue.
Enforce. `skipcheck` (custom).

## Testing and determinism

**`TS-T01` Core logic is deterministic. Time, randomness, and IDs are injected.**
Why. This is what makes simulation testing possible, and simulation testing is what found the bugs
that made TigerBeetle worth imitating.
Enforce. `depguard` and `forbidigo` (auto), plus the `time` and `rand` effects (`TS-F01`) and
`TS-I01`.

**`TS-T02` Map iteration order never reaches an output.**
Why. Go randomizes it deliberately. Sort the keys before you serialize, log, or hash.
Enforce. `maporder` (custom). Partial; backstopped by `TS-T11`.

**`TS-T03` Every parser, decoder, and state machine has a fuzz target.**
Why. Tests must be exhaustive, with invalid data as well as valid, and with valid data going invalid.
A fuzzer covers the negative space you did not think of. It proves the presence of bugs and never
their absence, so it is the last line of defence and not the first.
Enforce. `fuzzcoverage` (custom).

**`TS-T04` No `time.Sleep` in tests. Use `testing/synctest`.**
Why. A sleep in a test is a race you decided to lose slowly. `synctest` gives you a fake clock and a
deterministic scheduler.
Enforce. `forbidigo` scoped to `_test.go` (auto).

**`TS-T05` `-race` on every CI run. Seeds are logged and reproducible.**
Why. A failing simulation you cannot replay is a rumour. Every simulation run logs a seed sufficient
to replay it exactly.
Enforce. CI (runtime).

**`TS-T06` Tests state their goal and method.**
Why. A reader should be able to skip a test or dive into it without reverse-engineering the setup.
Enforce. `testdoc` (custom). Every `Test` function needs a doc comment with a `Goal.` line.

**`TS-T07` Error-path branch coverage is measured separately with its own floor.**
Why. The evidence behind `TS-E01` says catastrophic failures come from mishandling errors the software
already signalled, which makes error paths the code most worth covering and least likely to be
covered. An aggregate coverage number hides this, because error branches are a small fraction of
blocks and a large fraction of risk.
Enforce. CI, parsing the coverage profile and restricting to error-guarded blocks (runtime).

**`TS-T08` Mutation score floor.**
Why. The closest thing to a machine check for "tests must test exhaustively". Coverage says a line
ran; a mutation score says the test would have noticed if the line were wrong. **This is also the
anti-Goodhart oracle**: it is the one metric here that cannot be raised without the tests genuinely
improving, so a rising conformance score against a flat mutation score is a fire.
Enforce. Nightly mutation run (runtime).

**`TS-T09` Every parser has a rejection corpus, and every file in it is asserted to be rejected.**
Why. Turns "test with invalid data" from an intention into a file count, and it grows every time the
fuzzer finds something.
Enforce. `corpuscheck` (custom).

**`TS-T10` Table-driven tests have named cases.**
Why. A case with a `name` field explains its own intent in the failure output, which is where the
reader will be standing when they need it.
Enforce. `tablename` (custom).

**`TS-T11` The suite runs twice and the output is diffed.**
Why. `TS-T01` bans the sources of nondeterminism it knows about and `TS-T02` catches map iteration
heuristically; neither proves the result. Running twice with the same seed and diffing does prove it,
for the paths the suite covers, and it costs one CI job rather than one analyzer. Any diff is either a
nondeterminism you did not know about or a test logging a timestamp.
Enforce. CI (runtime).

---

# Part IV: Traceability

Every rule, the benefits it serves, and its class. **Sub** means the rule prevents a named failure
mode. **Coord** means the value is in unanimity and the specific answer is arbitrary. **Mixed** means
the rule is substantive and its particular form is convention.

Orphans, meaning rules citing no benefit: zero. That is the point of maintaining the table. A rule
that cannot fill the third column does not go in.

| ID | Rule | Benefits | Class |
| --- | --- | --- | --- |
| TS-P01 | A package's restriction set is declared; undeclared axe... | B8, B3 | Sub |
| TS-P02 | A package's effective precision is bounded by the weake... | B8 | Sub |
| TS-P03 | Weakening a package's restriction set requires a direct... | B7, B8 | Sub |
| TS-S01 | No recursion | B1, B6 | Sub |
| TS-S02 | Every loop has an upper bound | B1 | Sub |
| TS-S03 | An unbounded event loop selects on ctx.Done() and asser... | B1, B2 | Sub |
| TS-S04 | Hard limit of 70 lines per function | B3, B0 | Mixed |
| TS-S05 | Cyclomatic complexity at most 10, cognitive at most 15,... | B3, B0 | Mixed |
| TS-S06 | One logical operator per condition | B3, B2 | Sub |
| TS-S07 | Split compound assertions | B2 | Sub |
| TS-S08 | Every switch over a closed set is exhaustive and ends i... | B7, B2 | Sub |
| TS-S09 | No goto, no labeled break or continue | B3 | Sub |
| TS-S10 | No init() functions | B3, B4 | Sub |
| TS-S11 | No package-level mutable state | B8, B4 | Sub |
| TS-S12 | No unsafe, no cgo, no reflection | B8, B6, B2 | Sub |
| TS-S13 | Declare variables at the smallest scope, at the point o... | B3, B2 | Sub |
| TS-S14 | No shadowing | B2 | Sub |
| TS-S15 | Struct literals name every field | B7 | Sub |
| TS-S16 | No magic numbers. Every limit is a named constant with... | B1, B7, B3 | Sub |
| TS-S17 | Assert relationships between constants at compile time | B2, B7 | Sub |
| TS-S18 | No naked panic outside the assert package | B2, B3 | Sub |
| TS-S19 | Integer conversions are checked or provably safe | B2 | Sub |
| TS-S20 | Type assertions are always checked | B2 | Sub |
| TS-S21 | Every limit constant participates in a compile-time rel... | B1, B7 | Sub |
| TS-S22 | Every limit constant carries a derivation, and the anal... | B1, B7, B6 | Sub |
| TS-A01 | Average at least two assertions per function | B5, B2 | Sub |
| TS-A02 | Assert every precondition on entry and every postcondit... | B2, B5 | Sub |
| TS-A03 | Assert the invariant on both sides of every boundary | B2, B5 | Sub |
| TS-A04 | Assert the negative space | B5 | Sub |
| TS-A05 | Assertions never have side effects | B4, B2 | Sub |
| TS-A06 | Distinguish index, count, and size | B2 | Sub |
| TS-A07 | Every declared invariant is asserted in at least two di... | B2, B5, B7 | Sub |
| TS-A08 | Symmetric boundary functions assert the same invariant set | B2 | Sub |
| TS-A09 | Every invariant has a test that violates it | B5 | Sub |
| TS-Q01 | Domain quantities are named types. Conversions live in... | B2, B7 | Sub |
| TS-Q02 | Parse, do not validate. Exported functions take domain... | B2, B7, B3 | Sub |
| TS-Q03 | Units are types, not name suffixes | B2 | Sub |
| TS-E01 | Every error is handled. No exceptions | B2 | Sub |
| TS-E02 | Discarding a result with _ requires a justification com... | B2, B7 | Sub |
| TS-E03 | Wrap once with %w, compare with errors.Is and errors.As | B2, B7 | Sub |
| TS-E04 | Sentinel errors are ErrFoo, error types are FooError, m... | B0, B7 | Mixed |
| TS-E05 | Never return both a nil value and a nil error | B3, B7 | Sub |
| TS-E06 | Minimize return arity | B3 | Sub |
| TS-E07 | Close errors on writable resources are checked | B2 | Sub |
| TS-E08 | No naked returns | B3 | Sub |
| TS-C01 | Prefer a single owner goroutine over shared memory with... | B8, B4, B3 | Sub |
| TS-C02 | Every goroutine has an owner, a documented exit conditi... | B1, B8 | Sub |
| TS-C03 | Goroutine leaks fail the test suite | B1 | Sub |
| TS-C04 | Every channel has an explicit capacity, and the capacit... | B1, B6 | Sub |
| TS-C05 | Every blocking receive or send has a ctx.Done() case | B1 | Sub |
| TS-C06 | No mutex held across a channel operation or a call that... | B1, B6 | Sub |
| TS-C07 | context.Context is the first parameter, never stored in... | B1, B8 | Sub |
| TS-C08 | No time.Sleep in production code | B4, B6 | Sub |
| TS-C09 | Run at your own pace. Do not spawn work in direct react... | B1, B6 | Sub |
| TS-C10 | A type marked //tiger:owner has no exported fields... | B8, B4 | Sub |
| TS-C11 | No exported method returns a pointer, slice, or map tha... | B8 | Sub |
| TS-C12 | Channel types used across goroutines are declared in on... | B3, B1 | Sub |
| TS-C13 | One concurrency paradigm per package | B3 | Sub |
| TS-F01 | Every function's effect set is computed; a pin makes it a contract | B2, B8, B4 | Sub |
| TS-F02 | A pin bounds the entire subtree beneath it | B2, B8 | Sub |
| TS-F03 | A function marked //tiger:hot has effect set at mo... | B6 | Sub |
| TS-F04 | Assertion arguments are pure | B4, B2 | Sub |
| TS-F05 | A critical section has effect set excluding block | B1, B6 | Sub |
| TS-F06 | Deleted, superseded by TS-F01 + TS-T01 | — | Deleted |
| TS-F07 | Every function's frame is computed; writes outside a pi... | B8, B3 | Sub |
| TS-F08 | spawn is a declared effect, and only a supervisor may d... | B1, B8 | Sub |
| TS-V01 | Every unbounded loop has a verified variant: synthesize... | B1 | Sub |
| TS-V02 | Functions are total | B1, B6 | Sub |
| TS-V03 | Preconditions are declared and discharged at call sites | B2, B3 | Sub |
| TS-I01 | Every nondeterministic effect reaches the system throug... | B4, B5 | Sub |
| TS-I02 | Deleted, folded into TS-I03 | — | Deleted |
| TS-I03 | Every surface has a production and a simulated implemen... | B4, B2 | Sub |
| TS-I04 | The simulated implementation is at least as adversarial... | B5 | Sub |
| TS-I05 | A surface interface must be able to express every fault... | B5, B4 | Sub |
| TS-I06 | Adapters contain no logic | B4, B3 | Sub |
| TS-I07 | Fidelity is configuration, not code | B4, B7 | Sub |
| TS-M01 | Preallocate with a known capacity | B6 | Sub |
| TS-M02 | Hot paths allocate zero times | B6 | Sub |
| TS-M03 | Pass structs larger than 64 bytes by pointer | B6 | Sub |
| TS-M04 | Initialize large structs in place through an out pointer | B6 | Sub |
| TS-M05 | Pooled types implement Reset, and Put is preceded by a... | B8, B2 | Sub |
| TS-M06 | Wire and hot structs are field-aligned with explicit pa... | B6, B4 | Sub |
| TS-M07 | No any, no reflection, no fmt.Sprintf in hot paths | B6 | Sub |
| TS-M08 | Extract hot loops into standalone functions with primit... | B6, B3 | Sub |
| TS-M09 | Batch across every boundary you cross | B6 | Sub |
| TS-M10 | No IO inside a loop body | B6 | Sub |
| TS-M11 | Hot paths are declared and cross-checked against the pr... | B6, B7 | Sub |
| TS-X01 | No interface with exactly one implementation | B3, B6 | Sub |
| TS-X02 | No pass-through methods | B3 | Sub |
| TS-X03 | The package import graph is declared and enforced | B8, B3 | Sub |
| TS-K01 | One spelling per construct | B7, B0 | Mixed |
| TS-K02 | One import path per package, one alias per import path,... | B7, B0 | Mixed |
| TS-K03 | No dynamic dispatch where precision is claimed | B2, B8 | Sub |
| TS-K04 | Declaration order is total and deterministic | B0, B7 | Mixed |
| TS-K05 | No shadowed identifier anywhere in a function, includin... | B2, B7 | Sub |
| TS-K06 | Struct field order is semantic and declared | B4, B7 | Sub |
| TS-R01 | Extract-function, inline-function, and rename are prove... | B7, B3 | Sub |
| TS-R02 | Public surface changes are diffed against the previous... | B7 | Sub |
| TS-R03 | Generated and hand-written code are indistinguishable a... | B7, B2 | Sub |
| TS-N01 | MixedCaps. Acronyms keep their case | B0 | Coord |
| TS-N02 | No invented abbreviations. A short allowlist and nothin... | B3 | Sub |
| TS-N03 | Units and qualifiers go last, sorted by descending sign... | B3, B0 | Mixed |
| TS-N04 | Infuse names with meaning | B3 | Sub |
| TS-N05 | Related names have the same length | B3, B0 | Mixed |
| TS-N06 | A helper is prefixed with the name of its caller | B3, B0 | Mixed |
| TS-N07 | Two or more parameters of the same type means you need... | B2, B7 | Sub |
| TS-N08 | A boolean parameter is a lie. Use a named type | B3, B2 | Sub |
| TS-N09 | Prefer nouns to participles | B3, B0 | Mixed |
| TS-N10 | No stutter, no context-dependent overloading | B0 | Coord |
| TS-N11 | Money and other exact quantities are integers, never fl... | B2 | Sub |
| TS-N12 | Semantically empty name tokens are forbidden | B3 | Sub |
| TS-N13 | No type echo in names | B3, B7 | Sub |
| TS-N14 | Exported identifiers do not end in a present participle | B3, B0 | Mixed |
| TS-N15 | Known name pairs use the approved half | B3, B0 | Mixed |
| TS-L01 | gofmt is not negotiable. gofumpt on top | B0 | Coord |
| TS-L02 | Lines at most 100 columns, tab counted as 4 | B0, B3 | Coord |
| TS-L03 | Declaration order is const, var, type, func | B0 | Coord |
| TS-L04 | Important things near the top. Entry points first | B0, B3 | Coord |
| TS-L05 | Struct order is fields, nested types, constructor, methods | B0 | Coord |
| TS-L06 | Comments are sentences | B0 | Coord |
| TS-L07 | Every exported identifier has a doc comment starting wi... | B3, B0 | Mixed |
| TS-L08 | Always say why | B7, B3 | Sub |
| TS-L09 | Every escape-hatch directive carries a reason | B7, B2 | Sub |
| TS-L10 | Blank lines group acquisition with its defer, and no de... | B2, B3 | Sub |
| TS-L11 | Imports grouped standard, external, internal | B0 | Coord |
| TS-L12 | A doc comment may not restate its identifier | B3 | Sub |
| TS-L13 | Commit bodies contain a Why: section | B7 | Sub |
| TS-D01 | The standard library, and nothing else, where restricti... | B8, B6 | Sub |
| TS-D02 | Dependencies are vendored and pinned, including the too... | B4, B8 | Sub |
| TS-D03 | Scripts are Go programs, not shell | B4, B0 | Mixed |
| TS-D04 | Generated code is committed and verified | B4 | Sub |
| TS-D05 | TODO and FIXME reference a tracker ID that exists and i... | B7 | Sub |
| TS-D06 | Per-package budgets may only decrease | B7, B3 | Sub |
| TS-D07 | Skipped tests are reported on every run | B5 | Sub |
| TS-T01 | Core logic is deterministic. Time, randomness, and IDs... | B4 | Sub |
| TS-T02 | Map iteration order never reaches an output | B4 | Sub |
| TS-T03 | Every parser, decoder, and state machine has a fuzz target | B5 | Sub |
| TS-T04 | No time.Sleep in tests. Use testing/synctest | B4 | Sub |
| TS-T05 | -race on every CI run. Seeds are logged and reproducible | B4, B5 | Sub |
| TS-T06 | Tests state their goal and method | B3 | Sub |
| TS-T07 | Error-path branch coverage is measured separately with... | B5, B2 | Sub |
| TS-T08 | Mutation score floor | B5 | Sub |
| TS-T09 | Every parser has a rejection corpus, and every file in... | B5 | Sub |
| TS-T10 | Table-driven tests have named cases | B3 | Sub |
| TS-T11 | The suite runs twice and the output is diffed | B4 | Sub |

## Distribution

| Benefit | Rules citing it |
| --- | --- |
| B3 Local reasoning | 44 |
| B2 Short defect distance | 40 |
| B7 Change safety | 31 |
| B0 Uniformity | 23 |
| B4 Determinism | 23 |
| B6 Predictable cost | 22 |
| B8 Blast radius | 19 |
| B1 Bounded execution | 18 |
| B5 Defect discovery | 15 |

Of 145 rules, 120 are substantive, 14 are substantive in a conventional form, 9 are pure convention,
and 2 are deleted. Local reasoning and short defect distance carry the document between them, which is
a fair description of what TigerStyle is once the poetry is stripped off. B0 is cited by 23 rules, so
roughly one rule in six exists for agreement rather than correctness. Better to know that number than
to pretend it is zero.

## The nine coordination-only rules

`TS-N01`, `TS-N10`, `TS-L01`, `TS-L02`, `TS-L03`, `TS-L04`, `TS-L05`, `TS-L06`, `TS-L11`, plus the
formatting halves of the 14 `Mixed` rules.

These are conventions. Their entire value is that everyone follows them, they are enforced by tooling
so nobody has to think about them, and arguing about them is the most expensive thing you can do with
them. If someone wants to change one, the only valid argument is "the new answer is also unanimous",
never "the new answer is better".

Two consequences. Never defend a coordination rule with a safety story, because someone will
eventually notice and then distrust the rules that do have safety stories. And never spend review time
on them, since a tool does it.

## Thresholds are calibrated, not derived

Twelve numbers here are calibration points rather than consequences of any argument: 70 lines, 100
columns, cyclomatic 10, cognitive 15, nesting 3, 64 bytes for pointer passing, two assertions per
function, five methods per interface, four parameters, ten lines of declaration-to-use distance,
twenty profiled functions, and a dupl threshold of 150 tokens.

The distinction that matters: **the existence of each bound is justified** by B1 or B3 or B6, and the
particular number is a starting point from someone else's codebase. Changing a number needs data from
your codebase and is a normal thing to do. Removing a bound needs an argument against the benefit and
is not.

Anyone who says "70 is arbitrary" is correct and has not made an argument. Anyone who says "so there
should be no limit" has made one, it is against B3, and they need to take it up with the working
memory claim.

---

# Part V: Implementation

## The custom analyzers

All are `golang.org/x/tools/go/analysis` passes. Ship them as one binary via `multichecker` for local
use (`tiger check ./...`), and as a golangci-lint module plugin (`.custom-gcl.yml` plus
`golangci-lint custom`) so they run in the same pass as everything else.

**Output contract:** analyzers print computed function-scoped facts in pin syntax — the exact
`//tiger:...` line a developer would paste. Report and pin are one format, so adopting a pin is a
paste, comparing a failed pin to reality is a two-line diff, and a `tiger pin <func>` command can
write the declaration mechanically.

**Acceptance contract:** there are no effort estimates — implementation is AI-driven, and the
binding constraint is functional acceptance, which every analyzer defines before it merges. Per
rule it enforces: an `analysistest` corpus in which the rule's failure-mode example (from its Why)
fires the diagnostic and the compliant rewrite stays silent — the functional requirement,
executable. For pin-enforcing analyzers, a test asserting report output is byte-identical to pin
syntax. And no silent scope cuts: where coverage is narrower than the rule claims (heuristic,
advisory, partial predicate language), the corpus contains the known miss, marked as such. An
analyzer without its corpus does not merge, no matter how plausible its implementation looks.

| Analyzer | Rules | Approach |
| --- | --- | --- |
| `effects` | TS-F01–F05, F08, A05, C06, C08, T01 | `buildssa`, compute effect sets over the callgraph, report unpinned changes in pin syntax, enforce each pin over its callee subtree |
| `frames` | TS-F07 | `buildssa` points-to over the restricted fragment |
| `contracts` | TS-V03 | Abstract interpretation over nil-ness, integer ranges, length relations, invariant IDs |
| `variant` | TS-V01, V02 | `buildssa`; synthesizes candidate variants for linear loops, verifies pinned ones at loop head and back edges |
| `surfaces` | TS-I01, I03, I05, I07 | `types` plus `packages`; surface declarations, implementation counting, conformance suite presence |
| `closedworld` | TS-K03 | `types` plus `packages`; devirtualization check |
| `canonical` | TS-K01, K02, K04, K05 | AST, with auto-fix |
| `refactor` | TS-R01 | Not a linter. A transform tool that emits a proof obligation CI re-checks |
| `surfacediff` | TS-R02, P02, P03 | Serialize the exported surface plus each package's computed precision bound, diff against a checked-in file |
| `restrictions` | TS-P01–P03 | Per-package restriction declarations, transitive precision bound |
| `derivation` | TS-S22 | Parse a trailing `Name = expr` comment, evaluate against package constants, compare |
| `invariantrefs` | TS-A07 | Group `assert.Invariant` calls by constant, count referencing functions |
| `invariantsymmetry` | TS-A08 | Pair functions by naming convention, compare invariant sets |
| `invariantnegative` | TS-A09 | Require `assert.Violates` per declared invariant |
| `norecursion` | TS-S01 | `buildssa`, CHA callgraph, report any cycle |
| `boundedloop` | TS-S02, S03 | AST, classify every loop; unclassifiable loops are findings — no directive waives TS-S02 |
| `compoundcond` | TS-S06, S07, S08 | AST, count binary logical operators per condition |
| `assertdensity` | TS-A01, A02 | AST plus `types`, count assert calls per function and package |
| `quantitycast` | TS-Q01, Q03 | `types`, forbid direct conversions outside the quantity package |
| `domaintypes` | TS-Q02 | `types`, exported signatures may not take bare primitives |
| `sametypeparams` | TS-N07, N08 | `types`, adjacent identical types, bool params, arity |
| `namedeny` | TS-N12, N13 | AST plus token dictionary |
| `namepairs` | TS-N15 | Table lookup |
| `participle` | TS-N14 | Suffix check plus allowlist |
| `unitsuffix` | TS-N03 | AST plus token dictionary and a position rule |
| `noabbrev` | TS-N02 | AST plus dictionary; the dictionary is the work |
| `ownership` | TS-C10 | `types`, exported field count, sync types, noCopy |
| `escapecheck` | TS-C11 | AST, returns of `&x.field`, `x.slice`, `x.map` from exported methods |
| `paradigm` | TS-C13 | `types`, mutex fields versus channel fields per package |
| `chandecl` | TS-C12 | AST, channel type declarations per package |
| `selectctx` | TS-C05 | AST, every blocking select needs a Done case |
| `nogoroutine` | TS-C02, C09 | AST, `GoStmt` outside supervisors |
| `queuebound` | TS-C04 | AST, slice-backed queues with unbounded append |
| `ioinloop` | TS-M10 | `types`, IO calls inside loop bodies |
| `hotpath` | TS-F03, M11 | AST, constraints on `//tiger:hot` functions |
| `poolzero` | TS-M05 | `buildssa`, `sync.Pool.Put` argument must be reset |
| `outptr` | TS-M04 | `types`, constructors returning oversized value types |
| `wiretypes` | TS-S17, M06, K06 | AST plus `types`, fixed-size fields, explicit padding, size assertion |
| `limitrelate` | TS-S21 | AST, every Max/Min constant appears in a relational assertion |
| `singleimpl` | TS-X01 | `types` plus `packages`, whole-module implementation counting |
| `passthrough` | TS-X02 | AST, body is a single forwarding call |
| `adapters` | TS-I06 | AST, complexity of 1 beyond error mapping |
| `errignore` | TS-E02 | AST plus `types`, `_ =` on error requires a comment |
| `returnarity` | TS-E06 | `types`, result count and composition |
| `paniccheck` | TS-S18 | AST, panic outside the assert package |
| `nogoto` | TS-S09 | AST, `BranchStmt` with Goto or a label |
| `nofloat` | TS-N11 | `types`, float types in restricted packages |
| `declorder` | TS-N06, L04, L05 | AST, declaration and method order |
| `deferdistance` | TS-L10 | AST plus token positions |
| `declusedistance` | TS-S13 | AST, lines between declaration and first use. Noisy, keep advisory |
| `restatement` | TS-L12 | Token overlap between comment and identifier. Tune it |
| `directives` | TS-L09 | AST, every escape-hatch directive carries a reason |
| `maporder` | TS-T02 | `buildssa`, range over a map whose body appends or writes. Heuristic |
| `fuzzcoverage` | TS-T03 | `packages`, parsers must have a Fuzz function |
| `corpuscheck` | TS-T09 | Directory presence plus a test that iterates it |
| `tablename` | TS-T10 | AST, table test literals require a name field |
| `testdoc` | TS-T06 | AST, Test functions need a Goal line |
| `skipcheck` | TS-D07 | AST, every `t.Skip` surfaces as a standing advisory |

The bottom two thirds are cheap and cover most of the value. The top six are the ones that turn
heuristics into guarantees, and they are a team rather than a quarter.

## What does not port from Zig

| Original rule | Why it does not port | What replaces it |
| --- | --- | --- |
| `snake_case` for functions, variables, files | Go encodes export visibility in the first letter and the ecosystem assumes `MixedCaps`. Fighting it breaks `go doc`, struct tags, and every linter | `TS-N01`, with descriptive naming kept via `TS-N02` |
| 4 spaces of indentation | `gofmt` uses tabs and will not be configured. The argument was visual distance, and a tab renders at whatever width the reader chose | `TS-L01` plus `.editorconfig` |
| Braces on every `if` | Go requires them | nothing needed |
| Single-line `if (a) assert(b)` | `gofmt` splits composite statements, so the form is not expressible | `assert.Implies(a, b, msg)` |
| Explicitly-sized types everywhere | `len`, `cap`, and slice indices are `int` by language definition. Banning `int` means casting at every index, adding the bugs you were avoiding | `int` in memory, fixed-size types mandatory across wire, disk, and FFI, plus `TS-S19` |
| All memory statically allocated at startup | Go has a GC and no allocator control. Not expressible, and pretending otherwise produces cargo cult | `TS-M01`, `TS-F03`, `TS-C04`. Same goal, different mechanism |
| Pass arguments over 16 bytes as `*const` | Go has no `const` qualifier and no immutable reference | `TS-M03` for copy cost only. The immutability guarantee is lost; `TS-F07` frames are the partial recovery |
| In-place init for pointer stability | Go's GC does not move heap objects and escape analysis handles most of it | `TS-M04`, narrowed to large structs and mutex-containing types |
| Explicitly pass library options | Go has no default arguments. The equivalent failure is the zero value you did not think about | `TS-S15` |
| Compile-time assertions via `comptime` | Go has no compile-time execution. You get constant overflow and interface satisfaction checks | `TS-S17`, which covers sizes and constant relationships but not arbitrary predicates |
| `@divExact`, `@divFloor`, `div_ceil` | No such builtins, and `/` truncates toward zero, so `-7/2` is `-3`. Signed division is a sharper trap in Go | A `mathx` package with `DivExact`, `DivFloor`, `DivCeil`, plus an analyzer banning bare `/` on signed integers |
| Functions run to completion without suspending | Go has no async coloring. The scheduler preempts any goroutine at any instruction, so "does not suspend" is not a property a function can have | `TS-C01`, `TS-C10`, `TS-F07`. The reachable version is ownership, not suspension |
| Callbacks go last | Go's conventions are fixed: `ctx` first, `error` last | `TS-C07` |
| Follow the Zig style guide | Different language | Effective Go, Go Code Review Comments, Google Go Style Guide, in that order |
| Write scripts in Zig | Substrate | `TS-D03` |

## What no amount of tooling fixes

Six items, in a codebase where every rule above is enforced. They are not shrinking further, and they
are all the same kind of thing.

| What | Why no machine will do it | Where it lives |
| --- | --- | --- |
| Is this the right invariant | The analyzer proves the code satisfies `inv.SequenceMonotonic`. Whether monotonic sequence numbers are what the protocol needs is a claim about the protocol | The `inv` package |
| Are the derivation's inputs true | `TS-S22` checks that `BatchMax` follows from `SectorSize`. Whether the disk has 512-byte sectors is a fact about the world | The constants file |
| Is the contract right | `TS-V03` proves callers establish the precondition. Whether the header should be that size is design | Contract annotations |
| Is the fault model complete | `TS-I05` checks every declared fault is expressible and tested. Whether real disks fail in ways you did not declare cannot be answered from inside the model | Surface interfaces |
| Should this effect be permitted | `TS-F01` proves the effect set is accurate. Whether this function should be doing IO at all is design | Effect declarations |
| Is this the right product | No comment | Not a code review question, and never was |

All six are instances of one thing.

> A machine can prove code satisfies a specification. Nothing can prove the specification is the right
> one, because rightness is a relation between the specification and the world, and the code contains
> no representation of the world.

This is not a limitation to engineer around. It is the correct final resting place for human
attention, and it is where a reviewer's time was always worth the most and least often spent.

## The honest limits

**The specification gap is irreducible.** Stated above. Everything else here is engineering.

**Goodhart applies with force.** Once conformance is the merge criterion, conformance is what gets
optimized. The defence is the proxy test from Part I plus one genuine external oracle, and `TS-T08` is
that oracle.

**False positives are the real failure mode.** An analyzer wrong two percent of the time on a blocking
rule, across a thousand merges, is twenty forced directives, and directives are how a dialect degrades
into a style guide with extra steps. `TS-D06` tracks the count; treat every one as a bug report
against the analyzer rather than a resolved issue.

**Cost is exponential in the tail.** Off-the-shelf linters reach roughly 80 percent mechanization in an
afternoon. The promoted rules reach 96 percent for about 40 analyzers. The last few percent costs an
effect system, a points-to analysis, an abstract interpreter, and a refactoring prover. Whether the
tail is worth it depends entirely on how expensive your defects are, which is why this is a good idea
for a database and a bad idea for most software.

**You are hiring for a language nobody knows.** Onboarding is longer and some good engineers will not
want to work this way. The precision gradient is the mitigation and it is a partial one.

## Adoption

The chains only pay when complete, so build them in dependency order rather than by ease.

**Stage 0, the free stuff.** Every `auto` rule with no false positives: `gofumpt`, `godot`, `errcheck`,
`errorlint`, `nakedret`, `gochecknoinits`, `decorder`, `lll`. Mechanical, mostly auto-fixed, one
afternoon.

**Stage 1, the assert package.** Add it, then apply `TS-A01` and `TS-A02` with `--new-from-rev`.
Density on legacy code is a project; density on the diff is a habit.

**Stage 2, structural rules.** `funlen`, `cyclop`, `nestif`, `gochecknoglobals`, `depguard`. Real
refactoring, so run advisory and ratchet per package.

**Chain 1, ownership.** `TS-S11`, `TS-S12`, `TS-C10`, `TS-C11`, `TS-X01`, then `TS-F07`. Completing
this makes frames checkable, which makes "who wrote this value" a query. Highest value, and the
prerequisite for everything else.

**Chain 2, surfaces.** `TS-I01` through `TS-I07`. Independent of chain 1 and the thing that makes the
test suite worth trusting. Do it early; `TS-I05` in particular is worth more than any three analyzers.

**Chain 3, effects.** `TS-P01` declarations where precision will be claimed, then `TS-K03`, since
dispatch precision gates the analysis, then `TS-F01`
through `TS-F08`. Converts six heuristics into exact checks, so it removes analyzers as well as adding
them.

**Chain 4, termination.** `TS-S01`, `TS-V01`, `TS-V02`. Cheap, self-contained, makes worst-case
behaviour a stated property.

**Chain 5, contracts.** `TS-A07` through `TS-A09`, then `TS-V03`. Most expensive and last, because it
depends on the invariant vocabulary being mature. Do not start here.

**Chain 6, canonical form.** `TS-K01` through `TS-K06`, then `TS-R01`. Cheap and auto-fixable, can run
in parallel, but only pays off once the refactoring prover exists.

Measure one number throughout and make it the headline: **the fraction of merged lines that no human
read.** It starts near zero. If chains 1 and 3 complete, it should pass half. If it is not moving,
something in a chain is incomplete and the analyzers you added are decorative.

## Adding a rule

Fill this in. A blank line means the rule is not ready, and which line is blank tells you what kind of
bad rule you were about to write.

```
Rule.        One sentence, imperative.
Benefit.     Which of B0 through B8, by ID.
Chain.       Rule, to mechanism, to benefit, to goal. No step assumed.
Failure.     The bug it prevents, concrete enough to write a test for.
Cost.        What following it costs. "Nothing" is not an answer.
Falsifier.   What observation would retire this rule.
Enforcement. Tool and level, or the honest admission that it is review.
Threshold.   If it contains a number, is that number derived or calibrated.
Proxy test.  If it is a proxy, does satisfying it require the same thinking?
```

## Retiring a rule

A rule leaves the same way it arrived, on evidence.

Retire it when its falsifier fires. Retire it when its analyzer has produced no true positive in a
quarter, because a rule nobody violates is a rule nobody needs enforced, and a rule producing only
false positives is teaching the team to ignore the tooling. Retire it when the language changes
underneath it, which happens more than a style guide expects: several rules here exist because of Go's
loop variable semantics before 1.22, its lack of generics before 1.18, or its lack of a fake clock
before `testing/synctest`. The correct response to a language improvement is a smaller specification.

Keep the rule count in review. A document that only grows is a document that has stopped being read.

`TS-F06` and `TS-I02` are the worked examples. Both were deleted for failing the five-question test —
one as another rule plus a review judgment, one as another rule's existence precondition under a
second ID — and their IDs are retired rather than reused, so that a reference to either in an old
commit still resolves.
