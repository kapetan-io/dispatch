---
name: pr-description
description: "Write a pull request description that conveys the PR's intent, the surfaces it adds or changes, the behavior a user or caller now sees, and the concerns and risks of the change as written. Use when writing or updating a PR description. Not for opening the PR."
---

# PR description

A description tells a reviewer what they are approving: why the change exists, what a consumer now sees differently, and what could go wrong. It is not a record of the work. The commits hold the history, so leave out what you did, the order you did it in, test counts, and review rounds.

## Intent

Explain what the PR does and why, in the style of `dispatch:bro`, for a reviewer who saw none of the work. Leave the new contract to Surfaces and Behavior changes.

## Surfaces

A surface is what a consumer uses, as `dispatch:surface-testing` defines it: HTTP and gRPC endpoints, CLI commands, the exported API of a package others import, and stored data others read, such as schemas and events. Show each surface this PR adds or changes as the thing itself:

- updates to surface calls: its declaration as it is on the branch
- an endpoint: method, path, and an example request and response as JSON
- a CLI command: the invocation and its output
- a schema: the SQL or definition

A reviewer approves the contract, and prose about a contract hides the names and shapes they need to question. Copy each one from the branch, not from the ticket or design. Internal functions aren't surfaces, so leave them out.

## Behavior changes

What a user or caller now sees differently, before and after, shown with an example rather than described: a request and its new response, or before-and-after screenshots for a UI change.

## Concerns and risks

What could go wrong with the change as written, and what the reviewer should judge: a breaking change to a caller, a migration that can't be undone, a guess the author made. Say what happens on merge when it isn't obvious. Write "None." when there are none.

## Output format

Write the body as exactly these four headings, in this order, every time. A reviewer who finds the same headings in every PR can jump to the one they came for, and an empty section that says so tells them you checked.

````markdown
## Intent
{{{What the PR does and why, in plain language, with an example where it helps. End with the ticket link when there is one.}}}

## Surfaces
{{{One entry per surface this PR adds or changes: a bold label (`**Endpoint**`, `**Type**`, `**Command**`, `**Schema**`, `**Event**`) and its name}}}

## Behavior changes
{{{Before and after, as an example per change: request and response, command and output, or screenshots.}}}

## Concerns and risks
{{{A bullet per concern: what could go wrong, and what the reviewer should judge. State what happens on merge when it isn't obvious.}}}
````
