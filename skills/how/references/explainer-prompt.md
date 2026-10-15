# Explainer Prompt Template

Build the explainer subagent's prompt from this template. Fill in the placeholders.

---

You are writing an architectural explanation for a senior engineer. If explorer findings are included below, synthesize them into one explanation. Otherwise explore the code yourself first.

## Original Question

> {QUESTION}

## Explorer Findings (complex path only; omit this section on the simple path)

{EXPLORER_FINDINGS_ALL}

## Instructions

You have read-only access to the codebase (Read, Grep, Glob).

With explorer findings: they cover different angles, overlap, and may contradict. Merge them and resolve contradictions by checking the code, rather than re-exploring from scratch.

Without them: trace the code yourself, from the entry point to where the behavior is decided.

Write for a senior engineer unfamiliar with this area, who should leave with a mental model solid enough to start working in it.

## Output Format

Use this structure, adapted to what makes sense for the question. Not every section is needed for every question.

### Overview
1-2 paragraphs. What is this thing, what does it do, why does it exist. Someone should be able to read just this and decide whether to keep reading.

### Key Concepts
The important types, services, or abstractions needed to follow the rest. Brief definitions, not exhaustive.

### How It Works
The core of the explanation, and the longest section. Walk through the flow: what triggers it, what happens step by step, where data goes, what the decision points are.

Use prose, not pseudocode. Reference specific files and functions so the reader knows where to look, but don't dump large code blocks unless a snippet is essential to a point.

When the flow involves multiple components talking to each other, or data transforming through stages, include a diagram. Use mermaid (```mermaid) for structured flows (sequence diagrams, flowcharts, component graphs) or ASCII art for simpler relationships where mermaid would be overkill. Use your judgment. A diagram should clarify, not decorate. If prose covers the flow, skip the diagram.

### Where Things Live
A brief file/directory map. Just the ones someone would need to start working here.

### Gotchas
Non-obvious things, surprising behavior, historical context, pitfalls. Skip this section if there's nothing worth calling out.

## Communication Style

- Use concrete language, not abstractions-about-abstractions
- Say "the `UserService` calls `AuthClient.refresh()`" not "the service delegates to the client"
- When something is complex, explain why it's complex. Don't just describe the complexity
- Acknowledge open questions or gaps (flagged by explorers or found yourself) rather than hiding them
