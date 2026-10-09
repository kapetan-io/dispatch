# Handoff: dispatch, first piece in progress (2026-10-08)

The autonomous build workflow now lives in its own Claude Code plugin repo, `kapetan-io/dispatch`. Design is paused, and the first piece of the dispatcher is being built under ENG-226. Pick it up to finish piece 1 and run it on a real ticket.

## What We Worked On

- Settled the design through 2026-10-08. `docs/design/decision-log.md` is the record. Read its Glossary and "Working with you" first.
- Built and tuned the size-check eval suite (`docs/design/evals/size-check/`). The adopted wording is `arms/complete-pieces-5.md`.
- Created the repo and the Linear setup, then cut the dispatcher into 6 pieces with the size check. All 3 runs agreed on the first piece.
- Built most of piece 1, ENG-226 (https://linear.app/kapetan-io/issue/ENG-226): you move a ticket labeled `ai` to In Progress, and `dispatch tick` gives it a leased tree, a herdr workspace and a build worker.

## Where the work is

- Repo: https://github.com/kapetan-io/dispatch, private. Your main checkout is `~/Development/dispatch`, on `main`.
- ENG-226 is built in a leased treehouse tree, not your checkout, per the "always worktrees" rule.
  - Tree: `~/.treehouse/dispatch-711e16/1/dispatch`, lease holder `ENG-226`
  - Branch: `thrawn01/eng-226-dispatch-launch-a-build-worker-for-an-approved-ticket`, pushed. No PR yet.
- Commits on the branch, after main's `1ae4c68`:
  - `0a996fe` applies the owed blueprint edits that touch piece 1.
  - `be324e8` defaults workers to sonnet, per your coding-ticket model policy.
  - `9a432f8` adds `dispatch tick` and `--dry-run` with surface tests.
  - `0d256f6` adds `bin/dispatch-launch`, `scripts/shims/claude` and `skills/build/SKILL.md`.

## Files Changed on the branch

- `dispatch.go` and `cmd/dispatch/main.go`. `Run(ctx, args, opts)` with `tick [--dry-run]`. It takes the lock, reads config, cache, herdr, treehouse and Linear, plans, then launches or escalates. Two failed launches in a row move the ticket to Needs Human with the launcher's error. `pool_exhausted` never counts as a failure.
- `internal/` packages:
  - `config`, `lock` (an flock that the OS releases if a tick dies), `cache`
  - `linear` (GraphQL: managed tickets, state id, move, comment)
  - `herdr`, `treehouse`, `worker` (ticket to lease to workspace to agent)
  - `reconcile` (a pure `Plan`: launch, wait, skip, escalate)
  - `launcher` (runs `bin/dispatch-launch` and parses its JSON)
- `dispatch_test.go`. 12 surface tests against a fake Linear server and fake herdr, treehouse and launcher scripts. All pass. Two deliberate breaks, the worker cap and the failure counting, each failed their test.
- `bin/dispatch-launch`. Rewritten from `~/.claude/skills/captain/captain-launch` and keeps its herdr fixes.
  - Leases a tree under the ticket id. A repo without `treehouse.toml` returns `not_pooled`.
  - Opens a nested herdr workspace with an env-stamped tab.
  - Starts Claude with `--plugin-dir <plugin_root>`, `--disallowedTools "EnterPlanMode,ExitPlanMode"` and `--dangerously-skip-permissions`, and types `/dispatch:build <ticket>`.
  - Returns `already_running` when the workspace already has a Claude pane.
  - Passes shellcheck. **Never run against real herdr yet.**
- `scripts/shims/claude`. Re-adds the worker's name, the plugin and its flags when herdr restarts a worker with a bare `claude --resume`.
- `skills/build/SKILL.md`. The build-worker skill: the seven build steps, "decide how, never what", and stopping when the design proves wrong. **Never run yet.**
- `docs/design/blueprint.md`. The owed edits for piece 1 are applied, and the decision log marks them "(applied, ENG-226)".

## Setup done (you approved it on 2026-10-08)

- Linear API key in the login keychain under service `dispatch-linear-api-key`. Verified against Linear.
- `ai` label on ENG. It already existed and was reused, with a description added. **ENG-65 "Claude orchestration" already carries it**, in Todo. It's safe unless moved to In Progress. Remove the label there if it means something else.
- `dispatch` repo label under the `repo` group.
- Needs Human status on ENG, created through the API as a started-type status after In Review.
- `treehouse.toml` in the repo, with `max_trees = 4`, committed on main.
- `~/.config/dispatch/config.toml` written. A live `go run ./cmd/dispatch tick --dry-run` against real Linear, herdr and treehouse exited 0 with no actions, because no `ai` ticket is In Progress. That proves the Linear query parses, not that it returns tickets.
- No branch protection or auto-merge on the repo yet, by your choice. Workers leave PRs in In Review for you to merge.

## Key Decisions (brief), all in `decision-log.md`

- One design makes one build ticket per piece. The first piece blocks the rest, and a redesign never reverts shipped work.
- The grill runs poteto's arena as a trial. The grill and the review settle questions a run can answer with prototypes. `deliberate` is dropped.
- When unsure, take poteto's (pstack's) way and adapt later. Skills stay light, and wording a skill depends on gets an eval first (about 45 runs).
- The workflow is its own plugin repo. Implementation is iterative, one piece at a time.

## What's Left to Do

1. **Run the launcher for real once.** Done 2026-10-08 on throwaway ENG-227 (canceled). The lease, the nested workspace, the env-stamped tab, Claude on sonnet with the plugin, `/dispatch:build` and the JSON result all worked. It found three bugs, fixed in `52b0780`. The kickoff was typed twice, because while a skill runs Claude's screen never shows the submitted text, so the launcher now confirms by herdr's agent status. herdr refused a pool tree as the nesting source, so the launcher now names the main checkout. New branches tracked `origin/main`, so they now use `--no-track`. Still unexercised are the `claude` shim on a herdr restart and the consent and folder-trust dialogs, which this machine had already accepted. `DISPATCH_LAUNCH_TRACE=<file>` records every screen the kickoff step reads.
2. **Run the dispatcher in a herdr window.** Done 2026-10-08. You chose a herdr window you start and stop over launchd, so you can watch it. `dispatch run` ticks every `interval_minutes`, keeps going after a failed tick, and stops on Ctrl-C. The README has the install and run steps. A live 3-second run against real Linear, herdr and treehouse ticked cleanly and exited on SIGINT.
3. **Open the PR for ENG-226** in the Purpose and Implementation format, with `Fixes ENG-226`. You review and merge it. Workers load the plugin from `plugin_root`, which is your main checkout, so the launcher and skill must be on main before a dispatched run.
4. **Pilot end to end.** After merging, pull main and `go install`. Approve one real `ai` ticket, ideally piece 2 of the dispatcher, by moving it to In Progress. Start `dispatch run` in a herdr window and watch the ticket reach In Review or Needs Human.
   Piece 1 never returns a tree once a ticket is Done, Canceled or in Needs Human. That is the teardown piece's job (blueprint tick step 4, with the dirty-tree guard). Until it ships, return a finished ticket's tree by hand with `treehouse return <path>` from the repo, or launches stall on `pool_exhausted`, which the run window prints with the holders. The worker cap also counts live agents, not leased trees.
5. **Feed what the pilot teaches** into the decision log, then cut and build the next piece.

## Dependencies & Ordering

- Step 1 before step 3, because the launcher must work before it merges.
- Step 3 before step 4, because workers load the plugin from main.

## Pointers for Next Session

- Resume in the ENG-226 tree: `cd ~/.treehouse/dispatch-711e16/1/dispatch`. Return it later with `treehouse return ~/.treehouse/dispatch-711e16/1/dispatch` from the repo.
- Tests: `go test ./...` there. Live check: `go run ./cmd/dispatch tick --dry-run`.
- Plugin layout rules: https://code.claude.com/docs/en/plugins/components.md. Scripts in `bin/` go on the Bash PATH of sessions with the plugin, and `${CLAUDE_PLUGIN_ROOT}` works in SKILL.md bodies. Load the plugin locally with `claude --plugin-dir ~/Development/dispatch`.
- herdr 0.9.0 JSON:
  - `herdr workspace list` returns `.result.workspaces[]` with `workspace_id` and `worktree.checkout_path`.
  - `herdr agent list` returns `.result.agents[]` with `agent`, `agent_status`, `pane_id` and `workspace_id`.
  - `treehouse status --json` returns an array with `path`, `status` and `lease_holder`.
- Linear is reached through the API key, not the MCP, for the dispatcher. Personal keys go in `Authorization` with no scheme.
- An empty `~/.claude/docs/features/` directory is left over from the move. A safety check blocked removing it, so delete it yourself if you like.
- Transcripts:
  - This session: `~/.claude/projects/-Users-thrawn--claude/62a94c4f-c48b-45c2-8beb-39499c082d26.jsonl`
  - Earlier design sessions: `b0dcc5c7-…` and `2cb41809-…` in the same folder.
- How you want to work is in the decision log's "Working with you". Ask in plain chat, one question at a time, never with the AskUserQuestion questionnaire, and with the context restated.
