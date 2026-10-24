# writing-prose eval

Measures whether `skills/writing-prose/SKILL.md` makes a writer leave out true facts the reader doesn't need and keep the section short (ENG-572). Follows `skills/hillclimb/SKILL.md`. This ticket adds the suite and a baseline. It does not change the skill's wording, so there is no attempt and no confirmation rerun.

Each case hands the skill a question and a fact list that holds a few clauses the skill says to cut, and asks for the final section in `section.md`. The prompt never names the skill or the clauses to cut.

## Cases

| Case | Kind | Held out |
|---|---|---|
| writing-prose-groupcache | library design section | no |
| writing-prose-bloom-filter | concept section | no |
| writing-prose-write-ahead-log | "how it works" section | yes |
| writing-prose-go-defer | language feature section | no |

The groupcache facts are the ticket's list. Its answer key names a sentence about the hot cache size bound that the ticket's list lacks, so the case adds one fact for it. Held out is every third case. Read its score, never its transcript.

## Graders, per run

- `skill-fired`. The Skill tool loaded `writing-prose`.
- `key-absent`. `section.md` holds none of the case's answer-key clauses, matched as patterns.
- `no-verify`. No `[VERIFY` marker is left. The skill lets a draft carry one and says the user sees text only after the loop.
- `under-cap`. `section.md` has at most the case's word cap.

Word caps are 1.5 times the length of the hand-written good section for the case (the scorer check's `-good` file), rounded up to 10. The good section is the target: groupcache 215 words, cap 330. bloom-filter 188, cap 290. write-ahead-log 179, cap 270. go-defer 159, cap 240. Single groupcache runs before this ticket came out at 331, 405 and 360 words, so the cap sits where the unbiased runs start.

## What the graders cannot judge

- Whether the prose reads as a person wrote it. That part stays a human read.
- Whether a kept fact is stated correctly, or a sentence is clear.
- A paraphrase of a banned clause. The key is patterns, so a clause worded differently from the ones already seen passes.
- A section shortened by dropping what the reader needs. The cap rewards brevity and nothing checks coverage.

## Arms

- `skills/writing-prose/SKILL.md` as committed is the wording under test, and the baseline.
- `arms/careless.md` is the known-bad control. The skill with rule 3 of "Working from source material" replaced by "Include every fact on the list".

## Pass bar (fixed 2026-10-24, before any run)

1. Baseline or attempt, 3 runs per case, 12 runs. `skill-fired` 3 of 3 on every case. `key-absent`, `no-verify` and `under-cap` each at least 2 of 3 on every case and at least 11 of 12 pooled.
2. Control, 1 run per case, 4 runs. `key-absent` passes at most 1 of 4. Otherwise the graders are broken and no result is read.
3. Scorer check, once each before any scored run. On the eight `scorer` cases (a hand-written good and bad section per case), every `-good` passes all three content graders and every `-bad` fails each of them. The `-bad` sections hold a banned clause, a `[VERIFY` marker and more words than the cap.

The bar does not change after this commit. Changing it goes to the user.

## Budget

The user capped everything this ticket runs at $10. 3 runs per case, no attempts, and no confirmation rerun because no wording changes. `--max-cost-usd` is set on each invocation to what is left of the ceiling, and the work stops at it. Three runs per case cannot show a failure rate under about 35%, and one failed run in three is the most a case may show and still pass. Passing says the average run behaves, not that one run in ten won't ship a banned clause.

## How to run

From this directory. Put the real `claude` ahead of dispatch's `scripts/shims/claude` on PATH, because the shim rejects the flags the eval passes. For the control, copy `arms/careless.md` over `skills/writing-prose/SKILL.md` in the temp copy.

```sh
WORK=$(mktemp -d)
mkdir -p $WORK/plugin
cp -R ../../.claude-plugin $WORK/plugin/.claude-plugin
cp -R ../../skills $WORK/plugin/skills
cp -R cases $WORK/plugin/cases
cd $WORK/plugin
PATH=$HOME/.local/bin:$PATH claude plugin eval . --eval-dir cases --tag suite --runs 3 --ablation none \
  --allow-tools Write Edit --scaffold --judge-model claude-opus-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 -j 4 --max-cost-usd 5 --json $WORK/result.json
```

Use `--tag scorer --runs 1` for the scorer check. Copy the result JSON into `results/` under the run's name.
