---
name: principle-skills
description: "Apply when creating or changing a skill. Write only what the model wouldn't do unprompted, give reasons over rules, and keep it short."
---

# Writing Skills

The model reading a skill already knows most of what you are tempted to write. A skill earns its place by carrying what the model can't know: this repo's conventions, traps that already cost someone time, and decisions the user made.

- **Write what it wouldn't do unprompted.** Cut common knowledge, how a well-known tool behaves, and how to find what it can find itself.
- **Give the reason, not the procedure.** A reason lets the model judge the edge cases. A rule it can't see the point of gets followed literally or skipped.
- **Keep it short.** Every line is read on every run, so a line that changes no behavior costs something and buys nothing.
- **Review it with a fresh reader.** Use a sub-agent which loads `anthropic-skills:skill-creator`, reads the changed skill and reviews it without running test prompts. Run evals only when the user asks for them. Treat its findings like any review finding, set aside only with evidence.
