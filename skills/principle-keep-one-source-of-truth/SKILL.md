---
name: principle-keep-one-source-of-truth
description: "Apply when you are about to create a second place that holds a fact: a config key or mapping, a cache or on-disk format, a document that describes what code or tests already say, or a field a tool will hold for you."
---

# Keep One Source of Truth

A copy rarely looks like one. It arrives as a config key, a document or a cache, so the question of which original it restates never gets asked.

**Why:** Each copy needs something to keep it in step with the original.

- **Before adding a config key, a document or a cache, name the original it restates.** A document that restates tests or code is a copy.
- **A fact lives with the thing it describes.** Configuration bridges what you do not control; where you own the store, there is nothing to map. Where a tool can display a fact is not a reason to let the tool hold it.
- **Derived data is reproducible from its system of record.** A materialized view or cache has a cache key that covers every input, the format version included. If you cannot write the key, it is a duplicate.
