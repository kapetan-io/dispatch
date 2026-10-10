---
name: grill
description: "Design a feature with the user: one long conversation that settles what it must do, tests the design's shape against independent sketches, and ends in a blueprint and the ticket a worker builds. Use when started as `/dispatch:grill <ticket>`, or when asked to design, scope or grill a feature before building it. Not for bug fixes or changes too small to need a design."
---

# Grill

Run one continuous back-and-forth design discussion, product definition and technical design together. Settle what the feature must do, test the design's shape with independent sketches, then write a single blueprint and file the ticket a worker builds. The product half and the technical half are one thought. Keeping them in one document and one conversation stops the build from implementing something the product never asked for.

The blueprint fixes contracts, not content. How each function is written is the build's job, proved by tests.

The ticket you were started on is the feature ticket. The supervisor filed it with the `design`, `ai` and repo labels, and the dispatcher launched you when it moved to In Progress. It stays In Progress for the whole design. A design is a long conversation in a window the user can see. The dispatcher never nudges it, and you hold the tree for the whole session because prototypes run there.

## Start

Open a todolist with one entry per step before starting.

1. Orient
2. Discuss the product half and get the Objective approved
3. Ground (Phase A): load `dispatch:how` with the Skill tool over the subsystems the design touches, and `dispatch:why` over any existing shape it extends or replaces
4. Sketch (Phase B): load `dispatch:arena` with the Skill tool
5. Agree on the contract sketch
6. Discuss the rest of the technical half, then run the correctness and testability passes
7. Write the blueprint
8. Size it and file the child ticket
9. Hand off

### Orient

1. Read the feature ticket and its comments.
2. Read the domain glossary: `CONTEXT.md` files in the `docs/` directories this feature touches. Use them to catch terminology conflicts in both halves. 
3. List every file in `docs/adr/` and read each title. Read the full ADR for two kinds of title. A scope-capping title (users, tenancy, regions, roles, deployment targets) limits what the product can ask for, so frame product questions that do not invite answers it rules out, and surface the ADR if the user wants something it forbids. A title on a topic this design's technical half touches (storage, API patterns, auth, errors) sets precedent to honor. A departure is itself a decision that needs a new or superseding ADR.
4. Write the full list of product and technical questions the blueprint must settle, tailored to this feature. Present the product list and start on the first question without asking whether to start. The user can redirect at any point.

## The discussion

Ask in plain chat, one question at a time, not through AskUserQuestion. The user steps away during a long design and wants each question to carry its own context and a recommendation, which a multiple-choice picker squeezes out. A build worker uses AskUserQuestion so the dispatcher can see it is blocked, but the dispatcher never nudges a design session, so that signal has no use here.

In each question, say what is being decided and why you need it, then give your recommendation.

Work the product half first, then the technical half, because the technical design exists to satisfy the product intent. When an answer in one half settles a question in the other, record it and move on.

For each question:

- **Offer a suggested answer.** The user reacts and refines rather than generating from scratch. In the technical half, lean on established codebase patterns and ADRs, and name the reason for a departure.
- **Run it when a run can answer it.** Before asking something a script could answer, such as a timing, a library's behavior or whether an approach works at all, write the smallest throwaway script in the tree and decide from what it prints. No decision means no prototype. Record the question, the observation and the script in the blueprint. Product and preference calls stay with the user.
- **Resolve it before moving on.** Stay on it until it is answered or the user defers it.
- **Interrupt on vague or overloaded terms.** "You said 'user'. Do you mean the account owner or anyone with access?" Resolve the term from the glossary before continuing, and track resolved terms for the glossary writeback.
- **Test every scope boundary with one concrete scenario**, framed as confirmation: "So if a customer cancels mid-billing-cycle, that's out of scope?"

Keep a running decision log, marking each entry "you decided" or "agent assumed". Show it on request and after each domain (problem, users, scope; then data, API, failure handling). The user corrects it in real time.

### What the product half covers

Tailor to the feature.

- **Problem and why.** What problem does this solve, why now, and what technical goal follows?
- **Users.** Who is it for, and which is primary?
- **Mental model.** For platform and tooling: the core concepts and how they relate.
- **Core design principles.** What the product will always do and deliberately never do. Surface them early, because they resolve later questions and are ADR candidates.
- **Correctness constraints.** Two subsections. *State invariants* are always-true statements about data, each with what violates it and what the system does then. An invariant with no enforcement is a comment in dead code. *Behavioral constraints* are things the system must never do, even in failure. Elicit both with three prompts. Concurrency: what happens when two users touch the same resource at once? Reversibility: which operations can be undone? Partial failure: what happens if this succeeds halfway?
- **Acceptance criteria.** Each one mechanically verifiable. Ask "how would a test know this happened?" of every criterion, and rewrite any that needs human judgment. Cover failure cases explicitly.
- **Scope and non-goals.** What is in the first version and what is out.
- **Dependencies and constraints.**
- **User stories.** Only when several personas have divergent workflows, the interactions branch, many discrete behaviors need verifying, or engineers build in parallel. Skip them for infrastructure, simple features, or when the criteria already sit at story granularity. Each story's criteria are mechanically verifiable and trace to the section they come from.
- **Success metrics.** Post-ship outcomes only, never a restatement of scope. Omit the section when none exists.

When the product half is done, write the **Objective** and ask the user to approve it. The Objective is the anchor. Every candidate sketch, review round and build ticket is checked against it, so do not move on until the user says it is right.

## Phase A: Ground the problem

Build a real mental model of every system the new code touches. Run the **dispatch:how** skill over the relevant subsystems.

Naming a file isn't grounding. Produce the traced model `dispatch:how` prescribes. If the design redefines ownership or layering, also run the **dispatch:why** skill on the existing shape so the rationale becomes a constraint, not a guess. Choosing between extending an existing mechanism and adding a new one counts as layering.

Load both with the Skill tool and do not read the repo yourself in their place. `dispatch:how` holds the explorer and explainer prompts that make the trace thorough, and `dispatch:why` holds the sources that turn history into a recorded reason.

Write the grounding to one file in the scratchpad. It is what the candidates read, so it must hold the traced code and the decisions behind it, not a list of file names.

Skip Phase A only when the work is genuinely greenfield with no surrounding system to integrate. Then write "no surrounding system" in the grounding file, so the candidates read a file that says so.

## Phase B: Sketch

Run the **dispatch:arena** skill with the design-sketch task, the approved Objective and the Phase A grounding file. Pass `references/runner-prompt.md` as each runner's prompt. Each candidate produces a design package shaped per `references/rationale-template.md`. Load the arena with the Skill tool rather than reproducing it from memory, since it holds the judge, graft and verify steps.

Design it twice. Require at least two structurally distinct candidates before synthesis, even when the first looks sufficient. This is the **exhaust-the-design-space** principle skill made concrete. Whole-shape alternatives, not point fixes inside one shape. When only one shape fits, the arena's rationale says so ("this was the only viable shape because...").

Screen every candidate against [`references/design-red-flags.md`](references/design-red-flags.md) before synthesis. Assume the next contributor is an agent that sees only the files it opened, copies the nearest example, and takes the shortest path that compiles. Prefer the design where a change that looks right from one file is right for the whole repo.

Compare viable candidates on interface depth. Prefer the design that hides more complexity behind a smaller, simpler public surface. A rich interface can keep call chains short by concentrating capability instead of scattering it across layers.

Arena returns one synthesized design package. The synthesis decision populates the rationale's "Synthesis decision" section.

Two things differ from running the arena by hand. The arena always runs, because starting a grill means the work deserves design, and you do not ask first. Take the result directly and skip the arena's last step ("Ask the user"), because the user approves the contract sketch below and should not be asked twice.

## Agree on the contract sketch

The arena's Usage section is the first draft of the contract sketch: the surface a consumer or a test binds to. Show the user the sketch and a short note of what was considered and why it lost, not the candidate documents. Ask the user to approve it. Consumer-facing signatures (a library's exports, an HTTP or RPC API) are pinned, and internal surfaces get a contract, not a signature.

If the user pushes back on the shape, treat that as Phase A evidence. Re-ground and re-run Phase B before going on.

## The rest of the technical half

Settle what an engineer needs before writing code, not a checklist: data, dependencies, failure modes, security, PII, operations. Lean on the codebase patterns Phase A found.

- **Domain-boundary opacity.** A domain must not contain logic that belongs to a domain it calls. For each interface, ask whether the caller ever needs to know which implementation is behind it. If so, either the contract is missing a guarantee or the caller is absorbing the callee's job. State the principle in the blueprint when an interface has several implementations.
- **Correctness translation.** For each state invariant, name every operation that touches its data and show why none can violate it. Prefer designs where the data model makes the illegal state unrepresentable, and say which invariants rest on application logic and so need more tests. For each behavioral constraint, show how the architecture satisfies it. Make assumptions between components explicit as preconditions and postconditions.
- **Testing.** Name the surface a test enters through, the substitute for each external dependency, an observability API for any async effect the surface doesn't show, an injectable clock for time-dependent behavior, and a parity plan for any in-memory store beside a real one. Testing follows the `surface-testing` skill.
- **Level of detail.** Pin a signature only where a consumer or test binds to it. Describe an internal surface's responsibility, inputs and outputs, invariants and error categories, and leave its signature to the implementor, who sees the real types. A schema is pinned in full, and the interface that wraps it is not.

When a question can't be settled now, insert `[NEEDS CLARIFICATION: <question>]` and continue with the best assumption. Three or more flags mean the blueprint isn't ready to build from.

Before writing, run two passes. **Correctness:** every invariant has a preservation argument for each operation touching it, and no enforcement relies on a property of one implementation. **Testability:** every surface is reachable from a test, every external dependency has a substitute, and every async behavior can be observed. Gaps here are design findings. Resolve them before writing.

## Write the blueprint

Save the blueprint as a Linear document on the feature ticket, not under `docs/features/`. Save a draft after the Objective is approved and again after each phase, so a crash loses little. Write one document, product half first and then the technical half, with only the sections the discussion covered:

Objective, Mental Model, User Stories, Correctness Constraints (State Invariants, Behavioral Constraints), Acceptance Criteria, Success Metrics, Scope (In Scope, Out of Scope), Dependencies and Constraints, then Functional, Architecture, Data Design (Invariant Preservation, Illegal State Analysis), Security, PII, Scale, Testing, Limitations and Future Work, Open Questions.

Keep PII as its own section. Mark anything unresolved with `TODO:` and a line on what is missing. Keep it short, since a section that drives no decision is noise. Write for a reader who was not in the discussion, with no "the option we discussed". "We considered X but chose Y because Z" is useful. If the feature has an OpenAPI spec, the contract sketch becomes a second document on the ticket, as a fenced YAML block.

If the discussion resolved domain terms, write them to the right `docs/CONTEXT.md` in one pass. Do not commit. The tree is returned at hand-off, which discards the change, so the hand-off uploads it.

## Size it and file the child

Judge size once, after the user approves the design, with this wording. Tell the user the verdict and the pieces you counted.

> A **piece** is complete when, after it merges, a user or caller can do something they could not do before. Its acceptance criteria are observable at the surface and pass without any other unmerged piece. Group a piece by what a user does together, not by endpoint, flag or rule. Work with no surface of its own, such as a new seam, backend, package or refactor, is never a piece by itself. It ships inside the first piece a user can observe through it.
>
> A design is too big for one pull request when it holds more than one piece. Line count alone never makes a design too big. A design a reviewer can read in one sitting, about an hour, fits even when it touches more than one piece. When the two tests disagree, the one-sitting test wins, because easy review sets the size in both directions. Count only what the design commits to build. Work it defers, or lists as a limitation, open question or future work, is not part of any piece.

Create exactly one child of the feature ticket, in **Backlog** and not Todo, which overrides `dispatch:ticket`'s default. The dispatcher starts a Todo child of an In Progress parent at once, and the user's move to In Progress is the approval.

- **The design fits one PR and changes no public contract.** The child is one build ticket, labeled `ai` and the repo label, written with the `dispatch:ticket` skill. Its Governing design section links the blueprint and the grounding file.
- **Anything larger, or any change to a public contract.** The child is a review ticket, labeled `ai`, `design-review` and the repo label, never `design`, assigned to the owner of dispatch's Linear API key, and written with `dispatch:ticket` (with the Backlog override above). The dispatcher manages only tickets with the `ai` label that are assigned to that owner, so without both the review would never start after the user approves it. Its body links the blueprint. The dispatcher skips `design-review` tickets until the review step exists, so tell the user that moving it to In Progress starts no worker yet. Only the review writes the formal spec and lints it, so a public contract change takes this path even when it is small. Assume every repo lints OpenAPI specs with `duh` for now.

## Hand off

1. Upload the experiments worth keeping, the Phase A grounding file, and the glossary change (`git diff -- docs`), to the feature ticket as attachments, so build workers can get them.
2. Tell the user which ticket to move to In Progress, and that the move is the approval. Do not move it yourself.
3. Return the tree with `treehouse return <tree path> --if-lease-holder <feature ticket> --force`, where the tree path is `git rev-parse --show-toplevel`. Do this last. It discards anything uncommitted, including any experiment you did not upload, and it ends the session. A held tree blocks other launches.
