---
name: design
description: "Design a feature with the user: one long conversation that settles what it must do, tests the design's shape against independent sketches, and ends in a blueprint and the tickets a worker builds. Use when started as `/dispatch:design <ticket>`, or when asked to design or scope a feature before building it. Not for bug fixes or changes too small to need a design."
---

# Design

Run one continuous back-and-forth design discussion, product definition and technical design together. Settle what the feature must do, test the design's shape with independent sketches, then write a single blueprint and file the tickets a worker builds. The product half and the technical half are one thought. Keeping them in one document and one conversation stops the build from implementing something the product never asked for.

The blueprint fixes contracts, not content. How each function is written is the build's job, proved by tests.

The ticket you were started on is the feature ticket. The session that created it filed it with the `design`, `ai` and repo labels, and the dispatcher launches a design worker when it moves to In Progress. It stays In Progress for the whole design. A design is a long conversation in a window the user can see. The dispatcher never nudges it, and you hold the tree for the whole session because prototypes run there.

## Start

Load `dispatch:converse` with the Skill tool. Open a todolist with one entry per step before starting.

1. Orient
2. Discuss the product half and get the Objective approved
3. Ground (Phase A): load `dispatch:how` with the Skill tool over the subsystems the design touches, and `dispatch:why` over any existing shape it extends or replaces
4. Sketch (Phase B): load `dispatch:arena` with the Skill tool. It always runs without asking you first, and you skip its last step ("Ask the user"), because the user approves the contract sketch in step 5
5. Agree on the contract sketch
6. Discuss the rest of the technical half, then run the correctness and testability passes
7. Write the blueprint
8. Audit the transcript
9. Review or break down
10. Hand off

### Orient

1. Read the feature ticket and its comments. A design in the ticket's Notes is a suggestion. Carry it to the arena's premise list, not into the grounding as a constraint.
2. Read the domain glossary: `CONTEXT.md` files in the `docs/` directories this feature touches. Use them to catch terminology conflicts in both halves.
3. List every file in `docs/adr/` and read each title. Read the full ADR for two kinds of title. A scope-capping title (users, tenancy, regions, roles, deployment targets) limits what the product can ask for, so frame product questions that do not invite answers it rules out, and surface the ADR if the user wants something it forbids. A title on a topic this design's technical half touches (storage, API patterns, auth, errors) sets precedent to honor. A departure is itself a decision that needs a new or superseding ADR.
4. Read the code this feature will live in: existing patterns, related modules and prior art. Technical questions must ask where the feature fits in this codebase, not run a generic checklist.
5. Write the full list of product and technical questions the blueprint must settle, tailored to this feature.
6. Have a fresh sub-agent mark each question **settled** or **ask**. Give it the list, the ticket, the glossary, the relevant ADRs and the code scan, and these rules, and check each reason yourself before accepting it.
   - A technical question is settled by the code, an ADR or standard practice for this stack, with a one-line reason.
   - A product question is settled only when the ticket or the design it governs states the answer, quoted. An instruction that answers it indirectly counts ("follow `blueprint-review` as written" settles how much the review asks). An inference, a reasonable default or a partial answer does not. Anything under the ticket's "Not decided here" is ask.
7. Show the user the questions marked ask, and the settled product questions with their quoted lines, then start on the first one without asking whether to start. The user can redirect at any point. Record each settled answer in the decision log as "agent assumed", with the quoted line (product) or the one-line reason (technical), so the user can override it.

## The discussion

Ask in plain chat, one question at a time, not through AskUserQuestion. The dispatcher never nudges a design session, and a picker squeezes out the context and recommendation the user needs after stepping away.

In each question, say what is being decided and why you need it, then give your recommendation.

Work the product half first, then the technical half, because the technical design exists to satisfy the product intent. When an answer in one half settles a question in the other, record it and move on.

For each question:

- **Offer a suggested answer.** The user reacts and refines rather than generating from scratch. In the technical half, lean on established codebase patterns and ADRs, and name the reason for a departure.
- **Run it when a run can answer it.** Before asking something a script could answer, such as a timing, a library's behavior or whether an approach works at all, write the smallest throwaway script in the tree and decide from what it prints. No decision means no prototype. Record the question, the observation and the script in the blueprint. Product and preference calls stay with the user.
- **Resolve it before moving on.** Stay on it until it is answered or the user defers it.
- **Interrupt on vague or overloaded terms.** "You said 'user'. Do you mean the account owner or anyone with access?" Resolve the term from the glossary before continuing, and track resolved terms for the glossary writeback.
- **Test every scope boundary with one concrete scenario**, framed as confirmation: "So if a customer cancels mid-billing-cycle, that's out of scope?"

Keep a running decision log, marking each entry "you decided" or "agent assumed". Save it in the blueprint draft, and show it on request and after each domain (problem, users, scope; then data, API, failure handling), with the settled technical answers and their reasons. The user overrides any of them in real time.

### What the product half covers

Tailor to the feature.

- **Problem and why.** What problem does this solve, why now, and what technical goal follows?
- **Users.** Who is it for, and which is primary?
- **Mental model.** For platform and tooling: the core concepts and how they relate.
- **Core design principles.** What the product will always do and deliberately never do. Surface them early, because they resolve later questions and are ADR candidates.
- **Correctness constraints.** Two subsections. *State invariants* are always-true statements about data, each with what violates it and what the system does then. An invariant with no enforcement is a comment in dead code. *Behavioral constraints* are things the system must never do, even in failure. Elicit both with three prompts. Concurrency: what happens when two users touch the same resource at once? Reversibility: which operations can be undone? Partial failure: what happens if this succeeds halfway?
- **Acceptance criteria.** Each one an outcome a user or caller sees, mechanically verifiable, never which test or layer must catch it. Rewrite any that needs human judgment. Cover failure cases explicitly.
- **Scope and non-goals.** What is in the first version and what is out.
- **Dependencies and constraints.**
- **User stories.** Only when several personas have divergent workflows, the interactions branch, many discrete behaviors need verifying, or engineers build in parallel. Skip them for infrastructure, simple features, or when the criteria already sit at story granularity. Each story's criteria are mechanically verifiable and trace to the section they come from.
- **Success metrics.** Post-ship outcomes only, never a restatement of scope. Omit the section when none exists.

When the product half is done, write the **Objective** and ask the user to approve it. The Objective is the anchor. Every candidate sketch, review round and build ticket is checked against it, so do not move on until the user says it is right. Raise any acceptance criterion that names a mechanism as its own question first, since one "yes" to the whole Objective approves it unread.

## Phase A: Ground the problem

Build a real mental model of every system the new code touches. Run the **dispatch:how** skill over the relevant subsystems. Ask it and `dispatch:why` in the problem domain's terms: what the system asks of each dependency, what each one refuses, and which behaviors rely on it. Technical detail (command lines, wire formats, call mechanics) is evidence, not the model.

Naming a file isn't grounding. Produce the traced model `dispatch:how` prescribes. If the design redefines ownership or layering, also run the **dispatch:why** skill on the existing shape so the candidates see the recorded reason, not a guess. Choosing between extending an existing mechanism and adding a new one counts as layering.

Load both with the Skill tool and do not read the repo yourself in their place. `dispatch:how` holds the explorer and explainer prompts that make the trace thorough, and `dispatch:why` holds the sources that turn history into a recorded reason.

Write the grounding to one file in the scratchpad, in three sections. It is what the candidates read, so it must hold the traced code and the decisions behind it, not a list of file names.

- **Facts.** The domain model first: what each part is to the system, what the system asks of it, what it refuses, and how tests stand in for it today, each with a file and line or a command and its output. Technical detail goes in an appendix the Facts point to. No "should", "must" or "so".
- **History.** Each decision quoted in the source's words, with source, date and subject. Do not widen a quote past its subject.
- **Constraints.** Only items that end in a source: an Objective line, a dated user decision, or a History quote at its own scope.

An inference, however likely, goes to the arena's premise list instead. Before Phase B, a fresh sub-agent lists every should or must sentence in the grounding with its source line. Move the unsourced ones to the premise list and report how many moved in the arena's synthesis note.

Skip Phase A only when the work is genuinely greenfield with no surrounding system to integrate. Then write "no surrounding system" in the grounding file, so the candidates read a file that says so.

## Phase B: Sketch

Run the **dispatch:arena** skill with the design-sketch task, the approved Objective, the Phase A grounding file and the premise list. Pass `references/runner-prompt.md` as each runner's prompt. Each candidate produces a design package shaped per `references/rationale-template.md`. Load the arena with the Skill tool rather than reproducing it from memory, since it holds the judge, graft and verify steps.

Design it twice. Require at least two structurally distinct candidates before synthesis, even when the first looks sufficient. Whole-shape alternatives, not point fixes inside one shape. When only one shape fits, the arena's rationale says so ("this was the only viable shape because...").

The design principles are skills of their own, indexed by `dispatch:principles`. Load one with the Skill tool when a design question touches it.

Screen every candidate against [`references/design-red-flags.md`](references/design-red-flags.md) before synthesis. Assume the next contributor is an agent that sees only the files it opened, copies the nearest example, and takes the shortest path that compiles. Prefer the design where a change that looks right from one file is right for the whole repo.

Compare viable candidates on interface depth. Prefer the design that hides more complexity behind a smaller, simpler public surface. A rich interface can keep call chains short by concentrating capability instead of scattering it across layers.

Arena returns one synthesized design package. The synthesis decision populates the rationale's "Synthesis decision" section.

Starting a design means the work deserves one, so the arena always runs. Take its result directly, because the user approves the contract sketch below and should not be asked twice.

## Agree on the contract sketch

The arena's Usage section is the first draft of the contract sketch: the surface a consumer or a test binds to. Show the user the sketch and a short note of what was considered and why it lost, not the candidate documents. Ask the user to approve it. Consumer-facing signatures (a library's exports, an HTTP or RPC API) are pinned, and internal surfaces get a contract, not a signature.

If the user pushes back on the shape, treat that as Phase A evidence. Re-ground and re-run Phase B before going on.

## The rest of the technical half

Settle what an engineer needs before writing code, not a checklist: data, dependencies, failure modes, security, PII, operations. Lean on the codebase patterns Phase A found.

- **Domain-boundary opacity.** A domain must not contain logic that belongs to a domain it calls. For each interface, ask whether the caller ever needs to know which implementation is behind it. If so, either the contract is missing a guarantee or the caller is absorbing the callee's job. State the principle in the blueprint when an interface has several implementations.
- **Correctness translation.** For each state invariant, name every operation that touches its data and show why none can violate it. Prefer designs where the data model makes the illegal state unrepresentable, and say which invariants rest on application logic and so need more tests. For each behavioral constraint, show how the architecture satisfies it. Make assumptions between components explicit as preconditions and postconditions.
- **Testing.** Name the surface a test enters through, the substitute for each external dependency, standing in at a domain type, an observability API for any async effect the surface doesn't show, an injectable clock for time-dependent behavior, and a parity plan for any in-memory store beside a real one. Testing follows the `dispatch:surface-testing` skill.
- **Level of detail.** Pin a signature only where a consumer or test binds to it. Describe an internal surface's responsibility, inputs and outputs, invariants and error categories, and leave its signature to the implementor, who sees the real types. A schema is pinned in full, and the interface that wraps it is not.

When a question can't be settled now, insert `[NEEDS CLARIFICATION: <question>]` and continue with the best assumption. Three or more flags mean the blueprint isn't ready to build from, so stop and ask the user to settle them.

Before writing, run two passes. **Correctness:** every invariant has a preservation argument for each operation touching it, and no enforcement relies on a property of one implementation. **Testability:** every surface is reachable from a test, every external dependency has a substitute, and every async behavior can be observed. Gaps here are design findings. Resolve them before writing.

## Write the blueprint

Save the blueprint as a Linear document on the feature ticket, not under `docs/features/`. Save a draft after the Objective is approved and again after each phase, so a crash loses little. Write one document, product half first and then the technical half, with only the sections the discussion covered:

Objective, Mental Model, User Stories, Correctness Constraints (State Invariants, Behavioral Constraints), Acceptance Criteria, Success Metrics, Scope (In Scope, Out of Scope), Dependencies and Constraints, then Functional, Architecture, Data Design (Invariant Preservation, Illegal State Analysis), Security, PII, Scale, Testing, Limitations and Future Work, Open Questions, Decision Log (every entry marked "you decided" or "agent assumed").

Keep PII as its own section. Mark anything unresolved with `TODO:` and a line on what is missing. Keep it short, since a section that drives no decision is noise. Write for a reader who was not in the discussion, with no "the option we discussed". "We considered X but chose Y because Z" is useful. If the feature has an OpenAPI spec, the contract sketch becomes a second document on the ticket, as a fenced YAML block.

If the discussion resolved domain terms, write them to the right `docs/CONTEXT.md` in one pass. Do not commit. The tree is returned at hand-off, which discards the change, so the hand-off uploads it.

## Audit the transcript

A design runs for hours and several compactions, and early decisions get lost or reworded by the time the blueprint is written. Have a fresh sub-agent compare the blueprint with what the user said.

Give it the blueprint and this session's transcript, `~/.claude/projects/<project>/<session-id>.jsonl`. Tell it to keep scratch files in the scratchpad and run Python with `-I`, and to do this:

- Ignore system wrapper entries. Read each user message with the assistant text before it, because "yes" only means something next to the question it answers.
- List every decision the user made that the blueprint should reflect (scope, constraints, rejected options, names) as **PRESENT**, **SUPERSEDED** (the user later changed it), **CHANGED** (the blueprint words it differently) or **MISSING**, each with the user's words and time, checked against the blueprint and its decision log.
- List section dependencies: which parts must be built before which.

Check each CHANGED, MISSING and SUPERSEDED finding against the transcript yourself. Fix the confirmed ones in the blueprint, and put to the user any where the right answer isn't clear. The decision log is also the blueprint's last section, so a fix that changes a decision goes in both its section and the log. Add the section dependencies to the blueprint, where the reviewer and `dispatch:breakdown` will read them.

## Review or break down

After the user approves the design, ask whether it gets a review. Recommend one when the design looks larger than one PR or changes a public contract, because only the review writes and lints the formal spec.

- **No review.** Load `dispatch:breakdown` with the Skill tool and follow it on the feature ticket. It sizes the design, files the build tickets, and asks whether to start the work or hold it.
- **Review.** File a child of the feature ticket labeled `ai`, `design-review` and the repo label, never `design`, assigned to the owner of dispatch's Linear API key, with `dispatch:ticket`. Its body links the blueprint. Move it to In Progress and run `dispatch tick`. The next tick launches the review worker, which reviews the blueprint with the user and then runs `dispatch:breakdown`.

If you end with nothing filed, move the feature ticket to Backlog, since an In Progress `design` ticket with no children gets a new design worker.

## Hand off

1. Upload the experiments worth keeping, the Phase A grounding file, and the glossary change (`git diff -- docs`), to the feature ticket as attachments, so build workers can get them.
2. Return the tree with `treehouse return <tree path> --if-lease-holder <feature ticket> --force`, where the tree path is `git rev-parse --show-toplevel`. Do this last. It discards anything uncommitted, including any experiment you did not upload, and it ends the session. A held tree blocks other launches.
