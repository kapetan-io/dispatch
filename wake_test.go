package dispatch_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

// wake runs the Notification or UserPromptSubmit hook as a session working in dir.
func (h *harness) wake(dir string) (string, error) {
	h.t.Helper()
	input := fmt.Sprintf(`{"hook_event_name":"Notification","notification_type":"idle_prompt","session_id":"worker","cwd":%q}`, dir)
	out, _, err := h.runWith(context.Background(), dispatch.Options{Stdin: strings.NewReader(input)}, "wake")
	return out, err
}

func TestWakeRelabelsAnIdleWorkerWithoutWaitingForTheNextResync(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tick()
	h.recordStop("ENG-1", noTasks)

	out, err := h.wake(filepath.Join(h.dir, "trees", "ENG-1"))

	require.NoError(t, err)
	assert.Empty(t, out)
	require.Eventually(t, func() bool { return len(h.renames()) == 2 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|💤 ENG-1"}, h.renames())
}

func TestWakeRelabelsAWorkerThePromptSentBackToWork(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)
	h.tick()

	h.setWorkerStatus("ENG-1", "working")
	_, err := h.wake(filepath.Join(h.dir, "trees", "ENG-1"))

	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(h.renames()) == 2 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, []string{"w-ENG-1|💤 ENG-1", "w-ENG-1|🟢 ENG-1"}, h.renames())
}

func TestWakeStaysSilentWhenNoLoopRuns(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)

	out, err := h.wake(filepath.Join(h.dir, "trees", "ENG-1"))

	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestWakeIgnoresASessionInTheCloneItself(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tick()
	h.recordStop("ENG-1", noTasks)

	_, err := h.wake(h.repo)

	require.NoError(t, err)
	assert.Never(t, func() bool { return len(h.renames()) > 1 }, 300*time.Millisecond, 10*time.Millisecond)
}

func TestWakeIgnoresASessionOutsideEveryRepo(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tick()
	h.recordStop("ENG-1", noTasks)

	_, err := h.wake(t.TempDir())

	require.NoError(t, err)
	assert.Never(t, func() bool { return len(h.renames()) > 1 }, 300*time.Millisecond, 10*time.Millisecond)
}

func TestWakeDoesNotRetryWhatFailedAtTheLastResync(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	h.worker("ENG-2", "working")
	h.launchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)
	_, err := h.run("tick")
	require.Error(t, err)
	require.Len(t, h.launches(), 1)
	h.recordStop("ENG-2", noTasks)

	_, err = h.wake(filepath.Join(h.dir, "trees", "ENG-2"))

	require.NoError(t, err)
	assert.Never(t, func() bool { return len(h.launches()) > 1 }, 300*time.Millisecond, 10*time.Millisecond)
}

func TestWakeKeepsANudgedWorkerGreenWhileHerdrStillSaysIdle(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, reviewTick(t, h))
	require.Equal(t, []string{prPrompt("a failed check")}, h.prompts())
	renames := h.renames()

	_, err := h.wake(filepath.Join(h.dir, "trees", "ENG-1"))

	require.NoError(t, err)
	assert.Never(t, func() bool { return len(h.prompts()) > 1 || len(h.renames()) > len(renames) }, 300*time.Millisecond, 10*time.Millisecond)
	assert.Equal(t, "w-ENG-1|🟢 ENG-1", h.renames()[len(h.renames())-1])
}
