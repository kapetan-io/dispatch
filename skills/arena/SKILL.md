---
name: arena
description: "Spawn N parallel candidates at the same task, pick a base, graft the strongest parts of the losers into it. Use for /arena, 'arena this', 'throw it in the arena', or when one attempt at a non-trivial artifact would lock in the wrong shape."
---

# Arena

Fan out N parallel attempts at the same task. Read every candidate end to end. Pick the strongest as the base. Graft the best ideas from the others into it. Verify the synthesized result.

## Start

Open a todolist with one entry per phase before launching anything.

1. Frame
2. Fan out
3. Cross-judge
4. Pick
5. Graft
6. Verify
7. Ask the user

## Phase A: Frame

The N candidates will receive the same prompt, so the prompt is the contract.

1. State the artifact each candidate is producing.
2. Derive the rubric. State what success looks like for *this* task, then turn it into 3-6 concrete gradeable criteria. The rubric is the picker's tool in Phase D. Candidates only see the task. Do not put the criteria in the candidate prompt.
3. List the premises the question assumes (existing mechanisms, timers, config keys). For each, ask whether a signal now available (an event, hook or callback) makes it unnecessary. A premise that is a true constraint stays in the candidate prompt as a constraint. A premise that is not a true constraint stays out of the candidate prompt as a requirement. It may appear as background the candidate is free to drop.
4. Write the candidate prompt so it tells each candidate to name at least one premise of the question and answer "if we built this from scratch with every signal now available, would this mechanism exist?" before answering the question as asked. If the answer is no, the candidate says so first. A candidate has tested a premise when it argues from evidence that the premise is or is not a true constraint, and not merely when it names the premise.
5. Pick the runners. Default to three candidates, one each on the Agent tool's `model` values `opus`, `fable` and `sonnet`. Spawn more when the arena covers multiple design directions. Same model N times when the work is generation-bound rather than judgment-sensitive. Candidates use the default (general-purpose) agent, since they must write files. If a `model` value is rejected, run that seat on the default model and say so in the synthesis record.
6. Assign output paths. Each candidate writes to its own location (a git worktree where possible, otherwise `<scratchpad>/arena-<slug>/candidate-<n>/`). For a worktree, set `isolation: "worktree"` and read the path from the agent's result. Otherwise assign the path up front. Separate before serializing shared state: when concurrent actors might share mutable state, first ask whether they need the same mutable object. If not, eliminate the sharing by giving each actor its own owned file, key, branch, or state directory, and merge only at the read/reporting boundary.

## Phase B: Fan out

Spawn all N subagents in one message (they run in the background), each with the task, the path to the shared grounding, its own output path, and instructions to produce both the artifact and a short rationale.

Each rationale names the alternatives the candidate considered and what it rejected.

Tell any candidate that needs Linear, in its prompt, to load the Linear tools with ToolSearch and only read.

If a candidate fails to produce output, proceed with N-1 and note the dropout in the synthesis record.

## Phase C: Cross-judge

After all Phase B candidates complete, spawn one readonly judge subagent (`subagent_type: "Plan"`, `model: "opus"`) as a new session, not a fork, and tell it not to write anything. It sees the rubric, the premises and the candidates by path label, scores each criterion, and recommends a base with rationale. It runs in parallel with the parent's reading in Phase D, not with the candidates themselves. Don't spawn the judge while candidates are still writing.

## Phase D: Pick a base

Read every candidate end to end before picking.

Score each candidate against the rubric criterion by criterion, not on holistic feel. Compare against the cross-judge. Agreement on the base confirms the pick. Disagreement means one of you is biased or the rubric was ambiguous. Read both rationales before deciding.

Pick the base on which candidate a future maintainer can extend most easily without breaking invariants. Prefer the cleaner boundary or smaller API when two feel tied, per the Laziness Protocol: aim for the most result with the least code and complexity, and make the smallest change that solves the problem.

Record the pick and the reason in a short synthesis note alongside the base artifact, including the cross-judge's verdict.

## Phase E: Graft

Walk each losing candidate once more and identify what is worth porting into the base. The signal is usually one or two things per candidate, not most of it.

Fold each graft in by hand, by redesigning from first principles: ask "if we were writing this from scratch with this graft, what would we build?" and propagate the change through every reference. Don't paste mechanically. The result has to remain coherent under one mental model.

Record what was grafted, from which candidate, and what was rejected and why.

When N candidates converge on the same shape, that is a strong agreement signal. Note the convergence in the record. Convergence confirms the shape only if at least one candidate tested a premise. Otherwise re-run with the untested premise removed from the prompt. No graft is needed when the shape is confirmed. When N candidates wildly diverge, Phase A was under-specified. Reframe and re-run rather than averaging the divergence.

## Phase F: Verify

The synthesized artifact has to hold up under the same scrutiny as any other output, by checking the real thing directly. Do not infer from proxies, self-reports, or "it compiles." When verification fails, suspect the observation method before suspecting the system.

If verification surfaces a problem the arena did not catch, either Phase A was wrong (re-frame and re-run) or one candidate caught it and you missed the graft (go back to Phase E). Don't paper over.

## Outputs

One synthesized artifact. One short synthesis note alongside, naming the base, the grafts (with source candidate), the rejections, the dropouts if any, the premises tested, and the verification result.

The last step is to show the user the result and ask before anything is filed or changed. Do not file tickets, edit files outside the arena's own output paths, open PRs or merge anything until the user answers, then act only on what they approve. End the turn with the question.
