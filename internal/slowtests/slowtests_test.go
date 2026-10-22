package slowtests_test

// No exception needed: these tests run slowtests' own command line through Run, which is that tool's surface.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapetan-io/dispatch/internal/slowtests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixture = `{"Action":"run","Package":"ex/a","Test":"TestFast"}
{"Action":"output","Package":"ex/a","Test":"TestFast","Output":"ok\n"}
{"Action":"pass","Package":"ex/a","Test":"TestFast","Elapsed":0.2}
{"Action":"pass","Package":"ex/a","Test":"TestSlow","Elapsed":1.5}
{"Action":"pass","Package":"ex/a","Test":"TestSlower","Elapsed":42.25}
{"Action":"pass","Package":"ex/a","Test":"TestSlower/sub|pipe","Elapsed":3}
{"Action":"fail","Package":"ex/a","Test":"TestBroken","Elapsed":2}
{"Action":"skip","Package":"ex/a","Test":"TestSkipped","Elapsed":9}
{"Action":"fail","Package":"ex/a","Elapsed":50}
{"Action":"pass","Package":"ex/b","Test":"TestEdge","Elapsed":1}
{"Action":"pass","Package":"ex/b","Elapsed":301}
`

func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := slowtests.Run(t.Context(), args, slowtests.Options{Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr})
	return code, stdout.String(), stderr.String()
}

func TestSlowTestsReport(t *testing.T) {
	code, stdout, _ := run(t, fixture, "-input", "-")
	require.Equal(t, 0, code)

	assert.Contains(t, stdout, "4 of 6 tests and 1 of 2 packages are over budget")
	assert.Contains(t, stdout, "1 test results failed")
	assert.Contains(t, stdout, "Packages that failed: ex/a.")
	assert.Contains(t, stdout, "| 301.00s | `ex/b` |")
	assert.NotContains(t, stdout, "| 50.00s | `ex/a` |")

	// slowest first; exactly 1s is within budget and skipped tests are not counted
	slower := strings.Index(stdout, "| 42.25s | `ex/a.TestSlower` |")
	sub := strings.Index(stdout, "| 3.00s | `ex/a.TestSlower/sub\\|pipe` |")
	broken := strings.Index(stdout, "| 2.00s | `ex/a.TestBroken` |")
	slow := strings.Index(stdout, "| 1.50s | `ex/a.TestSlow` |")
	require.Positive(t, slower)
	assert.Less(t, slower, sub)
	assert.Less(t, sub, broken)
	assert.Less(t, broken, slow)
	assert.NotContains(t, stdout, "TestEdge")
	assert.NotContains(t, stdout, "TestFast")
	assert.NotContains(t, stdout, "TestSkipped")
}

func TestSlowTestsUnfinishedTests(t *testing.T) {
	// a package timeout kills tests mid-run, so they never emit pass, fail or skip
	code, stdout, stderr := run(t, `{"Action":"run","Package":"ex/a","Test":"TestDone"}
{"Action":"run","Package":"ex/a","Test":"TestHang"}
{"Action":"run","Package":"ex/a","Test":"TestHang/sub"}
{"Action":"pass","Package":"ex/a","Test":"TestDone","Elapsed":0.1}
{"Action":"fail","Package":"ex/a","Elapsed":120}
`, "-input", "-")
	require.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "Tests that never finished")
	assert.Contains(t, stdout, "* `ex/a.TestHang`\n* `ex/a.TestHang/sub`\n")
	assert.NotContains(t, stdout, "TestDone")
}

func TestSlowTestsLimits(t *testing.T) {
	code, stdout, _ := run(t, fixture, "-input", "-", "-test-limit", "10s", "-package-limit", "30s")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "| 42.25s | `ex/a.TestSlower` |")
	assert.NotContains(t, stdout, "TestSlow` |")
	assert.Contains(t, stdout, "| 301.00s | `ex/b` |")
	assert.Contains(t, stdout, "| 50.00s | `ex/a` |")
}

func TestSlowTestsNothingSlowExitsZero(t *testing.T) {
	code, stdout, _ := run(t, `{"Action":"pass","Package":"ex/a","Test":"TestFast","Elapsed":0.1}
{"Action":"pass","Package":"ex/a","Elapsed":0.2}
`, "-input", "-")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "0 of 1 tests and 0 of 1 packages are over budget")
	assert.NotContains(t, stdout, "###")
}

func TestSlowTestsInputFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.json")
	require.NoError(t, os.WriteFile(path, []byte(fixture), 0o644))
	code, stdout, _ := run(t, "", "-input", path)
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "ex/a.TestSlower")
}

func TestSlowTestsReportFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		args  []string
		want  string
	}{
		{"buildFailed", `{"Action":"fail","Package":"ex/a","Elapsed":0,"FailedBuild":"ex/dep"}` + "\n", []string{"-input", "-"}, "did not build in ex/a"},
		{"notJSON", "ok  \tex/a\t0.1s\n", []string{"-input", "-"}, "line 1 is not go test -json output"},
		{"empty", "", []string{"-input", "-"}, "no test results"},
		{"missingFile", "", []string{"-input", filepath.Join(t.TempDir(), "absent.json")}, "no such file"},
		{"badFlag", "", []string{"-nope"}, "flag provided but not defined"},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := run(t, test.input, test.args...)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, test.want)
		})
	}
}

func TestSlowTestsRunsGoTest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/sleepy\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sleepy_test.go"), []byte(`package sleepy

import (
	"testing"
	"time"
)

func TestNap(t *testing.T) { time.Sleep(300 * time.Millisecond) }

func TestBroken(t *testing.T) { t.Fatal("always fails") }
`), 0o644))

	var stdout, stderr bytes.Buffer
	code := slowtests.Run(t.Context(), []string{"-test-limit", "200ms"}, slowtests.Options{Dir: dir, Stdout: &stdout, Stderr: &stderr})
	require.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "example.test/sleepy.TestNap")
	assert.Contains(t, stdout.String(), "1 test results failed")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken_test.go"), []byte("package sleepy\n\nfunc init() { undefined() }\n"), 0o644))
	stderr.Reset()
	code = slowtests.Run(t.Context(), nil, slowtests.Options{Dir: dir, Stdout: &stdout, Stderr: &stderr})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "did not build")
}

func TestSlowTestsTags(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/tagged\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fast_test.go"), []byte("package tagged\n\nimport \"testing\"\n\nfunc TestQuick(t *testing.T) {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "slow_test.go"), []byte(`//go:build tests_slow

package tagged

import (
	"testing"
	"time"
)

func TestTaggedNap(t *testing.T) { time.Sleep(150 * time.Millisecond) }
`), 0o644))

	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{"withoutTags", []string{"-test-limit", "100ms"}, false},
		{"withTags", []string{"-tags", "tests_slow", "-test-limit", "100ms"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := slowtests.Run(t.Context(), test.args, slowtests.Options{Dir: dir, Stdout: &stdout, Stderr: &stderr})
			require.Equal(t, 0, code)
			assert.Equal(t, test.want, strings.Contains(stdout.String(), "example.test/tagged.TestTaggedNap"))
		})
	}
}

func TestSlowTestsTargetMeasuresTaggedTests(t *testing.T) {
	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^\t@go run \./cmd/slowtests -tags tests_slow$`, string(makefile))
}

func TestSlowTestsRunOnlyFromItsOwnWorkflow(t *testing.T) {
	workflows := filepath.Join("..", "..", ".github", "workflows")

	slow, err := os.ReadFile(filepath.Join(workflows, "slow-tests.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(slow), "make test-report")
	assert.Contains(t, string(slow), "schedule:")
	assert.Contains(t, string(slow), "workflow_dispatch:")
	assert.NotContains(t, string(slow), "pull_request")
	assert.NotContains(t, string(slow), "push:")

	files, err := filepath.Glob(filepath.Join(workflows, "*.yml"))
	require.NoError(t, err)
	for _, file := range files {
		if filepath.Base(file) == "slow-tests.yml" {
			continue
		}
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "slowtests")
	}
}
