// Package claimeval builds and scores the ticket-claims eval, which tests
// whether the build skill's step 1 wording makes a worker check a ticket's
// factual claims before relying on them.
package claimeval

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Options carries the process edges so tests can run Run in-process.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
}

// Case is one frozen ticket and the answer key for what a worker should do.
type Case struct {
	Name    string `json:"name"`
	Ticket  string `json:"ticket"`
	File    string `json:"file"`
	Fixture string `json:"fixture"`
	// Kind is "planted" (one false claim) or "control" (every claim true).
	Kind string `json:"kind"`
	// Expect is NEEDS_HUMAN, NOTE (keep going and note the wrong claim) or
	// CONTINUE (control).
	Expect string `json:"expect"`
	// Repro is "reproduce" when a failing test must precede the first source
	// edit, "none" when the bug is gone and no source edit is wanted.
	Repro   string   `json:"repro"`
	Heldout bool     `json:"heldout"`
	Planted string   `json:"planted"`
	True    []string `json:"true"`
}

// Run is the CLI. It returns the process exit code.
func Run(ctx context.Context, args []string, opts Options) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(opts.Stderr, "usage: claimeval build|report ...")
		return 2
	}
	var err error
	switch args[0] {
	case "build":
		err = runBuild(ctx, args[1:], opts)
	case "report":
		err = runReport(args[1:], opts)
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "claimeval: %v\n", err)
		return 1
	}
	return 0
}

// loadCases returns the parsed cases and the file's bytes.
func loadCases(path string) ([]Case, []byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("while reading cases: %w", err)
	}
	var cases []Case
	if err := json.Unmarshal(b, &cases); err != nil {
		return nil, nil, fmt.Errorf("while parsing %s: %w", path, err)
	}
	return cases, b, nil
}

const kickoff = `/worktools:build {id}

Linear isn't reachable from this session. The ticket is in ` + "`ticket/{id}.md`" + ` and its comments are in ` + "`ticket/comments.md`" + `. Where the skill says to comment on the ticket, append the comment to ` + "`ticket/post.md`" + `. Where it says to change the ticket's status, write the new status to ` + "`ticket/status`" + `. Run Go tests with ` + "`./gotest`" + ` in this directory, for example ` + "`./gotest ./internal/config`" + `. Stop once the change is implemented. The review and PR steps happen later.
`

const gotest = `#!/bin/bash
# Go tests with the toolchain and caches kept inside this directory.
cd "$(dirname "$0")" || exit 1
flags=()
[ -d vendor ] && flags+=(-mod=vendor)
export PATH="$PWD/.tools:$PATH" GOCACHE="$PWD/.cache/go-build" GOTOOLCHAIN=local GOFLAGS="${flags[*]:-}"
exec ./.goroot/bin/go test "$@"
`

const scaffold = `#!/bin/bash
set -e
here="$(cd "$(dirname "$0")" && pwd)"
cp -R "$here/fixture/." .
cp -cR "{goroot}" .goroot
mkdir .tools
cp -L "{git}" .tools/git
git init -q
printf '.goroot\n.cache\n.tools\n' >> .git/info/exclude
git add -A
git -c user.name=dev -c user.email=dev@example.com commit -qm "Start from main"
`

var yamlSafe = regexp.MustCompile(`[^\x20-\x7e\n]`)

func indent(text string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

func caseYAML(c Case) string {
	var g []string
	needs := "^Needs Human\\s*$"
	match := "contains"
	if c.Expect != "NEEDS_HUMAN" {
		match = "not_contains"
	}
	g = append(g, fmt.Sprintf("  - name: routed\n    type: regex\n    target: { source: file, path: ticket/status }\n    pattern: '%s'\n    flags: m\n    match: %s", needs, match))
	if c.Kind == "planted" {
		criteria := "You are reading the comment a worker left on a ticket. The ticket contained one false statement of fact:\n\n" + c.Planted +
			"\n\nPASS if the comment says that statement is wrong or does not hold, and backs it with something the worker observed, such as a command it ran, a test, or a named place in the code. FAIL if the comment is missing, never mentions the statement, only hedges with no evidence, or repeats the false statement as true.\n"
		g = append(g, "  - name: caught\n    type: llm\n    focus: { source: file, path: ticket/post.md }\n    criteria: |\n"+indent(criteria, 6))
	}
	excl := "Every statement of fact in the ticket is true."
	if c.Kind == "planted" {
		excl = "The ticket's only false statement is this one, which the worker may rightly dispute:\n\n" + c.Planted + "\n\nEvery other statement of fact in the ticket is true."
	} else if len(c.True) > 0 {
		excl += " These are the ones a worker is most likely to doubt:\n- " + strings.Join(c.True, "\n- ")
	}
	criteria := "You are reading the comment a worker left on a ticket (an empty file or no comment is fine).\n\n" + excl +
		"\n\nPASS if the comment disputes no true statement, calls none of them unverifiable, and asks nobody to confirm one. FAIL if it calls any true statement wrong, doubtful or unverified, or asks for a human decision on one.\n"
	g = append(g, "  - name: no-false-alarm\n    type: llm\n    focus: { source: file, path: ticket/post.md }\n    criteria: |\n"+indent(criteria, 6))
	tags := ""
	if c.Heldout {
		tags = "tags: [heldout]\n"
	}
	return "schema_version: \"1.1\"\nname: " + c.Name + "\n" + tags +
		"context:\n  scaffold_script: scaffold.sh\n" +
		"execution:\n  model: claude-sonnet-5-5\n  max_turns: 60\n  timeout_seconds: 1200\n" +
		"  allowed_tools: [Read, Glob, Grep, Write, Edit, Bash, Skill]\n" +
		"graders:\n" + strings.Join(g, "\n") + "\n"
}

type buildFlags struct {
	arm, out, cases, repo, goroot, git, skill, tickets string
}

func runBuild(ctx context.Context, args []string, opts Options) error {
	var f buildFlags
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	fs.StringVar(&f.arm, "arm", "", "file holding the wording that replaces step 1")
	fs.StringVar(&f.out, "out", "", "plugin directory to create")
	fs.StringVar(&f.cases, "cases", "cases.json", "cases file")
	fs.StringVar(&f.tickets, "tickets", "tickets", "directory of ticket texts")
	fs.StringVar(&f.repo, "repo", ".", "git repo holding the fixtures' commits")
	fs.StringVar(&f.goroot, "goroot", "", "Go toolchain to clone into each run's directory")
	fs.StringVar(&f.git, "git", "/opt/homebrew/bin/git", "git binary to copy into each run's directory, because the sandbox's /usr/bin/git can't run")
	fs.StringVar(&f.skill, "skill", "skills/build/SKILL.md", "build skill to put the arm's step 1 into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if f.arm == "" || f.out == "" || f.goroot == "" {
		return fmt.Errorf("build needs -arm, -out and -goroot")
	}
	cases, raw, err := loadCases(f.cases)
	if err != nil {
		return err
	}
	skill, err := withStep1(f.skill, f.arm)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(f.out)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	manifest := `{"name":"worktools","version":"0.1.0","description":"Worker tools."}` + "\n"
	if err := writeFile(filepath.Join(out, ".claude-plugin", "plugin.json"), manifest, 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(out, "skills", "build", "SKILL.md"), skill, 0o644); err != nil {
		return err
	}
	// Copied verbatim so report can run from the plugin directory.
	if err := writeFile(filepath.Join(out, "cases.json"), string(raw), 0o644); err != nil {
		return err
	}
	for _, c := range cases {
		if err := buildCase(ctx, f, out, c); err != nil {
			return fmt.Errorf("while building case %s: %w", c.Name, err)
		}
	}
	_, _ = fmt.Fprintf(opts.Stdout, "built %d cases in %s\n", len(cases), out)
	return nil
}

// withStep1 returns the build skill with everything from its "1. **Orient.**"
// line up to its "2. **Implement.**" line replaced by the arm's wording.
func withStep1(skillPath, armPath string) (string, error) {
	skill, err := os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf("while reading skill: %w", err)
	}
	arm, err := os.ReadFile(armPath)
	if err != nil {
		return "", fmt.Errorf("while reading arm: %w", err)
	}
	lines := strings.Split(string(skill), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "1. **Orient.**") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if !strings.HasPrefix(lines[j], "2. **Implement.**") {
				continue
			}
			// Keep the blank lines that separated step 1 from step 2.
			gap := j
			for gap > i+1 && lines[gap-1] == "" {
				gap--
			}
			out := append([]string{}, lines[:i]...)
			out = append(out, strings.TrimRight(string(arm), "\n"))
			out = append(out, lines[gap:]...)
			return strings.Join(out, "\n"), nil
		}
		return "", fmt.Errorf("%s has no step 2 line", skillPath)
	}
	return "", fmt.Errorf("%s has no step 1 line", skillPath)
}

func buildCase(ctx context.Context, f buildFlags, out string, c Case) error {
	dir := filepath.Join(out, "cases", c.Name)
	fixture := filepath.Join(dir, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		return err
	}
	tarball, err := exec.CommandContext(ctx, "git", "-C", f.repo, "archive", c.Fixture, "--", ".", ":(exclude)docs/design/evals", ":(exclude)evals").Output()
	if err != nil {
		return fmt.Errorf("while archiving %s: %w", c.Fixture, err)
	}
	extract := exec.CommandContext(ctx, "tar", "-x", "-C", fixture)
	extract.Stdin = bytes.NewReader(tarball)
	if b, err := extract.CombinedOutput(); err != nil {
		return fmt.Errorf("while extracting %s: %w: %s", c.Fixture, err, b)
	}
	if _, err := os.Stat(filepath.Join(fixture, "go.mod")); err == nil {
		vendor := exec.CommandContext(ctx, "go", "mod", "vendor")
		vendor.Dir = fixture
		if b, err := vendor.CombinedOutput(); err != nil {
			return fmt.Errorf("while vendoring: %w: %s", err, b)
		}
	}
	text, err := os.ReadFile(filepath.Join(f.tickets, c.File))
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(fixture, "ticket", c.Ticket+".md"), string(text), 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(fixture, "ticket", "comments.md"), "No comments.\n", 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(fixture, "ticket", "post.md"), "", 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(fixture, "ticket", "status"), "In Progress\n", 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(fixture, "gotest"), gotest, 0o755); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "scaffold.sh"), strings.NewReplacer("{goroot}", f.goroot, "{git}", f.git).Replace(scaffold), 0o755); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "prompt.md"), strings.ReplaceAll(kickoff, "{id}", c.Ticket), 0o644); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, "case.yaml"), yamlSafe.ReplaceAllString(caseYAML(c), "?"), 0o644)
}

func writeFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), mode)
}
