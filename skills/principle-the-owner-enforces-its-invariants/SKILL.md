---
name: principle-the-owner-enforces-its-invariants
description: "Apply when placing a validation check, a constraint or a consistency rule: in a constructor or mutator, a store's interface, a handler, a service layer or a gateway, or when something is about to read or write a store directly."
---

# The Owner Enforces Its Invariants

Hoare's representation invariant, Parnas's information hiding. Whoever owns the state owns the rule about it, and the rule holds only while every path to the state goes through the owner. A second path, a script or a query against the store, bypasses every check the owner makes.

**Why:** A check in a caller protects one entry point, and the next entry point copies it or forgets it. Meyer's non-redundancy principle: a check has one owner, never both sides.

- **A type with a constraint checks it in its constructors and mutators,** and never hands out the mutable field that could break it.
- **A store's interface carries its validation,** so every implementation enforces the same rules from one shared validator, and the contract tests check each one.
- **Handlers, service layers, gateways and buses decode the wire format and route the request.** Whether the value may be stored is the store's check, so when one of these makes it, move it to the store.
