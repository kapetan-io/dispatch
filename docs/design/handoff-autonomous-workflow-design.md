# Handoff: Autonomous workflow design (2026-10-08)

This is the design of a "loops, not code" workflow for Claude Code: a grill, a review worker, a dispatcher and build workers. As of 2026-10-08 design is paused, and implementation starts iteratively with the first piece of the dispatcher. Pick it up to cut and build that piece.

## What We Worked On

- Researched Cursor's pstack plugin (written by poteto) with four read-only sub-agents. Adopted several of her techniques. The rest are listed under "Ideas from the pstack research" in the decision log's Open section.
- Built a frozen eval suite for the size check, run with `claude plugin eval`, and iterated the wording through six attempts plus a held-out check.
- Settled the breakdown step, the redesign path, and how the grill and review handle alternatives and open questions.
- Agreed to stop adding design on paper and to build the workflow piece by piece, starting with the dispatcher.

## Files Changed

All paths are under `docs/design/` in the `kapetan-io/dispatch` repo (`~/Development/dispatch`). They moved there from `~/.claude/docs/features/autonomous-build/` on 2026-10-08. Eval `results/` and `viewer/` stay on disk but are not committed.

- `decision-log.md`. The main record, updated throughout. Read its Glossary and "Working with you" first.
- `evals/size-check/` (new). The size-check eval suite.
  - `README.md` holds the cases, the graders and the pass bar for each attempt, fixed before each run.
  - `log.md` records attempts 1 to 6 and the held-out check, with costs.
  - `arms/` holds every wording tried. `complete-pieces-5.md` is the adopted one. `by-layer.md` is the known-bad control.
  - `cases.json` lists the 15 cases with their answer keys. 6 of them are held out, from five other repos.
  - `inputs/` holds the design documents the cases read.
  - Scripts:
    - `build.py` builds a blinded plugin folder.
    - `run.sh <arm> <runs> <alias> [flags]` runs one arm. It needs `SIZE_EVAL_WORK` set to a scratch directory, and `SIZE_EVAL_OUT` optionally names the results folder.
    - `report.py <arm>` pulls each run's answer out of its trace and prints the measures.
    - `to_viewer.py` lays out results for skill-creator's viewer.
  - `results/<arm>/` holds `result.json` and every run's `size.yaml`.
  - `viewer/review.html` is the skill-creator review page comparing complete-pieces-2 with -3.
- `handoff-autonomous-workflow-design.md`. This file.
- `blueprint.md` is unchanged. Its owed edits are still listed in the decision log.

## Key Decisions (brief)

The details and evidence are in `decision-log.md`.

- One design makes one build ticket per piece. A piece is small, easy to review, and ships something a user can observe. The first piece blocks every other piece (breakdown 1, 2, 8).
- The adopted size-check wording is `evals/size-check/arms/complete-pieces-5.md`. It passed every criterion on 15 designs, 6 of them held out.
- A design may have build tickets in another repo only for a library the main ticket needs. A library ticket ends at a release that you approve. Agents always work in leased worktrees (breakdown 6).
- The grill runs poteto's arena as a trial. Two sketches are made, and a judge picks and merges them, so you see only the result. The grill and the review settle questions that a run can answer with throwaway prototypes. `deliberate` is dropped (design stage, decision 2).
- The PR has no "departures from the design" section. A repeating pattern of deviations stops the build and files a redesign review ticket that links to the build ticket that triggered it. Shipped work is never reverted (build stage, "When the design is wrong").
- Skills stay light. When you're unsure, take poteto's way and adapt later. Wording a skill depends on gets an eval first, at about 45 runs per attempt (Working with you).

## Unresolved Items

- The breakdown step's "Still to settle" list: order inside the review, ticket creation, ticket bodies that cite the blueprint, the decision log in the blueprint, safe reruns, hand-off and revision, zero build tickets, and follow-up placement.
- Open: how a grill starts, a design you reopen yourself, which spec copy wins once committed, and smaller items.
- The pstack ideas not yet discussed: build worker verification tied to `git patch-id`, tests that must fail when stubbed, blast-radius checks, CI failure classes, a review-round counter in the dispatcher, a WIP commit before a workspace closes, and the rest in the Open list.
- Paused until something runs: bug intake, intake, the improve stage, and skill names and boundaries.
- Your other session `claude-f0` ran this morning's size-check test and asked whether ENG-132 should split. The decision log now answers that (breakdown 8).

## What's Left to Do

1. Run the size check (`evals/size-check/arms/complete-pieces-5.md`) on `blueprint.md` to cut the dispatcher into pieces. The expected first piece is that a ticket you write by hand, marked with the AI label, gets a leased tree, a herdr workspace and a build worker, and ends as an open PR.
2. Apply to `blueprint.md` only the owed edits that touch the first piece.
3. List the setup the first piece needs and get your yes. Candidates are a Linear API key, the AI label, a Needs Human status, a `treehouse.toml`, and auto-merge with branch protection on one personal pilot repo.
4. Build the first piece on that repo and run it on a real ticket.
5. Feed what it teaches into the decision log, then cut and build the next piece.

## Dependencies & Ordering

- Setup needs your yes before step 4.
- The first piece blocks the rest, and later pieces are designed with what it teaches.
- The byte-exact Linear round-trip test still waits on the dispatcher's API key.

## Pointers for Next Session

- Run the eval suite from `evals/size-check/`. For example: `SIZE_EVAL_WORK=<scratch> ./run.sh complete-pieces-5 3 kit-x -j 5 --max-cost-usd 30`, then `./report.py complete-pieces-5`.
- `claude plugin eval` docs: https://code.claude.com/docs/en/plugin-evals.md
- pstack: https://github.com/cursor/plugins/tree/main/pstack. Fetch files with `gh api repos/cursor/plugins/contents/<path> -H "Accept: application/vnd.github.raw"`. The skills used are `skills/architect/`, `skills/arena/`, and `skills/poteto-mode/playbooks/` (prototype, multi-phase-plan, eval, hillclimb). The copies fetched this session were in a temporary scratchpad and are gone.
- Eval runs with `--keep-temp` leave sealed directories at `/private/tmp/e-*`. The traces were already extracted into `results/`.
- Today's eval runs cost about $138 across roughly 330 runs.
- Transcripts:
  - This session: `~/.claude/projects/-Users-thrawn--claude/62a94c4f-c48b-45c2-8beb-39499c082d26.jsonl`
  - The 2026-10-07 breakdown session: `~/.claude/projects/-Users-thrawn--claude/b0dcc5c7-b33a-4e82-a510-1fa40accc25d.jsonl`
  - The design-stage session: `~/.claude/projects/-Users-thrawn--claude/2cb41809-4946-4ccb-9f7c-97e19815dfd5.jsonl`
- Tools:
  - herdr 0.9.0 at `/opt/homebrew/bin/herdr`
  - treehouse v3.1.2 at `~/go/bin/treehouse`
  - Linear workspace `kapetan-io`, team `ENG`
- How you want to work is in the log's "Working with you". Ask in plain chat, one question at a time, never with the AskUserQuestion questionnaire, and with the context restated.
