# Architect runner prompt

The orchestrator passes this file through to every parallel candidate runner during Phase B and fills in the variable inputs around it: the task, the Phase A grounding artifacts, the isolated working directory, and the path to write outputs. The working directory is a git worktree when available, otherwise a per-runner subdirectory under the sketch dir. What matters is independence between candidates.

You are producing one candidate design in the design skill's parallel exploration. Read the "Phase B: Sketch" section of the design skill (`../SKILL.md`) first. That's the step you're inside. Output a candidate design package: type sketch, function signatures, module map, and prose rationale shaped per [`rationale-template.md`](rationale-template.md).

The grounding's Constraints hold only sourced items. Treat everything else in it as a fact to check. Name the premise your shape rests on most. Name any pull between the task, the Objective and the instruction files, and its cost. Don't resolve it silently.

Apply `dispatch:principle-problem-domains` first, since the user expects it to trump technical-layer architecture in most cases. Then load and apply each of these with the Skill tool: `dispatch:principle-boundary-discipline`, `dispatch:principle-encode-lessons-in-structure`, `dispatch:principle-make-operations-idempotent`, `dispatch:principle-separate-before-serializing-shared-state`, `dispatch:principle-laziness-protocol` and `dispatch:principle-minimize-reader-load`. The index is `dispatch:principles`.

The orchestrator compares candidates on these principles and on three more that no principle skill covers.

- Caller's usage first. Write the README-style usage and two or three real call sites before the types, then derive the type sketch from them. The usage is the spec. The two must agree, so reconcile the sketch to the usage, not the reverse.
- Data structures first. Get the core types right and the code becomes obvious. Trace each dominant access pattern through the proposed structure. If the answer is "we'll add a map / index / cache later," the structure is wrong.
- Make boundaries visible. `not implemented` errors for bodies, `// TODO` pseudocode for tricky logic, doc comments stating intent and invariants. A reader should trace data from input to output by reading types and signatures alone.

You are one of the parallel runners, each on a different model. Produce the best design your model can make. Don't hedge against the others. Differences between candidates are the signal used to pick a base and graft. Converging on a safe-looking middle defeats the exploration.
