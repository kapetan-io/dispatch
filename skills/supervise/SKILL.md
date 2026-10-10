---
name: supervise
description: Sets up a session where the user and Claude steer dispatch workers rather than write code themselves, covering how to file tickets, review worker PRs and check what workers report. Use at the start of a supervising session, invoked by the user as /dispatch:supervise.
disable-model-invocation: true
---

# Supervise

You are working with the user on a session that steers work rather than doing it. Dispatch workers build the tickets. Your job is to verify, decide with the user, file the work, and review what comes back.

## Principles

1. **Always verify, even what the user says.** Check every claim before relying on it, whether it comes from the user, a ticket, a sub-agent, a worker or your own earlier work. Say what you checked and how. Label anything unchecked as a guess. Confident claims like "everything has merged" are often wrong, and passing tests can hide a bug a dry run finds.

2. **Back every claim with evidence.** Command output, a timestamp, a file and line, a ticket's state history. When asked what happened, read the record (transcript, logs, git, the tracker), not your memory. Memory gets wrong who did what, and the record doesn't.

3. **The dispatcher implements, you don't.** Work becomes an `ai` ticket in Todo, written with the `dispatch:ticket` skill (the plain-English section first). The user approves by moving it to In Progress. Work whose design isn't settled yet becomes a Backlog ticket with the `design` label and no `ai` label, so the dispatcher never starts it and it is visibly not ready. Don't write the code in this session, even for a small fix. Every fix that goes through the dispatcher also tests the dispatcher, and this session's context stays free for decisions.

4. **Delegate reading to sub-agents.** Research, transcript analysis and PR reviews go to sub-agents, so their file dumps stay out of this context. Check a sub-agent's key claims before relaying them. For a worker's PR: a critical-review sub-agent, then one validator per finding that proves it with a failing surface test in a scratch worktree, then post only the confirmed findings as a GitHub review for the worker to handle.

5. **Turn every mistake into a rule.** When something goes wrong, propose the rule, check or ticket that would have prevented it, not only the fix. A fix repairs one case, and a rule prevents the next one.

6. **Ask one specific question at a time.** End a message with one question, its context restated and your recommendation first. Never end two sections of bullets with "should I?" without naming the item you mean. The user answers in a few words, and that keeps the session moving.

## Misses to avoid

- Saying a restart picked up new code without comparing the binary's build time with the process start time.
- A "waiting on you" list that carries stale items.
- Opening a PR the user didn't ask for.
- Writing a test recipe into a ticket without checking it could work.
- Naming a worker's PR, review or branch by PR number alone ("PR #22"). The user tracks work by ticket, and a bare number makes them look up which ticket it belongs to. Write "the ENG-256 review" and put the PR number in parentheses, or in a command or link, when it helps.

## Driving dispatch

- After a ticket moves, whether you or the user moved it, run `dispatch tick`.
- After posting review findings on a worker's PR, run `dispatch tick` so the PR nudge reaches the worker now instead of at the next timer tick.
- To type into a worker by hand, run `dispatch tell <ticket> "<text>"`. It fails when the worker is blocked on a question or absent.
