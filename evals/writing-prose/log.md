# writing-prose eval log

The per-run result JSONs from `claude plugin eval` were not kept, at the user's call, which departs from `skills/hillclimb/SKILL.md`. The tables below are the record of every score. Only the user's blind ratings are kept as data (`results/human-check.json` and `results/human-check-sections.md`).

## 2026-10-24, scorer check ($0.35)

Eight fixture cases copy a hand-written section to `section.md`. Every `-good` section passed `key-absent`, `no-verify` and `under-cap`. Every `-bad` section failed all three. The first version of the `under-cap` pattern, `^(?:\s*\S+){1,N}\s*$`, hung on an over-cap file because `\S+` can split a word across iterations and backtrack exponentially. It was fixed before any run to `^\s*(?:\S+(?:\s+|$)){1,N}$`.

## Control ($0.32)

`arms/careless.md`, 1 run per case. The skill loaded and `section.md` was written in all 4 runs. `key-absent` passed 0 of 4, under the bar's 1 of 4. `under-cap` failed in 3 of 4. `no-verify` passed 4 of 4. The control fails on the banned clauses and not on format.

## Baseline ($0.90)

Today's `skills/writing-prose/SKILL.md`, 3 runs per case, 12 runs.

| Case | skill-fired | key-absent | no-verify | under-cap |
|---|---|---|---|---|
| groupcache | 3/3 | 0/3 | 3/3 | 0/3 |
| bloom-filter | 3/3 | 0/3 | 3/3 | 0/3 |
| write-ahead-log (held out) | 3/3 | 0/3 | 3/3 | 0/3 |
| go-defer | 3/3 | 0/3 | 3/3 | 0/3 |

The baseline does not meet the bar. `key-absent` and `under-cap` are 0 of 12 against a floor of 11 of 12.

I read one visible transcript (a groupcache run). It loaded the skill and wrote `section.md` directly in 4 turns and 20 seconds, with no sub-agent call. The section had 341 words and kept the network-call clause, the `Sink` sentence and the hot cache bound. So the skill's writer, critic and rewrite loop did not run, though `Agent` was an allowed tool. Whether the loop would cut those clauses is not measured here. The other 11 transcripts were not read, and the held-out case's were not opened.

`key-absent` was 0 for the control and for every baseline run, so it has only been seen to pass on the hand-written `-good` sections, and `no-verify` has only been seen to fail on the `-bad` ones. The baseline and the control are not told apart by the graders, only by transcripts. Review also noted that the skill quotes two groupcache key clauses as examples.

Limits. Three runs per case cannot show a failure rate under about 35%. The same 4 cases are both the baseline and any later tuning set, with write-ahead-log held out. All runs used `claude-sonnet-5-5`. Total spend about $1.57 of the $10 ceiling.

## 2026-10-24, closing-organized added at the user's direction

The user read a groupcache draft whose last paragraph listed the default cache, transport, stats and global state as four unrelated sentences, and asked for that to be what the suite measures. Reason, independent of any score: the skill's rule 2 puts each fact where the reader first needs it, and a closing paragraph of leftovers means the writer had no place for them. The existing four graders stay.

Changes. A fifth grader, `closing-organized`, judged by `claude-sonnet-5-5` with 3 votes. A ninth scorer case, `groupcache-userdraft`, holds the user's draft. The `-bad` sections now end with a run of separate facts. The groupcache `-good` section was rewritten so each leftover fact sits in the mechanism, and then its last paragraph was split so it covers writes only.

Scorer check ($0.55). All `-bad` sections and `groupcache-userdraft` failed `closing-organized` 3 votes to 0. `bloom-filter-good`, `go-defer-good` and `write-ahead-log-good` passed 3 to 0. The first groupcache `-good` failed 0 to 3, because its last paragraph joined the unreachable-owner fallback to write propagation. It was changed to one idea per paragraph, for a reason that does not depend on the score, and rerun alone ($0.05). It passed 2 votes to 1, so the judge is noisy on that section.

Control ($0.39, 1 run per case). `key-absent` 0 of 4, `under-cap` 0 of 4, `closing-organized` 1 of 4.

Baseline ($1.12, 3 runs per case).

| Case | skill-fired | key-absent | no-verify | under-cap | closing-organized |
|---|---|---|---|---|---|
| groupcache | 3/3 | 0/3 | 3/3 | 3/3 | 0/3 |
| bloom-filter | 3/3 | 0/3 | 3/3 | 0/3 | 3/3 |
| write-ahead-log (held out) | 3/3 | 0/3 | 3/3 | 0/3 | 2/3 |
| go-defer | 3/3 | 0/3 | 3/3 | 0/3 | 1/3 |
| pooled | 12/12 | 0/12 | 12/12 | 3/12 | 6/12 |

The baseline does not meet the bar on `key-absent`, `under-cap` or `closing-organized`. The three visible groupcache runs each end in a paragraph like "The default cache is an LRU. The Otter cache uses lock-free S3-FIFO eviction. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state." That is the paragraph the user described. The groupcache `under-cap` went from 0 of 3 to 3 of 3 between the two baselines, at 302, 303 and 288 words against 341, 447 and 332 before, so one baseline's word count is noise at three runs. The held-out case's transcripts were not read.

Spend so far about $3.68 of the $10 ceiling.

## Attempt 1 ($1.31) — reverted

Idea. Rule 2 of "Working from source material" says what to do with a fact no step of the mechanism needs: join the sentence it extends, or cut it, and the last paragraph is not a home for it (41 words added to rule 2, which reads "Order by dependency, not by source. A fact goes where the reader first needs it, which is often mid-sentence in a clause, not in its own sentence."; added text: "A fact that no step of the mechanism needs joins the sentence it extends, or is cut as rule 3 allows. The last paragraph tells the last stage of the mechanism; facts collected there shuffle freely, so it is a list."). Three proposers (the visible failing closings, Anthropic's prompting guidance, skill-creator guidance) each proposed a rule 2 extension, and a fourth merged them.

| Case | key-absent | no-verify | under-cap | closing-organized |
|---|---|---|---|---|
| groupcache | 0/3 | 3/3 | 2/3 | 0/3 |
| bloom-filter | 0/3 | 3/3 | 0/3 | 3/3 |
| write-ahead-log (held out) | 0/3 | 3/3 | 0/3 | 0/3 |
| go-defer | 0/3 | 3/3 | 0/3 | 1/3 |
| pooled | 0/12 | 12/12 | 2/12 | 4/12 |

`closing-organized` went from 6/12 to 4/12 and nothing else moved, so the bar's numbers did not move the right way. Reverted. The skill is unchanged and the arm file stays as a record. All three visible groupcache runs still end in "The default cache is an LRU... HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state." Two of them called `Agent` or `Edit` after writing, so the critic or a rewrite ran, and the paragraph survived. The go-defer closings are one trailing sentence about the Go 1.14 cost. That is a banned clause on the answer key, which suggests the leftovers and the answer key share a cause. The held-out case's transcripts were not read.

Spend so far about $4.99 of the $10 ceiling.

## Attempt 2 ($1.18) — reverted

Idea, from the user. The writer scores each fact from 1 to 5 by how much the answer depends on it, with how the thing works and how it is used highest, orders the list by score, builds the section from the top and drops the bottom unless a sentence needs it (in "The reader and the purpose", after "then list the true, sourced facts that bear on it." it replaced "The writer picks from that list what the reader needs, and the critic checks claims against it, so both check the same thing." with: "Score each fact from 1 to 5 by how much the answer depends on it, with how the thing works and how it is used scoring highest, and order the list by score. The writer builds the section from the top of the list and drops the bottom unless a sentence needs it. The critic checks claims against the list, so both check the same thing."). No proposer round, since the idea was the user's.

| Case | key-absent | no-verify | under-cap | closing-organized |
|---|---|---|---|---|
| groupcache | 0/3 | 3/3 | 2/3 | 0/3 |
| bloom-filter | 0/3 | 3/3 | 0/3 | 3/3 |
| write-ahead-log (held out) | 0/3 | 3/3 | 0/3 | 0/3 |
| go-defer | 0/3 | 3/3 | 0/3 | 0/3 |
| pooled | 0/12 | 12/12 | 2/12 | 3/12 |

`closing-organized` went from 6/12 (baseline) to 3/12, and `under-cap` from 3/12 to 2/12. Reverted. In the nine visible runs, six called only `Skill` and `Write`, so no ranking step is visible, and the sections are not shorter. The bloom-filter sections ran 451 to 502 words and the go-defer ones 395 to 421, against caps of 290 and 240. The groupcache closing is unchanged ("The default cache is an LRU... HTTP is the default transport, stats are OpenTelemetry metrics, and there is no global state"). The held-out case's transcripts were not read.

Reading across attempts 1 and 2. Both added an instruction that asks for a step the writer does not take on the page (rank, then cut). In one-shot runs the writer does not do it. Spend about $6.17 of the $10 ceiling.

## Loop test ($1.01)

Question. Can a real run pass the graders when the skill's whole loop runs? The baseline writes in one shot, so the first four tries never showed. Case `writing-prose-groupcache-loop` (tag `loop`, not part of the bar) is the groupcache prompt plus "run the skill's whole loop: the writer, the critic and the second writer each as a separate sub-agent". Today's wording, 3 runs.

Each run called `Agent` three times, as asked. `skill-fired`, `no-verify` and `under-cap` passed 3/3 (256, 273 and 269 words against a cap of 330). `key-absent` and `closing-organized` failed 3/3. So length is reachable, and the other two failed even with the loop.

Why, read from the critic's answers in the three visible runs.
- The critic's answer to question 1 names the leftovers paragraph in every run. Run 1: "a catalogue of four unrelated facts (a list in prose) and does not advance the mechanism". Run 3: "a list of unrelated facts". The second writer still kept the paragraph, only trimming it, because step 3 of the loop says to cut the clauses the critic named as costing nothing, and says nothing about a paragraph named under question 1.
- The critic judged the answer-key clauses worth keeping. Run 1 on "which takes no network call": "needed. Keep it". So `key-absent` fails on the critic's judgement, not on a missing step. Whether those clauses should count as banned is the author's judgement, as the README says.

Spend about $7.18 of the $10 ceiling.

## Attempt 3 ($1.29) — reverted

Idea, from the research. The writer first writes a scratch file `plan.md` with the three or four questions the reader's question breaks into and a word budget. Every fact on the list must answer one of those questions, and a fact that answers none is cut. The section is drafted from the plan in the order of its questions (56 words added after "...a section usually leaves some of it out." in "The reader and the purpose": "Before drafting, write a scratch file `plan.md` with the three or four questions the reader's question breaks into and a word budget for the section. Every fact on the list must answer one of those questions, and a fact that answers none is cut. The section is drafted from the plan, in the order of its questions."). Sources: question-answer blueprints (Narayan et al., TACL 2023) and a fixed length budget (Chain of Density, 2023), both read only as summaries.

| Case | key-absent | no-verify | under-cap | closing-organized | plan.md written |
|---|---|---|---|---|---|
| groupcache | 0/3 | 3/3 | 2/3 | 0/3 | 3/3 |
| bloom-filter | 0/3 | 3/3 | 0/3 | 3/3 | 2/3 |
| write-ahead-log (held out) | 0/3 | 3/3 | 0/3 | 0/3 | 2/3 |
| go-defer | 0/3 | 3/3 | 0/3 | 1/3 | 3/3 |
| pooled | 0/12 | 12/12 | 2/12 | 4/12 | 10/12 |

`closing-organized` 4/12 against 6/12 for the baseline, `under-cap` 2/12 against 3/12. Reverted. The step happened (`plan.md` in 10 of 12 runs), so earlier attempts' problem of an invisible step is not the cause here. The plans show why it did not help. In the two groupcache plans read, the writer made up a catch-all question for the leftover facts ("What are the defaults?" and "What must the adopter accept?") so that every fact answered one, and both final sections still end with the transport, stats and global-state sentences. Both plans set their own word budget at about 350, above the 330 cap. One plan noted it left out "no network call" because it invites a question, which is the skill's own rule 3 example applied. The held-out case's transcripts were not read.

Reading across three attempts. Each added a rule the writer satisfied by relabelling the leftovers, and none changed the list the writer starts from. The suite hands the writer the full list of true facts, where in use the skill builds that list itself from sources (see "Working from source material"). Spend about $8.47 of the $10 ceiling.

## Human check ($0)

The user rated ten groupcache sections blind (1 to 5) and picked a best. They were two baseline runs, two from attempt 1, one from attempt 2, two from attempt 3, one forced-loop run, the user's own draft and the hand-written good section from the scorer check. Source labels are in the file.

| Section | Source | Rating |
|---|---|---|
| E | user's draft | 4 (best) |
| F | forced-loop run | 3 |
| D | hand-written good | 2 |
| B, C, G, H, I, J | baseline run 1, attempt 3 run 1, attempt 1 runs 1 and 2, attempt 2, attempt 3 run 2 | 1 each |
| A | baseline run 2 | not rated |

Every one-shot run rated 1, whatever wording produced it. The best section is the user's draft, whose closing paragraph is the list of defaults, transport, stats and global state that `closing-organized` fails 3 votes to 0. The user's note on it: it starts the story from `Get()` and tells what happens in order, "the sort of narrative a human enjoys reading", and still shoehorns `Set()`, `Remove()` and `RemoveKeys()` in as data points. The hand-written good section, which passes all four content graders, rated 2.

So the graders and the user disagree. `key-absent` and `closing-organized` do not track what the user values most, which is a section organized as the story of one call. Spend is unchanged at about $8.47 of the $10 ceiling.

## Judge calibration against the human ratings ($0.61)

A new judge, `walkthrough`, passes a section organized as a walk through what happens in order (one call or action as the spine, each paragraph continuing the previous one) and fails one organized as a tour of the parts. The ten sections the user rated were run as one-case fixtures (now removed from the repo to keep it small; the section text is in `results/human-check-sections.md` and the mapping in `the run's result file`). The rubric and fixtures were committed before the run. Rubric, verbatim: "The section answers the question of how a thing works. PASS if it is organized as a walk through what happens, in order: it takes one call or one action as its spine (what the caller does, then what happens next, then what happens when something goes wrong), and each paragraph after the opening continues that sequence from where the previous one stopped, so a reader could retell it as a story. FAIL if it is organized as a tour of the parts, with one paragraph or sentence per component, option or default, so that the paragraphs could be reordered without changing what the reader understands. Judge the whole section, and do not judge the sentences or the choice of facts." Target: pass E (rated 4) and F (3), fail the sections rated 1.

Result. 30 of 30 votes FAIL, on every section including E and F. The judge does not separate the user's best section from the worst, so it cannot stand in for the user's reading as written. Two causes are likely and neither is tested. The rubric is stricter than the user's bar, since the best section still describes its parts after the opening walk-through. And the harness judge returns pass or fail, so it cannot reproduce a 1 to 4 scale.

The `walkthrough` judge is not added to the suite cases. Spend about $9.08 of the $10 ceiling.
