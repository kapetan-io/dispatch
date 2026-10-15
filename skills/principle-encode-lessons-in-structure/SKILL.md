---
name: principle-encode-lessons-in-structure
description: "Apply when a mistake or correction just happened and you are choosing the fix, or before writing the same instruction a second time. Encode the rule as a linter, static check, runtime check, or script instead of more text."
---

# Encode Lessons in Structure

Encode recurring fixes in mechanisms (tools, code, metadata, automation) instead of textual instructions. Every error, human correction, and unexpected outcome is a learning signal. Capture it, route it, and close the loop.

**Why:** Textual instructions are easy to miss. They require the reader to notice, remember, and comply. Structural mechanisms (lint rules, metadata flags, runtime checks, automation scripts) enforce the rule without cooperation.

**Pattern:**
Before writing the same instruction a second time:
1. Ask: can this be a lint rule, a static check, a metadata flag, a runtime check, or a script?
2. If yes, encode it. Delete the instruction
3. If no (requires judgment), make the instruction more prominent and add an example of the failure mode

**Pick the strongest mechanism.** When more than one mechanism would work, choose the strongest the situation allows (an unrepresentable state that cannot compile, then a linter or banned API that fails CI, then a canonical helper, then a runtime check), because agents copy whatever the surrounding code already does and a weaker guard becomes the next template.

**Corollary:** If the fix is structural, only use the structural fix. The instruction is the symptom.

**Tests are not enforcement.** A test checks that the system behaves as a requirement says. A linter or static check holds code, text and conventions to a rule. Do not write a Go test to police how code is written or what a file says. Use a linter, and write a test only for behavior.

**Feedback loop:**
- **Capture every correction.** When the human intervenes or a check fails, decide whether it is a one-off or a pattern.
- **Route to the right layer.** A one-off is fixed in place. A recurring fix becomes a linter rule, or failing that a line in the relevant skill or CLAUDE.md. A design decision becomes an ADR. A systemic issue becomes a principle.
- **Close the loop.** Don't just record. Apply it now or file a ticket. "I'll keep that in mind" does not persist, and fixing one instance while leaving the pattern intact guarantees a repeat.

Adapted from pstack by Lauren Tan (MIT). Notice in `../principles/LICENSE-pstack`.
