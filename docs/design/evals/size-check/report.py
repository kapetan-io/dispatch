#!/usr/bin/env python3
"""report.py <arm>...: pull each run's size.yaml out of its kept trace and print the pass-bar measures."""
import collections
import json
import pathlib
import re
import sys

HERE = pathlib.Path(__file__).resolve().parent
EXPECTED = {c["name"]: c["expected"] for c in json.loads((HERE / "cases.json").read_text())}


def size_yaml(trace):
    content = None
    for line in pathlib.Path(trace).read_text().splitlines():
        event = json.loads(line)
        if event.get("type") != "assistant":
            continue
        for block in event["message"].get("content", []):
            if block.get("type") == "tool_use" and block["name"] in ("Write", "Edit"):
                if str(block["input"].get("file_path", "")).endswith("size.yaml") and "content" in block["input"]:
                    content = block["input"]["content"]
    return content


def report(arm):
    result = json.loads((HERE / "results" / arm / "result.json").read_text())
    out = HERE / "results" / arm
    rows, split_runs = [], collections.Counter()
    judge = collections.Counter()
    print(f"\n## {arm}  (cost ${result.get('costUsd', 0):.2f})\n")
    print("| case | expected | verdicts | designs per split | surface fails | deferred fails | errors |")
    print("|---|---|---|---|---|---|---|")
    for case in result["cases"]:
        verdicts, counts, surface_fail, deferred_fail, errors = [], [], 0, 0, 0
        for i, run in enumerate(case["arms"]["with"], 1):
            graders = {g["name"]: g["passed"] for g in run["graders"]}
            text = size_yaml(run["tracePath"]) if run.get("tracePath") and pathlib.Path(run["tracePath"]).exists() else None
            if run.get("error") or text is None:
                errors += 1
                verdicts.append("ERR")
                continue
            (out / f"{case['name']}-{i}.yaml").write_text(text)
            m = re.search(r"^verdict:\s*(FITS|TOO_BIG)\s*$", text, re.M)
            verdict = m.group(1) if m else "BAD"
            verdicts.append(verdict)
            if verdict == "TOO_BIG":
                counts.append(len(re.findall(r"^\s*-\s+name:", text, re.M)))
                split_runs[arm] += 1
                judge["surface_total"] += 1
                judge["deferred_total"] += 1
                if not graders.get("surface", True):
                    surface_fail += 1
                    judge["surface_fail"] += 1
                if not graders.get("no-deferred", True):
                    deferred_fail += 1
                    judge["deferred_fail"] += 1
        tally = collections.Counter(verdicts)
        print(f"| {case['name']} | {EXPECTED.get(case['name']) or 'open'} | "
              f"{', '.join(f'{k} {v}' for k, v in tally.most_common())} | {counts or '-'} | "
              f"{surface_fail} | {deferred_fail} | {errors} |")
    total = judge["surface_total"]
    if total:
        print(f"\nTOO_BIG runs {total}: surface pass {1 - judge['surface_fail'] / total:.0%}, "
              f"no-deferred pass {1 - judge['deferred_fail'] / total:.0%}")


for arm in sys.argv[1:]:
    report(arm)
