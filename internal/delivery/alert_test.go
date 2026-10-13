package delivery_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/delivery"
)

const (
	envArgs   = "OSASCRIPT_TEST_ARGS"
	envStderr = "OSASCRIPT_TEST_STDERR"
	envExit   = "OSASCRIPT_TEST_EXIT"
)

// The test binary stands in for osascript when envArgs is set, so the
// adapter's command line is tested without a shell script.
func TestMain(m *testing.M) {
	if file, ok := os.LookupEnv(envArgs); ok {
		if err := os.WriteFile(file, []byte(strings.Join(os.Args[1:], "\x00")), 0o644); err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprint(os.Stderr, os.Getenv(envStderr))
		if os.Getenv(envExit) == "1" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNotificationCenterPassesTheTextAsAScriptArgument(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	argsFile := t.TempDir() + "/args"
	t.Setenv(envArgs, argsFile)

	err = delivery.NewNotificationCenter(exe).Show(context.Background(), `ENG-1 needs "you"`)

	require.NoError(t, err)
	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Split(string(got), "\x00")
	assert.Equal(t, []string{`ENG-1 needs "you"`, "dispatch"}, args[len(args)-2:])
	// The text must reach osascript only as a trailing argument, never inside a -e script line.
	assert.NotContains(t, strings.Join(args[:len(args)-2], " "), "needs")
}

func TestNotificationCenterReportsWhatOsascriptSaid(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	t.Setenv(envArgs, t.TempDir()+"/args")
	t.Setenv(envExit, "1")
	t.Setenv(envStderr, "not allowed\n")

	err = delivery.NewNotificationCenter(exe).Show(context.Background(), "x")

	require.ErrorContains(t, err, "exit status 1: not allowed")
}

func TestNotificationCenterShowsTextThatLooksLikeAnOption(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	argsFile := t.TempDir() + "/args"
	t.Setenv(envArgs, argsFile)

	err = delivery.NewNotificationCenter(exe).Show(context.Background(), "-e")

	require.NoError(t, err)
	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Split(string(got), "\x00")
	assert.Equal(t, []string{"--", "-e", "dispatch"}, args[len(args)-3:])
}
