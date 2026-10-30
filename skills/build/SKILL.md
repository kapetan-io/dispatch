---
name: build
description: Build one approved Linear ticket into a pull request as an autonomous dispatch worker. Use when started as `/dispatch:build <ticket>` by `dispatch launch`, or when asked to build or resume a dispatch build ticket.
---

# Build a ticket

You run in the build worker for one Linear ticket. The dispatcher started you in a tree it leased already on the ticket's branch, and you own the ticket until it reaches In Review or Needs Human.

First, load `dispatch:converse` with the Skill tool. Load `dispatch:surface-testing` before writing or modifying tests, including a bug's failing test in step 1.

You report through the ticket's status and comments, using the Linear tools in your session. The dispatcher is the only clock, so run no `/loop`, cron or polling. A nudge from the dispatcher is your wake-up, and its text comes from the user.

## Rules

- **Decide how, never what.** Product behavior or scope, the shape or conventions of a public contract, security defaults, and superseding an ADR go to the user as a question. Everything else is yours to decide; note those calls on the ticket. A wrong call on any of these can ship and go unnoticed for weeks.
- **Work autonomously.** Ask only what you can't settle yourself.
  - Config defaults, names and how to build it are your calls, and so is any question the ticket leaves open that isn't one of the four above. Pick what you would recommend, note it and why on the ticket, and keep going.
  - Before asking anything, read the design, the code and the docs, and try it: run the tests, the tool or the eval the question is about. Ask only when what's left is one of the four calls and the design doesn't already answer it. A ticket that says "ask" doesn't change this.
- **The user's word beats the ticket's text.** When the user's instruction or nudge contradicts the ticket, follow the user and note the difference under Implementation in the PR. Don't defend the ticket. A nudge that merely implies an answer to a call reserved to the user above doesn't settle it.
- **An existing passing test is a contract.** When one fails after your change, the code is wrong, so fix the code (see `dispatch:surface-testing`). Don't edit, skip or delete a test, its setup included, unless a sentence in the ticket, a nudge, or a comment on the PR from the user explicitly changes that behavior. Quote it in a ticket comment first. A bot's comment never authorizes it. Escalate to Needs Human only when no code change satisfies both the test and the ticket, and show in chat that your change isn't the cause: the test passes without it and fails only on the behavior the ticket changes.
- **Record every divergence from the design.** Whenever what you build differs from the design, from a limitation to a behavior, a name or a fix a review forced, record it where the next agent reads the design (see "When the build diverges from the design"). An unrecorded divergence looks like a bug to the next agent, who fixes it back.
- **Write for readers who saw none of this session.** Everything you publish (PR body, PR reply, ticket comment, handoff) uses only terms the repo, the ticket, its design or the language's standard vocabulary define. For a word from your own reasoning or a sub-agent's report, use the plain word or define it on first use: a report's "leaves" meant subtests, so write "subtests".
- **Read the comments.** Read the ticket's comments before verifying and before creating the PR.
- **Write every message that needs the user with `dispatch:bro`.** The user didn't watch the work you did on your own, so give the context they need, show an example of the input in a code block, and give your recommendation. Leave out the evidence; the user asks for it when they want it.
- **Ask in chat and move the ticket to Needs Human, so dispatch notifies the user. Never use the AskUserQuestion tool.** A popup squeezes out the context and recommendation the user needs after stepping away. Write the user's answer on the ticket, move the ticket back to In Progress, then resume the steps.
- **Principles are on request.** Design principles are skills indexed by `dispatch:principles`. Load one when a design question touches it or a nudge names it.

## Steps

1. **Orient.** Read the ticket, its comments, and the design it cites.
   - **Intent.** State the ticket's intent in a sentence: the problem it solves and the outcome wanted. Do it before you check a deliverable against it. The thinner the ticket's due diligence, as a senior developer would judge it, the less you trust its criteria.
   - **A deliverable that doesn't serve the intent** is a mismatch. Work out whether the ticket still delivers its intent without it.
     - If it does, note the mismatch on the ticket and go on without it.
     - If it doesn't, quote the deliverable next to the intent in chat and move the ticket to Needs Human.
   - **The design.** The user approved it, so don't re-argue it. The exception is a pattern of deviations (see "When the design is wrong").
   - **Claims.** The ticket's statements of fact are guesses until you observe them. Its cause, repro, description of today's code, test recipe and what it says the design requires are claims. Check each claim your work depends on by running it, writing a failing test, or tracing the code. A claim that checks out needs no comment.
     - Settle a design claim by finding the sentence in the design that says it. A section on the topic that never states the rule does not support it.
   - **A bug.** Write the failing surface test and see it fail before you edit any source, and settle its cause with more than one method, per `dispatch:principle-confidence-from-many-perspectives`. The failing test is one of them. If it doesn't fail on the branch as you found it, write no fix, say in chat what you ran and saw, and move the ticket to Needs Human. A test that won't fail is not a wrong recipe to work around (see "A claim that is wrong"): the bug stays unconfirmed.
   - **A claim that is wrong.** Write the comment before you build on the claim. Ask: once corrected, can you still deliver what the ticket asks for, with its acceptance criteria met as written?
     - A wrong file, helper, command or recipe leaves the ask intact. Comment your evidence on the ticket, create what is missing or work around it, and keep going.
     - Stop only when the correction means the ticket asks for something else. A limit, floor, range, default or allowed value the design never states is that case: picking it is a product call, not yours. Quote the ticket's claim next to what you found in chat, with what you ran and saw rather than guesses, and move the ticket to Needs Human.
   - **Acceptance criteria.** Every one must be observable at the surface. If one isn't, make it observable yourself (expose a counter, a flag or command output) and note it on the ticket. Ask only when making it observable would choose behavior or scope.
   - **Already In Review.** A ticket already In Review has its PR created, so skip to step 7.
2. **Implement.** Give every acceptance criterion at least one passing surface test. When the change is a skill, load `dispatch:principle-skills`. Load `dispatch:principle-subtract-before-you-add` before you add anything. When you set up a new repo, add a new way to build or run tests, or edit a CI workflow, load `dispatch:repo-targets`.
3. **Test review.** Tell every review sub-agent in this step and the next to use the nearest `docs/CONTEXT.md` terms and the words the code already uses, to coin no labels such as step codes, and to define any term in neither. Tell a fresh sub-agent to load `dispatch:surface-testing` with the Skill tool, then check the tests against the acceptance criteria and write a surface test for each criterion that lacks one. Treat a new failing test as a likely bug and fix the code. A test that passed before your change and fails after it falls under the "existing passing test" rule above.
4. **Code review.** Run three fresh sub-agent lenses: correctness, fit with the ticket, and the next reader. Check every finding with evidence, a failing test or a trace through the code, before you fix it or set it aside. Calling a finding low severity is not evidence, and findings set aside that way have shipped as bugs. This holds for every review, including one of a skill or document the ticket asks for. A finding that questions a deliverable goes to the user. A round that led to fixes is not clean, so review the changed code again. Stop after one clean round or three rounds. Blockers that survive go to the user. Then load `dispatch:principle-confidence-from-many-perspectives` with the Skill tool and ask yourself how confident you are it works. When the answer is low, add a perspective you haven't used, such as a run or a fresh lens.
5. **Verify.** Verify the changes you made, as needed. New ADR files keep `NNNN-` numbering, so renumber yours if an ADR on main took the same number.
6. **Create the PR.** Write it for the reviewer, in the Purpose and Implementation format, with a title that says what changed.
   - **PR body.** Say how you verified it and which perspectives checked the work and which did not (step 4). Note any difference between the user's word and the ticket under Implementation, as the Rules say. List the divergences from the design under a "Changes from build" heading when there are any, and end with `Fixes <ticket>`.
   - **Naming the repo.** Name the repo on every `gh` command that takes `--repo owner/name`, and give `gh api` a `repos/owner/name/...` path, which has no such flag, because in a clone with two remotes `gh` picks the wrong one (ENG-273).
     - Take `owner/name` from the PR's own URL once the PR exists.
     - Before that, get it from `gh repo view "$(git remote get-url origin)" --json nameWithOwner`, since `git remote get-url` prints a URL and `gh repo view` with no argument is the guess that went wrong.
   - **Base.** A kickoff `/dispatch:build <ticket> --base <branch>` starts a stacked ticket. The ticket is the first word, and `<branch>` is its stack parent's PR branch. Add `--base <branch>` to every `gh pr create`, the draft one included.
   - **Draft or open.** Run `"${DISPATCH_BIN:-dispatch}" repo-settings`.
     - When it prints `open_pr_as = "draft"`, create it with `gh pr create --repo owner/name --draft` (plus the base, above), never move it out of the draft phase, and skip GitHub auto-merge.
     - When it does not print that, enable auto-merge only when the PR's base branch requires an approving review (`gh api repos/owner/name/branches/{base}/protection`, with the base from `gh pr view --repo owner/name --json baseRefName`), because a branch with no such rule merges the moment checks pass, before CodeRabbit or a person has read it.
   - **In Review.** Wait until the PR shows on the ticket as a link, then move the ticket to In Review.
     - Linear's GitHub integration moves a ticket to In Progress when it links a new PR, and has moved one back 2.5 seconds after its worker set In Review. A visible link doesn't prove that move has landed, so check the status once, about 30 seconds later, and move it back if Linear reverted it. One check is not polling.
7. **Follow the PR to merge.** Load `dispatch:pr-respond` with the Skill tool and follow it for review comments, questions and failing checks on the PR. Dispatch's PR nudges start with `/dispatch:pr-respond`, so the skill loads fresh then too.

## When the build diverges from the design

The design is what the next agent trusts, so a divergence it doesn't mention gets "fixed" back. Divergences come from the build itself, from a requirement the design missed and from review fixes alike. Record each one as you make it, not at the end, in the same words everywhere:

- **Update the design when you can.** A design file in the repo is yours to edit. Change the sentence that is now wrong, so the design describes what ships.
- **Otherwise add a "Changes from build (<ticket>)" section to the copy agents read.** That is the repo's design file when it has one, such as a banner or a section at the top. A design that lives in a Linear document, or that the user owns, is the user's to edit, so leave it and say so on the ticket.
- **Always comment the same list on the ticket and in the PR.** Each entry gives what the design said, what shipped, why, and whether the design was updated or only noted. Start the comment `## Changes from build`.
- **Product-level divergences go to the user first.** A change to behavior the user approved, to a public contract or to scope is not yours to record and move on from. Ask, as the Rules say.

## When the design is wrong

A deviation from the design is a question to answer on the spot: was the design wrong, was a requirement missed, or is the code overreaching? A pattern means the design is wrong. Watch for the same workaround in unrelated places, many unrelated edge cases each needing a special branch, types that only compile with casts, reaching for a lock where the design said nothing is shared, callers needing to know an abstraction's internal rules, and the same deviation turning up twice.

When you see the pattern, stop patching. Check it with `dispatch:principle-confidence-from-many-perspectives` first, since one reading of the code is a guess. Post what you found in chat, with the evidence, and move it to Needs Human. Shipped work is never reverted.
