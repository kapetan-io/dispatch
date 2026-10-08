#!/usr/bin/env python3
"""Build a blinded plugin folder for one arm: build.py <arm> <plugin-dir>.

The plugin folder holds the arm's wording as skills/size-check/SKILL.md and
every case under reviews/, so no path the agent sees names the arm or the eval.
"""
import json
import os
import pathlib
import shutil
import sys

HERE = pathlib.Path(__file__).resolve().parent

PROMPT = """I've finished reviewing the design in {design}. Before I create build tickets from it, run the size check on it.

Write the result to `size.yaml` in the current directory, in exactly this shape:

```yaml
verdict: FITS
reason: |
  Your reasoning, citing the design's sections.
designs: []
```

or, when it should be split:

```yaml
verdict: TOO_BIG
reason: |
  Your reasoning, citing the design's sections.
designs:
  - name: short name
    scope: what this design covers
    after: []      # names of earlier designs in this list that it builds on
    why_here: one sentence on its place in the order
```
"""

SURFACE = """You are grading a size-check result, the file size.yaml.

If its verdict is FITS, answer PASS.

If its verdict is TOO_BIG, read every entry under `designs`. Each entry must deliver something a user or caller can observe at the surface once it ships on top of the earlier entries: an HTTP or RPC response, a CLI command or its output, a git protocol exchange such as clone, fetch or push, an exported library function's result, a linter or analyzer report on code, or a CI check result.

FAIL if any entry, judged from its name and scope, is only internal structure with nothing a user or caller could observe on its own: an interface or seam, a storage layer or backend that no surface uses yet, a data model, a package skeleton, a refactor with no behavior change, or tests or documentation alone. Otherwise PASS.
"""

DEFERRED = """You are grading a size-check result, the file size.yaml.

If its verdict is FITS, answer PASS.

If its verdict is TOO_BIG, the design it splits lists the following as deferred, out of scope, a limitation, an open question or future work:

{items}

FAIL if any entry under `designs` exists mainly to build one of those items. An entry that mentions such an item only to say it stays out, or that builds something the list does not name, does not fail. Otherwise PASS.
"""


def indent(text, n):
    pad = " " * n
    return "\n".join(pad + line if line else line for line in text.splitlines())


def case_yaml(case, deferred):
    graders = [
        "  - name: wrote-file\n    type: file_exists\n    path: size.yaml",
        "  - name: one-verdict\n    type: regex\n    target: { source: file, path: size.yaml }\n"
        "    pattern: '^verdict: (FITS|TOO_BIG)\\s*$'\n    flags: m\n    match: count:1",
    ]
    if case["expected"]:
        graders.append(
            "  - name: expected-verdict\n    type: regex\n    target: { source: file, path: size.yaml }\n"
            f"    pattern: '^verdict: {case['expected']}\\s*$'\n    flags: m\n    match: contains"
        )
    graders.append(
        "  - name: surface\n    type: llm\n    focus: { source: file, path: size.yaml }\n    criteria: |\n"
        + indent(SURFACE, 6)
    )
    items = "\n".join(f"- {item}" for item in deferred)
    graders.append(
        "  - name: no-deferred\n    type: llm\n    focus: { source: file, path: size.yaml }\n    criteria: |\n"
        + indent(DEFERRED.format(items=items), 6)
    )
    graders.append("  - name: skill-fired\n    type: tool_used\n    tool: Skill")
    return (
        'schema_version: "1.1"\n'
        f"name: {case['name']}\n"
        + ("tags: [heldout]\n" if case.get("heldout") else "")
        + "context:\n  add_dirs: [design]\n"
        "execution:\n  model: claude-opus-5-5\n  max_turns: 40\n  timeout_seconds: 1500\n"
        "  allowed_tools: [Read, Glob, Grep, Write, Skill]\n"
        "graders:\n" + "\n".join(graders) + "\n"
    )


def main():
    arm, out = sys.argv[1], pathlib.Path(sys.argv[2]).resolve()
    cases = json.loads(pathlib.Path(os.environ.get("SIZE_EVAL_CASES", HERE / "cases.json")).read_text())
    if out.exists():
        shutil.rmtree(out)
    (out / ".claude-plugin").mkdir(parents=True)
    (out / ".claude-plugin" / "plugin.json").write_text(
        json.dumps({"name": out.name, "version": "0.1.0", "description": "Design review helpers."}, indent=2) + "\n"
    )
    skill = out / "skills" / "size-check"
    skill.mkdir(parents=True)
    shutil.copy(HERE / "arms" / f"{arm}.md", skill / "SKILL.md")
    for case in cases:
        root = out / "reviews" / case["name"]
        (root / "design").mkdir(parents=True)
        shutil.copy(HERE / "inputs" / case["input"], root / "design" / "design.md")  # absolute input paths pass through the join
        (root / "prompt.md").write_text(PROMPT.format(design=root / "design" / "design.md"))
        (root / "case.yaml").write_text(case_yaml(case, case["deferred"]))


if __name__ == "__main__":
    main()
