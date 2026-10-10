package claimeval_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/claimeval"
)

func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = claimeval.Run(context.Background(), args, claimeval.Options{Stdout: &out, Stderr: &errOut})
	return out.String(), errOut.String(), code
}

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "bin", "tool"), "echo hi\n")
	write(t, filepath.Join(dir, "docs", "design", "evals", "key.md"), "answer key\n")
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "-A"},
		{"-c", "user.name=dev", "-c", "user.email=dev@example.com", "commit", "-qm", "start"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return dir
}

const skillText = "# Build\n\n## Steps\n\n1. **Orient.** Old wording.\n2. **Implement.** Code it.\n"

func TestBuildWritesBlindedCases(t *testing.T) {
	repo, work := gitRepo(t), t.TempDir()
	write(t, filepath.Join(work, "skill.md"), skillText)
	write(t, filepath.Join(work, "arm.md"), "1. **Orient.** New wording.\n")
	write(t, filepath.Join(work, "tickets", "a.md"), "# ENG-1: a ticket\n")
	cases, err := json.Marshal([]claimeval.Case{
		{Name: "wrong-cause", Ticket: "ENG-1", File: "a.md", Fixture: "HEAD", Kind: "planted", Expect: "NOTE", Planted: "the cause is X"},
		{Name: "all-true", Ticket: "ENG-1", File: "a.md", Fixture: "HEAD", Kind: "control", Expect: "CONTINUE", Heldout: true, True: []string{"a is b"}},
	})
	require.NoError(t, err)
	write(t, filepath.Join(work, "cases.json"), string(cases))
	out := filepath.Join(work, "plugin")

	stdout, stderr, code := run(t, "build", "-arm", filepath.Join(work, "arm.md"), "-out", out,
		"-cases", filepath.Join(work, "cases.json"), "-tickets", filepath.Join(work, "tickets"),
		"-repo", repo, "-goroot", "/opt/go", "-skill", filepath.Join(work, "skill.md"))

	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "built 2 cases")
	skill, err := os.ReadFile(filepath.Join(out, "skills", "build", "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(skill), "New wording.")
	assert.NotContains(t, string(skill), "Old wording.")
	assert.Contains(t, string(skill), "2. **Implement.**")
	assert.FileExists(t, filepath.Join(out, "cases", "wrong-cause", "fixture", "bin", "tool"))
	assert.FileExists(t, filepath.Join(out, "cases", "wrong-cause", "fixture", "ticket", "ENG-1.md"))
	assert.NoFileExists(t, filepath.Join(out, "cases", "wrong-cause", "fixture", "docs", "design", "evals", "key.md"))

	planted, err := os.ReadFile(filepath.Join(out, "cases", "wrong-cause", "case.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(planted), "name: caught")
	assert.Contains(t, string(planted), "the cause is X")
	assert.Contains(t, string(planted), "match: not_contains")
	control, err := os.ReadFile(filepath.Join(out, "cases", "all-true", "case.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(control), "name: caught")
	assert.Contains(t, string(control), "tags: [heldout]")
	assert.Contains(t, string(control), "- a is b")

	prompt, err := os.ReadFile(filepath.Join(out, "cases", "all-true", "prompt.md"))
	require.NoError(t, err)
	for _, word := range []string{"eval", "rubric", "grader", "planted", "control", "experiment"} {
		assert.NotContains(t, strings.ToLower(string(prompt)), word)
		assert.NotContains(t, strings.ToLower(string(skill)), word)
	}
}

func TestBuildNeedsStep1InTheSkill(t *testing.T) {
	work := t.TempDir()
	write(t, filepath.Join(work, "skill.md"), "# Build\n")
	write(t, filepath.Join(work, "arm.md"), "1. **Orient.** x\n")
	write(t, filepath.Join(work, "cases.json"), "[]")

	_, stderr, code := run(t, "build", "-arm", filepath.Join(work, "arm.md"), "-out", filepath.Join(work, "p"),
		"-cases", filepath.Join(work, "cases.json"), "-goroot", "/opt/go", "-skill", filepath.Join(work, "skill.md"))

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "has no step 1 line")
}

// traceFile writes a trace holding the given tool calls, each with its result.
func traceFile(t *testing.T, steps ...[3]string) string {
	t.Helper()
	var lines []string
	for i, s := range steps {
		id := fmt.Sprintf("t%d", i)
		input, err := json.Marshal(map[string]string{s[1]: s[2]})
		require.NoError(t, err)
		lines = append(lines,
			fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`, id, s[0], input),
			fmt.Sprintf(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":%q}]}}`, id, resultFor(s)))
	}
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

func resultFor(step [3]string) string {
	switch {
	case strings.Contains(step[2], "gotest") && strings.Contains(step[2], "build"):
		return "FAIL\tpkg [build failed]"
	case strings.Contains(step[2], "gotest") && strings.Contains(step[2], "red"):
		return "--- FAIL: TestX\nFAIL"
	}
	return "ok"
}

type runResult struct {
	trace   string
	graders map[string]bool
}

func resultJSON(t *testing.T, byCase map[string][]runResult) string {
	t.Helper()
	var cases []map[string]any
	for name, runs := range byCase {
		var arm []map[string]any
		for _, r := range runs {
			var graders []map[string]any
			for g, ok := range r.graders {
				graders = append(graders, map[string]any{"name": g, "passed": ok})
			}
			arm = append(arm, map[string]any{"tracePath": r.trace, "graders": graders})
		}
		cases = append(cases, map[string]any{"name": name, "arms": map[string]any{"with": arm}})
	}
	b, err := json.Marshal(map[string]any{"cases": cases})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "result.json")
	require.NoError(t, os.WriteFile(path, b, 0o644))
	return path
}

func casesFile(t *testing.T, cases ...claimeval.Case) string {
	t.Helper()
	b, err := json.Marshal(cases)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "cases.json")
	require.NoError(t, os.WriteFile(path, b, 0o644))
	return path
}

func repeat(n int, r runResult) []runResult {
	out := make([]runResult, n)
	for i := range out {
		out[i] = r
	}
	return out
}

var (
	allPass = map[string]bool{"caught": true, "no-false-alarm": true, "routed": true}
	missed  = map[string]bool{"caught": false, "no-false-alarm": true, "routed": false}
)

func TestReportMeetsTheBarWhenEveryRunPasses(t *testing.T) {
	red := traceFile(t, [3]string{"Write", "file_path", "/w/x_test.go"}, [3]string{"Bash", "command", "./gotest red"},
		[3]string{"Edit", "file_path", "/w/bin/tool"})
	cases := casesFile(t,
		claimeval.Case{Name: "bug", Kind: "planted", Repro: "reproduce"},
		claimeval.Case{Name: "fine", Kind: "control"})
	result := resultJSON(t, map[string][]runResult{
		"bug":  repeat(5, runResult{trace: red, graders: allPass}),
		"fine": repeat(5, runResult{trace: red, graders: allPass}),
	})

	stdout, stderr, code := run(t, "report", "-cases", cases, result)

	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "BAR MET")
}

func TestReportFailsACaseUnderFourOfFive(t *testing.T) {
	red := traceFile(t, [3]string{"Bash", "command", "./gotest red"}, [3]string{"Edit", "file_path", "/w/bin/tool"})
	runs := append(repeat(3, runResult{trace: red, graders: allPass}), repeat(2, runResult{trace: red, graders: missed})...)
	cases := casesFile(t, claimeval.Case{Name: "bug", Kind: "planted"})

	stdout, _, code := run(t, "report", "-cases", cases, resultJSON(t, map[string][]runResult{"bug": runs}))

	require.Equal(t, 1, code)
	assert.Contains(t, stdout, "BAR NOT MET")
	assert.Contains(t, stdout, "bug caught 3/5 is under 4 of 5")
}

func TestReportNeedsAFailingTestBeforeTheFirstSourceEdit(t *testing.T) {
	editFirst := traceFile(t, [3]string{"Edit", "file_path", "/w/bin/tool"}, [3]string{"Bash", "command", "./gotest red"})
	testFirst := traceFile(t, [3]string{"Bash", "command", "./gotest red"}, [3]string{"Edit", "file_path", "/w/bin/tool"})
	cases := casesFile(t, claimeval.Case{Name: "bug", Kind: "control", Repro: "reproduce"})

	stdout, _, _ := run(t, "report", "-cases", cases, resultJSON(t, map[string][]runResult{
		"bug": append(repeat(3, runResult{trace: testFirst, graders: allPass}), repeat(2, runResult{trace: editFirst, graders: allPass})...),
	}))

	assert.Contains(t, stdout, "3/5")
	assert.Contains(t, stdout, "bug repro")
}

func TestReportWantsNoSourceEditWhenTheBugIsGone(t *testing.T) {
	read := traceFile(t, [3]string{"Bash", "command", "./gotest ok"}, [3]string{"Write", "file_path", "/w/ticket/post.md"})
	edited := traceFile(t, [3]string{"Edit", "file_path", "/w/bin/tool"})
	cases := casesFile(t, claimeval.Case{Name: "fixed", Kind: "planted", Repro: "none"})

	stdout, _, _ := run(t, "report", "-cases", cases, resultJSON(t, map[string][]runResult{
		"fixed": append(repeat(4, runResult{trace: read, graders: allPass}), runResult{trace: edited, graders: allPass}),
	}))

	assert.Contains(t, stdout, "4/5")
}

func TestReportKnownBadArmMustMissPlantedClaims(t *testing.T) {
	cases := casesFile(t, claimeval.Case{Name: "bug", Kind: "planted"})
	trace := traceFile(t, [3]string{"Bash", "command", "ls"})

	good, _, goodCode := run(t, "report", "-known-bad", "-cases", cases, resultJSON(t, map[string][]runResult{"bug": repeat(5, runResult{trace: trace, graders: missed})}))
	broken, _, brokenCode := run(t, "report", "-known-bad", "-cases", cases, resultJSON(t, map[string][]runResult{"bug": repeat(5, runResult{trace: trace, graders: allPass})}))

	assert.Contains(t, good, "graders ok")
	assert.Equal(t, 0, goodCode)
	assert.Contains(t, broken, "BROKEN GRADERS")
	assert.Equal(t, 1, brokenCode)
}

func TestRunRejectsAnUnknownCommand(t *testing.T) {
	_, stderr, code := run(t, "frobnicate")

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unknown command")
}

func TestReportScoresTheHeldOutCasesAsAGroup(t *testing.T) {
	trace := traceFile(t, [3]string{"Bash", "command", "ls"})
	four := append(repeat(4, runResult{trace: trace, graders: allPass}), runResult{trace: trace, graders: missed})
	cases := casesFile(t,
		claimeval.Case{Name: "a", Kind: "planted"}, claimeval.Case{Name: "b", Kind: "planted"}, claimeval.Case{Name: "c", Kind: "planted"},
		claimeval.Case{Name: "h1", Kind: "planted", Heldout: true}, claimeval.Case{Name: "h2", Kind: "planted", Heldout: true}, claimeval.Case{Name: "h3", Kind: "planted", Heldout: true})
	result := resultJSON(t, map[string][]runResult{
		"a": repeat(5, runResult{trace: trace, graders: allPass}), "b": repeat(5, runResult{trace: trace, graders: allPass}), "c": repeat(5, runResult{trace: trace, graders: allPass}),
		"h1": four, "h2": four, "h3": four,
	})

	stdout, _, code := run(t, "report", "-cases", cases, result)

	// 27 of 30 planted claims caught passes the pooled bar, 12 of 15 held out does not.
	require.Equal(t, 1, code)
	assert.Contains(t, stdout, "BAR NOT MET")
	assert.Contains(t, stdout, "held-out pooled caught 12/15")
	assert.NotContains(t, stdout, "\n  pooled caught")
}

func TestReportGivesNoVerdictWhenACaseOrRunIsMissing(t *testing.T) {
	trace := traceFile(t, [3]string{"Bash", "command", "ls"})
	cases := casesFile(t, claimeval.Case{Name: "bug", Kind: "planted"}, claimeval.Case{Name: "fine", Kind: "control"})
	missingCase := resultJSON(t, map[string][]runResult{"fine": repeat(5, runResult{trace: trace, graders: allPass})})
	shortCase := resultJSON(t, map[string][]runResult{
		"bug": repeat(1, runResult{trace: trace, graders: allPass}), "fine": repeat(5, runResult{trace: trace, graders: allPass}),
	})

	out1, _, code1 := run(t, "report", "-cases", cases, missingCase)
	out2, _, code2 := run(t, "report", "-cases", cases, shortCase)

	assert.Equal(t, 1, code1)
	assert.Contains(t, out1, "bug is missing from the result")
	assert.NotContains(t, out1, "BAR MET")
	assert.Equal(t, 1, code2)
	assert.Contains(t, out2, "bug has 1 runs, want 5")
}

func TestReportCountsShellEditsAndIgnoresBuildFailures(t *testing.T) {
	sedFirst := traceFile(t, [3]string{"Bash", "command", "sed -i s/a/b/ bin/tool"}, [3]string{"Bash", "command", "./gotest red"})
	editAndRun := traceFile(t, [3]string{"Bash", "command", "python3 - <<'EOF'\nopen('bin/tool','w').write('x')\nEOF\n./gotest red"})
	buildFailure := traceFile(t, [3]string{"Write", "file_path", "/w/x_test.go"}, [3]string{"Bash", "command", "./gotest build"}, [3]string{"Edit", "file_path", "/w/bin/tool"})
	catGotest := traceFile(t, [3]string{"Bash", "command", "cat gotest red"}, [3]string{"Edit", "file_path", "/w/bin/tool"})
	testFirst := traceFile(t, [3]string{"Bash", "command", "./gotest red 2>&1 | tail -5"}, [3]string{"Bash", "command", "sed -i s/a/b/ bin/tool"})
	cases := casesFile(t, claimeval.Case{Name: "bug", Kind: "control", Repro: "reproduce"})

	for name, trace := range map[string]string{"sed first": sedFirst, "edit and run together": editAndRun, "build failure": buildFailure, "cat gotest": catGotest} {
		stdout, _, _ := run(t, "report", "-cases", cases, resultJSON(t, map[string][]runResult{"bug": repeat(5, runResult{trace: trace, graders: allPass})}))
		assert.Contains(t, stdout, "0/5", name)
	}
	stdout, _, code := run(t, "report", "-cases", cases, resultJSON(t, map[string][]runResult{"bug": repeat(5, runResult{trace: testFirst, graders: allPass})}))
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "BAR MET")
}

func TestReportIgnoresPathsAComentOnlyMentions(t *testing.T) {
	comment := traceFile(t,
		[3]string{"Bash", "command", "./gotest ok"},
		[3]string{"Bash", "command", "cat >> ticket/post.md <<'EOF'\nNo fix: bin/dispatch-ticket-hint already reads the path.\nEOF"},
		[3]string{"Bash", "command", "./gotest ok 2>&1 | tee ticket/out.txt > /dev/null"})
	edit := traceFile(t, [3]string{"Bash", "command", "cat > bin/dispatch-ticket-hint <<'EOF'\nx\nEOF"})
	cases := casesFile(t, claimeval.Case{Name: "fixed", Kind: "planted", Repro: "none"})

	good, _, _ := run(t, "report", "-cases", cases, resultJSON(t, map[string][]runResult{"fixed": repeat(5, runResult{trace: comment, graders: allPass})}))
	bad, _, _ := run(t, "report", "-cases", cases, resultJSON(t, map[string][]runResult{"fixed": repeat(5, runResult{trace: edit, graders: allPass})}))

	assert.Contains(t, good, "BAR MET")
	assert.Contains(t, bad, "0/5")
}
