package inbox_test

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

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/inbox"
)

const (
	envStdout = "GH_TEST_STDOUT"
	envStderr = "GH_TEST_STDERR"
	envExit   = "GH_TEST_EXIT"
	envArgs   = "GH_TEST_ARGS"
	envDir    = "GH_TEST_DIR"
	envSleep  = "GH_TEST_SLEEP"
)

// The test binary stands in for the gh command when envExit is set.
func TestMain(m *testing.M) {
	_ = os.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	if code, ok := os.LookupEnv(envExit); ok {
		if path := os.Getenv(envArgs); path != "" {
			wd, _ := os.Getwd()
			f, _ := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			_, _ = fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
			_, _ = fmt.Fprintln(f, wd)
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

const page = `[
 {"id":"3","unread":false,"reason":"mention","updated_at":"2026-10-31T09:18:30Z","subject":{"title":"Third","url":"https://api.github.com/repos/acme/widgets/issues/3","type":"Issue"},"repository":{"full_name":"acme/widgets"}},
 {"id":"1","unread":true,"reason":"review_requested","updated_at":"2026-10-31T09:20:00Z","subject":{"title":"First","url":"https://api.github.com/repos/acme/widgets/pulls/1","type":"PullRequest"},"repository":{"full_name":"acme/widgets"}},
 {"id":"2","unread":true,"reason":"subscribed","updated_at":"2026-10-31T09:19:00Z","subject":{"title":"Release","url":null,"type":"Release"},"repository":{"full_name":"acme/gadgets"}}
]`

func TestCLIReadAsksGHForTheOwnersInboxWithNoWorkingDirectory(t *testing.T) {
	exe, args := standIn(t, "[]", "", "0")
	wd, err := os.Getwd()
	require.NoError(t, err)

	items, err := inbox.NewCLI(exe).Read(context.Background())

	require.NoError(t, err)
	assert.Empty(t, items)
	b, err := os.ReadFile(args)
	require.NoError(t, err)
	assert.Equal(t, "api notifications --method GET -f all=true -f per_page=50\n"+wd+"\n", string(b))
}

func TestCLIReadCopiesGitHubsFieldsAndSortsNewestFirst(t *testing.T) {
	exe, _ := standIn(t, page, "", "0")

	items, err := inbox.NewCLI(exe).Read(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []inbox.Item{
		{ID: "1", Reason: "review_requested", Unread: true, UpdatedAt: "2026-10-31T09:20:00Z", RepoName: "acme/widgets", SubjectType: "PullRequest", Title: "First", APIURL: "https://api.github.com/repos/acme/widgets/pulls/1"},
		{ID: "2", Reason: "subscribed", Unread: true, UpdatedAt: "2026-10-31T09:19:00Z", RepoName: "acme/gadgets", SubjectType: "Release", Title: "Release"},
		{ID: "3", Reason: "mention", Unread: false, UpdatedAt: "2026-10-31T09:18:30Z", RepoName: "acme/widgets", SubjectType: "Issue", Title: "Third", APIURL: "https://api.github.com/repos/acme/widgets/issues/3"},
	}, items)
}

func TestCLIReadKeepsTheNewest50OfALongerPage(t *testing.T) {
	var elems []string
	for n := 1; n <= 60; n++ {
		elems = append(elems, fmt.Sprintf(`{"id":"%d","unread":true,"reason":"subscribed","updated_at":"2026-10-31T%02d:%02d:00Z","subject":{"title":"t","url":null,"type":"Issue"},"repository":{"full_name":"a/b"}}`, n, n/60, n%60))
	}
	exe, _ := standIn(t, "["+strings.Join(elems, ",")+"]", "", "0")

	items, err := inbox.NewCLI(exe).Read(context.Background())

	require.NoError(t, err)
	require.Len(t, items, 50)
	assert.Equal(t, "60", items[0].ID)
	assert.Equal(t, "11", items[49].ID)
}

func TestCLIReadFailsWithGHsMessage(t *testing.T) {
	for _, test := range []struct {
		name, stdout, stderr, exit, want string
	}{
		{name: "bad gateway", stderr: "gh: HTTP 502\n", exit: "1", want: "gh: HTTP 502"},
		{name: "unauthorized", stderr: "gh: HTTP 401\n", exit: "1", want: "gh: HTTP 401"},
		{name: "not json", stdout: "<html>", exit: "0", want: "while reading gh output for the inbox"},
		{name: "no id", stdout: `[{"unread":true,"updated_at":"2026-10-31T09:20:00Z","repository":{"full_name":"a/b"}}]`, exit: "0", want: "has no id"},
		{name: "no repository", stdout: `[{"id":"1","updated_at":"2026-10-31T09:20:00Z"}]`, exit: "0", want: "has no repository"},
		{name: "bad time", stdout: `[{"id":"1","updated_at":"yesterday","repository":{"full_name":"a/b"}}]`, exit: "0", want: "not RFC 3339"},
	} {
		t.Run(test.name, func(t *testing.T) {
			exe, _ := standIn(t, test.stdout, test.stderr, test.exit)

			items, err := inbox.NewCLI(exe).Read(context.Background())

			require.ErrorContains(t, err, test.want)
			assert.Nil(t, items)
		})
	}
}

func TestCLIReadGivesUpWhenTheCallerDoes(t *testing.T) {
	exe, _ := standIn(t, "[]", "", "0")
	t.Setenv(envSleep, "5s")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()

	_, err := inbox.NewCLI(exe, inbox.WithEnv(environment.Process())).Read(ctx)

	require.Error(t, err)
	assert.Less(t, time.Since(start), 3*time.Second)
}

func TestNewestKeepsTheNewest50AndBreaksTiesByID(t *testing.T) {
	var items []inbox.Item
	for n := range 60 {
		items = append(items, inbox.Item{ID: fmt.Sprintf("%02d", n), UpdatedAt: "2026-10-31T09:00:00Z"})
	}

	kept := inbox.Newest(items)

	require.Len(t, kept, 50)
	assert.Equal(t, "00", kept[0].ID)
	assert.Equal(t, "49", kept[49].ID)
}
