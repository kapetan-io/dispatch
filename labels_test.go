package dispatch_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			h := newHarness(t)
			tk := ticket("ENG-1", test.status, "dispatch")
			if test.design {
				tk = withLabel(tk, "design")
			}
			h.tickets(tk)
			h.worker("ENG-1", test.agent)
			switch test.stop {
			case "idle":
				h.recordStop("ENG-1", noTasks)
			case "busy":
				h.recordStop("ENG-1", runningTasks)
			case "unreported":
				h.recordStop("ENG-1", "")
			}
			if test.unconfirmed {
				markLaunch(t, h.state, filepath.Join(h.dir, "trees", "ENG-1"), h.clock.Now())
			}
			if test.pr != nil {
				h.pr("ENG-1", *test.pr)
			}

			h.tick()

			// A worker dispatch nudged shows 🟢 once the nudge lands, after a first 💤.
			renames := h.renames()
			require.NotEmpty(t, renames)
			assert.Equal(t, "w-ENG-1|"+test.want+" ENG-1", renames[len(renames)-1])
		})
	}
}

func TestTickShowsAnIdleWorkerAsSoonAsItsTurnEnds(t *testing.T) {
	for _, label := range []string{"design", "design-review"} {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), label))
			h.worker("ENG-1", "working")
			h.tick()

			h.setWorkerStatus("ENG-1", "idle")
			h.recordStop("ENG-1", noTasks)
			h.tick()

			assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|💤 ENG-1"}, h.renames())
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.notifications())
		})
	}
}

func TestTickShowsAnIdleBuildWorkerAsWaitingOnTheUserWithoutNudgingIt(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)

	for range 3 {
		h.advance(10 * time.Minute)
		h.tick()
	}

	assert.Equal(t, []string{"w-ENG-1|💤 ENG-1"}, h.renames())
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.notifications())
}

func TestTickShowsGreenAgainWhenTheUserTypesIntoAnIdleWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)
	h.tick()

	h.setWorkerStatus("ENG-1", "working")
	h.tick()

	assert.Equal(t, []string{"w-ENG-1|💤 ENG-1", "w-ENG-1|🟢 ENG-1"}, h.renames())
}

func TestTickShowsAnIdleWorkerAsWaitingOnceItsBackgroundTaskStopCountExpires(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", runningTasks)
	h.tick()

	h.advance(59 * time.Minute)
	h.tick()
	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())

	h.advance(time.Minute)
	h.tick()
	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|💤 ENG-1"}, h.renames())
}

func TestTickShowsAnInReviewWorkerAsWaitingWhenItsChecksFinishWithoutAnythingToDo(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)
	h.pr("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})
	h.tick()

	h.pr("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1})
	h.tick()

	require.Equal(t, []string{"w-ENG-1|🟡 ENG-1", "w-ENG-1|💤 ENG-1"}, h.renames())
}
