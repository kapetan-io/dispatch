// Package slowtests reports the tests and packages that run over the speed budget.
// It reports and never gates: finding slow tests is not an error.
package slowtests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type Options struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Dir is where `go test` runs when no -input is given. It defaults to the current directory.
	Dir string
}

type event struct {
	Action      string
	Package     string
	Test        string
	Output      string
	Elapsed     float64
	FailedBuild string
}

type timing struct {
	name    string
	elapsed time.Duration
}

type results struct {
	tests       []timing
	packages    []timing
	failedTests int
	// failedPackages includes packages whose tests all passed, such as a TestMain that exits 1.
	failedPackages []string
	// unfinished are tests that started and never reported a result, which is
	// what a package timeout or crash leaves behind.
	unfinished  []string
	buildFailed []string
	// buildOutput is the compiler's complaint, which only the build-output events carry.
	buildOutput strings.Builder
}

// Run is the CLI. It returns the process exit code, which is non-zero only when
// the report itself could not be produced, such as when the tests did not build.
func Run(ctx context.Context, args []string, opts Options) int {
	err := run(ctx, args, opts)
	if err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "slowtests: %v\n", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string, opts Options) error {
	fs := flag.NewFlagSet("slowtests", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	input := fs.String("input", "", "read go test -json output from this `file`, or - for stdin, instead of running go test on the package patterns left after the flags (default ./...)")
	testLimit := fs.Duration("test-limit", time.Second, "longest a test may run")
	packageLimit := fs.Duration("package-limit", 5*time.Minute, "longest a package may run")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	var data io.Reader
	switch *input {
	case "":
		out, err := goTest(ctx, fs.Args(), opts.Dir)
		if err != nil {
			return err
		}
		data = out
	case "-":
		data = opts.Stdin
	default:
		file, err := os.Open(*input)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		data = file
	}

	found, err := parse(data)
	if err != nil {
		return err
	}
	if len(found.buildFailed) > 0 {
		return fmt.Errorf("tests did not build in %s\n%s", strings.Join(found.buildFailed, ", "), strings.TrimSpace(found.buildOutput.String()))
	}
	if len(found.packages) == 0 {
		return fmt.Errorf("no test results in the input")
	}
	return write(opts.Stdout, found, *testLimit, *packageLimit)
}

// goTest ignores go's exit status because it is 1 whenever a test fails, and
// the output says which packages failed to build.
func goTest(ctx context.Context, patterns []string, dir string) (io.Reader, error) {
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	cmd := exec.CommandContext(ctx, "go", append([]string{"test", "-json", "-count=1"}, patterns...)...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, exited := err.(*exec.ExitError); !exited {
			return nil, fmt.Errorf("run go test: %w", err)
		}
		if out.Len() == 0 {
			return nil, fmt.Errorf("go test produced no output: %s", strings.TrimSpace(stderr.String()))
		}
	}
	return &out, nil
}

func parse(in io.Reader) (results, error) {
	var found results
	running := map[string]int{}
	started := 0
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var ev event
		if err := json.Unmarshal([]byte(text), &ev); err != nil {
			return found, fmt.Errorf("line %d is not go test -json output: %v", line, err)
		}
		if ev.Action == "build-output" {
			found.buildOutput.WriteString(ev.Output)
			continue
		}
		name := ev.Package + "." + ev.Test
		if ev.Test != "" && ev.Action == "run" {
			running[name] = started
			started++
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			delete(running, name)
		}
		if ev.Action != "pass" && ev.Action != "fail" {
			continue
		}
		if ev.Test == "" {
			if ev.FailedBuild != "" {
				found.buildFailed = append(found.buildFailed, ev.Package)
				continue
			}
			if ev.Action == "fail" {
				found.failedPackages = append(found.failedPackages, ev.Package)
			}
			found.packages = append(found.packages, timing{ev.Package, toDuration(ev.Elapsed)})
			continue
		}
		if ev.Action == "fail" {
			found.failedTests++
		}
		found.tests = append(found.tests, timing{name, toDuration(ev.Elapsed)})
	}
	for name := range running {
		found.unfinished = append(found.unfinished, name)
	}
	sort.Slice(found.unfinished, func(i, j int) bool { return running[found.unfinished[i]] < running[found.unfinished[j]] })
	return found, scanner.Err()
}

func toDuration(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// overBudget returns the timings over limit, slowest first.
func overBudget(all []timing, limit time.Duration) []timing {
	var over []timing
	for _, t := range all {
		if t.elapsed > limit {
			over = append(over, t)
		}
	}
	sort.SliceStable(over, func(i, j int) bool { return over[i].elapsed > over[j].elapsed })
	return over
}

func write(w io.Writer, found results, testLimit, packageLimit time.Duration) error {
	slowTests := overBudget(found.tests, testLimit)
	slowPackages := overBudget(found.packages, packageLimit)

	var b strings.Builder
	b.WriteString("## Slow tests\n\n")
	fmt.Fprintf(&b, "Budget is %s per test and %s per package. %d of %d tests and %d of %d packages are over budget.\n",
		format(testLimit), format(packageLimit), len(slowTests), len(found.tests), len(slowPackages), len(found.packages))
	b.WriteString("Subtests count as tests, and a test's time includes its subtests. Skipped tests are left out.\n")
	if found.failedTests > 0 {
		fmt.Fprintf(&b, "\n%d test results failed in this run (a failed subtest also fails its parent). The times below include them.\n", found.failedTests)
	}
	if len(found.failedPackages) > 0 {
		fmt.Fprintf(&b, "\nPackages that failed: %s.\n", strings.Join(found.failedPackages, ", "))
	}
	if len(found.unfinished) > 0 {
		b.WriteString("\n### Tests that never finished\n\nThe package timed out or crashed while these ran, so they have no time.\n\n")
		for _, name := range found.unfinished {
			fmt.Fprintf(&b, "* %s\n", code(name))
		}
	}
	if len(slowPackages) > 0 {
		b.WriteString("\n### Packages over budget\n\n| Time | Package |\n| --- | --- |\n")
		for _, p := range slowPackages {
			fmt.Fprintf(&b, "| %s | %s |\n", format(p.elapsed), code(p.name))
		}
	}
	if len(slowTests) > 0 {
		b.WriteString("\n### Tests over budget\n\n| Time | Test |\n| --- | --- |\n")
		for _, t := range slowTests {
			fmt.Fprintf(&b, "| %s | %s |\n", format(t.elapsed), code(t.name))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func format(d time.Duration) string { return fmt.Sprintf("%.2fs", d.Seconds()) }

// code wraps a name in a Markdown code span so the run summary shows it literally; a pipe still needs escaping inside a table cell.
func code(s string) string { return "`" + strings.ReplaceAll(s, "|", `\|`) + "`" }
