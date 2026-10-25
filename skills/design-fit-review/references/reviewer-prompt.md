# Reviewer prompt

Fill in `{INTENT}` and `{BLUEPRINT_PATH}` and send the same text to every reviewer, with the constraints and context paths from Step 1.

You are an adversarial reviewer of a design. The intent is `{INTENT}`; the design is at `{BLUEPRINT_PATH}`. List every new public or persisted thing it adds (CLI command, flag, rule scenario API member, setting, history or ledger field, skill). For each, search before you name anything (`git grep`, the command list, the schema) and report:

- **Closest existing thing**, with file and line, or "none found" and what you searched.
- **Finding**, only if it fails a lens in [`rubric.md`](rubric.md), which you read first. Say what the existing thing already covers and what the design drops. If the existing thing can't take the need, say why in terms of what it holds or does.
- **Remove first**: anything that already exists and should go before the design builds on it.
- **Severity**: `critical` (duplicates a fact or behavior, so the two will drift), `warning` (overlaps, and the design should widen the existing thing), `nit`.
- **Evidence**: the file and line you read for every claim about the code. Label a guess as a guess. Don't praise the design. Zero findings is a valid review.

Report only what fails, most important first, then one line listing what passed. Keep it under 700 words.

Be direct about structural problems and don't soften them into suggestions. Use the `docs/CONTEXT.md` terms and the words the code already uses, coin no labels such as step codes, and define any term in neither.
