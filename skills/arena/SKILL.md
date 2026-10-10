---
name: arena
description: Run several independent attempts at the same question, have a judge score them against a rubric, pick the strongest and fold in the best parts of the others. Use whenever the user or a ticket says "arena this", "throw it in the arena" or "run the arena", or asks for a few independent takes, a bake-off, best of three, or several agents to try the same thing and compare. Also use when a decision is hard to undo or one attempt would lock in its shape, such as a design sketch, a contested technical choice or a wording with no obvious best form. Skip it for reversible or obvious choices.
---

# Arena

Fan out independent attempts at one question, read them all, pick a base and graft the best of the rest into it. One agent's first answer sets the shape, and the arena finds out whether the shape holds when others start from scratch.

Open a todo list with one entry per phase before launching anything: frame, fan out, cross-judge, pick, graft, verify. Add an entry for a re-run if one is triggered.

## 1. Frame

Every candidate gets the same prompt, so the prompt is the contract.

1. **Artifact.** State what each candidate produces, such as a design sketch, a recommendation or a rewrite.
2. **Premises.** List what the question assumes: existing mechanisms, timers, polling, config keys, workarounds. For each, ask whether a signal now available (an event, a hook, a callback) makes it unnecessary. Answer from the grounding material. If you can't tell, treat the premise as inherited. A premise that is a true constraint (a public contract, a decided item) stays in the prompt as a constraint. A premise that is only inherited from how things work today stays out of the prompt as a requirement, so candidates meet it as something to question.
3. **Rubric.** Write 3-6 gradeable criteria for this question before any candidate starts, including one for whether the candidate questioned a premise and justified its answer. Put extensibility in the rubric if it matters. Candidates never see the rubric. It is for the picker and the judge.
4. **Candidate prompt.** Include the task, where to read the grounding material, the candidate's own output path, and a request for the artifact plus a short rationale naming the alternatives it rejected. Tell each candidate to:
   - name at least one premise of the question, and
   - answer "if we built this from scratch with every signal now available, would this mechanism exist?" before answering the question as asked. If the answer is no, say so first.
5. **Runners.** Three candidates by default, one each on the Agent tool's `model` values `opus`, `fable` and `sonnet`. Use more when the question has several design directions, and the same model repeatedly when the work is generation-bound and not judgment-sensitive. The judge is a new `opus` Agent call. Avoid `haiku` for judging, because it mis-attributes quoted lines.
6. **Isolation.** Each candidate writes only to its own path, `<scratchpad>/arena-<slug>/candidate-<n>/`, or a worktree (Agent `isolation: "worktree"`) when the artifact is code. Candidates never read or write each other's paths or edit the repo. A candidate that needs Linear loads the Linear tools with ToolSearch and only reads.

## 2. Fan out

Launch every candidate as its own Agent call in one message. They run in the background and you are notified as each finishes. Read no output until all have finished.

A candidate that errors out gets one retry. A candidate that returns off-task output or ignores its output path is a Frame problem. Go on with the rest and record the dropout. Two such dropouts mean the prompt was unclear, so fix it and run again.

## 3. Cross-judge

Start the judge only after every candidate has finished. Use a new Agent call, not a fork, with `subagent_type: "Plan"` (it has no Edit or Write) and `model: "opus"`. Give it the rubric, the premise list and the candidates by path label with no model names. It scores each criterion with a line of reason, quotes the text each score rests on, and returns its recommended base as its final message. It runs while you do Phase 4.

## 4. Pick

First check the shape.

- **Convergence.** Candidates that reach the same shape confirm it only if at least one candidate tested a premise, meaning it named the premise and concluded, with a reason, that it should stay or go. If none did, the agreement may come from a shared assumption. If the premise was in the prompt as a constraint, drop it. If it was left out and every candidate still adopted it, state it in the prompt as a question. Then run again, once. If it still fails, report that to the user.
- **Divergence.** If candidates answer different questions, or no criterion separates them and the base is contested, Frame was under-specified. Reframe and run again instead of averaging.

Then read every candidate end to end and write your scores against the rubric, criterion by criterion and never on overall feel, before opening the judge's. Agreement confirms the pick. Disagreement means one of you is biased or the rubric is ambiguous, so read both rationales and check each quoted line against its source before deciding. When two candidates tie, take the smaller.

## 5. Graft

Walk each losing candidate again for the one or two ideas worth porting. Fold each in by hand and re-derive it for the base, so the result holds together under one mental model. Do not paste.

Record what was grafted and from whom, and what was rejected and why.

## 6. Verify

Check the synthesized artifact the way you would check any other output. For code, run or test it. For a design or wording, check each factual claim against the grounding material and each rubric criterion against the final text. If that turns up something the arena missed, either Frame was wrong (reframe and run again) or a candidate caught it and you missed the graft (go back to Graft).

## Output

One artifact and a short synthesis note beside it, both in `<scratchpad>/arena-<slug>/` unless the caller names a path. The note names the base and why it won (with the judge's verdict), each graft and its source, each rejection, any dropouts, the models each candidate ran on, and the premises tested and what each candidate concluded.

Last, show the user the result and ask before anything is filed, merged or changed. When a grill or review calls the arena, hand the result back to it. That caller decides what changes.
