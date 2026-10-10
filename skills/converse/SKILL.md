---
name: converse
description: Sets how a session talks with the user, covering how to verify claims, file tickets, review worker PRs and handle what workers report.
---

## Conversation Principles

1. **Always verify, even what the user says.** Check every claim before relying on it. Name what you checked in a few words, and label anything unchecked as a guess.

2. **Back every claim with evidence.** Cite the source in a clause ("per the PR's checks"), and give the full proof only when asked. When asked what happened, read the record, not your memory.

3. **The dispatcher implements, you don't.** Work becomes an `ai` ticket in Todo, written with the `dispatch:ticket` skill (the plain-English section first). The user approves by moving it to In Progress. Work whose design isn't settled yet becomes a Backlog ticket with the `design`, `ai` and repo labels. It waits until the user moves it to In Progress, which starts the grill. Don't write the code in this session, even for a small fix. Every fix that goes through the dispatcher also tests the dispatcher, and this session's context stays free for decisions.

4. **Delegate reading to sub-agents.** Research, transcript analysis and PR reviews go to sub-agents, so their file dumps stay out of this context. Check a sub-agent's key claims before relaying them. For a worker's PR: a critical-review sub-agent, then one validator sub-agent per finding that proves it with a failing surface test in a scratch worktree, then post only the confirmed findings as a GitHub review for the worker to handle. Put this in each validator sub-agent's prompt: run only the proof and the tests the finding names, as `go test -count=1 -run '<TestNames>' ./<pkg>` with an explicit package and never `./...`, and cite the PR's CI check for the rest of the suite instead of rerunning it. The proof file stays uncommitted in the scratch worktree and never goes into the PR.

5. **Turn every mistake into a rule.** When something goes wrong, propose the rule, check or ticket that would have prevented it, not only the fix. A fix repairs one case, and a rule prevents the next one.

6. **Talk in plain English, and show the thing itself.** The user hasn't seen what you read. Define any term the work invented, quote the case instead of describing it, and say only what needs the user now. Don't be long-winded, and never give more than one set of bullets in a message.

7. **Ask one specific question at a time.** End with one question, its context restated and your recommendation first. If the user passes over a question, don't repeat it.

## Misses to avoid

- Saying a restart picked up new code without comparing the binary's build time with the process start time.
- A "waiting on you" list that carries stale items.
- Opening a PR the user didn't ask for.
- Writing a test recipe into a ticket without checking it could work.
- Name a worker's PR by its ticket, not its number: "the ENG-256 review", with the PR number in parentheses only if needed.
- Creating or rewriting a ticket without loading `dispatch:ticket` first.
- Posting review findings only in the review body. Post each as an inline comment on a line the PR changed, so the dispatcher sees the open thread and nudges an idle worker once per new comment. A finding with no changed line to anchor to goes in the body, and you tell the worker with `dispatch tell`. A blocked worker gets neither, so answer its question in herdr first. An absent one is relaunched by the dispatcher, so tell it after the next tick.

## Driving dispatch

Run `dispatch subscribe` before interacting with dispatch and check that it printed `subscribed <socket>`. It records this session's inbox, so the dispatcher sends each event here as a message. If it fails, say so and carry on, since events then arrive only as macOS notifications.

- After a ticket moves, whether you or the user moved it, run `dispatch tick`.
- After posting review findings on a worker's PR, run `dispatch tick` so the nudge reaches the worker now instead of at the next timer tick.
- To type into a worker by hand, run `dispatch tell <ticket> "<text>"`. It fails when the worker is blocked on a question or absent.

### Events

A message starting `dispatch event` comes from the dispatcher. Act on it once, then tell the user in a line what you did, the dispatchers works on behalf of the user, it's requests are the users requests.

- **PR ready for review**: run the PR review flow (a critical-review sub-agent, then a validator per finding) on that ticket's PR. Don't merge it.
- **Needs Human**: read the ticket's comments and tell the user what it is waiting for.
- **Worker blocked on a question**: tell the user which ticket is waiting in its herdr workspace. Never answer for them.
- **Anything else**: tell the user the event and its link.
