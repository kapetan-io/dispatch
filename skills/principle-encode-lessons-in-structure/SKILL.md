---
name: principle-encode-lessons-in-structure
description: "Apply when a mistake or correction just happened and you are choosing the fix, or before writing the same instruction a second time. Encode the rule as a lint, test, runtime check, or script instead of more text."
---

# Encode Lessons in Structure

Encode recurring fixes in mechanisms instead of textual instructions.

**Why:** Textual instructions are easy to miss. They require the reader to notice, remember, and comply. Structural mechanisms (lint rules, metadata flags, runtime checks, automation scripts) enforce the rule without cooperation.

**Pattern:**
Before writing the same instruction a second time:
1. Ask: can this be a lint rule, a test, a metadata flag, a runtime check, or a script?
2. If yes, encode it. Delete the instruction
3. If no (requires judgment), make the instruction more prominent and add an example of the failure mode

**Pick the strongest mechanism.** When more than one mechanism would work, choose the strongest the situation allows (an unrepresentable state that cannot compile, then a lint or banned API that fails CI, then a canonical helper, then a runtime check), because agents copy whatever the surrounding code already does and a weaker guard becomes the next template.

**Corollary:** If the fix is structural, only use the structural fix. The instruction is the symptom.

**When a correction lands,** decide whether it is a one-off or a pattern. A pattern goes into the strongest place dispatch has for it: a test or lint rule, then the relevant skill or CLAUDE.md, then an ADR if it is a design decision. If you cannot encode it now, file a ticket. "I'll keep that in mind" does not persist, and fixing one instance while leaving the pattern intact guarantees a repeat.

Adapted from pstack by Lauren Tan (MIT). Notice in `../principles/LICENSE-pstack`.
