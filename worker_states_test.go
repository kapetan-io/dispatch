package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

func TestTickLabelsEveryRowOfTheInReviewTableWhoseWorkerIsGoneOrUnconfirmed(t *testing.T) {
	pending := prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"}
	failed := prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"}
	unresolved := prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1}

	for _, test := range []struct {
		name        string
		agent       string
		unconfirmed bool
		pr          prState
		want        string
	}{
		{name: "UnconfirmedChecksRunning", agent: "idle", unconfirmed: true, pr: pending, want: "🟡"},
		{name: "UnconfirmedFailedCheck", agent: "idle", unconfirmed: true, pr: failed, want: "🟢"},
		{name: "UnconfirmedUnresolvedThread", agent: "idle", unconfirmed: true, pr: unresolved, want: "🟢"},
		{name: "GoneFailedCheck", pr: failed, want: "🟢"},
		{name: "GoneUnresolvedThread", pr: unresolved, want: "🟢"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", test.agent)
			h.recordStop("ENG-1", noTasks)
			if test.unconfirmed {
				markLaunch(t, h.state, filepath.Join(h.dir, "trees", "ENG-1"), h.clock.Now())
			}
			h.pr("ENG-1", test.pr)

			h.tick()

			renames := h.renames()
			require.NotEmpty(t, renames)
			assert.Equal(t, "w-ENG-1|"+test.want+" ENG-1", renames[len(renames)-1])
		})
	}
}

func TestTickShowsAnIdleStoppedInReviewWorkerAsWaitingOnTheUserWhenItsPRHasNothingForTheWorkerToDo(t *testing.T) {
	for _, test := range []struct {
		name string
		pr   prState
	}{
		{name: "UnresolvedThread", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1}},
		{name: "MergeabilityNotComputed", pr: prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "UNKNOWN"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.recordStop("ENG-1", noTasks)
			h.pr("ENG-1", test.pr)

			for range 3 {
				h.advance(10 * time.Minute)
				h.tick()
			}

			assert.Equal(t, []string{"w-ENG-1|💤 ENG-1"}, h.renames())
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.notifications())
		})
	}
}

func TestTickShowsAnInReviewWorkerWithAFailingPRAsGreenOnceNudged(t *testing.T) {
	for _, test := range []struct {
		name string
		pr   prState
	}{
		{name: "FailedCheck", pr: prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"}},
		{name: "ChangesRequested", pr: prState{head: "a1", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"}},
		{name: "MergeConflict", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "CONFLICTING"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.recordStop("ENG-1", noTasks)
			h.pr("ENG-1", test.pr)

			h.tick()

			assert.Len(t, h.prompts(), 1)
			renames := h.renames()
			require.NotEmpty(t, renames)
			assert.Equal(t, "w-ENG-1|🟢 ENG-1", renames[len(renames)-1])
		})
	}
}

func TestTickKeepsTheLabelThePRGaveWhenThePRCannotBeRead(t *testing.T) {
	for _, test := range []struct {
		name string
		pr   prState
		want string
	}{
		{name: "ChecksRunning", pr: prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"}, want: "🟡"},
		{name: "NothingToDo", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1}, want: "💤"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.recordStop("ENG-1", noTasks)
			h.pr("ENG-1", test.pr)
			h.tick()

			h.tools.PullRequests.FailRead(errors.New("exit status 1"))
			h.advance(time.Minute)
			h.tick()
			h.advance(time.Minute)
			h.tick()

			assert.Equal(t, []string{"w-ENG-1|" + test.want + " ENG-1"}, h.renames())
			assert.Empty(t, h.prompts())
		})
	}
}

func TestTickKeepsTheWaitingLabelOfANudgedWorkerWhenThePRCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 10\n")
	h.failingPR()
	h.recordStop("ENG-1", noTasks)
	h.tick()
	require.Len(t, h.prompts(), 1)
	before := h.renames()

	h.tools.PullRequests.FailRead(errors.New("exit status 1"))
	h.advance(time.Minute)
	h.tick()

	assert.Len(t, h.prompts(), 1)
	assert.Equal(t, before[len(before)-1], h.renames()[len(h.renames())-1])
	assert.Equal(t, "w-ENG-1|🟢 ENG-1", h.renames()[len(h.renames())-1])
}

func TestTickShowsAnInReviewWorkerWithAStaleBackgroundTaskAsWaitingOnCIOnceTheTaskStopCountExpires(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", runningTasks)
	h.pr("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})
	h.tick()

	h.advance(59 * time.Minute)
	h.tick()
	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())

	h.advance(time.Minute)
	h.tick()
	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|🟡 ENG-1"}, h.renames())
}

func TestTickIgnoresAStopOfAnotherSessionWhenLabelingAnIdleWorker(t *testing.T) {
	for _, status := range []string{"In Progress", "In Review"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", status, "dispatch"))
			h.worker("ENG-1", "idle")
			h.recordStopFrom("stranger", "ENG-1", noTasks)

			h.tick()
			h.advance(10 * time.Minute)
			h.tick()

			assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())
		})
	}
}

func TestTickShowsAnIdleWorkerAsBusyWhileAnotherSessionsStopSaysNothingOfItsOwnTasks(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStopFrom("worker", "ENG-1", runningTasks)
	h.recordStopFrom("stranger", "ENG-1", noTasks)

	h.tick()

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())
}

func TestTickStillNudgesAnInReviewWorkerWhoseStopLeftABackgroundTaskRunning(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 10\n")
	h.failingPR()
	h.recordStop("ENG-1", runningTasks)

	h.tick()

	assert.Equal(t, []string{prPrompt("a failed check")}, h.prompts())
}

func TestTickNeverNudgesAnIdleInReviewWorkerWhosePRNeedsNothingFromIt(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1})

	for range 4 {
		h.advance(30 * time.Minute)
		h.recordStop("ENG-1", noTasks)
		h.tick()
	}

	assert.Empty(t, h.prompts())
	assert.Empty(t, h.notifications())
	assert.Empty(t, h.linear.mutations())
}

func TestWakeIgnoresAHookPayloadItCannotUse(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
	}{
		{name: "BadJSON", input: `{"cwd": `},
		{name: "NotJSON", input: `idle_prompt`},
		{name: "Empty", input: ``},
		{name: "NoCWD", input: `{"hook_event_name":"UserPromptSubmit"}`},
		{name: "CWDNotADirectory", input: `{"cwd":"/no/such/dir"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			h.worker("ENG-1", "idle")
			h.tick()
			h.recordStop("ENG-1", noTasks)

			out, stderr, err := h.runWith(context.Background(), dispatch.Options{Stdin: strings.NewReader(test.input)}, "wake")

			require.NoError(t, err)
			assert.Empty(t, out)
			assert.Empty(t, stderr)
			assert.Never(t, func() bool { return len(h.renames()) > 1 }, 300*time.Millisecond, 10*time.Millisecond)
		})
	}
}

func TestWakeStaysSilentForAWorkerWhenTheLoopIsNotRunningAndPrintsNothingToStderr(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)
	input := fmt.Sprintf(`{"hook_event_name":"UserPromptSubmit","session_id":"worker","cwd":%q}`, filepath.Join(h.dir, "trees", "ENG-1"))

	out, stderr, err := h.runWith(context.Background(), dispatch.Options{Stdin: strings.NewReader(input)}, "wake")

	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Empty(t, stderr)
}

func TestWakeHooksRunDispatchWakeAndNeverFailTheSession(t *testing.T) {
	body, err := os.ReadFile("hooks/hooks.json")
	require.NoError(t, err)
	var plugin struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(body, &plugin))

	for _, event := range []string{"Notification", "UserPromptSubmit"} {
		t.Run(event, func(t *testing.T) {
			require.Len(t, plugin.Hooks[event], 1)
			require.Len(t, plugin.Hooks[event][0].Hooks, 1)
			command := plugin.Hooks[event][0].Hooks[0].Command
			assert.Contains(t, command, "dispatch wake")
			assert.True(t, strings.HasSuffix(command, "exit 0"))
			assert.Contains(t, command, "2>/dev/null")
		})
	}
}
