---
name: principle-mechanism-not-policy
description: "Apply when designing a tool, library or platform that other projects build on and you are adding a restriction, a default, a special-case path or a feature request to it."
---

# Mechanism, Not Policy

Brinch Hansen's rule: the nucleus supplies the operations and leaves strategy to the programs above it. Your workflow preference is policy, and it looks like a design decision while you are making it. Build the mechanism and let the consumer's configuration or rules hold the choice.

**Why:** A policy built into the mechanism is a rule every other user has to fight.

- **A restriction needs a safety property.** Otherwise it is your preference about how the work should go.
- **A special path for a problem nobody has is policy.** One rule for every case, and a plain mechanism the user reaches for when the problem arrives.
- **Whether the artifact is committed, ignored or shared is the project's call.** Write it in a predictable place and stop.
- **Ask a platform for primitives, not for your workflow.** The workflow goes in your own code or an extension.
