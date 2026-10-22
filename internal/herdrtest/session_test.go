package herdrtest_test

// Package exception: this package is a fake of herdr, so every test here is a contract test of the fake.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

const envSessions = "HERDR_TEST_SESSIONS"

// The test binary stands in for herdr when it runs under that name, so the
// session listing is tested without a shell script. It prints envSessions as
// the listing, or fails when that is unset.
func TestMain(m *testing.M) {
	// The race detector sleeps 1s at exit by default. The stand-in processes this binary re-execs inherit this setting.
	_ = os.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	if filepath.Base(os.Args[0]) == "herdr" {
		listing, ok := os.LookupEnv(envSessions)
		if !ok {
			fmt.Fprintln(os.Stderr, "server not reachable")
			os.Exit(1)
		}
		fmt.Print(listing)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func standInHerdr(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Symlink(exe, filepath.Join(dir, "herdr")))
	t.Setenv("PATH", dir)
}

func TestLeftoversFailsWhenHerdrCannotListSessions(t *testing.T) {
	standInHerdr(t)

	left, err := herdrtest.Leftovers(os.Getpid())

	require.ErrorContains(t, err, "while listing herdr sessions")
	assert.Empty(t, left)
}

func TestLeftoversListsOnlyThisProcessesThrowawaySessions(t *testing.T) {
	standInHerdr(t)
	pid := os.Getpid()
	t.Setenv(envSessions, fmt.Sprintf(`name                 status   directory   socket
default              running  /h          /h/herdr.sock
dispatch-drift-%d-17  running  /h          /h/a.sock
dispatch-drift-%d-18  stopped  /h          /h/b.sock
dispatch-drift-1-19   running  /h          /h/c.sock
`, pid, pid))

	left, err := herdrtest.Leftovers(pid)

	require.NoError(t, err)
	assert.Equal(t, []string{fmt.Sprintf("dispatch-drift-%d-17", pid), fmt.Sprintf("dispatch-drift-%d-18", pid)}, left)
}
