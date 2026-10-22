package pullrequest_test

// Package exception: this package wraps the gh tool, so every test here is an adapter or contract test of it.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/pullrequest"
)

const (
	envStdout = "GH_TEST_STDOUT"
	envStderr = "GH_TEST_STDERR"
	envExit   = "GH_TEST_EXIT"
	envArgs   = "GH_TEST_ARGS"
	envSleep  = "GH_TEST_SLEEP"
)

const onePage = `{"data":{"repository":{"pullRequests":{"nodes":[{"number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"a1","mergeable":"MERGEABLE","reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}]}}}}`

// The test binary stands in for the gh command when envExit is set, so the
// adapter's command lines and its reading of gh output are tested without a
// shell script.
func TestMain(m *testing.M) {
	// The race detector sleeps 1s at exit by default. The stand-in processes this binary re-execs inherit this setting.
	_ = os.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	if code, ok := os.LookupEnv(envExit); ok {
		if path := os.Getenv(envArgs); path != "" {
			f, _ := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			args := os.Args[1:]
			for i, arg := range args {
				if strings.HasPrefix(arg, "query=") {
					args[i] = "query=..."
				}
			}
			_, _ = fmt.Fprintln(f, strings.Join(args, " "))
			_ = f.Close()
		}
		if d, err := time.ParseDuration(os.Getenv(envSleep)); err == nil {
			time.Sleep(d)
		}
		_, _ = fmt.Fprint(os.Stdout, os.Getenv(envStdout))
		_, _ = fmt.Fprint(os.Stderr, os.Getenv(envStderr))
		if code != "0" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// standIn makes the test binary answer as gh and returns the file that records
// the arguments of each call.
func standIn(t *testing.T, stdout, stderr, exit string) (exe, args string) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	args = t.TempDir() + "/args"
	t.Setenv(envExit, exit)
	t.Setenv(envStdout, stdout)
	t.Setenv(envStderr, stderr)
	t.Setenv(envArgs, args)
	return exe, args
}

func recorded(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestCLIMergeNamesThePRAndPinsItsHead(t *testing.T) {
	exe, args := standIn(t, "", "", "0")

	err := pullrequest.NewCLI(exe).Merge(context.Background(), "https://github.com/o/r/pull/7", "a1")

	require.NoError(t, err)
	assert.Equal(t, []string{"pr merge https://github.com/o/r/pull/7 --merge --match-head-commit a1"}, recorded(t, args))
}

func TestCLIMergeRefusedCarriesGHsMessage(t *testing.T) {
	exe, _ := standIn(t, "", "Head branch was modified. Review and try the merge again.\n", "1")

	err := pullrequest.NewCLI(exe).Merge(context.Background(), "https://github.com/o/r/pull/7", "a1")

	require.ErrorContains(t, err, "while running gh pr merge https://github.com/o/r/pull/7")
	require.ErrorContains(t, err, "Head branch was modified")
}

func TestCLIReadAsksGHForTheBranchsOpenPRs(t *testing.T) {
	exe, args := standIn(t, onePage, "", "0")

	pr, found, err := pullrequest.NewCLI(exe).Read(context.Background(), t.TempDir(), "thrawn01/eng-1", pullrequest.Rules{})

	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "a1", pr.Head)
	call := recorded(t, args)[0]
	assert.True(t, strings.HasPrefix(call, "api graphql -F owner={owner} -F repo={repo} -f branch=thrawn01/eng-1 -F threads=100 -f query=..."), call)
	assert.NotContains(t, call, "after=")
}

func TestCLIReadReportsADraft(t *testing.T) {
	exe, _ := standIn(t, `{"data":{"repository":{"pullRequests":{"nodes":[{"number":7,"url":"https://github.com/o/r/pull/7","isDraft":true,"headRefOid":"a1","mergeable":"MERGEABLE","reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}]}}}}`, "", "0")

	pr, found, err := pullrequest.NewCLI(exe).Read(context.Background(), t.TempDir(), "b", pullrequest.Rules{})

	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, pr.Draft)
}

func TestCLIReadFollowsTheCursorToTheNextPage(t *testing.T) {
	const fork = `{"data":{"repository":{"pullRequests":{"pageInfo":{"hasNextPage":true,"endCursor":"c1"},"nodes":[{"number":9,"url":"https://github.com/o/r/pull/9","isCrossRepository":true,"headRefOid":"evil"}]}}}}`
	exe, args := standIn(t, fork, "", "0")
	repo := t.TempDir()

	// The stand-in answers every call alike, so the walk stops at the page limit.
	_, _, err := pullrequest.NewCLI(exe).Read(context.Background(), repo, "b", pullrequest.Rules{})

	require.ErrorContains(t, err, "none from this repo")
	calls := recorded(t, args)
	require.Len(t, calls, 5)
	assert.NotContains(t, calls[0], "after=")
	assert.Contains(t, calls[1], "-f after=c1")
}

func TestCLIReadsWhatGHSaysWhenItFails(t *testing.T) {
	for _, test := range []struct {
		name    string
		stdout  string
		stderr  string
		exit    string
		wantErr string
	}{
		{name: "BadCredentials", stderr: "HTTP 401: Bad credentials", exit: "1", wantErr: "HTTP 401: Bad credentials"},
		{name: "BadJSON", stdout: "not json", exit: "0", wantErr: "while reading gh output for b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			exe, _ := standIn(t, test.stdout, test.stderr, test.exit)

			_, _, err := pullrequest.NewCLI(exe).Read(context.Background(), t.TempDir(), "b", pullrequest.Rules{})

			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestCLICutsOffACallThatHangs(t *testing.T) {
	exe, _ := standIn(t, onePage, "", "0")
	t.Setenv(envSleep, "20s")
	cli := pullrequest.NewCLI(exe, pullrequest.WithTimeout(300*time.Millisecond))
	start := time.Now()

	_, _, readErr := cli.Read(context.Background(), t.TempDir(), "b", pullrequest.Rules{})
	mergeErr := cli.Merge(context.Background(), "https://github.com/o/r/pull/7", "a1")

	assert.Less(t, time.Since(start), 5*time.Second)
	require.Error(t, readErr)
	require.Error(t, mergeErr)
}

func TestCLIReadAsksForOlderCommentsWhenRulesIgnoreTheNewest(t *testing.T) {
	const newest = `{"data":{"repository":{"pullRequests":{"nodes":[{"number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"a1","mergeable":"MERGEABLE","comments":{"nodes":[{"createdAt":"2026-10-09T12:00:00Z","author":{"login":"Bot"}}],"pageInfo":{"hasPreviousPage":true,"startCursor":"c9"}},"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}]}}}}`
	exe, args := standIn(t, newest, "", "0")

	// The stand-in answers every call alike, so the older page it gets back is empty.
	_, _, err := pullrequest.NewCLI(exe).Read(context.Background(), t.TempDir(), "b", pullrequest.Rules{IgnoreAuthors: []string{"bot"}})

	require.NoError(t, err)
	calls := recorded(t, args)
	require.Len(t, calls, 2)
	assert.True(t, strings.HasPrefix(calls[1], "api graphql -F owner={owner} -F repo={repo} -F number=7 -f before=c9 -f query=..."), calls[1])
}

func TestCLIReadAsksForOlderReviewsWhenRulesIgnoreTheNewest(t *testing.T) {
	const newest = `{"data":{"repository":{"pullRequests":{"nodes":[{"number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"a1","mergeable":"MERGEABLE","reviews":{"nodes":[{"state":"COMMENTED","body":"x","createdAt":"2026-10-09T12:00:00Z","author":{"login":"Bot"}}],"pageInfo":{"hasPreviousPage":true,"startCursor":"r9"}},"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}]}}}}`
	exe, args := standIn(t, newest, "", "0")

	_, _, err := pullrequest.NewCLI(exe).Read(context.Background(), t.TempDir(), "b", pullrequest.Rules{IgnoreAuthors: []string{"BOT"}})

	require.NoError(t, err)
	calls := recorded(t, args)
	require.Len(t, calls, 2)
	assert.True(t, strings.HasPrefix(calls[1], "api graphql -F owner={owner} -F repo={repo} -F number=7 -f before=r9 -f query=..."), calls[1])
}

func TestCLIReadAsksForNoOlderPagesWhenAnUnlistedItemIsNewest(t *testing.T) {
	const newest = `{"data":{"repository":{"pullRequests":{"nodes":[{"number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"a1","mergeable":"MERGEABLE","comments":{"nodes":[{"createdAt":"2026-10-09T12:00:00Z","author":{"login":"alice"}}],"pageInfo":{"hasPreviousPage":true,"startCursor":"c9"}},"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}]}}}}`
	exe, args := standIn(t, newest, "", "0")

	_, _, err := pullrequest.NewCLI(exe).Read(context.Background(), t.TempDir(), "b", pullrequest.Rules{IgnoreAuthors: []string{"bot"}})

	require.NoError(t, err)
	assert.Len(t, recorded(t, args), 1)
}

func TestCLIReadAsksForTheChecksByNumberWhenOnlyRequiredOnesCount(t *testing.T) {
	const failing = `{"data":{"repository":{"pullRequests":{"nodes":[{"number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"a1","mergeable":"MERGEABLE","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]},"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}]}}}}`
	exe, args := standIn(t, failing, "", "0")

	// The stand-in answers the checks query with the PR page, which names no commit.
	pr, found, err := pullrequest.NewCLI(exe).Read(context.Background(), t.TempDir(), "b", pullrequest.Rules{RequiredChecksOnly: true})

	require.NoError(t, err)
	require.True(t, found)
	assert.False(t, pr.Ready)
	calls := recorded(t, args)
	require.Len(t, calls, 2)
	assert.Equal(t, "api graphql -F owner={owner} -F repo={repo} -F number=7 -f query=...", calls[1])
}

func TestCLIReadAsksForNoChecksWhenEveryCheckCounts(t *testing.T) {
	const failing = `{"data":{"repository":{"pullRequests":{"nodes":[{"number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"a1","mergeable":"MERGEABLE","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]},"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}]}}}}`
	exe, args := standIn(t, failing, "", "0")

	pr, _, err := pullrequest.NewCLI(exe).Read(context.Background(), t.TempDir(), "b", pullrequest.Rules{})

	require.NoError(t, err)
	assert.False(t, pr.Ready)
	assert.Len(t, recorded(t, args), 1)
}
