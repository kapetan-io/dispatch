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
	h.config("[linear]\napi_key_command = \"" + command + "\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, err := h.run("tick", "--dry-run")

	require.NoError(t, err)
	assert.Equal(t, "launch ENG-1\n", out)
}

func TestApiKeyCommandWinsOverTheKeychain(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	command, counter := keyScript(t, h.dir, "echo test-key")
	h.config("[linear]\napi_key_command = \"" + command + "\"\napi_key_keychain = \"no-such-service\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick", "--dry-run")

	require.NoError(t, err)
	assert.Equal(t, 1, runCount(t, counter))
}

func TestApiKeyCommandYieldsToTheEnvironmentKey(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	t.Setenv("LINEAR_API_KEY", "test-key")
	command, counter := keyScript(t, h.dir, "echo other-key")
	h.config("[linear]\napi_key_command = \"" + command + "\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick", "--dry-run")

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
			h.config("[linear]\napi_key_command = \"" + command + "\"\n")

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
	h.config("[linear]\napi_key_command = \"" + command + "\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	done := make(chan error, 1)
	go func() {
		_, _, err := h.runWith(ctx, dispatch.Options{}, "run", "--interval", "20ms")
		done <- err
	}()

	// Several ticks have run once the Linear fake has answered several times.
	require.Eventually(t, func() bool { return h.linear.calls() >= 3 }, 5*time.Second, 10*time.Millisecond)
	stop()
	require.NoError(t, <-done)
	assert.Equal(t, 1, runCount(t, counter))
}

func TestLinearViewerPrintsTheKeyOwner(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	command, _ := keyScript(t, h.dir, "echo test-key")
	h.config("[linear]\napi_key_command = \"" + command + "\"\n")

	out, err := h.run("linear-viewer")

	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"viewer-1","name":"Ada Lovelace"}`, out)
}

func TestLinearViewerFailsWithoutAKey(t *testing.T) {
	h := newHarness(t)
	h.useConfiguredKey(t)
	command, _ := keyScript(t, h.dir, "exit 1")
	h.config("[linear]\napi_key_command = \"" + command + "\"\n")

	out, err := h.run("linear-viewer")

	require.ErrorContains(t, err, "api_key_command")
	assert.Empty(t, out)
}
