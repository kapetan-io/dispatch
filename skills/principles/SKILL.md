---
name: principles
description: "Index of the design principle skills. Use to find which principle fits a design or review question when none is named."
---

# Principles

Each principle is a skill of its own. Load one with the Skill tool and apply it, and name it in your reply when it changes a decision.

| Skill | Applies when |
|---|---|
| `dispatch:principle-problem-domains` | Naming a package, type or method, or reviewing structure. |
| `dispatch:principle-boundary-discipline` | Wiring validation, error handling or adapters. |
| `dispatch:principle-the-owner-enforces-its-invariants` | Placing a validation check, constraint or consistency rule in a constructor, a store's interface, a handler, a service layer or a gateway, or reading or writing a store directly. |
| `dispatch:principle-design-to-the-port-not-the-vendor` | Writing or changing a port's contract, adding a product or delivery mechanism behind one, naming config or terms after a product, or branching on which product or source is in use. |
| `dispatch:principle-encode-lessons-in-structure` | A mistake just happened and you are choosing the fix, or you write the same instruction twice. |
| `dispatch:principle-laziness-protocol` | Refactoring, sizing a diff, tempted to add a layer. |
| `dispatch:principle-make-operations-idempotent` | Commands or loops that must survive crashes and retries. |
| `dispatch:principle-minimize-reader-load` | Code is hard to trace. |
| `dispatch:principle-separate-before-serializing-shared-state` | Several actors might write the same file, branch or key. |
| `dispatch:principle-subtract-before-you-add` | About to add code, a command, a setting or a part of a design. |
| `dispatch:principle-question-the-requirement` | A requirement is about to cost structure: a mechanism, special case, criterion or threshold to satisfy it, a wider scope, or a repair to keep a feature. |
| `dispatch:principle-keep-one-source-of-truth` | About to create a second place that holds a fact: a config key or mapping, a cache or on-disk format, a document restating code or tests, or a field a tool holds for you. |
| `dispatch:principle-mechanism-not-policy` | Designing a tool, library or platform others build on, and adding a restriction, a default, a special-case path or a feature request to it. |
| `dispatch:principle-guard-the-context-window` | Before reading or pasting bulky content into context. |
| `dispatch:principle-zero-value-settings` | Naming a setting, config key or option a user can leave unset. |
| `dispatch:principle-fast-tests` | Writing, reviewing or speeding up tests. |
| `dispatch:principle-confidence-from-many-perspectives` | About to call a design, plan, ticket or root-cause claim done, or a change that would be costly if wrong. |
| `dispatch:principle-verify-dont-assume` | Building on a claim from a ticket, design, brief, report, the user or memory, or briefing a reviewer. |
| `dispatch:principle-skills` | Creating or changing a skill. |

The pstack principles are adapted from Lauren Tan's MIT-licensed pstack plugin. Her notice is in `LICENSE-pstack` beside this file.
