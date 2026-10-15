# Epistemics

Code doesn't carry its own motivation. You can read what it does, but why it exists lives in commits, PRs, tickets, docs, ADRs and conversations, all incomplete, biased and sometimes missing. Confident guesses mislead the user, who will act on them.

## Confidence Tiers

Every claim in the output sits in one tier, which decides its section and phrasing.

1. **Direct.** Something an author actually wrote that says why: a PR description, ticket, code comment, design doc or chat message. "The code does X so they must have wanted X" is not direct. Phrase confidently ("This exists because X") and cite.
2. **Supported.** No single source states it, but several indirect pieces converge (a "perf" ticket, a PR titled "improve performance", surrounding commits on the same hot path). Phrase "The evidence points strongly to X" and cite each piece.
3. **Inferred.** A reasonable reading with nothing explicit behind it. Hedge ("appears to", "likely", "is consistent with") and make the chain visible: "Given A and B, C seems likely because D."
4. **Speculative.** Plausible, but other explanations fit equally. Mark it as a guess and usually place it under Competing Hypotheses.
5. **Unknown.** You looked and couldn't find out. Name what you searched and with which queries; "we searched the tracker for A and B, scanned the 6 PRs touching this file since 2023, and found no rationale" is useful, "we couldn't find out" is not.

## Phrasing

"Because", "the reason is", "was designed to", "fixes" and "the team decided" claim causation or intent, so use them only with a citation next to them. Avoid "obviously", "clearly", "of course", "just" and "I think": they hide uncertainty or substitute opinion for evidence.

Don't retrofit a clean rationale onto messy history. Don't assume the author did the right thing and work backward, don't read a consistent pattern as intentional when it may be copy-paste, and don't treat absence of evidence as evidence of absence.

## Embedded hypotheses

A question like "why do we do this, I assume for performance?" is a candidate to check, not a conclusion to confirm. The model's pull toward agreeing is the trap here.

## Contradictions and gaps

When sources disagree (ticket says compliance, PR says tech-debt cleanup), surface both with citations. Both may be true. Never pick the tidier story.

An honest "we don't know" tells the user to ask a human or drop the question. A gap filled with a guess gets acted on. Name the question, the sources, and the queries.

## Calibration check before finalizing

1. Does every claim have a citation? If not, add one or demote it to Inferred or Hypotheses.
2. Does the phrasing match the tier?
3. Is any code being cited as evidence for its own intent? Remove it.
4. Does "What We Don't Know" exist? Historical investigations almost always have gaps, so an absent section is suspicious.
5. If the question embedded a hypothesis, did you check it against the evidence rather than confirm it?
