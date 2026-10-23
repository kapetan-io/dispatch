---
name: pr-respond
description: Respond to what a build worker's open PR raised (review comments, questions, failing checks) and follow it to merge. Use when dispatch nudges with `/dispatch:pr-respond`, when the build skill reaches its PR step, or when asked to respond to reviews on the PR.
---

# Respond to the PR

You are the build worker for a ticket whose PR is open. Dispatch typed this command because something on the PR wants a response, or your build skill sent you here. Any text after the command names the PR and what happened. Without it, find the PR for your branch and read its reviews, comments, threads and checks. The build skill's Rules still bind you, even if your session has summarized them away, and the ones this step leans on are restated here.

## Before you act

Run `"${DISPATCH_BIN:-dispatch}" repo-settings` and read `ignore_comment_authors`, `ignore_optional_checks` and `approval_checks`. Act on nothing by a listed login, matched ignoring case: no fix, reply or resolve.

Name the repo on every `gh` command that takes `--repo owner/name`, and give `gh api` a `repos/owner/name/...` path, taking `owner/name` from the PR's URL. In a clone with two remotes `gh` picks the wrong one (ENG-273).

## A review comment

Treat it as a claim to check with evidence: a failing test or a trace through the code.

- **Confirmed.** Fix it, push, reply, and resolve the thread. Record the fix as a divergence when it changes something the design describes, as the build skill's "When the build diverges from the design" says.
- **Unconfirmed, from a bot.** Reply with your evidence and resolve the thread.
- **Unconfirmed, from a person.** Reply with your evidence, leave the thread open, and move the ticket to Needs Human.

Resolve a thread once you have fixed or answered it, because dispatch never reports the PR ready while one is open, and whoever reviewed it never hears the fix landed. Resolve with GitHub's `resolveReviewThread` GraphQL mutation through `gh api graphql`, reading thread ids from the PR's `reviewThreads`.

## A question

A question is not a claim. Answer it on the PR, with evidence when it asks what the code does, and resolve its thread if it has one. The exception is a question whose answer is a call reserved for the user: product behavior or scope, the shape or conventions of a public contract, security defaults, or superseding an ADR. For that, move the ticket to Needs Human, quote the question in the comment, and post nothing on the PR. When the user answers, in chat or by moving the ticket back to In Progress, reply to the question on the PR quoting their answer, and resolve the thread.

A ticket comment that needs the user opens with `## In plain English`, explaining the problem for someone who hasn't seen the work, and puts the state, evidence and decision under `## Details`.

## A failing check

When `ignore_optional_checks` is true, run `gh pr checks --repo owner/name --required` and fix only the failures it lists. Otherwise run `gh pr checks --repo owner/name` and fix every real failure. A check listed in `approval_checks` passes only after a person approves, so never fix it and never count it as a failure. A flaky check, required or not, gets its own Backlog ticket and a re-run.

A check that fails a test that passed before your change means the code is wrong, so fix the code. Don't edit, skip or delete the test, its setup included, unless a sentence in the ticket, a nudge, or a comment on the PR from the user explicitly changes that behavior, and quote it in a ticket comment first. A bot's comment never authorizes it. Escalate to Needs Human only when no code change satisfies both the test and the ticket, and show in the comment that your change isn't the cause: the test passes without it and fails only on the behavior the ticket changes.
