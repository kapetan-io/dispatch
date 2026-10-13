---
name: principle-problem-domains
description: "Apply (also PD, Problem Domains, domain-driven architecture) when you create a package, name a type or function, add a method to an existing type, or review code structure. Structure code around the problem domain's own language, not technical layers."
---

# Problem Domains

Also called PD, PD Code Architecture or Domain Driven architecture. It means code structured around the problem domain:

- Code, package and interface names use the domain's language, meaning terms a domain expert would recognize. A vague catch-all such as a Helper or Util suffix that doesn't say what the thing does is a miss.
- Packages are organized around domain concepts, not technical layers.
- Each package and class is cohesive around a single domain concept. A busy class that spans several concepts is a missing subdomain to split out.
- Tactical DDD patterns (value objects, aggregates, repositories, domain events) are ways of fixing naming, cohesion or boundary problems, not requirements in their own right.

**Before naming or structuring anything**, walk up from the current location to the nearest `docs/CONTEXT.md` (per-service in a monorepo, with an optional repo-wide one). It is the project's common language. When one exists, use its terms and respect its "avoid" aliases instead of inventing names.

**Sign it was skipped:** a `utils` or `helpers` package, a type named for its technical role (`Manager`, `Handler`) instead of what it does, or a name that appears in no doc or conversation about the product.
