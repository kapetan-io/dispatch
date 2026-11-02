# 8. Ticket systems do not link to each other

Date: 2026-11-01

## Status

Accepted

## Context

- Dispatch reads tickets from two ticket systems, Linear and its own local tickets. Links (parent, blockers, children) drive promotion, blocker holds and closing a parent.
- Within Linear, blockers and children come from any team (`internal/linear/linear.go`), so a link across systems looks like a small step.
- The owner decided: "different ticket systems cannot depend on tickets in other systems. I guess I don't see why they couldn't in the future but right now we definitely don't want to support that."
- A cross-system link would make one system's facts depend on another system's read, so a failed Linear read would hold local tickets again, which per-system unknown exists to avoid.

## Decision

- A local ticket's parent, blockers and children are local tickets. The store refuses a link to any id outside the database.
- A Linear read that returns a `LOC-` id fails, so no Linear ticket links to a local one.

## Consequences

- Unknown stays per system. A failed read of one system never holds the other's tickets.
- Work that spans systems is linked by hand, through a comment or an attribute that names the other ticket.
- Allowing cross-system links later means a new ADR that says how unknown crosses the link.
