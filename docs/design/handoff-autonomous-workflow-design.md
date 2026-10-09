# Handoff: dispatch, piece 1 shipped and the pilot running (2026-10-09)

Piece 1 of the dispatcher (ENG-226) is merged, and the pilot is live: `dispatch run` launched a real build worker for piece 2 (ENG-228), which is building now. Pick this up to land the two fix PRs, carry the pilot to a merged PR, and cut piece 3.

Read `docs/design/decision-log.md` first, its Glossary and "Working with you" above all. Ask the user in plain chat, one question at a time, never with the AskUserQuestion questionnaire, and restate the context.

## What We Worked On

- Ran the launcher for real on a throwaway ticket (ENG-227, canceled). It worked end to end and found three bugs, all fixed before merge.
- Replaced the planned launchd job with `dispatch run`, a foreground loop the user runs and stops in a herdr window. Each tick prints a summary line with Linear call stats.
- Took PR #1 through a review sub-agent, validator sub-agents and CodeRabbit until it approved. The user merged it, and ENG-226 is Done.
- Started the pilot. Created ENG-228 (piece 2, nudges and questions), and the user moved it to In Progress. The worker stalled on a question herdr couldn't see, which led to ENG-229 (PR #2). CI was missing, which led to ENG-230 (PR #3).

## PR(s) In Flight

### PR #2: Let herdr see worker questions, and keep workers from asking non-product questions
- Branch: `thrawn01/eng-229-dispatch-let-herdr-see-worker-questions-and-keep-workers`
- Linear: ENG-229, In Review, no `ai` label
- **State:** CodeRabbit approved `33cba9f`. This handoff is a later commit on the branch, so CodeRabbit reviews again. No CI runs on it until PR #3 merges.
- **Outstanding threads:** none
- **Ready for:** merge by the user once CodeRabbit approves the handoff commit

### PR #3: Run vet, the tests and shellcheck in CI on every PR
- Branch: `thrawn01/ci-run-tests`, head `e44b05f`
- Linear: ENG-230, In Review, no `ai` label
- **State:** CI passed on the first push in 1m21s. CodeRabbit asked for `persist-credentials: false` on checkout. That is fixed in `e44b05f`, with the token also limited to `contents: read`. CodeRabbit approved `e44b05f` and CI passed on it.
- **Outstanding threads:** none
- **Ready for:** merge by the user

## The pilot (ENG-228)

- Ticket: https://linear.app/kapetan-io/issue/ENG-228, In Progress, labels `ai`, `dispatch`, `Feature`
- Worker: herdr agent `eng-228`, workspace `w3F`, pane `w3F:p2`, in tree `~/.treehouse/dispatch-711e16/2/dispatch` (leased to ENG-228)
- The user runs `dispatch run` in their own herdr window and starts and stops it themselves.
- At handoff the worker was in build step 2, implementing and writing tests, with nothing committed. It had added `internal/notify/`, `tell.go` and `nudge_test.go`, and edited `dispatch.go` plus the `cache`, `config`, `herdr` and `reconcile` packages.
- Its ticket comment records the re-notify answer (`blocked_renotify_ticks`, default 3, chosen by the user's agent on the user's say-so) and three calls it made itself: notifying through `osascript` with the text passed as an argument, notifying in the same tick it escalates, and counting the resume nudge toward the two-nudge limit.
- **This worker was launched before PR #2.** Its Claude session has `--name eng-228`, so herdr reports it `idle` or `done`, never `blocked`, while it waits on a question. Look at its window if it seems quiet. A note telling it to settle non-product questions itself was typed into its session.

## Files Changed (PR #2 and #3, on top of main `27ebb1b`)

- `bin/dispatch-launch`: no `--name` for Claude, and `session_name` renamed to `worker_name`, which is herdr's agent name.
- `scripts/shims/claude`: no longer re-adds a name.
- `skills/build/SKILL.md`: a "Work autonomously" rule. Ask only about product behavior or scope, contract shape, security defaults, superseding an ADR, or an acceptance criterion that can't be observed at the surface. Decide everything else, note it on the ticket, and keep going.
- `launch_test.go`: tests that no `--name` reaches `herdr agent start`, and that the shim restores the plugin and flags but no name.
- `docs/design/decision-log.md`: decision 9 records the herdr `--name` limitation.
- `.github/workflows/test.yml` (PR #3): `go vet`, `go test -race`, shellcheck.

## Key Decisions (brief)

- The dispatcher runs as `dispatch run` in a herdr window the user watches, not under launchd. Recorded in the decision log's Framing.
- Workers ask only the four decision-4 calls, plus a criterion that can't be observed. A ticket's open question is not by itself a reason to ask.
- Claude is never started with `--name`, because herdr then cannot see its questions (upstream herdrdev/herdr#4573, unfixed in herdr 0.9.3 and master).
- Fix PRs found in a pilot get their own ticket without the `ai` label, so the dispatcher never picks them up.
- Review loop used on every PR: a critical-review sub-agent, one validator sub-agent per finding before fixing, then CodeRabbit until it approves. Older CodeRabbit summaries are hidden as outdated so new ones stand out.

## Unresolved Items

- **Trees are never returned.** Piece 1 has no teardown. Tree 1 is still leased to ENG-226 (Done). Return it when this session ends, from the main checkout: `treehouse return ~/.treehouse/dispatch-711e16/1/dispatch`. Tree 2 must be returned by hand after ENG-228 is Done.
- **The build skill's new wording has had no eval.** The decision log wants about 45 runs before wording a skill depends on ships. The user has not decided between an eval and adjusting as the pilot runs.
- **herdrdev/herdr#4573.** The user was asked whether to add the experiment and a suggested fix upstream (only the blocked check should ignore rules that carry text). No answer yet.
- **A stray empty pool state** at `~/.treehouse/gitnotoml-037230`, left by a validator's probe. Removing it was denied to the agent. The user can run `rm -r ~/.treehouse/gitnotoml-037230`.
- **ENG-65 "Claude orchestration"** carries the `ai` label in Todo. It is harmless unless moved to In Progress. Remove the label if it means something else.

## What's Left to Do

1. The user merges PR #3, then PR #2 once CodeRabbit approves the handoff commit. Afterwards `git -C ~/Development/dispatch pull`. Neither PR changes Go code the binary uses, so no `go install` is needed. Workers load the launcher, shim and skill from that checkout, so the next launch picks them up.
2. Watch ENG-228 to In Review or Needs Human. Once PR #3 is on main, its PR gets CI after its rebase.
3. When ENG-228's PR is up, review it with a critical-review sub-agent and validators, as with PR #1. Its worker follows the PR through CodeRabbit itself (build step 7).
4. After ENG-228 merges and is Done, close workspace `w3F` and return tree 2 by hand.
5. Feed what the pilot taught into `decision-log.md`: the `--name` finding (done), questions herdr can't see, the autonomy rule, CI. Then cut and file piece 3, getting the PR merged (blueprint criteria 6, 9 and 17), in the same shape as ENG-228.
6. Setup that needs the user's yes: branch protection on main requiring the `test` check and CodeRabbit, and auto-merge.

## Dependencies & Ordering

- PR #3 before ENG-228's PR is reviewed, so that PR runs CI.
- PR #2 before any new worker launches, so herdr can see that worker's questions.
- ENG-228 merged before piece 3, because pieces build in order and the first unmerged piece blocks the rest.

## Pointers for Next Session

- Main checkout and plugin root: `~/Development/dispatch`. This session's tree: `~/.treehouse/dispatch-711e16/1/dispatch`.
- Check the worker: `herdr agent list | jq '.result.agents[] | select(.name=="eng-228")'` and `herdr agent read w3F:p2 --source visible --lines 40`.
- Trace a launch's kickoff screens: `DISPATCH_LAUNCH_TRACE=<file> bin/dispatch-launch <TICKET> <branch>`.
- Live checks: `dispatch tick --dry-run`. Tests: `go test -race ./...`.
- CodeRabbit inline comments put the finding text after a long block of tool logs. Extract the bold title and the paragraph after it.
- This session's transcript: `~/.claude/projects/-Users-thrawn--treehouse-dispatch-711e16-1-dispatch/e71827ff-2843-441e-a5b5-52ca1142fed9.jsonl`.
