package herdr_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

func TestRexPanesListsEachBlockWithWhetherItIsAtItsPromptAndWhetherItHoldsAWorker(t *testing.T) {
	r := newStandInRig(t)
	var session, idle, busy, exited, worker string
	r.edit(t, func(s *standInRex) {
		session, idle = s.block("claude", "Notes", standInBlock{Label: "shell", Argv0: "-fish", AtPrompt: true})
		_, busy = s.block("claude", "Dr Claude", standInBlock{Label: "Dr Claude", Argv0: "claude"})
		_, exited = s.block("claude", "Old", standInBlock{Label: "shell", Argv0: "-fish", AtPrompt: true, LastExit: true})
		_, worker = s.block("claude", "ENG-1 title", standInBlock{Label: workerLabelOf("01234567", "/tree"), Argv0: "zsh", AtPrompt: true})
	})

	panes, err := r.rex.Panes(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []herdr.RexPane{
		{ID: session + "/" + idle, Session: "claude", Window: "Notes", AtPrompt: true},
		{ID: session + "/" + busy, Session: "claude", Window: "Dr Claude"},
		{ID: session + "/" + exited, Session: "claude", Window: "Old"},
		{ID: session + "/" + worker, Session: "claude", Window: "ENG-1 title", AtPrompt: true, Worker: true},
	}, panes)
}

func TestRexPanesFailsWhenRexIsNotRunning(t *testing.T) {
	r := newStandInRig(t)
	r.edit(t, func(s *standInRex) { s.Down = true })

	_, err := r.rex.Panes(context.Background())

	require.ErrorIs(t, err, herdr.ErrRexNotRunning)
}

func TestRexInstanceIsTheStartOfTheServersInstanceID(t *testing.T) {
	r := newStandInRig(t)

	instance, err := r.rex.Instance(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "01234567", instance)
}

func TestRexTypeLineTypesTheLineAndThenPressesEnter(t *testing.T) {
	r := newStandInRig(t)
	var session, block string
	r.edit(t, func(s *standInRex) {
		session, block = s.block("claude", "Notes", standInBlock{Label: "shell", Argv0: "-fish", AtPrompt: true})
	})

	err := r.rex.TypeLine(context.Background(), session+"/"+block, "cd '/work' && claude --resume abc")

	require.NoError(t, err)
	var writes []string
	r.edit(t, func(s *standInRex) { writes = s.find(session, block).Writes })
	assert.Equal(t, []string{"cd '/work' && claude --resume abc", "\r"}, writes)
}

func TestRexTypeLineRefusesABlockThatIsGone(t *testing.T) {
	r := newStandInRig(t)
	var session string
	r.edit(t, func(s *standInRex) {
		session, _ = s.block("claude", "Notes", standInBlock{Label: "shell", Argv0: "-fish", AtPrompt: true})
	})

	err := r.rex.TypeLine(context.Background(), session+"/missing", "ls")

	var refusal *herdr.Refusal
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, "not_found", refusal.Code)
}
