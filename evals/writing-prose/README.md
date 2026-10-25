# writing-prose eval

Measures whether `skills/writing-prose/SKILL.md` makes a writer leave out true facts the reader doesn't need and keep the section short (ENG-572). Follows `skills/hillclimb/SKILL.md`, with the departures noted in `log.md`.

## Where this stands (2026-10-25)

For the next agent. No wording change was adopted, and the skill is as it was. The graders do not yet track what the user rates well, so do not hillclimb against them as they are. Every run and score is in `log.md`.

- Today's wording scores `key-absent` 0 of 12, `under-cap` 3 of 12 and `closing-organized` 6 of 12 on the baseline (bar 11 of 12). Three wording attempts did not move it. The writer satisfied each rule by relabelling the leftover facts.
- In one-shot runs the skill loads and the model writes once. With the whole loop forced (the groupcache prompt plus "run the skill's whole loop: the writer, the critic and the second writer each as a separate sub-agent"), the critic names the paragraph of leftover facts, but the second writer is only told to cut named clauses, so the paragraph stays. A rewrite step that also cuts a paragraph the critic names under question 1 is untested.
- The user rated ten groupcache sections blind (table in `log.md`). Every one-shot run rated 1, the forced-loop run 3, and the user's own draft 4 and best. That draft ends in the list of defaults, transport, stats and global state that `closing-organized` fails, so that grader does not match the user's taste. The user's reason is a section that starts from one call and tells what happens in order. A pass or fail judge for that voted FAIL on all ten, so it separates nothing.

Where to pick up.
1. Fix the measurement before the wording. Ask a judge for a 1 to 5 score on a walk-through rubric (rubric in `log.md`), called directly and not through `claude plugin eval`, whose judge is pass or fail. Check its ranking against the user's ratings before trusting it. A looser rubric is the first thing to try. The ten rated sections are in commit 5137317 at `evals/writing-prose/results/human-check-sections.md`.
2. Then make the skill's loop run reliably and test the rewrite-step change on a forced-loop run first.
3. The `key-absent` answer keys other than groupcache's are the author's judgement. The forced-loop critic kept "no network call" as needed. Settle with the user whether those clauses stay banned.
4. The $10 ceiling for this ticket is spent (about $9.08). More runs need a new one.

Removed to keep the suite small and recoverable from commit 5137317 `evals/writing-prose/`: the nine scorer fixtures (a hand-written good and bad section per case, plus the user's draft), the forced-loop case, `arms/careless.md` (the control), the per-run result JSONs and the ten rated sections.

## Cases

Each case hands the skill a question and a fact list that holds a few clauses the author judged distracting or an answer to an objection nobody raised (rule 3 of "Working from source material"), and asks for the final section in `section.md`. The prompt never names the skill or the clauses to cut.

| Case | Kind | Held out |
|---|---|---|
| writing-prose-groupcache | library design section | no |
| writing-prose-bloom-filter | concept section | no |
| writing-prose-write-ahead-log | "how it works" section | yes |
| writing-prose-go-defer | language feature section | no |

The answer keys are the author's judgement, not a list the skill names. A reader may disagree that a key clause costs nothing, for example the nil function and Go 1.14 clauses in go-defer. The groupcache facts are the ticket's list, and the skill itself quotes two of its key clauses ("and no network call", "When a peer joins or leaves") as examples, so a reworded skill can pass that case by echoing them. The ticket's answer key names a sentence about the hot cache size bound that its list lacks, so the case adds one fact for it. Held out is every third case. Read its score, never its transcript.

## Graders, per run

- `skill-fired`. The Skill tool loaded `writing-prose`.
- `key-absent`. `section.md` holds none of the case's answer-key clauses, matched as patterns.
- `no-verify`. No `[VERIFY` marker is left. The skill lets a draft carry one and says the user sees text only after the loop.
- `under-cap`. `section.md` has at most the case's word cap: groupcache 330, bloom-filter 290, write-ahead-log 270, go-defer 240. Each is 1.5 times a hand-written good section for the case, rounded up to 10. The cap is a hard edge, so a run a few words over fails on noise.
- `closing-organized`. A model judge reads only the last paragraph and passes it when its sentences build on one another toward one point, and fails a run of separate facts that could be shuffled without loss. Added at the user's direction after a draft ended in a list of defaults, transport, stats and global state.

## What the graders cannot judge

- Whether the skill's writer, critic and rewrite loop ran. In the baseline's visible runs it did not, so the suite scores a one-shot writer under the skill's text.
- Whether the prose reads as a person wrote it. That part stays a human read.
- Whether a kept fact is stated correctly, or a sentence is clear.
- A paraphrase of a banned clause. The key is patterns, so a clause worded differently from the ones already seen passes.
- A section shortened by dropping what the reader needs. The cap rewards brevity and nothing checks coverage.
- Whether a section is organized as the user wants. The model judge for the closing paragraph is noisier than a pattern.

## Pass bar (fixed 2026-10-24, before any run)

1. Baseline or attempt, 3 runs per case, 12 runs. `skill-fired` 3 of 3 on every case. `key-absent`, `no-verify`, `under-cap` and `closing-organized` each at least 2 of 3 on every case and at least 11 of 12 pooled.
2. Known-bad control, 1 run per case, 4 runs: the skill with rule 3 of "Working from source material" replaced by "Include every fact on the list". `key-absent` passes at most 1 of 4. Otherwise the graders are broken and no result is read.
3. Scorer check before any scored run: a hand-written good section passes every content grader and a bad one fails each.

The bar does not change after this commit. Changing it goes to the user. Budget was $10, 3 runs per case. Three runs per case cannot show a failure rate under about 35%.

## How to run

From this directory, with the real `claude` ahead of dispatch's `scripts/shims/claude` on PATH, because the shim rejects the flags the eval passes.

```sh
WORK=$(mktemp -d)
mkdir -p $WORK/plugin
cp -R ../../.claude-plugin $WORK/plugin/.claude-plugin
cp -R ../../skills $WORK/plugin/skills
cp -R cases $WORK/plugin/cases
cd $WORK/plugin
PATH=$HOME/.local/bin:$PATH claude plugin eval . --eval-dir cases --tag suite --runs 3 --ablation none \
  --allow-tools Write Edit --scaffold --judge-model claude-sonnet-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 -j 4 --max-cost-usd 5 --json $WORK/result.json
```

Record the scores in `log.md`.
