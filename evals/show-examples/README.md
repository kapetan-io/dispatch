# Show-examples replay

Replays one real miss against rule 5 of `skills/converse/SKILL.md` (ENG-510). In a design session for ENG-506 in the tiger repo, a session proposed moving the body of `tiger changed` into an exported `changed.Run` function and explained it in prose. The user read it as a new subcommand per domain. Only after "explain this proposal with examples please" did the session show the code, and the user agreed at once.

`context.md` holds what that session had read and its first prose message. `prompt.txt` is the user's misreading. The replay sends the prompt with the skill and the context as the system prompt, with no tools.

## Run it

```
go run ./cmd/showeval
```

It runs `claude -p` on Sonnet 5 times per arm, so it spends 10 short replies. `-out <dir>` keeps every reply, `-n` changes the run count.

```
arm      replies with code      avg prose words
current  0 of 5                 200
skill    5 of 5                 224
```

- `current` is the skill with rule 5 swapped for `arms/current.md`, the wording on main before ENG-510.
- `skill` is `skills/converse/SKILL.md` as it stands, so the table it prints changes whenever rule 5 does.

The table above measured `arms/run2.md`, not the rule 5 now in the skill. Two skill-review edits came after that run: "the smallest piece that makes it clear" replaced "the piece that matters", and "Don't describe what the example already shows, and add only the context needed to read it" replaced "The example replaces the prose that would describe it, so leave that prose out". The final wording has not been run.

A reply counts as having code when it holds a fenced block. Prose words are the words outside fenced blocks. No model grades anything.

## Results

`results/run1` ran with a bug that put the old wording in both arms, so it measures noise. It gave 0 of 5 and 1 of 5 replies with code, at 224 and 220 prose words.

`results/run2` is the run above, with the rule 5 in `arms/run2.md`. The replies with code went from 0 of 5 to 5 of 5. Prose words went from 200 to 224, so the rule does not make the reply shorter here. The user's message asks "thoughts?" on a different proposal, so every reply spends words answering that before it shows the code.
