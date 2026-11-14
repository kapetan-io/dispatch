package dispatch_test

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

// Session makes the commands that follow run in the Claude session with this id.
func (d *Dispatch) Session(session string) {
	d.env = d.env.With("CLAUDE_CODE_SESSION_ID", session)
}

// RunWhileDown runs a command with no dispatch running and none started for it.
func (d *Dispatch) RunWhileDown(args ...string) Result {
	d.t.Helper()
	return d.run(d.options(dispatch.Options{}), args...)
}

// LoopStderr is what the running dispatch has printed to stderr.
func (d *Dispatch) LoopStderr() string { return d.loopErr() }

// deadSession is a session that crashed: its list entry is there, its socket file is there, and
// nothing listens on it.
func deadSession(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ds")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dead := filepath.Join(dir, "dead.sock")
	ln, err := net.Listen("unix", dead)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	return listSession(t, dead)
}

// Unreachable from the CLI: no command lists the state directory, so the test counts its entries.
func TestWatchWritesNoFileToTheStateDir(t *testing.T) {
	t.Parallel()
	socket, _ := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	before := d.StateEntries()

	require.NoError(t, d.Run("watch", "ENG-1").Err)
	require.NoError(t, d.Run("unwatch", "ENG-1").Err)

	assert.Equal(t, before, d.StateEntries())
}

func TestFollowersAreForgottenWhenDispatchRestarts(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Restart()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()

	assert.Empty(t, lines)
	assert.Equal(t, []string{readyText1}, d.Followed.Told())
}

func TestWatchFailsOutsideAClaudeSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": ""}})

	require.ErrorContains(t, d.Run("watch", "ENG-1").Err, "CLAUDE_CODE_SESSION_ID is not set")
}

func TestWatchRecordsTheSessionsID(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": "session-42"}})

	res := d.Run("watch", "ENG-1")

	require.NoError(t, res.Err)
	assert.Equal(t, "watching ENG-1\n", res.Stdout)
}

func TestWatchFailsWhenNoDispatchRuns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": "session-42"}})

	for _, command := range []string{"watch", "unwatch"} {
		require.ErrorContains(t, d.RunWhileDown(command, "ENG-1").Err, "not running")
	}
}

func TestTickMessagesFollowersAboutAReadyPR(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	got := next(t, lines)
	assert.Contains(t, got, `"type":"user"`)
	assert.Contains(t, got, "dispatch event (PR ready for review): ENG-1 is ready for review")
	assert.Contains(t, got, prURL1)
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
	assert.Equal(t, []string{readyText1}, d.Followed.Told())

	// The next tick has nothing new to say.
	d.Tick()
	assert.Empty(t, lines)
}

func TestTickMessagesFollowersThatAPRIsApprovedAndReadyToMergeNotThatItIsReady(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{RepoSettings: draftSetting, Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.StartBeforeTheEvents()
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	// The PR waits on its approvers, so followers hear nothing.
	d.Tick()
	assert.Empty(t, lines)

	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	d.Tick()

	assert.Contains(t, next(t, lines), "dispatch event (approved, ready to merge): ENG-1 is approved and ready to merge")
}

func TestTickMessagesFollowersAboutNeedsHumanAndBlockedWorkers(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status string
		worker string
		want   string
	}{
		{name: "NeedsHuman", status: "Needs Human", worker: "working", want: "dispatch event (entered Needs Human): ENG-1 is in Needs Human"},
		{name: "Blocked", status: "In Progress", worker: "blocked", want: "dispatch event (worker is blocked on a question): ENG-1 is waiting on your answer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket, lines := sessionSocket(t)
			d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
			d.StartBeforeTheEvents()
			d.Linear.Tickets(ticket("ENG-1", test.status, "dispatch"))
			d.Workers.Running("ENG-1", test.worker)
			require.NoError(t, d.Run("watch", "ENG-1").Err)

			d.Tick()
			d.Advance(5 * time.Minute)
			d.Tick()

			got := next(t, lines)
			assert.Contains(t, got, test.want)
			assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
		})
	}
}

func TestTickDropsAFollowerWhoseSessionExited(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	dead := deadSession(t)
	d.Session(dead)
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Followed.Told())
	assert.Contains(t, d.LoopStderr(), "dropped session "+dead+", which is not open")
	assert.Contains(t, next(t, lines), "ENG-1 is ready for review")
}

func TestTickDropsAFollowerWhoseSocketFileIsGone(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	missing := listSession(t, filepath.Join(os.TempDir(), "dispatch-test-no-such.sock"))
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Session(missing)
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Followed.Told())
	assert.Contains(t, d.LoopStderr(), "dropped session "+missing+", which is not open")
	assert.Contains(t, next(t, lines), "ENG-1 is ready for review")

	// A dropped follower is not tried again by the next event.
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.GitHub.PullRequest("ENG-1", cleanPR("b1"))
	d.Tick()
	assert.Equal(t, 1, strings.Count(d.LoopStderr(), "dropped session "+missing+", which is not open"))
}

func TestTickMessagesEveryFollowerOfATicket(t *testing.T) {
	t.Parallel()
	socket1, lines1 := sessionSocket(t)
	socket2, lines2 := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	for _, socket := range []string{socket1, socket2} {
		d.Session(socket)
		require.NoError(t, d.Run("watch", "ENG-1").Err)
	}

	d.Tick()

	assert.Contains(t, next(t, lines1), "ENG-1 is ready for review")
	assert.Contains(t, next(t, lines2), "ENG-1 is ready for review")
}

func TestTickMessagesFollowersAboutAMerge(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting, Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	got := next(t, lines)
	assert.Contains(t, got, mergedText1)
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
	assert.Equal(t, []string{mergedText1}, d.Followed.Told())
}

func TestTickMessagesFollowersAboutAnEscalation(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.LaunchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	// Each tick launches again, and the second failure reaches the limit.
	res := d.Run("tick")
	require.ErrorContains(t, res.Err, "pool_error")
	assert.Empty(t, lines)
	// A receipt is told from the next tick's by its start time, which the fake clock only changes when moved.
	d.Advance(time.Second)
	res = d.Run("tick")
	require.ErrorContains(t, res.Err, "pool_error")

	got := next(t, lines)
	assert.Contains(t, got, "ENG-1 is in Needs Human")
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
	assert.Len(t, d.Followed.Told(), 1)
}

func TestWatchRefusesAMissingTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": "session-42"}})

	require.ErrorContains(t, d.Run("watch").Err, "watch takes a ticket")
}

func TestWatchUppercasesTheTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": "session-42"}})

	res := d.Run("watch", "eng-1")

	require.NoError(t, res.Err)
	assert.Equal(t, "watching ENG-1\n", res.Stdout)
}

func TestTickMessagesOnlyTheSessionFollowingTheTicket(t *testing.T) {
	t.Parallel()
	socket1, lines1 := sessionSocket(t)
	socket2, lines2 := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.GitHub.PullRequest("ENG-2", cleanPR("a2"))
	d.Session(socket1)
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Session(socket2)
	require.NoError(t, d.Run("watch", "ENG-2").Err)

	d.Tick()

	assert.Contains(t, next(t, lines1), "ENG-1 is ready for review")
	assert.Contains(t, next(t, lines2), "ENG-2 is ready for review")
	assert.Empty(t, lines1)
	assert.Empty(t, lines2)
	assert.Len(t, d.Followed.Told(), 2)
}

func TestTickMessagesASessionFollowingSeveralTickets(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.GitHub.PullRequest("ENG-2", cleanPR("a2"))
	for _, id := range []string{"ENG-1", "ENG-2"} {
		require.NoError(t, d.Run("watch", id).Err)
	}

	d.Tick()

	got := next(t, lines) + next(t, lines)
	assert.Contains(t, got, "ENG-1 is ready for review")
	assert.Contains(t, got, "ENG-2 is ready for review")
}

func TestUnwatchStopsMessagesForThatTicketOnly(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.GitHub.PullRequest("ENG-2", cleanPR("a2"))
	for _, id := range []string{"ENG-1", "ENG-2"} {
		require.NoError(t, d.Run("watch", id).Err)
	}

	res := d.Run("unwatch", "ENG-1")
	require.NoError(t, res.Err)
	assert.Equal(t, "unwatching ENG-1\n", res.Stdout)
	d.Tick()

	assert.Contains(t, next(t, lines), "ENG-2 is ready for review")
	assert.Empty(t, lines)
}

func TestUnwatchATicketNeverWatchedSucceeds(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": "session-42"}})

	require.NoError(t, d.Run("unwatch", "ENG-1").Err)
}

func TestUnwatchFailsOutsideAClaudeSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": ""}})

	require.ErrorContains(t, d.Run("unwatch", "ENG-1").Err, "CLAUDE_CODE_SESSION_ID is not set")
}

func TestTickDroppingADeadSessionStopsItFollowingEveryTicket(t *testing.T) {
	t.Parallel()
	dead := deadSession(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": dead}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	for _, id := range []string{"ENG-1", "ENG-2"} {
		require.NoError(t, d.Run("watch", id).Err)
	}

	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Workers.Running("ENG-2", "idle")
	d.GitHub.PullRequest("ENG-2", cleanPR("b1"))
	d.Tick()

	// ENG-2's event finds no follower left to try.
	assert.Equal(t, 1, strings.Count(d.LoopStderr(), "dropped session "+dead+", which is not open"))
}

func TestWatchRefusesAnythingButATicketIdentifier(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"banana", "ENG286", "ENG-", "ENG-1x"} {
		d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": "session-42"}})

		require.ErrorContains(t, d.Run("watch", arg).Err, "not a ticket identifier")
	}
}

func TestUnwatchAcceptsALowercaseTicket(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))

	require.NoError(t, d.Run("unwatch", "eng-1").Err)
	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Followed.Told())
	assert.Empty(t, lines)
}

func TestWatchingATicketTwiceMessagesTheSessionOnce(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	for range 2 {
		require.NoError(t, d.Run("watch", "ENG-1").Err)
	}
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))

	d.Tick()

	assert.Contains(t, next(t, lines), "ENG-1 is ready for review")
	assert.Empty(t, lines)
}

// subscribe was replaced by watch and unwatch.
func TestSubscribeIsGone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": "session-42"}})

	require.Error(t, d.Run("subscribe").Err)
}

func TestTickMessagesFollowersWithTheReasonARulePasses(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		opts string
		want string
	}{
		{name: "ReasonGiven", opts: `{ once: ["deploy", t.id], reason: "deploy finished" }`, want: "dispatch event (deploy finished): ENG-1 deployed"},
		{name: "NoReasonUsesTheOnceTopic", opts: `{ once: ["deploy", t.id] }`, want: "dispatch event (deploy): ENG-1 deployed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket, lines := sessionSocket(t)
			d := newDispatch(t, dispatchOptions{
				Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket},
				Rules: rule(`whenever({ name: "tell", each: "ticket", if: () => true,
  then: (t, ctx) => ctx.notify.queueSend(t, t.id + " deployed", ` + test.opts + `) });`),
			})
			d.StartBeforeTheEvents()
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			require.NoError(t, d.Run("watch", "ENG-1").Err)

			d.Tick()

			assert.Contains(t, next(t, lines), test.want)
		})
	}
}

func TestTickMessagesFollowersThatAMergedPRIsApproved(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting, Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	assert.Contains(t, next(t, lines), "dispatch event (PR approved): ENG-1 was approved and merged")
}

func TestTickDoesNotMessageFollowersThatAnApprovedPRIsReadyToMergeWhenAutoMergeMergesIt(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{RepoSettings: draftSetting + autoMergeSetting, Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.StartBeforeTheEvents()
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	assert.Contains(t, next(t, lines), "dispatch event (PR approved): ENG-1 was approved and merged")
	assert.Empty(t, lines)
}

func TestTickMessagesAFollowerThroughItsOwnHarness(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{FakeHarness: true})
	inbox := d.Tools().Harness.Open()
	d.FakeSession(inbox)
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	require.Len(t, d.Tools().Harness.Delivered(inbox), 1)
	assert.Contains(t, d.Tools().Harness.Delivered(inbox)[0], "dispatch event (PR ready for review): ENG-1 is ready for review")
}

func TestTickDropsAFollowerOfAnotherHarnessWhoseSessionExited(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{FakeHarness: true})
	inbox := d.Tools().Harness.Open()
	d.FakeSession(inbox)
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Tools().Harness.Exit(inbox)

	d.Tick()

	assert.Contains(t, d.LoopStderr(), "dropped session "+inbox+", which is not open")
	assert.Empty(t, d.Tools().Harness.Delivered(inbox))
}

func TestWatchOutsideAnyHarnessSessionNamesWhatEachHarnessNeeds(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{FakeHarness: true, Env: map[string]string{"CLAUDE_CODE_SESSION_ID": ""}})

	err := d.Run("watch", "ENG-1").Err

	require.ErrorContains(t, err, "not a session of a harness dispatch drives")
	assert.ErrorContains(t, err, "CLAUDE_CODE_SESSION_ID is not set")
	assert.ErrorContains(t, err, "FAKE_HARNESS_INBOX"+" is not set")
}

func TestUnwatchStopsMessagesToAFollowerOfAnotherHarness(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{FakeHarness: true})
	inbox := d.Tools().Harness.Open()
	d.FakeSession(inbox)
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	require.NoError(t, d.Run("unwatch", "ENG-1").Err)

	d.Tick()

	assert.Empty(t, d.Tools().Harness.Delivered(inbox))
}

func TestTickTellsEachFollowingSessionOnceAboutARuleThatThrew(t *testing.T) {
	t.Parallel()
	socket1, lines1 := sessionSocket(t)
	socket2, lines2 := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({ name: "throws", each: "ticket", if: () => true, then: () => { throw new Error("boom"); } });`)})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Session(socket1)
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	require.NoError(t, d.Run("watch", "ENG-2").Err)
	d.Session(socket2)
	require.NoError(t, d.Run("watch", "ENG-2").Err)

	d.Tick()
	d.Tick()

	// Session 1 follows two tickets and the rule threw for both, yet it hears once.
	assert.Contains(t, next(t, lines1), "rule throws threw in its then")
	assert.Contains(t, next(t, lines2), "rule throws threw in its then")
	assert.Empty(t, lines1)
	assert.Empty(t, lines2)
}

func TestTickSendsNothingToASessionThatDoesNotFollowTheTicket(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "Needs Human", "dispatch"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	assert.Contains(t, d.Followed.Told(), "ENG-2 is in Needs Human: ticket ENG-2")
	assert.Empty(t, lines)
}

func TestTickTellsASessionThatFollowsAfterARuleThrewOnce(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}, Rules: rule(`
whenever({ name: "throws", each: "dispatch", if: () => true, then: () => { throw new Error("boom"); } });`)})
	d.StartBeforeTheEvents()
	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()
	d.Tick()

	assert.Contains(t, next(t, lines), "rule throws threw in its then")
	assert.Empty(t, lines)
	assert.Equal(t, 1, strings.Count(d.LoopStderr(), "rule throws threw in its then"))
}

func TestTickTellsAgainWhenTheOnlyFollowerWasGone(t *testing.T) {
	t.Parallel()
	live, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({ name: "throws", each: "dispatch", if: () => true, then: () => { throw new Error("boom"); } });`)})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Followed.Leave()
	d.Session(deadSession(t))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	// The only follower is dead, so nobody heard the message.
	d.Tick()
	assert.Contains(t, d.LoopStderr(), "dropped session")

	d.Session(live)
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Tick()

	assert.Contains(t, next(t, lines), "rule throws threw in its then")
}

func TestTickKeepsAFollowerWhoseDeliveryFailedWithAWriteError(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Workers.FailNextDeliveryTo("recorder", errors.New("write unix: broken pipe"))
	d.Tick()
	require.Empty(t, d.Followed.Told())
	assert.Contains(t, d.LoopStderr(), "while messaging session recorder: write unix: broken pipe")
	assert.NotContains(t, d.LoopStderr(), "dropped session")

	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Tick()
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Len(t, d.Followed.Told(), 1)
}
