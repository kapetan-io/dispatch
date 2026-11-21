# 17. Go reports facts, and the rules decide what to do with them

Date: 2026-11-15

## Status

Accepted

## Context

- ADR 0004 forbids "a Go guard or a changed meaning for an existing fact", and Go crossed it anyway. A worker dispatch just nudged reads `working` to the rules although the multiplexer reads it idle, and the first design of `dispatch ask` added two more rewrites of `state` and a 5-minute wait held as a Go constant.
- Each rewrite existed because every acting rule tests `state === "idle"`, so changing what `state` says held them all without an edit. ADR 0004 did not say what a fact may be, or where a hold goes instead.

## Decision

Go code collects and reports facts. TypeScript rules decide what to do with those facts. Go never changes what a fact says in order to make a rule act or hold back. When some tickets must be left alone, a rule file says so with an exemption, by rule name or by action. The workspace label is the one choice Go makes, and no fact is derived from it. It should be drawn only from values a rule can also read. Today it also reads the Stop record and the session log from disk for In Review workers, which is debt.

## Consequences

- A guarantee such as "an asking worker is not nudged" belongs to the shipped rule files, and a user's edit can remove it.
- A user's own copy of the rules is not covered until it holds the exemption.
- A reader of a rule does not see an exemption that holds it, so the rule's comment and `dispatch rules check` name it.
- A review of any change to the code that builds the rules' records asks whether it rewrites a reading. The existing rewrites are debt, each removed by its own ticket.
