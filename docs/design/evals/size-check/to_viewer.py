#!/usr/bin/env python3
"""to_viewer.py <iteration-dir> <arm>=<config> ...

Lays out size-check results the way skill-creator's eval viewer and benchmark
aggregator expect: <iteration>/eval-<case>/<config>/run-<n>/{outputs/size.yaml,
grading.json, timing.json}, with eval_metadata.json per case. Run report.py on
each arm first, so results/<arm>/<case>-<n>.yaml exists.
"""
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent

TEXT = {
    "wrote-file": "Writes size.yaml",
    "one-verdict": "Gives exactly one verdict",
    "expected-verdict": "Verdict matches the expected answer",
    "surface": "Every split piece gives a user something visible",
    "no-deferred": "No split piece builds deferred work",
    "skill-fired": "Loads the size-check skill",
}


def main():
    iteration = pathlib.Path(sys.argv[1]).resolve()
    cases = {c["name"]: c for c in json.loads((HERE / "cases.json").read_text())}
    for spec in sys.argv[2:]:
        arm, config = spec.split("=")
        result = json.loads((HERE / "results" / arm / "result.json").read_text())
        for case in result["cases"]:
            name = case["name"]
            eval_dir = iteration / f"eval-{name}"
            eval_dir.mkdir(parents=True, exist_ok=True)
            expected = cases.get(name, {}).get("expected") or "open"
            heldout = " (held out)" if cases.get(name, {}).get("heldout") else ""
            (eval_dir / "eval_metadata.json").write_text(json.dumps({
                "eval_id": name,
                "eval_name": f"{name}{heldout}, expected {expected}",
                "prompt": case["promptMarkdown"],
                "assertions": sorted({TEXT.get(g["name"], g["name"]) for r in case["arms"]["with"] for g in r["graders"]}),
            }, indent=2))
            for i, run in enumerate(case["arms"]["with"], 1):
                run_dir = eval_dir / config / f"run-{i}"
                (run_dir / "outputs").mkdir(parents=True, exist_ok=True)
                answer = HERE / "results" / arm / f"{name}-{i}.yaml"
                (run_dir / "outputs" / "size.yaml").write_text(answer.read_text() if answer.exists() else "(no size.yaml)\n")
                expectations = [
                    {"text": TEXT.get(g["name"], g["name"]), "passed": g["passed"], "evidence": g["explanation"]}
                    for g in run["graders"] if g.get("scored", True)
                ]
                passed = sum(e["passed"] for e in expectations)
                (run_dir / "grading.json").write_text(json.dumps({
                    "expectations": expectations,
                    "summary": {"passed": passed, "failed": len(expectations) - passed, "total": len(expectations),
                                "pass_rate": round(passed / len(expectations), 2) if expectations else 0.0},
                    "timing": {"total_duration_seconds": run.get("durationSeconds", 0)},
                }, indent=2))
                (run_dir / "timing.json").write_text(json.dumps({
                    "total_duration_seconds": run.get("durationSeconds", 0), "total_tokens": 0,
                }, indent=2))


if __name__ == "__main__":
    main()
