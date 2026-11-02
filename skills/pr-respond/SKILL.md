---
name: pr-respond
description: Respond to what a build worker's PR raised (review comments, questions, failing checks) and follow it to merge. Use when dispatch nudges with `/dispatch:pr-respond`, when the build skill hands off to it after creating the PR, or when asked to respond to reviews on the PR.
---

# Respond to the PR

A ticket whose id starts with `LOC-` is a local ticket. Wherever this skill says to comment on or move the ticket, run `"${DISPATCH_BIN:-dispatch}" ticket comment LOC-<n> "<text>"` or `"${DISPATCH_BIN:-dispatch}" ticket move LOC-<n> "<status>"` instead of using the Linear tools. A flaky check's Backlog ticket is a Linear ticket, even when this one is local.

Each time you start, run `"${DISPATCH_BIN:-dispatch}" repo-settings` and read `ignore_comment_authors`, `ignore_optional_checks` and `approval_checks`. Act on nothing by a listed login, matched ignoring case: no fix, reply or resolve.

Name the repo on every `gh` command (`--repo owner/name`, or a `repos/owner/name/...` path for `gh api`; the queries below pass `owner` and `name` themselves), because in a clone with two remotes `gh` picks the wrong one. Take `owner/name` from the PR's own URL.

Read all the feedback. Conversation comments, review bodies and comments on lines of code (review threads) are three separate lists, and `gh pr view --comments` misses the code comments. This query returns all three, with the thread ids:

```sh
gh api graphql -F owner=OWNER -F name=NAME -F number=N -f query='
query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){
  comments(last:100){nodes{author{login} body createdAt}}
  reviews(last:100){nodes{author{login} state body createdAt}}
  reviewThreads(first:100){nodes{id isResolved path line comments(first:50){nodes{author{login} body createdAt}}}}}}}'
```

Resolve a thread once you have fixed or answered it, since dispatch never reports the PR ready while one is open:

```sh
gh api graphql -F id=THREAD_ID -f query='mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{isResolved}}}'
```

## A review comment

Treat every review comment as a claim to check with evidence: a failing test or a trace through the code. List each claim the finding rests on, including the claims behind your proposed fix, and mark each confirmed, not confirmed or not checked. A page summarizer's output (such as WebFetch) is not opening the source, so a claim checked only through one is not checked.

A finding is confirmed only when every claim on its list is confirmed. Until then, don't tell the user it is real, change the PR body, or move the ticket because of it (an escalation to Needs Human below is not that). Check the not-checked claims first, and leave one not checked only after you tried and couldn't.

Fix and reply to a confirmed finding, resolve it, and record the fix as a divergence when it changes something the design describes. Reply to an unconfirmed finding from a bot with your evidence, saying which claims you couldn't check, and resolve it. Reply to an unconfirmed finding from a person with your evidence, ask the user in chat whether to hold your ground, and move the ticket to Needs Human.
## A question

A question is not a claim. Answer a question about what the code does on the PR, with evidence. If the answer is a call the Rules reserve for the user, move the ticket to Needs Human, ask in chat, and post nothing on the PR. When the user answers in chat, write the answer on the ticket, move the ticket back to In Progress, and reply to the question on the PR quoting their answer. Any other question is yours to answer on the PR. Resolve a question's thread once you have answered it.

## A failing check

When `ignore_optional_checks` is true, run `gh pr checks --repo owner/name --required` and fix only the failures it lists. Otherwise run `gh pr checks --repo owner/name` and fix every real failure. A check listed in `approval_checks` passes only after a person approves, so never fix it and never count it as a failure. A flaky check, required or not, gets its own Backlog ticket and a re-run.

A check that fails a test that passed before your change falls under the build skill's rule that an existing passing test is a contract: the code is wrong, so fix the code, and don't edit, skip or delete the test unless the user explicitly changed that behavior.
