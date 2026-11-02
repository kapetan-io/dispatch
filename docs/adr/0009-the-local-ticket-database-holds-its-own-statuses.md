# 9. The local ticket database holds its own statuses

Date: 2026-11-01

## Status

Accepted

## Context

- Linear's statuses get their jobs from `[plugins.linear.statuses]` in config, because the owner does not always have admin control over a Linear workspace.
- Dispatch owns the local database fully. The owner decided that "the database is the Configuration and the ticketing database", so the statuses move with the file.
- The owner decided that defaults are written into a brand-new database "and then that's it. It's never, ever updated again, Even if the default statuses in our code change."

## Decision

- Each status in the database carries its name and its jobs. No config table maps local statuses.
- The default statuses, named as in Linear today and matched case-insensitively, are seeded once into a database that has no statuses. Dispatch never changes the set afterwards.

## Consequences

- A change to the default statuses in code reaches only new databases. Existing ones keep theirs.
- Changing a database's statuses needs a status-editing command, a follow-up to this feature.
- History records the local statuses in each segment's header, so replay reads the statuses that were in force.
