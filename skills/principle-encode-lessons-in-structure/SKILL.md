---
name: principle-encode-lessons-in-structure
description: "Apply when you write, enforce or review an instruction or convention that code, text or files must follow (in a skill, CLAUDE.md, a design or a review, or the user states one), and when a mistake or correction just happened and you are choosing the fix. An instruction a script can check becomes a linter, static check, runtime check or script that fails CI the first time it comes up, instead of text."
---

# Encode Lessons in Structure

Encode rules in mechanisms (tools, code, metadata, automation) instead of textual instructions. Every error, human correction, and unexpected outcome is a learning signal. Capture it, route it, and close the loop.

**Why:** Textual instructions are easy to miss. They require the reader to notice, remember, and comply. Structural mechanisms (lint rules, metadata flags, runtime checks, automation scripts) enforce the rule without cooperation.

**Pattern:** The first time an instruction or convention that code, text or files must follow comes up, before it is ever broken, sort it. It is judgment-free when a script gives the same verdict every time for the same input without reading for meaning: a banned string, a required field, a forbidden path or reference.
1. Judgment-free: encode it now as a lint rule, a static check, a runtime check or a script, and delete the instruction. A reviewer who can't edit asks for the check.
2. Needs judgment: write the instruction, make it prominent, and add an example of the failure mode.

**A check blocks.** It fails CI or the command that runs it, and its error says what is wrong and what to do instead. Build it that way without asking the user whether it should block or only warn, because a warning gets ignored the way text does. Fix the existing violations in the same change. If the pattern is already common, fail only when a change adds more, since a check that fails every change gets switched off, and file a ticket for the rest.

**Pick the strongest mechanism.** When more than one mechanism would work, choose the strongest the situation allows (an unrepresentable state that cannot compile, then a linter or banned API that fails CI, then a canonical helper backed by a check that fails when code bypasses it, then a runtime check), because agents copy whatever the surrounding code already does and a weaker guard becomes the next template.

**Tests are not enforcement.** A test checks that the system behaves as a requirement says. A linter or static check holds code, text and conventions to a rule. Do not write a Go test to police how code is written or what a file says. Use a linter, and write a test only for behavior.

**Feedback loop:**
- **Capture every correction.** When the human intervenes or a check fails, decide whether it is a one-off or a pattern.
- **Route to the right layer.** An instruction a script can check becomes a check on first sight, missed or not. For one that needs judgment, a one-off is fixed in place and a recurring fix becomes a line in the relevant skill or CLAUDE.md. A systemic issue becomes a principle.
- **Close the loop.** Don't just record. Apply it now, or file a ticket for a judgment fix that can't land in this change. "I'll keep that in mind" does not persist, and fixing one instance while leaving the pattern intact guarantees a repeat.

**Anti-pattern.** Recording without routing. A note about a linter rule that should exist is wasted unless the rule gets implemented.

Adapted from pstack by Lauren Tan (MIT). Notice in `../principles/LICENSE-pstack`.
