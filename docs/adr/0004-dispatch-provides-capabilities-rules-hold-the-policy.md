# 4. Dispatch provides capabilities, rules hold the policy

Date: 2026-10-29

## Status

Accepted

## Context

- Users own their rules (`docs/rules.md`), and the rules make every decision. Go's own checks today only confirm that the world still matches what a rule saw (the executor's "changed in Linear since the cycle" refusal, a merge refused when the head moved).
- The first stacked tickets design put stacking policy in Go: the executor refused to merge a stacked branch, and `pr.autoMerge`, `pr.ready` and `pr.needsWork` changed meaning for stacked branches, "so a user's own merge rule can't skip it". A user who wanted different behavior could not get it by editing rules.
- The user decided that dispatch holds no opinion on such things. Anyone who wants a behavior should be able to build it, and anyone who doesn't should be able to write rules that make it impossible.

## Decision

We will add capabilities to dispatch (facts rules can read, actions and options rules can queue, declarations such as `exempt`) and keep policy in rule files. When a shipped rule's behavior doesn't fit a new feature, the answer is a capability that lets a rule file change it, not a Go guard or a changed meaning for an existing fact. A feature's policy ships as an example rule file that users import, edit or replace.

## Consequences

- Without the import, nothing changes for any user, including in failure modes.
- An example can need several exemptions and near-copies of shipped rules where a Go guard would be a few lines. Scenarios pin each copy to its original so they don't drift silently.
- A guarantee such as "a stacked PR never merges into its stack parent's branch" belongs to the example rules, not to dispatch, and a user's edit can remove it.
- Go keeps its staleness and safety checks. Those confirm a rule's view of the world, and choose nothing for the user.
