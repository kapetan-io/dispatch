package dispatch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/linear"
)

// keyScript writes an executable that appends a line to a counter file on each
// run and prints body, and returns the shell command that runs it.
func keyScript(t *testing.T, dir, body string) (command, counter string) {
	t.Helper()
	counter = filepath.Join(dir, "runs")
	script := filepath.Join(dir, "key.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho run >> "+counter+"\n"+body+"\n"), 0o755))
	return script, counter
}

func runCount(t *testing.T, counter string) int {
	t.Helper()
	raw, err := os.ReadFile(counter)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return len(raw) / len("run\n")
}

func TestApiKeyCommandSuppliesTheLinearKey(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	command, _ := keyScript(t, h.dir, "echo test-key")
	h.config("[plugins.linear]\napi_key_command = \"" + command + "\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick")

	require.NoError(t, err)
	assert.Len(t, h.launches(), 1)
}

func TestApiKeyCommandWinsOverTheKeychain(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	command, counter := keyScript(t, h.dir, "echo test-key")
	h.config("[plugins.linear]\napi_key_command = \"" + command + "\"\napi_key_keychain = \"no-such-service\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick")

	require.NoError(t, err)
	assert.Equal(t, 1, runCount(t, counter))
}

func TestApiKeyCommandYieldsToTheEnvironmentKey(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	t.Setenv("LINEAR_API_KEY", "test-key")
	command, counter := keyScript(t, h.dir, "echo other-key")
	h.config("[plugins.linear]\napi_key_command = \"" + command + "\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick")

	require.NoError(t, err)
	assert.Equal(t, 0, runCount(t, counter))
}

func TestApiKeyCommandFailureNamesCommandAndStatus(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"ExitStatus", "echo secret-value\nexit 3", "exit status 3"},
		{"PrintsNothing", "true", "printed nothing (exit status 0)"},
		{"PrintsBlank", "echo '   '", "printed nothing (exit status 0)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.useConfiguredKey(t)
			command, _ := keyScript(t, h.dir, test.body)
			h.config("[plugins.linear]\napi_key_command = \"" + command + "\"\n")

			_, err := h.run("tick")

			require.ErrorContains(t, err, command)
			require.ErrorContains(t, err, test.want)
			assert.NotContains(t, err.Error(), "secret-value")
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestApiKeyCommandRunsOncePerRunProcess(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	command, counter := keyScript(t, h.dir, "echo test-key")
	h.config("[plugins.linear]\napi_key_command = \"" + command + "\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	h.tick()
	callsAfter1 := h.linear.calls()
	h.tick()
	h.advance(5 * time.Minute)

	require.Greater(t, h.linear.calls(), callsAfter1)
	assert.Equal(t, 1, runCount(t, counter))
}

func TestLinearViewerPrintsTheKeyOwner(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	command, _ := keyScript(t, h.dir, "echo test-key")
	h.config("[plugins.linear]\napi_key_command = \"" + command + "\"\n")

	out, err := h.run("linear-viewer")

	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"viewer-1","name":"Ada Lovelace"}`, out)
}

func TestLinearViewerFailsWithoutAKey(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	command, _ := keyScript(t, h.dir, "exit 1")
	h.config("[plugins.linear]\napi_key_command = \"" + command + "\"\n")

	out, err := h.run("linear-viewer")

	require.ErrorContains(t, err, "api_key_command")
	assert.Empty(t, out)
}

func TestApiKeyCommandRunsAgainOnTheNextStartAfterLinearRejectsTheKey(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	command, counter := keyScript(t, h.dir, `if [ "$(wc -l < `+filepath.Join(h.dir, "runs")+`)" -le 1 ]; then echo bad; else echo test-key; fi`)
	h.config("[plugins.linear]\napi_key_command = \"" + command + "\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	// Linear answers 401 to the first key, so only the rerun's key can reach it.
	_, err := h.run("run")
	require.ErrorIs(t, err, linear.ErrUnauthorized)
	_, err = h.run("tick")

	require.NoError(t, err)
	assert.Len(t, h.launches(), 1)
	assert.Equal(t, 2, runCount(t, counter))
}

func TestApiKeyCommandStopsWhenRunIsCancelled(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	// The sleep is a grandchild of the shell that holds the command's stdout.
	command, counter := keyScript(t, h.dir, "sleep 30")
	h.config("[plugins.linear]\napi_key_command = \"" + command + "\"\n")
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	done := make(chan error, 1)
	go func() {
		_, _, err := h.runWith(ctx, dispatch.Options{}, "run")
		done <- err
	}()
	require.Eventually(t, func() bool { return runCount(t, counter) == 1 }, 5*time.Second, 10*time.Millisecond)
	stop()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("dispatch run did not return within 3s of cancel")
	}
}

func TestApiKeyCommandThatBlocksTimesOutAndTheNextStartRetries(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	command, counter := keyScript(t, h.dir, `if [ "$(wc -l < `+filepath.Join(h.dir, "runs")+`)" -le 1 ]; then sleep 30; fi; echo test-key`)
	h.config("[plugins.linear]\napi_key_command = \"" + command + "\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, _, err := h.runWith(context.Background(), dispatch.Options{KeyCommandTimeout: time.Second}, "tick")

	require.ErrorContains(t, err, command)
	require.ErrorContains(t, err, "timed out after 1s")
	_, _, err = h.runWith(context.Background(), dispatch.Options{KeyCommandTimeout: time.Second}, "tick")
	require.NoError(t, err)
	assert.Len(t, h.launches(), 1)
	assert.Equal(t, 2, runCount(t, counter))
}
