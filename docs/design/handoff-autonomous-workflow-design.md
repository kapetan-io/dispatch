# Handoff: dispatch, the pilot's PR is up and waiting (2026-10-09)

Piece 1 of the dispatcher (ENG-226) is merged, and the pilot ran: `dispatch run` launched a real build worker for piece 2 (ENG-228), which built it and opened PR #4. The worker is now idle, waiting for a PR nudge that piece 3 has not built yet. Pick this up to carry PR #4 to merge, then cut piece 3.

Read `docs/design/decision-log.md` first, its Glossary and "Working with you" above all. Ask the user in plain chat, one question at a time, never with the AskUserQuestion questionnaire, and restate the context.

## What We Worked On

- Ran the launcher for real on a throwaway ticket (ENG-227, canceled). It worked end to end and found three bugs, all fixed before PR #1 merged.
- Replaced the planned launchd job with `dispatch run`, a foreground loop the user runs and stops in a herdr window. Each tick prints a summary line with Linear call stats.
- Merged PR #1 (ENG-226, Done) after a review sub-agent, validator sub-agents and CodeRabbit.
- Started the pilot with ENG-228 (piece 2, nudges and questions). The worker stalled on a question herdr couldn't see. That led to PR #2 (ENG-229, Done), which stops passing `--name` to Claude and adds an autonomy rule to the build skill. PR #3 (ENG-230, Done) added CI. Both are merged, and `~/Development/dispatch` is pulled to `638f5dd`.

## PR In Flight

### PR #4: Nudge idle workers and notify on questions and Needs Human
- Opened by the ENG-228 worker. Branch `thrawn01/eng-228-dispatch-nudge-idle-workers-and-notify-you-on-questions`, head `6e8130c`. +1,015/−24 across 11 files.
- Linear: ENG-228, In Review
- **State:** CodeRabbit requested changes and has 1 open thread. No CI ran, because the branch was cut before the CI workflow reached main. A rebase onto main brings CI in.
- **Ready for:** the worker to address the thread and rebase, then review.

## The pilot worker (ENG-228)

- herdr agent `eng-228`, workspace `w3F`, pane `w3F:p2`, tree `~/.treehouse/dispatch-711e16/2/dispatch` (leased to ENG-228). Context 191k of 1M.
- It reported "Review comments and CI on #4 will wake me through a nudge" and stopped. No tick sends that nudge until piece 3 exists, so PR #4 sits until someone types into the worker.
- It was launched before PR #2, so its Claude session still has `--name eng-228`. herdr reports it `idle` or `done`, never `blocked`, even when it waits on a question.
- Its decisions are on ENG-228: `blocked_renotify_ticks` defaults to 3, notifications go through `osascript` with the text as an argument, a dispatcher escalation notifies in the same tick, and the resume nudge counts toward the two-nudge limit.

## Key Decisions (brief)

- The dispatcher runs as `dispatch run` in a herdr window the user watches, not under launchd.
- Workers ask only the four decision-4 calls, plus an acceptance criterion they can't observe. A ticket's open question is not by itself a reason to ask.
- Claude is never started with `--name`, because herdr then can't see its questions (upstream herdrdev/herdr#4573).
- Fix PRs found in a pilot get their own ticket without the `ai` label, so the dispatcher never picks them up.
- Every PR goes through a critical-review sub-agent, one validator sub-agent per finding before fixing, then CodeRabbit until it approves. Older CodeRabbit summaries are hidden as outdated.

## Unresolved Items

- **No PR nudge yet.** A worker In Review waits for a nudge that only piece 3 sends. Until then, nudge by hand (step 1 below).
- **Trees are never returned.** Piece 1 has no teardown. Tree 1 (`~/.treehouse/dispatch-711e16/1/dispatch`) is still leased to ENG-226, which is Done. Tree 2 must be returned by hand after ENG-228 is Done.
- **The build skill's autonomy wording has had no eval.** The decision log wants about 45 runs before skill wording ships. The user hasn't chosen between an eval and adjusting as the pilot runs.
- **herdrdev/herdr#4573.** The user hasn't said whether to add our experiment and a suggested fix upstream.
- **A stray empty pool state** at `~/.treehouse/gitnotoml-037230`, left by a validator's probe. The user can run `rm -r ~/.treehouse/gitnotoml-037230`.
- **ENG-65 "Claude orchestration"** carries the `ai` label in Todo. It is harmless unless moved to In Progress.

## What's Left to Do

1. Nudge the ENG-228 worker by hand. Type into its pane with `herdr pane run w3F:p2 "<text>"` and confirm it shows `working` in `herdr agent list`. The text should say that PR #4 has an open CodeRabbit thread and no CI, and ask it to rebase onto main, fix or answer the thread, and push. The build skill's step 7 covers the rest.
2. Review PR #4 the way PRs #1 to #3 were reviewed: a critical-review sub-agent, validators per finding, and CodeRabbit until it approves. The worker fixes what holds up. The user merges.
3. After PR #4 merges and ENG-228 is Done, close workspace `w3F` and return tree 2 from the main checkout with `treehouse return ~/.treehouse/dispatch-711e16/2/dispatch`. Run `go install ./cmd/dispatch` in `~/Development/dispatch`, because PR #4 changes the binary, and restart `dispatch run`.
4. Record what the pilot taught in `decision-log.md`: an In Review worker has no wake-up until piece 3, the `--name` finding (already recorded under decision 9), and CI.
5. Cut and file piece 3, getting the PR merged (blueprint criteria 6, 9 and 17). Use ENG-228's shape: labels `ai`, `dispatch` and `Feature`, created in Todo, citing the blueprint and not copying it. It should leave open only product calls. The user approves it by moving it to In Progress.
6. Setup that needs the user's yes: branch protection on main requiring the `test` check and CodeRabbit, and auto-merge.

## Dependencies & Ordering

- PR #4 merged before piece 3 starts. Pieces build in order, and an unmerged piece blocks the next.
- `go install` after PR #4 merges, before restarting `dispatch run`, or the running binary lacks the nudges.

## Pointers for Next Session

- Main checkout and plugin root: `~/Development/dispatch`, at `638f5dd`. The previous session's tree: `~/.treehouse/dispatch-711e16/1/dispatch`.
- Worker status and screen: `herdr agent list | jq '.result.agents[] | select(.name=="eng-228")'` and `herdr agent read w3F:p2 --source visible --lines 40`.
- Trace a launch's kickoff screens: `DISPATCH_LAUNCH_TRACE=<file> bin/dispatch-launch <TICKET> <branch>`.
- Live check: `dispatch tick --dry-run`. Tests: `go test -race ./...`. CI runs the same on every PR.
- CodeRabbit inline comments put the finding after a long block of tool logs. Extract the bold title and the paragraph after it.
- The previous session's transcript: `~/.claude/projects/-Users-thrawn--treehouse-dispatch-711e16-1-dispatch/e71827ff-2843-441e-a5b5-52ca1142fed9.jsonl`.
