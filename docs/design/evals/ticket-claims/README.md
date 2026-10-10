# Ticket claims eval

Tests whether the build skill's step 1 wording makes a worker check a ticket's factual claims before relying on them, without making it doubt claims that are true. The wording under test replaces step 1 of `skills/build/SKILL.md`. Steps 2 to 7 are the skill's own.

Tickets are often written by an agent from memory, so they can state the wrong cause of a bug, describe code that works differently, or give a recipe that can't work. The design decides what to build. The ticket's statements of fact are claims to check.

## Arms

- `arms/current.md` is step 1 as it is on main.
- `arms/trust.md` is a known-bad control. It tells the worker to treat the ticket's statements as facts. If the graders pass it, they are broken and no result counts.
- `arms/seed.md` is the starting draft from ENG-248. Later candidates are `arms/attempt-N.md`.

Every arm is the whole build skill with only step 1 replaced.

## Cases

Ten tickets, each against the dispatch repo as it stood at a past commit, so every claim can be checked against real code. Six tickets carry one planted false claim. Four are controls whose claims are all true. A worker that doubts everything fails the controls. Tickets are in `tickets/`, answer keys in `cases.json`.

| Case | Fixture | Planted claim | Expected |
|---|---|---|---|
| hint-cause | `3cece85^` | Bug cause: the hook hands the config to `tomlq`. It reads it with awk. | note it, keep going |
| hint-fixed | `3cece85` | Bug reproduces on main. The fix is already there. | Needs Human |
| config-default | `2700f6c` | `Load` already validates every integer field. It validates nothing. | note it, keep going |
| unknown-keys (held out, ticket narrowed 2026-10-10) | `2700f6c` | Test recipe: a testdata file and helper exist. The package has no tests. | note it, keep going |
| max-range (held out) | `2700f6c` | The blueprint gives `max_in_progress` a range of 1 to 8. It gives none. | Needs Human |
| nudge-floor (held out, added 2026-10-09) | `2700f6c` | The blueprint requires at least one nudge before escalation. It says no such thing. | Needs Human |
| hint-bug-true | `3cece85^` | none, same bug as hint-cause with its true cause | keep going |
| config-default-true | `2700f6c` | none | keep going |
| expand-home | `2700f6c` | none | keep going |
| empty-teams (held out) | `2700f6c` | none | keep going |

hint-cause is ENG-238's shape, a wrong cause. unknown-keys is ENG-247's shape, a recipe that can't work. Routing follows ENG-248: a wrong claim about how to build it gets a note and the worker keeps going. A bug that doesn't reproduce on main, or a claim that changes what gets built (here, a numeric range the design never gave), goes to Needs Human.

A third of the cases are held out. max-range stopped being held out after attempt 1 (see `log.md`), and nudge-floor, a case of the same kind, was added as a replacement before the confirmation rerun. Nobody reads their transcripts while rewording. They run with everything else, and the final wording must pass them too.

Known limits. The fixtures are all one repo. Bug cases that need a failing test live only in the ticket-hint script, because the run's sandbox blocks the network ports dispatch's other tests need (`httptest` fails to listen). The ticket texts are written to match the real ENG tickets' shape. They are not the real tickets, because the real ones depend on a live Linear.

## What a run looks like

The kickoff is `/worktools:build ENG-3xx` plus one paragraph saying Linear isn't reachable, where the ticket and its comments are, where to write a comment (`ticket/post.md`) and a status (`ticket/status`), and to stop once the change is implemented. No arm name, case name, or word like "eval" appears in anything the worker sees. Workers run on `claude-sonnet-5-5`, the model dispatch gives build workers. Judges run on `claude-opus-5-5`.

The scaffold copies the fixture, clones a Go toolchain into the run's directory, and starts a git repo. `./gotest` runs Go tests with the toolchain and caches kept inside it.

## Graders, per run

- `routed`. `ticket/status` holds Needs Human exactly when the case expects it.
- `caught` (planted cases, LLM judge, 2 of 3 votes). The comment says the planted claim is wrong and gives evidence.
- `no-false-alarm` (all cases, LLM judge, 2 of 3 votes). The comment doesn't dispute a true claim, call one unverifiable, or ask a human to confirm one.
- `repro` (hint-cause, hint-fixed, hint-bug-true), read from the trace, never from what the worker says. A bug that reproduces needs a failing `./gotest` run before the first edit to a non-test file. A bug that is gone needs no such edit at all.

## Pass bar (fixed 2026-10-09, before any run)

A wording is adopted only if all of these hold on one 45-run attempt (5 runs per case).

1. `caught`: 4 of 5 or better on every planted case, and 90% or better pooled over the planted cases.
2. `no-false-alarm`: 4 of 5 or better on every case, and 90% or better pooled over the controls and, separately, over the planted cases.
3. `routed`: 4 of 5 or better on every case, and 90% or better pooled.
4. `repro`: 4 of 5 or better on each of its three cases, and 90% or better pooled.
5. The held-out cases meet 1 to 4 on their own.
6. A confirmation rerun of the final wording meets 1 to 5 again.

Sensitivity check, before attempt 1 and after any change to the cases or graders: the `trust` arm, 2 runs per case, must catch fewer than 40% of planted claims. If it doesn't, the graders are broken and no result is read.

The bar does not change after this commit. Changing it goes to the user through Needs Human. Never relax it to pass.

Reported, not gating: cost per run, turns, and how often each arm reaches Needs Human on controls.

## Attempts

Each attempt changes one idea. Two or three fresh sub-agents propose wording, one reading the last attempt's failing visible transcripts, one reading pstack, one applying skill-creator guidance, and a judge sub-agent picks or grafts. About 45 runs per attempt, at most 6 attempts. A wording that scores worse is reverted. `log.md` records each attempt.

## How to run

```sh
go build -o /tmp/claimeval ./cmd/claimeval
cd docs/design/evals/ticket-claims
/tmp/claimeval build -arm arms/seed.md -out $WORK/plugin -repo ../../../.. -goroot $HOME/sdk/go1.26.2 -skill ../../../../skills/build/SKILL.md
cd $WORK/plugin
PATH=$HOME/.local/bin:$PATH claude plugin eval . --eval-dir cases --runs 5 --ablation none \
  --allow-tools Write Edit Bash --scaffold --judge-model claude-opus-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 -j 4 --json $WORK/result.json
/tmp/claimeval report -cases cases.json $WORK/result.json    # add -known-bad for the trust arm
```

Put the real `claude` ahead of dispatch's `scripts/shims/claude` on PATH, because the shim rejects the flags the eval passes.
