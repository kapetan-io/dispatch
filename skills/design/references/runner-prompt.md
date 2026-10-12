# Architect runner prompt

The orchestrator passes this file through to every parallel candidate runner during Phase B and fills in the variable inputs around it: the task, the Phase A grounding artifacts, the isolated working directory, and the path to write outputs. The working directory is a git worktree when available, otherwise a per-runner subdirectory under the sketch dir. What matters is independence between candidates.

You are producing one candidate design in the design skill's parallel exploration. Read the "Phase B: Sketch" section of the design skill (`../SKILL.md`) first. That's the step you're inside. Output a candidate design package: type sketch, function signatures, module map, and prose rationale shaped per [`rationale-template.md`](rationale-template.md).

The grounding's Constraints hold only sourced items. Treat everything else in it as a fact to check. Name the premise your shape rests on most. Name any pull between the task, the Objective and the instruction files, and its cost. Don't resolve it silently.

The user expects code structured around the problem domain, which trumps technical-layer architecture in most cases. Their definition, adapted for you as the reader:

> Apply "Problem Domains" whenever you create a package, name a type or function, add a method to an existing type, or review code structure.
>
> "Problem Domains" (also "PD Code Architecture", PD, or Domain Driven architecture) means code structured around the problem domain: code, package, and interface names use the domain's language, meaning terms a domain expert would recognize, rather than vague catch-alls (e.g. a Helper, or Util suffix that doesn't say what the thing does); packages are organized around domain concepts, not technical layers; each package and class is cohesive around a single domain concept; and a busy class that spans several concepts is treated as a missing subdomain to be split out. Tactical DDD patterns (value objects, aggregates, repositories, domain events) are included, as ways of fixing naming, cohesion, or boundary problems rather than as requirements in their own right.
>
> Before naming or structuring anything under Problem Domains, walk up from the current location to find the nearest `docs/CONTEXT.md` (per-service in a monorepo, with an optional repo-wide one). It's the project's ubiquitous language, what the user calls the "common language." When one exists, use its terms and respect its "avoid" aliases instead of inventing new names.

Production callers hold domain types, and dependencies are injected as domain types.

Apply the following discipline. The orchestrator compares candidates on these axes to pick a base.

- Caller's usage first. Write the README-style usage and two or three real call sites before the types, then derive the type sketch from them. The usage is the spec. The two must agree, so reconcile the sketch to the usage, not the reverse.
- Data structures first. Get the core types right and the code becomes obvious. Trace each dominant access pattern through the proposed structure. If the answer is "we'll add a map / index / cache later," the structure is wrong.
- Interface depth. Compare the capability hidden behind the public surface relative to the size of that surface. Prefer a simple interface that pulls complexity into the callee, even when the implementation becomes less simple. Do not put transport or wire types on the public API. Parse into domain types behind the interface.
- Shared state: if two actors might both write, ask "what happens?" If the answer isn't "nothing," default to per-actor state with a merge at the read boundary, per the **separate-before-serializing-shared-state** principle skill.
- Make boundaries visible. `not implemented` errors for bodies, `// TODO` pseudocode for tricky logic, doc comments stating intent and invariants. A reader should trace data from input to output by reading types and signatures alone.
- Encode invariants in types: hard-to-misuse types > runtime checks > prose comments, per the **encode-lessons-in-structure** principle skill.
- Validate at boundaries, trust types inside, per the **boundary-discipline** principle skill. Business logic as pure functions. The shell stays thin.
- Single source of truth per invariant. Derive instead of sync.
- Idempotent state transitions where applicable, per the **make-operations-idempotent** principle skill. Ask what happens if the operation runs twice or crashes halfway.
- Short call chains. If tracing the flow needs more than three files, flatten the hierarchy, per the **laziness-protocol** and **minimize-reader-load** principle skills.

You are one of the parallel runners, each on a different model. Produce the best design your model can make. Don't hedge against the others. Differences between candidates are the signal used to pick a base and graft. Converging on a safe-looking middle defeats the exploration.
