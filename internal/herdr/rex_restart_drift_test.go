//go:build drift

package herdr_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// restartProcess is the part of a block's process reply that says whether the block is at its prompt.
type restartProcess struct {
	Child      *struct{ PID int } `json:"child"`
	Foreground *struct {
		PID  int    `json:"pid"`
		Name string `json:"name"`
	} `json:"foreground"`
	Ancestors []struct{ PID int } `json:"ancestors"`
}

func readRestartProcess(t *testing.T, server *herdrtest.RexServer, session, block string) restartProcess {
	t.Helper()
	body, err := json.Marshal(map[string]any{"session_id": session, "block_id": block, "method": "process", "args": map[string]any{}})
	require.NoError(t, err)
	out, err := server.Output("api", "call", "-s", session, "block.method", string(body))
	require.NoError(t, err)
	var proc restartProcess
	require.NoError(t, json.Unmarshal([]byte(out), &proc))
	return proc
}

// A pane record is keyed by block id and restore types into the restored shell, so both rest on
// what a restart does to a block the user made, which carries no Worker label.
func TestRexRestartKeepsAUsersBlockAndItsShellTakesTypedText(t *testing.T) {
	server := herdrtest.StartRexServer(t, herdrtest.CleanEnv())
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
	// The shell has to be up before the restart, or the server has nothing of it to save.
	require.Eventually(t, func() bool {
		return readRestartProcess(t, server, made.Session, block).Foreground != nil
	}, 20*time.Second, 100*time.Millisecond)

	server.Restart(t)

	view, err := server.Output("api", "call", "-s", made.Session, "session.view")
	require.NoError(t, err)
	assert.Contains(t, view, `"block_id":"`+block+`"`)
	assert.Contains(t, view, `"label":"notes"`)
	// At its prompt: the foreground is the block's shell, with nothing between it and the block's child.
	var idle restartProcess
	require.Eventually(t, func() bool {
		idle = readRestartProcess(t, server, made.Session, block)
		return idle.Foreground != nil && idle.Child != nil
	}, 20*time.Second, 100*time.Millisecond)
	assert.LessOrEqual(t, len(idle.Ancestors), 1)

	server.Type(t, made.Session+"/"+block, "sleep 300")

	require.Eventually(t, func() bool {
		proc := readRestartProcess(t, server, made.Session, block)
		return proc.Foreground != nil && proc.Foreground.Name == "sleep" && len(proc.Ancestors) > len(idle.Ancestors)
	}, 20*time.Second, 100*time.Millisecond)
}
