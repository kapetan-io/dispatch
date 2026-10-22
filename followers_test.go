package dispatch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unreachable from the CLI: no command lists dispatch's own files, and that watch writes none is the claim.
func TestWatchWritesNoFileToTheStateDir(t *testing.T) {
	socket, _ := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.StartBeforeTheEvents()
	before := d.StateEntries()

	require.NoError(t, d.Run("watch", "ENG-1").Err)
	require.NoError(t, d.Run("unwatch", "ENG-1").Err)

	assert.Equal(t, before, d.StateEntries())
}

func TestFollowersAreForgottenWhenDispatchRestarts(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.StartBeforeTheEvents()
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Restart()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()

	assert.Empty(t, lines)
	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
}
