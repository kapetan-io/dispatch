# The startup screen

`dispatch run` prints what it is doing while it starts and what its first read of the world found. A slow Linear, GitHub, multiplexer or treehouse shows up as a start that is waiting on that source, not as a blank terminal.

## What it shows

In a terminal the screen looks like this, with the logo in a cyan to magenta fade:

```
  ██████╗ ██╗███████╗██████╗  █████╗ ████████╗ ██████╗██╗  ██╗
  ...
  ━━━━━━━━━━━●━━━━━━━━━━━━━━━●━━━━━━━━━━━━━━━━━━●━━━━━━━━━━━━▶
  event-driven reconcile loop for agents

 CONFIG   ~/.config/dispatch/config.toml
 RULES    27 loaded
 READING  linear, local, rex, github, treehouse
 HISTORY  42 receipts from events-20261115T221521Z.jsonl

 SOURCES  🟢 linear     team ENG, label ai, 3 tickets
          🟢 local      1 ticket
          🟢 rex        3 workspaces, 3 agents
          🔴 github     2 pull requests on tracked tickets
             could not read the pull request of ENG-725
          🟢 treehouse  10 trees, 4 leased
          ⚪ herdr      off

 REPOS    🟢 dispatch  🟢 gubernator

 ▶ watching 4 tickets   next resync 16:20:21
          💤 ENG-716  Keep dispatch's worker records in its own database
          🔴 ENG-535  Signal a waiting worker without the Needs Human status
          🟢 ENG-725  Keep dispatch working during a slow Linear write
             LOC-3  Todo  Retry webhooks on 502
```

The lines print in the order dispatch learns them, and nothing is redrawn.

- **Before anything is read.** The logo and `CONFIG` print before the config loads, `RULES` once the rules load, and `READING` names the sources dispatch is about to read, before it runs the Linear API key command or asks Linear who owns the key. A start that stops here, such as on a bad config key or a key Linear refuses, shows these lines and then the error.
- **After the first read.** `HISTORY` says how many ledger receipts the start restored and from which history file, so a restart shows it picked its history back up. The plain form has no `HISTORY` line, because its `start:` line says the same. `SOURCES` gives each source's result. 🟢 means it read, ⚪ means this config doesn't use it, and 🔴 means the read failed, with dispatch's error under it in red. Linear, GitHub and treehouse have no switch and are always read. Local tickets are on when `[plugins.local]` is written. Exactly one of herdr and Rex runs the workers. GitHub's count is the pull requests of tracked tickets, not every open pull request. A failed read never stops the start. The `dispatch: while reading ...` lines dispatch writes to stderr for some failures, such as a failed Linear read, still print as before.
- **`REPOS`** lists every `[repos.<label>]` table, 🔴 when its treehouse pool could not be read.
- **The tracked tickets** close the screen, with the time the next resync is due. A ticket whose worker has a workspace shows its label emoji, from [worker-states.md](worker-states.md). A ticket with no workspace shows its status.

## When it is plain

When stdout is not a terminal, such as a log file or a pipe, the screen has no logo, no emoji and no color codes, and ends with the `start:` line dispatch has always printed:

```
CONFIG   /Users/you/.config/dispatch/config.toml
RULES    27 loaded
READING  linear, herdr, github, treehouse

SOURCES  linear     team ENG, label ai, 2 tickets
         local      off
         herdr      failed
         herdr unread: herdr is down
         ...
WATCHING 2 tickets   next resync 16:20:21
         ENG-716  In Progress  Keep dispatch's worker records in its own database
16:15:21 start: 2 tracked tickets, 27 rules, ledger 0 receipts from no earlier history
```

In a terminal with `NO_COLOR` set to anything but an empty string, the screen keeps the logo and emoji and drops the color.
