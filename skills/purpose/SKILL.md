---
name: purpose
description: "Draft the Purpose section of a docs/CONTEXT.md: what the problem domain it describes is for, the tradeoffs it holds to, and what each capability gives its user. Use when asked to write or refresh a Purpose section, or before unattended work in a domain that has none."
---

# Purpose

Draft the Purpose section of one `docs/CONTEXT.md`. An agent working alone reads it to settle a question the way the owner would. Code says what the domain does and a decision record says what was chosen. Neither says what the domain is for, so an agent that has only those reopens settled choices or grows the domain past its job.

The owner corrects the draft, since only the owner knows the purpose. Your job is a draft close enough that they change a few words.

## Scope

You are given the path of a `docs/CONTEXT.md`, or of the `docs/` directory one will go in. The directory that holds that `docs/` is the domain: `services/billing/docs/CONTEXT.md` describes billing and nothing else in the repo. Read outside it only to learn who uses the domain and what binds it (a repo-wide decision record, a caller). Write nothing about a sibling domain. When the `docs/` sits at the repo root, the whole repo is the domain. Packages under the domain that its user reaches are part of it. Tools that exist only for the people who maintain it are not.

The user is whoever the domain serves at its outer edge. For an application that is a person. For a library or a service it is the code that calls it and the developer who wrote that code. Find out which before you read further, and write the section from that side.

## What to write

Three parts, in this order.

1. **The opening paragraph.** Three or four short sentences, under 70 words: who the user is and what they can do with the domain that they couldn't without it, what the domain takes off their hands, and what it leaves with them. Write it about the user in the third person, not about the software. Name no package, file or internal part, and list no steps: the capabilities carry those. A term from the domain's glossary is fine.

2. **Tradeoffs.** Zero to five lines under a `### Tradeoffs` heading, as many as pass the tests below and no more. Each is a bold phrase, then one sentence for the choice and one for the cost, saying, in whatever words fit the domain, what it chooses and what it gives up for that. A tradeoff is a value that explains several decisions. It is not one of the decisions. Give one cost, the largest, in the user's terms (what they wait for, lose or must do themselves), and leave the examples and the smaller costs to the evidence list. Write none if none passes the tests below, and say so in your report. A domain with one small job may have none.

3. **Capabilities.** One entry per thing the user can do or get, three to eight in all, under a `### Capabilities` heading. Each is a bold name and one or two sentences from the user's side: what they do or call, and what they then see or get back. Use the words the glossary and the surface already use.

## How to find it

Start three sub-agents in one message, each on a fresh context, and give each this skill's Scope and What to write sections, the path, and one of the starting points below. Each reads from its starting point outward until it can draft all three parts, then returns its draft with the place that shows each claim (file and line, or document and heading). Tell them to edit nothing. Do not tell any of them what you expect to find. Ask all three one more question, to answer from their own evidence: what does the domain spend freely, and what does it spend that to save? Every design treats something as cheap (the machine's time, memory, the user's time, the user's attention, bytes on the wire, lines of code) and something as scarce.

- **From what the user is told.** The README, the user docs, the examples, the help text or the documented API. What does the user do, and what is done for them? What are they promised?
- **From what was turned down.** The decision records, design notes, the change history and comments that explain why. For each choice, what was rejected, and what would the rejected choice have been better at (less code, faster, simpler to explain)? What did the domain give that up for?
- **From the edges of the code.** The surface (commands, endpoints, exported functions, screens), then what the code does where it could go either way: a failed read, a missing or malformed input, two sources that disagree, a place it stops, refuses or returns an error instead of going on. Also what the user can change without changing the domain's code (settings, options, plugins, hooks, callbacks) and what it keeps for itself.

## Build the section

Read all three drafts end to end before writing.

- **Opening paragraph.** Take the draft that best separates what the user does from what is done for them, and fix it with what the others saw.
- **Capabilities.** Take the union, then merge entries that serve one want. Group by what the user wants, not by how the code is split.
- **Tradeoffs.** Put the three answers to the spend-and-save question side by side. Where two or more agree, that is your first candidate, written as what the domain spends freely and what it saves by doing so. Keep it a line of its own and name both halves in it. Never fold it into another candidate. A candidate that is one way the domain does this spending, and whose cost to the user is the same cost, becomes evidence for it. One with a cost of its own stays a candidate and is tested like any other. Then list every candidate from every draft. Then read your opening paragraph again: where it puts the line between what the user does and what is done for them is a choice too. If the domain gives something up to put the line there, add that as a candidate, even though it repeats the paragraph. Merge two candidates only when one line can state both without losing either one's cost. Candidates with different costs are different tradeoffs, even when they share evidence. A merged line names the larger cost. Then test each.

Keep a tradeoff only if all of these hold.

- Two drafts found it from different starting points, or one draft found it and you checked its evidence yourself.
- At least three separate places show it. Two places are separate when one could change and the other would still stand. A doc, its help text and the code it describes are one place when they came from one change.
- The opposite is something a reasonable design would choose. "Prefers correct behavior" fails, because nobody prefers the opposite.
- It rules something out. Name a change that works, that a reviewer who had not read this line could approve, and that this line says no to. If you can't, drop it.
- It does not rest on one decision record alone. A value that a record states and that other decisions, docs or code also show passes. A choice only that record shows stays in the record.
- The user would recognize it as what the domain is like to use. A candidate that names a timer, a retry, a startup check or another mechanism is a decision. Ask what the user gets from that mechanism and state that instead. If the answer is a tradeoff you already have, add the mechanism to its evidence and drop the candidate. Check each bold phrase last: one that names a part of the domain (a clock, a file, a setting, a status) and not something the user gets or gives up has failed this test.
- It is about what the domain does, not how its code is built or tested.

Order what is left by how many separate decisions each explains, most first, and keep at most five. Don't fill the five: a line the tests only just passed costs the reader more than it gives. Evidence against a tradeoff does not drop it: list it for the owner, who decides whether the line or the exception is wrong.

## Output

Write the section as it would appear in the `CONTEXT.md`, under `## Purpose`. Below it, outside the section, list for each tradeoff the places that show it, any evidence against it, which starting points found it, and the change it rules out. Then list each candidate you dropped or merged away, with the test or merge that removed it. The owner uses that list to check the draft, and it is deleted once they approve the section.

Do not quote a decision record's number or title inside the section. Do not describe internals: they change, and an agent can read them from the code when it needs them.
