# writing-prose eval log

## 2026-10-24, scorer check (results/scorer-check.json, $0.35)

Eight fixture cases copy a hand-written section to `section.md`. Every `-good` section passed `key-absent`, `no-verify` and `under-cap`. Every `-bad` section failed all three. The first version of the `under-cap` pattern, `^(?:\s*\S+){1,N}\s*$`, hung on an over-cap file because `\S+` can split a word across iterations and backtrack exponentially. It was fixed before any run to `^\s*(?:\S+(?:\s+|$)){1,N}$`.

## Control (results/control.json, $0.32)

`arms/careless.md`, 1 run per case. The skill loaded and `section.md` was written in all 4 runs. `key-absent` passed 0 of 4, under the bar's 1 of 4. `under-cap` failed in 3 of 4. `no-verify` passed 4 of 4. The control fails on the banned clauses and not on format.

## Baseline (results/baseline.json, $0.90)

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

Scorer check (results/scorer-check-closing.json, $0.55). All `-bad` sections and `groupcache-userdraft` failed `closing-organized` 3 votes to 0. `bloom-filter-good`, `go-defer-good` and `write-ahead-log-good` passed 3 to 0. The first groupcache `-good` failed 0 to 3, because its last paragraph joined the unreachable-owner fallback to write propagation. It was changed to one idea per paragraph, for a reason that does not depend on the score, and rerun alone (results/scorer-check-closing-groupcache-good.json, $0.05). It passed 2 votes to 1, so the judge is noisy on that section.

Control (results/control-closing.json, $0.39, 1 run per case). `key-absent` 0 of 4, `under-cap` 0 of 4, `closing-organized` 1 of 4.

Baseline (results/baseline-closing.json, $1.12, 3 runs per case).

| Case | skill-fired | key-absent | no-verify | under-cap | closing-organized |
|---|---|---|---|---|---|
| groupcache | 3/3 | 0/3 | 3/3 | 3/3 | 0/3 |
| bloom-filter | 3/3 | 0/3 | 3/3 | 0/3 | 3/3 |
| write-ahead-log (held out) | 3/3 | 0/3 | 3/3 | 0/3 | 2/3 |
| go-defer | 3/3 | 0/3 | 3/3 | 0/3 | 1/3 |
| pooled | 12/12 | 0/12 | 12/12 | 3/12 | 6/12 |

The baseline does not meet the bar on `key-absent`, `under-cap` or `closing-organized`. The three visible groupcache runs each end in a paragraph like "The default cache is an LRU. The Otter cache uses lock-free S3-FIFO eviction. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state." That is the paragraph the user described. The groupcache `under-cap` went from 0 of 3 to 3 of 3 between the two baselines, at 302, 303 and 288 words against 341, 447 and 332 before, so one baseline's word count is noise at three runs. The held-out case's transcripts were not read.

Spend so far about $3.68 of the $10 ceiling.
