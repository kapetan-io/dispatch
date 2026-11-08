# 12. A session's address is its session id, looked up in Claude Code's session list at each send

Date: 2026-11-07

## Status

Accepted

## Context

- Dispatch delivers follower messages through `harness.Harness.Deliver`, addressed by the session's messaging socket path. Nudges were typed into the pane.
- A Claude session's socket is named for its pid (`/tmp/cc-socks/<pid>.sock`), so a restart or resume keeps the session id and moves the socket.
- Claude Code keeps one undocumented file per running session, `~/.claude/sessions/<pid>.json`, holding `sessionId` and `messagingSocketPath`. It removes the file on exit and writes a new one under the same id on resume.
- herdr already reports each worker's session id on every host (`world.Worker.Session`).
- Storing the socket that hooks report was weighed. If dispatch is down while a worker restarts, the stored socket goes stale, and an idle worker fires no hook to refresh it.

## Decision

We will address every session, worker and follower alike, by its session id. The Claude Code harness finds the session's current socket in Claude's session list each time it sends, picking the newest entry for the id. Dispatch stores no address.

## Consequences

- No stored address can go stale, and a reused pid can't misroute a message.
- Dispatch depends on an undocumented Claude Code file, read in one adapter and pinned by drift tests. A format change breaks every delivery in one place.
- A session whose id changes in a running process (`/clear`) is unreachable on hosts that keep the launch-time id, until ENG-684 lands.
- A follower under a different `CLAUDE_CONFIG_DIR` from dispatch reads as not open.
