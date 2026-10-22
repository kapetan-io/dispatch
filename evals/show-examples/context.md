# Session so far

You are in a design session (`/dispatch:design ENG-506`) in the tiger repo, a Go static-analysis CLI. The user redirected ENG-506 from splitting CLI subcommands to restructuring the whole project around domain concepts, with no change to tiger's usage, following the dispatch principles skills (problem domains, fast tests, surface testing).

Facts you established earlier with tools:
- `go test -count=1 ./internal/cli` takes 178s, and the `tiger changed` test files take 133s of it.
- `internal/cli` imports 58 tiger packages, every analyzer included. `internal/changed` depends only on `internal/callinventory` and `internal/revision`.
- The exit code constants (ExitClean=0 and others) live in `internal/cli`.

Files you read:

`internal/cli/cli.go` as read at line 99 (excerpt):

```go
// Run executes one tiger invocation and returns its exit code.
func Run(args []string, streams Streams) int {
	if len(args) == 0 {
		fmt.Fprint(streams.Stderr, usage)
		return ExitOperational
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:], streams)
	case "budget":
		return runBudget(args[1:], streams)
	case "golangci":
		return runGolangci(args[1:], streams)
	case "pattern":
		return runPattern(args[1:], streams)
	case "changed":
		return runChanged(args[1:], streams)
	case "pin":
		return runPin(args[1:], streams)
	case "help", "-h", "--help":
		fmt.Fprint(streams.Stdout, usage)
		return ExitClean
	default:
		fmt.Fprintf(streams.Stderr, "tiger: unknown command %q\n", args[0])
		fmt.Fprint(streams.Stderr, usage)
		return ExitOperational
	}
}
```

`internal/cli/changed.go` as read at line 99 (excerpt):

```go
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/kapetan-io/tiger/internal/callinventory"
	"github.com/kapetan-io/tiger/internal/changed"
	"github.com/kapetan-io/tiger/internal/revision"
)

// runChanged is tiger changed: compare the calls each function makes at the
// merge base of a base ref and HEAD against the calls it makes in the working
// tree, and print the report. A report exits 0 whatever it holds. When none
// can be produced it prints one line to stderr and exits 2, with nothing on
// stdout, so a failed run is never mistaken for an empty report.
func runChanged(args []string, streams Streams) int {
	flags := flag.NewFlagSet("tiger changed", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	chdir := flags.String("C", ".", "run as if tiger was started in this directory")
	format := flags.String("format", "text", "report format: text, markdown or json")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			err = errors.New("usage: tiger changed [-C dir] [--format text|markdown|json] <base>")
		}
		return changedFailure(streams, err)
	}
	if flags.NArg() == 0 {
		return changedFailure(streams, errors.New(
			"expected a base revision, as in: tiger changed [-C dir] <base>"))
	}
	if flags.NArg() > 1 {
		return changedFailure(streams, fmt.Errorf(
			"%q comes after the base revision; put flags before it, "+
				"as in: tiger changed [-C dir] <base>",
```

Test helper in `internal/cli/cli_test.go` as read at line 189 (excerpt):

```go
package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/tiger/internal/cli"
	"github.com/kapetan-io/tiger/internal/directive"
)

// outcome is one CLI invocation's observable result.
type outcome struct {
	code   int
	stdout string
	stderr string
}

// run invokes the CLI surface once.
func run(t *testing.T, args ...string) outcome {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cli.Run(args, cli.Streams{Stdout: stdout, Stderr: stderr})
	return outcome{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// TestCheckCleanTreeIsSilent covers the exit-0 contract.
//
// Goal: a conforming package produces no output and exit code 0.
func TestCheckCleanTreeIsSilent(t *testing.T) {
	got := run(t, "check", "-C", "testdata/fixtures/clean", "./...")
	assert.Equal(t, cli.ExitClean, got.code)
	assert.Empty(t, got.stdout)
	assert.Empty(t, got.stderr)
```

`go list -deps` output at line 168 (first lines):

```
changed: internal/callinventory internal/revision internal/changed 
callinventory: internal/callinventory 
revision: internal/revision 
pattern: internal/pattern 
```

# Your previous message to the user

I rewrote ENG-506 around restructuring the project by domain concept: https://linear.app/kapetan-io/issue/ENG-506/restructure-the-codebase-around-domain-concepts. It records your three calls: the whole project is in scope, tiger's usage doesn't change, and the design follows the `dispatch:principles` skills. I also corrected one sentence of mine that claimed `internal/pins` pulls in the analyzers. It doesn't.

**Next question: how should tests reach each domain concept once it has its own package?** Two of your principles pull in opposite directions here. Surface testing says enter at the highest surface, which is `cli.Run`. But `cli.Run` links every analyzer, so a test that calls it rebuilds and reruns whenever any analyzer changes. That's why the 133s of `tiger changed` tests rerun today on PRs that never touch `tiger changed`.

I'd have each domain package own the command for its concept, as an exported function that takes the arguments after the subcommand name and returns the exit code. `cli.Run` keeps only the name lookup and the usage text. A `tiger changed` test would call `changed.Run([]string{"main"}, streams)` instead of `cli.Run([]string{"changed", "main"}, streams)`. That's still the command line, with the same arguments, output and exit code, minus the one dispatch step. The `tiger changed` tests would then link only `changed`, `callinventory` and `revision`, so they stay cached unless that code changes. A few tests in `internal/cli` would cover the dispatch itself.

The alternative keeps every test on `cli.Run`. It's cleaner by the surface-testing rule but gives up the caching. Does the per-domain command entry work for you?
