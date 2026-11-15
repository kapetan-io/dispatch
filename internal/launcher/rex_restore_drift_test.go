//go:build drift

package launcher_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// TestRexRestoreResumesARecordedSessionAfterARexRestart runs the pane record hook and `dispatch
// rex restore` against a scratch Rex server, with the stand-in Claude as the claude the pane's
// shell finds. The server's HOME is empty so the shell reads none of the maintainer's own config.
func TestRexRestoreResumesARecordedSessionAfterARexRestart(t *testing.T) {
	const claudeSession = "6f0c1e2a-0000-4000-8000-0000000000b2"
	work, err := os.MkdirTemp("/tmp", "rr")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	work, err = filepath.EvalSymlinks(work)
	require.NoError(t, err)
	env := herdrtest.CleanEnv().With("PATH", herdrtest.StandInClaude(t)+string(os.PathListSeparator)+os.Getenv("PATH")).
		With("HOME", t.TempDir()).With("STANDIN_NO_DIALOGS", "1")
	server := herdrtest.StartRexServer(t, env)
	out, err := server.Output("api", "call", "session.create", `{"label":"mine","initial_windows":[{"window_label":"notes","layout":{"block":{"flavor":"com.superlogical.terminal.shell","label":"shell"}}}]}`)
	require.NoError(t, err)
	var made struct {
		Session string `json:"session_id"`
		Windows []struct {
			Blocks []string `json:"block_ids"`
		} `json:"initial_windows"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &made))
	require.Len(t, made.Windows, 1)
	require.Len(t, made.Windows[0].Blocks, 1)
	block := made.Windows[0].Blocks[0]
	state := t.TempDir()
	run := func(stdin string, env []string, args ...string) (string, error) {
		var stdout bytes.Buffer
		err := dispatch.Run(context.Background(), args, dispatch.Options{
			StateDir: state, ConfigPath: filepath.Join(state, "missing.toml"), RexSocket: server.Socket,
			Env: env, Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: os.Stderr,
		})
		return stdout.String(), err
	}

	_, err = run(`{"session_id":"`+claudeSession+`","cwd":"`+work+`"}`, server.Env.With("REX_SESSION", made.Session).With("REX_BLOCK", block), "record-start")
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(state, "panes", made.Session, block+".json"))
	server.Restart(t)

	var restored string
	// A shell that has not started yet reads as busy, so restore is run again until it finds the prompt.
	require.Eventually(t, func() bool {
		restored, err = run("", server.Env, "rex", "restore")
		return err == nil && strings.HasPrefix(restored, "resumed")
	}, 30*time.Second, 200*time.Millisecond)
	assert.Equal(t, "resumed       mine / notes  "+claudeSession+"\n", restored)
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(work, "standin-claude.json"))
		if err != nil {
			return false
		}
		var recorded struct {
			Args []string `json:"args"`
		}
		// The maintainer's shell may find dispatch's claude shim first, which adds its own flags after these.
		return json.Unmarshal(b, &recorded) == nil && strings.HasPrefix(strings.Join(recorded.Args, " "), "--resume "+claudeSession)
	}, 20*time.Second, 100*time.Millisecond)

	again, err := run("", server.Env, "rex", "restore")

	require.NoError(t, err)
	assert.Equal(t, "busy          mine / notes\n", again)
}
