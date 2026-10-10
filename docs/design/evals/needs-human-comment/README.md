# Needs-human comment eval

Tests whether the build skill's wording makes a worker open every comment that needs the user with an `## In plain English` section and a concrete example, and put the state and evidence under `## Details`. Made for ENG-258 with the `anthropic-skills:skill-creator` method (frozen test prompts, an old-wording baseline, a pass check per run), but driven by `cmd/commenteval` because this repo writes its tooling in Go.

## Arms

- `arms/current.md` is the whole build skill as it was on main before ENG-258. It is the baseline. If it passes, the check is broken.
- The wording under test is `skills/build/SKILL.md` itself.

`unknown-keys` and `max-range` quote the blueprint and config as of commit `2700f6c`, where the blueprint's example config had keys the config struct lacked. Main has since fixed that.

## Scenarios

Four situations, one per comment kind the skill names. Each ends with the worker moving the ticket to Needs Human and writing the comment. No tools are available, so the reply is the comment.

| Scenario | Comment kind | Source |
|---|---|---|
| `unknown-keys` | question about a product call | ENG-248's `unknown-keys` case |
| `max-range` | question about a public contract | ENG-248's `max-range` case |
| `person-review` | unconfirmed review claim from a person | made for this eval |
| `stop-patching` | the design is wrong | made for this eval |

## Check, per reply

- `opens`: the first line of the comment is the `## In plain English` heading.
- `example`: a code block sits inside that section.
- `details`: a `## Details` heading follows.

Whether the explanation reads well to a newcomer is judged by reading replies, not scored.

## Run

```
go run ./cmd/commenteval run -arm skills/build/SKILL.md -scenarios docs/design/evals/needs-human-comment/scenarios -out /tmp/run -n 5
go run ./cmd/commenteval report /tmp/run
```

Workers run on `claude-sonnet-5-5`, the model dispatch gives build workers. 4 scenarios x 5 runs = 20 runs per arm, 40 per attempt, inside the 45-run budget.

## Results

| Run | opens | example | details |
|---|---|---|---|
| `results/current` (old wording) | 0 of 20 | 0 | 0 |
| `results/final` (adopted) | 20 of 20 | 20 | 20 |

An earlier draft said "start with" the heading, and one of its 20 replies put a status line above it. The adopted wording says the heading is the first line, ahead of any status line, and asks for the example in a fenced code block, which is what the `example` check looks for.
