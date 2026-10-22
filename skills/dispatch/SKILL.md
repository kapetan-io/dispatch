---
name: dispatch
description: Drives dispatch from a session, covering how to file tickets, review worker PRs and handle what workers report.
---

Load `dispatch:converse` with the Skill tool first.

## Dispatch Principles

1. **The dispatcher implements, you don't.** Work becomes an `ai` ticket in Todo, written with the `dispatch:ticket` skill (the plain-English section first). The user approves by moving it to In Progress. Work whose design isn't settled yet becomes a Backlog ticket with the `design`, `ai` and repo labels. It waits until the user moves it to In Progress, which starts the design skill. Don't write the code in this session, even for a small fix. Every fix that goes through the dispatcher also tests the dispatcher, and this session's context stays free for decisions.

2. **Review a worker's PR with sub-agents, only when the user asks.** A critical-review sub-agent, then one validator sub-agent per finding that proves it, with a failing surface test in a scratch worktree for code and a quoted diff or line for skill and doc text, then post only the confirmed findings as a GitHub review for the worker to handle. Post them without asking, then tell the user in a line what you posted. A confirmed finding that needs a decision only the user can make is posted too with its evidence, no proof required, and you also put that decision to the user. Put this in each validator sub-agent's prompt: run only the proof and the tests the finding names, as `go test -count=1 -run '<TestNames>' ./<pkg>` with an explicit package and never `./...`, and cite the PR's CI check for the rest of the suite instead of rerunning it. The proof file stays uncommitted in the scratch worktree and never goes into the PR.

## Misses to avoid

- Opening a PR the user didn't ask for.
- Reviewing a worker's PR the user didn't ask you to review. The sub-agents cost tokens, and declining is the user's call.
- Writing a test recipe into a ticket without checking it could work.
- Name a worker's PR by its ticket, not its number: "the ENG-256 review", with the PR number in parentheses only if needed.
- Creating or rewriting a ticket without loading `dispatch:ticket` first.
- Asking the user whether to post a confirmed review finding. Silence on the PR reads as a clean review, and a merged PR shipped that way once (ENG-366).
- Posting review findings only in the review body. Post each as an inline comment on a line the PR changed, so the dispatcher sees the open thread and nudges an idle worker once per new comment. A finding with no changed line to anchor to goes in the body, and you tell the worker with `dispatch tell`. A blocked worker gets neither, so answer its question in herdr first. An absent one is relaunched by the dispatcher, so tell it after the next tick.

## Driving dispatch

Run `dispatch watch <ticket>` only when the user asks you to follow it, or says yes when you offer. After moving a ticket to In Progress or taking over one whose worker is running, you may offer to review its PR. A yes means follow the ticket and review the PR when it is ready. Check that it printed `watching <ticket>`. The dispatcher then sends that ticket's events here as messages. If you watched a ticket, run `dispatch unwatch <ticket>` once its PR merges or the ticket closes. If watching fails, say so and carry on, since events then arrive only as notifications.

- After a ticket moves, whether you or the user moved it, run `dispatch tick`.
- After posting review findings on a worker's PR, run `dispatch tick` so the nudge reaches the worker now instead of at the next resync.
- Before moving a ticket, read its status from Linear, not from memory. A merged PR has already moved its ticket to Done, so ask the user before moving a ticket out of Done.
- To type into a worker by hand, run `dispatch tell <ticket> "<text>"`. It fails when the worker is blocked on a question popup or absent.

### Breaking down and starting work

- Asked to break down a design or a ticket, load `dispatch:breakdown` with the Skill tool and follow it.
- A finished design asks the user whether to review it. Review means a `design-review` child of the feature ticket, filed with `dispatch:ticket` and moved to In Progress. Otherwise the design worker breaks the design down itself.
- Asked to start a feature ticket's held work, do the Start move in `dispatch:breakdown` and report whether the first piece was promoted.

### Events

A message starting `dispatch event` comes from the dispatcher. Act on it once, then tell the user in a line what you did, the dispatcher acts for the user, so its requests are the user's.

- **PR ready for review**: this event is not a review request. If the user asked for a review of this ticket, run the PR review flow (a critical-review sub-agent, then a validator per finding) on its PR. Otherwise say the PR is ready and ask once if they want a review, and start nothing until they say yes. Never merge it.
- **Needs Human**: read the ticket's comments and tell the user what it is waiting for.
- **Worker blocked on a question popup**: tell the user which ticket is waiting in its herdr workspace. Never answer for them.
- **Workspace shows 💤**: the worker is waiting on the user or stopped early. Tell the user which window to open.
- **Anything else**: tell the user the event and its link.
