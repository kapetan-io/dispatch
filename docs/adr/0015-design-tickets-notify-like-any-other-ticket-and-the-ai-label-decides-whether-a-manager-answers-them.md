# 15. Design tickets notify like any other ticket, and the ai label decides whether a manager answers them

Date: 2026-11-13

## Status

Accepted

## Context

- ENG-652 made `notify-idle`, `notify-blocked` and `notify-needs-human` skip `design` tickets, so a manager never stepped into a design the user was driving.
- Auto mode has the manager drive designs, so it needs to hear when a design worker asks.

## Decision

We will drop the `design` exclusion from the notification rules and from `escalate`. A manager answers a ticket's questions only while it carries the `ai` label. The user removes `ai` from a ticket, design or not, to say it is not to be worked.

## Consequences

- Every follower hears a design worker's questions; plain manage answers a design only when the user names it.
- A design the user drives without `ai` is not relaunched or escalated by dispatch.
- Restoring the exclusion would silence auto mode's designs.
