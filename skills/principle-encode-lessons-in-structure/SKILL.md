---
name: principle-encode-lessons-in-structure
description: "Apply when you write the same instruction a second time, or a correction recurs. Encode the rule as a type, lint, runtime check or script instead of more text."
---

# Encode Lessons in Structure

Text instructions need the reader to notice, remember and comply. A structural mechanism enforces the rule without cooperation.

When you catch yourself writing the same instruction twice, ask whether it can be enforced instead. Pick the strongest mechanism that works, in this order: a state that cannot be represented or compile, a lint or banned API that fails CI, a canonical helper, a runtime check. Agents copy whatever the surrounding code does, so a weaker guard becomes the next template. If the fix is structural, delete the instruction.

If the rule needs judgment, make the instruction more prominent and add an example of the failure.

Adapted from pstack by Lauren Tan (MIT). Notice in `principles/LICENSE-pstack`.
