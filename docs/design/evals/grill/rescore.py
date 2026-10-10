#!/usr/bin/env python3
"""One-off rescore of stored runs after the three candidates-get-* graders changed
to also read the file an Agent prompt points at (log.md, "grader fix and rescore").

Usage: rescore.py results/attempt3-skill.json results/attempt1-control.json > results/rescore.json

It reads each run's trace (tracePath in the result file, which must still exist) and
counts the Agent calls made after grounding.md was written whose prompt, plus any file
the prompt names that was written earlier by Write or a shell redirect, matches the old
pattern. Every other grader keeps the verdict stored in the result file. The script
approximates the LLM graders and does not call a model."""
import json, sys, re, os

PATTERNS = {
    'candidates-get-grounding': r'grounding\.md',
    'candidates-get-runner-prompt': r"Caller.s usage first|runner-prompt",
    'candidates-get-objective': r'Objective|once per failing check',
}

def agent_matches(trace):
    files, out = [], {k: 0 for k in PATTERNS}
    for line in open(trace):
        try:
            o = json.loads(line)
        except ValueError:
            continue
        m = o.get('message', {})
        c = m.get('content') if isinstance(m, dict) else None
        if not isinstance(c, list):
            continue
        for b in c:
            if b.get('type') != 'tool_use':
                continue
            i = b['input']
            if b['name'] == 'Write':
                files.append((os.path.basename(i.get('file_path', '')), i.get('content', '')))
            elif b['name'] == 'Bash':
                files.append(('*', i.get('command', '')))
            elif b['name'] == 'Agent':
                p = i.get('prompt', '')
                if not any('grounding.md' in (n if n != '*' else c2)
                           for n, c2 in files
                           if n != '*' or re.search(r'>\s*\S*grounding\.md', c2)):
                    continue
                extra = ''
                for n, content in files:
                    if n == '*':
                        for fn in set(re.findall(r'>\s*\S*?([\w.-]+\.md)', content)):
                            if fn in p:
                                extra += content
                                break
                    elif n and n in p:
                        extra += content
                for k, pt in PATTERNS.items():
                    if re.search(pt, p + extra):
                        out[k] += 1
    return out

result = {}
for path in sys.argv[1:]:
    runs = json.load(open(path))['cases'][0]['arms']['with']
    rows, passes, total = [], 0, 0
    for r in runs:
        verdict = {g['name']: bool(g['passed']) for g in r['graders']}
        counts = agent_matches(r['tracePath'])
        for k, n in counts.items():
            verdict[k] = n >= 3
        gated = {k: v for k, v in verdict.items() if k != 'skill-fired'}
        passes += sum(gated.values())
        total += len(gated)
        rows.append({'verdicts': verdict, 'agent_calls_matching': counts})
    result[path] = {'runs': rows, 'passes': passes, 'total': total}
json.dump(result, sys.stdout, indent=1)
