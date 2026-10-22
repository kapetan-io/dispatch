package dispatch_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Renames lists the workspace renames herdr accepted, as "<workspace>|<label>".
func (w *Workers) Renames() []string { return w.d.h.renames() }

// Unconfirmed marks the ticket's tree as one whose launch never confirmed its kickoff (unreachable
// from the CLI: a test cannot kill a launch halfway).
func (w *Workers) Unconfirmed(ticket string) {
	w.d.h.t.Helper()
	markLaunch(w.d.h.t, w.d.h.state, w.Tree(ticket), w.d.Clock.Now())
}

// Wake runs the Notification hook as a session working in dir.
func (d *Dispatch) Wake(dir string) Result {
	d.h.t.Helper()
	return d.Hook("wake", fmt.Sprintf(`{"hook_event_name":"Notification","notification_type":"idle_prompt","session_id":"worker","cwd":%q}`, dir))
}

func (w *Workers) lastRename(t *testing.T) string {
	t.Helper()
	renames := w.Renames()
	require.NotEmpty(t, renames)
	return renames[len(renames)-1]
}

func TestTickLabelsEveryStateAWorkerCanBeIn(t *testing.T) {
	pending := &prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"}
	failed := &prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"}
	ready := new(cleanPR("a1"))

	for _, test := range []struct {
		name   string
		status string
		// agent is the herdr status of the worker, empty for a workspace without a Claude.
		agent string
		// stop is what the worker's Stop hook reported, empty when it never ran.
		stop        string
		unconfirmed bool
		design      bool
		pr          *prState
		want        string
	}{
		{name: "ProgressWorking", status: "In Progress", agent: "working", want: "🟢"},
		{name: "ProgressIdleNoStop", status: "In Progress", agent: "idle", want: "🟢"},
		{name: "ProgressIdleStopped", status: "In Progress", agent: "idle", stop: "idle", want: "💤"},
		{name: "ProgressIdleBackgroundTask", status: "In Progress", agent: "idle", stop: "busy", want: "🟢"},
		{name: "ProgressIdleStopUnreported", status: "In Progress", agent: "idle", stop: "unreported", want: "🟢"},
		{name: "ProgressIdleUnconfirmed", status: "In Progress", agent: "idle", stop: "idle", unconfirmed: true, want: "🟢"},
		{name: "ProgressBlocked", status: "In Progress", agent: "blocked", want: "🔴"},
		{name: "ProgressNoAgent", status: "In Progress", want: "🟢"},
		{name: "ProgressDesignIdleStopped", status: "In Progress", agent: "idle", stop: "idle", design: true, want: "💤"},
		{name: "ProgressDesignBlocked", status: "In Progress", agent: "blocked", design: true, want: "🔴"},

		{name: "ReviewWorking", status: "In Review", agent: "working", want: "🟢"},
		{name: "ReviewBlocked", status: "In Review", agent: "blocked", want: "🔴"},
		{name: "ReviewNoAgentReady", status: "In Review", pr: ready, want: "👀"},
		{name: "ReviewNoAgentChecksRunning", status: "In Review", pr: pending, want: "🟡"},
		{name: "ReviewNoAgentNoPR", status: "In Review", want: "🟢"},
		{name: "ReviewUnconfirmedReady", status: "In Review", agent: "idle", stop: "idle", unconfirmed: true, pr: ready, want: "👀"},
		{name: "ReviewUnconfirmedNoPR", status: "In Review", agent: "idle", stop: "idle", unconfirmed: true, want: "🟢"},

		{name: "ReviewIdleNoStopReady", status: "In Review", agent: "idle", pr: ready, want: "👀"},
		{name: "ReviewIdleNoStopChecksRunning", status: "In Review", agent: "idle", pr: pending, want: "🟡"},
		{name: "ReviewIdleNoStopNoPR", status: "In Review", agent: "idle", want: "🟢"},

		{name: "ReviewIdleStoppedReady", status: "In Review", agent: "idle", stop: "idle", pr: ready, want: "👀"},
		{name: "ReviewIdleStoppedChecksRunning", status: "In Review", agent: "idle", stop: "idle", pr: pending, want: "🟡"},
		{name: "ReviewIdleStoppedNoPR", status: "In Review", agent: "idle", stop: "idle", want: "💤"},
		{name: "ReviewIdleStoppedFailedCheck", status: "In Review", agent: "idle", stop: "idle", pr: failed, want: "🟢"},

		{name: "ReviewIdleBackgroundTaskReady", status: "In Review", agent: "idle", stop: "busy", pr: ready, want: "👀"},
		{name: "ReviewIdleBackgroundTaskChecksRunning", status: "In Review", agent: "idle", stop: "busy", pr: pending, want: "🟢"},
		{name: "ReviewIdleBackgroundTaskNoPR", status: "In Review", agent: "idle", stop: "busy", want: "🟢"},

		{name: "ReviewIdleStopUnreportedChecksRunning", status: "In Review", agent: "idle", stop: "unreported", pr: pending, want: "🟡"},
		{name: "ReviewIdleStopUnreportedNoPR", status: "In Review", agent: "idle", stop: "unreported", want: "🟢"},

		{name: "NeedsHumanWorking", status: "Needs Human", agent: "working", want: "🔴"},
		{name: "NeedsHumanIdle", status: "Needs Human", agent: "idle", stop: "idle", want: "🔴"},
		{name: "NeedsHumanBlocked", status: "Needs Human", agent: "blocked", want: "🔴"},
		{name: "NeedsHumanNoAgent", status: "Needs Human", want: "🔴"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			tk := ticket("ENG-1", test.status, "dispatch")
			if test.design {
				tk = withLabel(tk, "design")
			}
			d.Linear.Tickets(tk)
			d.Workers.Running("ENG-1", test.agent)
			switch test.stop {
			case "idle":
				d.Workers.Stop("ENG-1", stopTasks{})
			case "busy":
				d.Workers.Stop("ENG-1", stopTasks{Running: 1})
			case "unreported":
				d.Workers.Stop("ENG-1", stopTasks{Unreported: true})
			}
			if test.unconfirmed {
				d.Workers.Unconfirmed("ENG-1")
			}
			if test.pr != nil {
				d.GitHub.PullRequest("ENG-1", *test.pr)
			}

			d.Tick()

			// The newest label is the one that counts, since a nudged worker shows 🟢 after a first 💤.
			assert.Equal(t, "w-ENG-1|"+test.want+" ENG-1", d.Workers.lastRename(t))
		})
	}
}

func TestTickShowsAnIdleWorkerAsSoonAsItsTurnEnds(t *testing.T) {
	for _, label := range []string{"design", "design-review"} {
		t.Run(label, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), label))
			d.Workers.Running("ENG-1", "working")
			d.Tick()

			d.Workers.SetStatus("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{})
			d.Tick()

			assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
			assert.Empty(t, d.Workers.Prompts())
			assert.Empty(t, d.Alerts.Shown())
		})
	}
}

func TestTickShowsAnIdleBuildWorkerAsWaitingOnTheUserWithoutNudgingIt(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})

	for range 3 {
		d.Advance(10 * time.Minute)
		d.Tick()
	}

	assert.Equal(t, []string{"w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
	assert.Empty(t, d.Workers.Prompts())
	assert.Empty(t, d.Alerts.Shown())
}

func TestTickShowsGreenAgainWhenTheUserTypesIntoAnIdleWorker(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Tick()

	d.Workers.SetStatus("ENG-1", "working")
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|💤 ENG-1", "w-ENG-1|🟢 ENG-1"}, d.Workers.Renames())
}

func TestTickShowsAnIdleWorkerAsWaitingOnceItsBackgroundTaskStopCountExpires(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Tick()

	d.Advance(59 * time.Minute)
	d.Tick()
	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, d.Workers.Renames())

	d.Advance(time.Minute)
	d.Tick()
	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
}

func TestTickShowsAnInReviewWorkerAsWaitingWhenItsChecksFinishWithoutAnythingToDo(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})
	d.Tick()

	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1})
	d.Tick()

	require.Equal(t, []string{"w-ENG-1|🟡 ENG-1", "w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
}

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
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", test.agent)
			d.Workers.Stop("ENG-1", stopTasks{})
			if test.unconfirmed {
				d.Workers.Unconfirmed("ENG-1")
			}
			d.GitHub.PullRequest("ENG-1", test.pr)

			d.Tick()

			assert.Equal(t, "w-ENG-1|"+test.want+" ENG-1", d.Workers.lastRename(t))
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
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{})
			d.GitHub.PullRequest("ENG-1", test.pr)

			for range 3 {
				d.Advance(10 * time.Minute)
				d.Tick()
			}

			assert.Equal(t, []string{"w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
			assert.Empty(t, d.Workers.Prompts())
			assert.Empty(t, d.Alerts.Shown())
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
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{})
			d.GitHub.PullRequest("ENG-1", test.pr)

			d.Tick()

			assert.Len(t, d.Workers.Prompts(), 1)
			assert.Equal(t, "w-ENG-1|🟢 ENG-1", d.Workers.lastRename(t))
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
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{})
			d.GitHub.PullRequest("ENG-1", test.pr)
			d.Tick()

			d.GitHub.FailRead(errors.New("exit status 1"))
			d.Advance(time.Minute)
			d.Tick()
			d.Advance(time.Minute)
			d.Tick()

			assert.Equal(t, []string{"w-ENG-1|" + test.want + " ENG-1"}, d.Workers.Renames())
			assert.Empty(t, d.Workers.Prompts())
		})
	}
}

func TestTickKeepsTheWaitingLabelOfANudgedWorkerWhenThePRCannotBeRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nnudge_limit = 10\n"})
	withFailingPR(d)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Tick()
	require.Len(t, d.Workers.Prompts(), 1)
	before := d.Workers.Renames()

	d.GitHub.FailRead(errors.New("exit status 1"))
	d.Advance(time.Minute)
	d.Tick()

	assert.Len(t, d.Workers.Prompts(), 1)
	assert.Equal(t, before[len(before)-1], d.Workers.lastRename(t))
	assert.Equal(t, "w-ENG-1|🟢 ENG-1", d.Workers.lastRename(t))
}

func TestTickShowsAnInReviewWorkerWithAStaleBackgroundTaskAsWaitingOnCIOnceTheTaskStopCountExpires(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})
	d.Tick()

	d.Advance(59 * time.Minute)
	d.Tick()
	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, d.Workers.Renames())

	d.Advance(time.Minute)
	d.Tick()
	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|🟡 ENG-1"}, d.Workers.Renames())
}

func TestTickIgnoresAStopOfAnotherSessionWhenLabelingAnIdleWorker(t *testing.T) {
	for _, status := range []string{"In Progress", "In Review"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", status, "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.Workers.StopFrom("stranger", "ENG-1", stopTasks{})

			d.Tick()
			d.Advance(10 * time.Minute)
			d.Tick()

			assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, d.Workers.Renames())
		})
	}
}

func TestTickShowsAnIdleWorkerAsBusyWhileAnotherSessionsStopSaysNothingOfItsOwnTasks(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{Running: 1})
	d.Workers.StopFrom("stranger", "ENG-1", stopTasks{})

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, d.Workers.Renames())
}

func TestTickStillNudgesAnInReviewWorkerWhoseStopLeftABackgroundTaskRunning(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nnudge_limit = 10\n"})
	withFailingPR(d)
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})

	d.Tick()

	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Prompts())
}

func TestTickNeverNudgesAnIdleInReviewWorkerWhosePRNeedsNothingFromIt(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1})

	for range 4 {
		d.Advance(30 * time.Minute)
		d.Workers.Stop("ENG-1", stopTasks{})
		d.Tick()
	}

	assert.Empty(t, d.Workers.Prompts())
	assert.Empty(t, d.Alerts.Shown())
	noLinearChanges(t, d)
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
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.Tick()
			d.Workers.Stop("ENG-1", stopTasks{})

			res := d.Hook("wake", test.input)

			require.NoError(t, res.Err)
			assert.Empty(t, res.Stdout)
			assert.Empty(t, res.Stderr)
			assert.Never(t, func() bool { return len(d.Workers.Renames()) > 1 }, 300*time.Millisecond, 10*time.Millisecond)
		})
	}
}

func TestWakeStaysSilentForAWorkerWhenTheLoopIsNotRunningAndPrintsNothingToStderr(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})

	res := d.Wake(d.Workers.Tree("ENG-1"))

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stdout)
	assert.Empty(t, res.Stderr)
}

// Reads the plugin's hooks file: no command prints which hooks Claude Code runs.
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

func TestWakeRelabelsAnIdleWorkerWithoutWaitingForTheNextResync(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Tick()
	d.Workers.Stop("ENG-1", stopTasks{})

	res := d.Wake(d.Workers.Tree("ENG-1"))

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stdout)
	require.Eventually(t, func() bool { return len(d.Workers.Renames()) == 2 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
}

func TestWakeRelabelsAWorkerThePromptSentBackToWork(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Tick()

	d.Workers.SetStatus("ENG-1", "working")
	require.NoError(t, d.Wake(d.Workers.Tree("ENG-1")).Err)
	require.Eventually(t, func() bool { return len(d.Workers.Renames()) == 2 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, []string{"w-ENG-1|💤 ENG-1", "w-ENG-1|🟢 ENG-1"}, d.Workers.Renames())
}

func TestWakeIgnoresASessionInTheCloneItself(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Tick()
	d.Workers.Stop("ENG-1", stopTasks{})

	require.NoError(t, d.Wake(d.Repo).Err)
	assert.Never(t, func() bool { return len(d.Workers.Renames()) > 1 }, 300*time.Millisecond, 10*time.Millisecond)
}

func TestWakeIgnoresASessionOutsideEveryRepo(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Tick()
	d.Workers.Stop("ENG-1", stopTasks{})

	require.NoError(t, d.Wake(t.TempDir()).Err)
	assert.Never(t, func() bool { return len(d.Workers.Renames()) > 1 }, 300*time.Millisecond, 10*time.Millisecond)
}

func TestWakeDoesNotRetryWhatFailedAtTheLastResync(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.Running("ENG-2", "working")
	d.Workers.LaunchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)
	require.Error(t, d.Run("tick").Err)
	require.Len(t, d.Workers.Launched(), 1)
	d.Workers.Stop("ENG-2", stopTasks{})

	require.NoError(t, d.Wake(d.Workers.Tree("ENG-2")).Err)
	assert.Never(t, func() bool { return len(d.Workers.Launched()) > 1 }, 300*time.Millisecond, 10*time.Millisecond)
}

func TestWakeKeepsANudgedWorkerGreenWhileHerdrStillSaysIdle(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, d.TickAfterASecond())
	require.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Prompts())
	renames := d.Workers.Renames()

	require.NoError(t, d.Wake(d.Workers.Tree("ENG-1")).Err)
	assert.Never(t, func() bool { return len(d.Workers.Prompts()) > 1 || len(d.Workers.Renames()) > len(renames) }, 300*time.Millisecond, 10*time.Millisecond)
	assert.Equal(t, "w-ENG-1|🟢 ENG-1", d.Workers.lastRename(t))
}
