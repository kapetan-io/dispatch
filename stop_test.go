package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kapetan-io/dispatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	noTasks      = `"background_tasks":[]`
	runningTasks = `"background_tasks":[{"type":"shell","status":"running"}]`
)

// recordStop runs the Stop hook as the worker of ticket, in a real worktree of the
// harness repo. tasks is the background_tasks member of the hook input, or ""
// to leave it out.
func (h *harness) recordStop(ticket, tasks string) {
	h.t.Helper()
	h.recordStopFrom("worker", ticket, tasks)
}

// recordStopFrom is recordStop for the Claude session with the given id.
func (h *harness) recordStopFrom(session, ticket, tasks string) {
	h.t.Helper()
	tree := filepath.Join(h.dir, "trees", ticket)
	if _, err := os.Stat(filepath.Join(h.repo, ".git")); err != nil {
		hintGit(h.t, h.repo, "init", "-q")
		hintGit(h.t, h.repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	}
	if _, err := os.Stat(tree); err != nil {
		hintGit(h.t, h.repo, "worktree", "add", "-q", "-b", "br-"+ticket, tree)
	}
	input := fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q,"cwd":%q`, session, tree)
	if tasks != "" {
		input += "," + tasks
	}
	_, _, err := h.runWith(context.Background(), dispatch.Options{Stdin: strings.NewReader(input + "}")}, "record-stop")
	require.NoError(h.t, err)
}

func TestTickLeavesAWorkerWaitingOnBackgroundTasksAlone(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "done")
	h.recordStop("ENG-1", runningTasks)

	for range 3 {
		h.advance(5 * time.Minute)
		assert.Empty(t, h.tick())
	}
	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.linear.mutations())
	assert.Empty(t, h.notifications())
}

func TestTickNudgesAWorkerOnceForEachStopWithNoRunningTasks(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)

	assert.Equal(t, "nudge ENG-1: stall\n", h.tick())
	h.advance(5 * time.Minute)
	assert.Empty(t, h.tick())
	assert.Empty(t, h.tick())
	assert.Len(t, h.prompts(), 1)

	// A newer Stop gets its own nudge.
	h.advance(5 * time.Minute)
	h.recordStop("ENG-1", noTasks)
	assert.Equal(t, "nudge ENG-1: stall\n", h.tick())
	assert.Empty(t, h.tick())
	assert.Equal(t, []string{stallPrompt, stallPrompt}, h.prompts())
}

func TestTickNudgesAWorkerWhoseStopRanOutOfBusyTime(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", runningTasks)

	h.advance(59 * time.Minute)
	assert.Empty(t, h.tick())

	h.advance(2 * time.Minute)
	assert.Equal(t, "nudge ENG-1: stall\n", h.tick())
	assert.Empty(t, h.tick())
}

func TestTickNudgesOnceWhenTheWorkerLeftNoStopRecord(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(h *harness)
	}{
		{"NoRecord", func(h *harness) {}},
		{"NoBackgroundTasksField", func(h *harness) { h.recordStop("ENG-1", "") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			h.worker("ENG-1", "idle")
			test.stop(h)

			assert.Equal(t, "nudge ENG-1: stall\n", h.tick())
			for range 3 {
				h.advance(5 * time.Minute)
				assert.Empty(t, h.tick())
			}

			// The worker worked and went idle again, which is a new stretch of idling.
			h.setWorkerStatus("ENG-1", "working")
			h.advance(5 * time.Minute)
			assert.Empty(t, h.tick())
			h.setWorkerStatus("ENG-1", "idle")
			h.advance(5 * time.Minute)
			assert.Equal(t, "nudge ENG-1: stall\n", h.tick())
			assert.Len(t, h.prompts(), 2)
		})
	}
}

func TestTickNeverEscalatesAnInProgressWorkerForStalling(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")

	for range 8 {
		h.advance(5 * time.Minute)
		h.recordStop("ENG-1", noTasks)
		assert.Equal(t, "nudge ENG-1: stall\n", strings.SplitAfter(h.tick(), "\n")[0])
	}

	assert.Len(t, h.prompts(), 8)
	assert.Empty(t, h.linear.mutations())
}

func TestTickIgnoresNudgeLimitForInProgressTickets(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 1\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")

	for range 3 {
		h.advance(5 * time.Minute)
		h.recordStop("ENG-1", noTasks)
		assert.Contains(t, h.tick(), "nudge ENG-1: stall")
	}

	assert.Empty(t, h.linear.mutations())
}

func TestTickNotifiesOnEverySixthStallNudge(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")

	for i := 1; i <= 12; i++ {
		h.advance(5 * time.Minute)
		h.recordStop("ENG-1", noTasks)
		out := h.tick()
		if i%6 != 0 {
			assert.Equal(t, "nudge ENG-1: stall\n", out)
			continue
		}
		assert.Equal(t, fmt.Sprintf("nudge ENG-1: stall\nnotify ENG-1: %d stall nudges with no status change\n", i), out)
	}

	assert.Equal(t, []string{
		"ENG-1 has had 6 stall nudges with no status change",
		"ENG-1 has had 12 stall nudges with no status change",
	}, h.notifications())
	assert.Empty(t, h.linear.mutations())
}

func TestTickRestartsTheStallNotificationCountWhenStatusChanges(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	for range 5 {
		h.advance(5 * time.Minute)
		h.recordStop("ENG-1", noTasks)
		h.tick()
	}
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.tick()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	for range 5 {
		h.advance(5 * time.Minute)
		h.recordStop("ENG-1", noTasks)
		h.tick()
	}

	assert.Empty(t, h.notifications())
}

func TestTickDoesNotNotifyAboutANudgeHerdrRefused(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	for range 5 {
		h.advance(5 * time.Minute)
		h.recordStop("ENG-1", noTasks)
		h.tick()
	}
	h.blockAfterSnapshot("ENG-1")
	h.advance(5 * time.Minute)
	h.recordStop("ENG-1", noTasks)

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Contains(t, stderr, "while nudging ENG-1")
	assert.Empty(t, h.notifications())
}

func TestTickNotifiesOnceWhenAWorkerIgnoresItsNudgeForAnHour(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)
	h.tick()

	h.advance(59 * time.Minute)
	assert.Empty(t, h.tick())
	assert.Empty(t, h.notifications())

	h.advance(2 * time.Minute)
	assert.Equal(t, "notify ENG-1: worker hasn't responded to its nudge in an hour\n", h.tick())
	assert.Equal(t, []string{"ENG-1: the worker hasn't responded to its nudge in an hour"}, h.notifications())

	h.advance(time.Hour)
	assert.Empty(t, h.tick())
	assert.Len(t, h.notifications(), 1)
	assert.Empty(t, h.linear.mutations())

	// The next nudged Stop can raise it once more.
	h.recordStop("ENG-1", noTasks)
	assert.Equal(t, "nudge ENG-1: stall\n", h.tick())
	h.advance(61 * time.Minute)
	assert.Contains(t, h.tick(), "notify ENG-1")
	assert.Len(t, h.notifications(), 2)
}

func TestTickRetriesTheSixthStallNotificationThatFailed(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	for range 5 {
		h.advance(5 * time.Minute)
		h.recordStop("ENG-1", noTasks)
		h.tick()
	}
	h.advance(5 * time.Minute)
	h.recordStop("ENG-1", noTasks)
	h.tools.Alerts.Fail(errors.New("exit status 1"))

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	assert.Contains(t, stderr, "while notifying about ENG-1")
	assert.Empty(t, h.notifications())

	h.tools.Alerts.Fail(nil)
	h.advance(5 * time.Minute)
	assert.Equal(t, "notify ENG-1: 6 stall nudges with no status change\n", h.tick())
	assert.Equal(t, []string{"ENG-1 has had 6 stall nudges with no status change"}, h.notifications())

	h.advance(5 * time.Minute)
	assert.Empty(t, h.tick())
	assert.Len(t, h.notifications(), 1)
}

func TestTickRetriesTheUnresponsiveWorkerNotificationThatFailed(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tick()
	h.advance(61 * time.Minute)
	h.tools.Alerts.Fail(errors.New("exit status 1"))

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	assert.Contains(t, stderr, "while notifying about ENG-1")

	h.tools.Alerts.Fail(nil)
	assert.Contains(t, h.tick(), "notify ENG-1")
}

func TestTickStaysQuietWhenANewerStopIsBusyAfterAnIgnoredNudge(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)
	h.tick()
	h.advance(30 * time.Minute)
	h.recordStop("ENG-1", runningTasks)

	h.advance(40 * time.Minute)

	assert.Empty(t, h.tick())
	assert.Empty(t, h.notifications())
}

func TestTickLeavesAWorkerAloneWhenAnotherSessionInItsTreeStops(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStopFrom("worker", "ENG-1", runningTasks)
	h.advance(5 * time.Minute)

	h.recordStopFrom("stranger", "ENG-1", noTasks)

	assert.Empty(t, h.tick())
	assert.Empty(t, h.prompts())
}

func TestTickNudgesAnIdleWorkerAfterAnotherSessionStopsBusyInItsTree(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStopFrom("worker", "ENG-1", noTasks)
	assert.Equal(t, "nudge ENG-1: stall\n", h.tick())

	h.advance(time.Minute)
	h.recordStopFrom("stranger", "ENG-1", runningTasks)
	h.advance(5 * time.Minute)
	h.recordStopFrom("worker", "ENG-1", noTasks)

	assert.Equal(t, "nudge ENG-1: stall\n", h.tick())
	assert.Len(t, h.prompts(), 2)
}

func TestTickDoesNotNudgeAgainWhenAnotherSessionStopsIdleInTheTree(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStopFrom("worker", "ENG-1", noTasks)
	assert.Equal(t, "nudge ENG-1: stall\n", h.tick())

	h.advance(5 * time.Minute)
	h.recordStopFrom("stranger", "ENG-1", noTasks)

	assert.Empty(t, h.tick())
	assert.Len(t, h.prompts(), 1)
}

func TestTickNudgesARelaunchedWorkerWhoseOldSessionWasBusy(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStopFrom("worker", "ENG-1", runningTasks)
	h.advance(5 * time.Minute)

	h.setWorkerSession("ENG-1", "relaunched")
	h.recordStopFrom("relaunched", "ENG-1", noTasks)

	assert.Equal(t, "nudge ENG-1: stall\n", h.tick())
}

func TestTickSendsTheResumeNudgeWhateverTheStopRecordSays(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	h.tick()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.recordStop("ENG-1", runningTasks)

	assert.Equal(t, "nudge ENG-1: resume\n", h.tick())
	assert.Empty(t, h.tick())
	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, h.prompts())
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
}

func TestTickForgetsTheStopRecordOfATreeItReturns(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", runningTasks)
	require.Len(t, stopRecords(t, h.state), 1)
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	remote := filepath.Join(h.dir, "remotes", "ENG-1.git")
	require.NoError(t, os.MkdirAll(filepath.Dir(remote), 0o755))
	hintGit(t, h.dir, "init", "-q", "--bare", remote)
	tree := filepath.Join(h.dir, "trees", "ENG-1")
	hintGit(t, tree, "remote", "add", "origin", remote)
	hintGit(t, tree, "push", "-q", "--set-upstream", "origin", "br-ENG-1")

	out := h.tick()

	assert.Contains(t, out, "teardown ENG-1")
	assert.Empty(t, stopRecords(t, h.state))
}

func TestRecordStopDoesNothingOutsideAWorkerTree(t *testing.T) {
	h := newHarness(t)
	h.recordStop("ENG-1", noTasks)
	other := newHintRepo(t)
	stateBefore := stopRecords(t, h.state)

	for _, test := range []struct {
		name  string
		cwd   string
		input string
	}{
		{"MainCheckout", h.repo, ""},
		{"UnmanagedRepo", other, ""},
		{"NotARepo", t.TempDir(), ""},
		{"NotJSON", "", "not json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := test.input
			if input == "" {
				input = fmt.Sprintf(`{"cwd":%q,%s}`, test.cwd, noTasks)
			}

			stdout, stderr, err := h.runWith(t.Context(), dispatch.Options{Stdin: strings.NewReader(input)}, "record-stop")

			require.NoError(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Equal(t, stateBefore, stopRecords(t, h.state))
		})
	}
}

func TestRecordStopStaysSilentWithoutAConfig(t *testing.T) {
	var stdout, stderr strings.Builder

	err := dispatch.Run(t.Context(), []string{"record-stop"}, dispatch.Options{
		ConfigPath: filepath.Join(t.TempDir(), "missing.toml"),
		StateDir:   t.TempDir(),
		Stdin:      strings.NewReader(`{"cwd":"/tmp","background_tasks":[]}`),
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	require.NoError(t, err)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}

func TestStopHookRunsRecordStop(t *testing.T) {
	body, err := os.ReadFile("hooks/hooks.json")
	require.NoError(t, err)

	assert.Contains(t, string(body), `"Stop"`)
	assert.Contains(t, string(body), "exec dispatch record-stop")
}

func stopRecords(t *testing.T, stateDir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(stateDir, "stops"), func(path string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		out = append(out, e.Name()+string(b))
		return err
	})
	if !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	return out
}
