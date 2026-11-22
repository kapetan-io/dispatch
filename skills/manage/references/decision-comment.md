# The decision comment

The manager posts one `## Decision` comment on the ticket that asked, per turn. Build and pr-respond match only the heading, the line after it and the `Turn:` line. The comment opens with the marker line `dispatch:dispatch` asks of every manage post.

```
Posted by a dispatch manage session.
## Decision
Follows internal/email/retry.go:41: retry a failed webhook send for 24 hours, then drop it and log.
Turn: 2026-11-14T06:40:00.123Z
Settled by: question arena

### Question as the arena received it
> ...
### Worker's closing message
> ...
### Reasons
- ...
```

- **First line under the heading.** Starts `Follows <file:line, ADR or the ticket's intent>:`, `Departs from <file:line or the ticket's intent>:` or `No precedent:` when nothing in the repo applies, then the decision in a sentence. Pull requests sort on it, departures first.
- **`Turn:`** is the time `dispatch turn` printed, as it printed it.
- **`Settled by:`** is `question arena`, `question arena, approval` or `the manager` (decided without an arena).
- **Question section.** The restater's `question.md` exactly. An approval's `question.md` starts `Approval:` and holds the proposal and the ticket's intent. When the restater could not remove the worker's lean after two rewrites, add `, question still names the options` to `Settled by:`.
- **Closing message section.** The closing message from `turn.json` as it is, even though the arena never saw it.
- **A decision the user beat** is retitled `## Decision (not sent)`.
