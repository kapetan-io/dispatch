package dispatch_test

import (
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

// Session makes the commands that follow run in the Claude session whose inbox is socket.
func (d *Dispatch) Session(socket string) {
	d.t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
}

// RunWhileDown runs a command with no dispatch running and none started for it.
func (d *Dispatch) RunWhileDown(args ...string) Result {
	d.t.Helper()
	return d.run(d.options(dispatch.Options{}), args...)
}

// LoopStderr is what the running dispatch has printed to stderr.
func (d *Dispatch) LoopStderr() string { return d.loopErr() }

// deadSocket is the socket file of a session that crashed: it is there and nothing listens on it.
func deadSocket(t *testing.T, near string) string {
	t.Helper()
	dead := filepath.Join(filepath.Dir(near), "dead.sock")
	ln, err := net.Listen("unix", dead)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	return dead
}

// Unreachable from the CLI: no command lists the state directory, so the test counts its entries.
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

func TestWatchFailsOutsideAClaudeSession(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": ""}})

	require.ErrorContains(t, d.Run("watch", "ENG-1").Err, "CLAUDE_CODE_MESSAGING_SOCKET is not set")
}

func TestWatchRecordsTheSessionsSocket(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc-socks/42.sock"}})

	res := d.Run("watch", "ENG-1")

	require.NoError(t, res.Err)
	assert.Equal(t, "watching ENG-1\n", res.Stdout)
}

func TestWatchFailsWhenNoDispatchRuns(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc-socks/42.sock"}})

	for _, command := range []string{"watch", "unwatch"} {
		require.ErrorContains(t, d.RunWhileDown(command, "ENG-1").Err, "not running")
	}
}

func TestWatchRefusesARelativeSocket(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "cc-socks/42.sock"}})

	require.ErrorContains(t, d.Run("watch", "ENG-1").Err, "not absolute")
}

func TestTickMessagesFollowersAboutAReadyPR(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
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
	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())

	// The next tick has nothing new to say.
	d.Tick()
	assert.Empty(t, lines)
}

func TestTickMessagesFollowersThatReviewsAreHandledNotThatAPRIsReady(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{RepoSettings: draftSetting, Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.StartBeforeTheEvents()
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	assert.Contains(t, next(t, lines), "dispatch event (reviews handled): ENG-1's reviews are handled")
}

func TestTickMessagesFollowersAboutNeedsHumanAndBlockedWorkers(t *testing.T) {
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
			d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
			d.StartBeforeTheEvents()
			d.Linear.Tickets(ticket("ENG-1", test.status, "dispatch"))
			d.Workers.Running("ENG-1", test.worker)
			require.NoError(t, d.Run("watch", "ENG-1").Err)

			d.Tick()

			got := next(t, lines)
			assert.Contains(t, got, test.want)
			assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
		})
	}
}

func TestTickDropsAFollowerWhoseSessionExited(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	dead := deadSocket(t, socket)
	d.Session(dead)
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
	assert.Contains(t, d.LoopStderr(), "dropped watcher socket "+dead)
	assert.Contains(t, next(t, lines), "ENG-1 is ready for review")
}

func TestTickDropsAFollowerWhoseSocketFileIsGone(t *testing.T) {
	socket, lines := sessionSocket(t)
	missing := filepath.Join(filepath.Dir(socket), "gone.sock")
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Session(missing)
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
	assert.Contains(t, d.LoopStderr(), "dropped watcher socket "+missing)
	assert.Contains(t, next(t, lines), "ENG-1 is ready for review")

	// A dropped follower is not tried again by the next event.
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.GitHub.PullRequest("ENG-1", cleanPR("b1"))
	d.Tick()
	assert.Equal(t, 1, strings.Count(d.LoopStderr(), "dropped watcher socket "+missing))
}

func TestTickMessagesNoFollowerWhileTheAlertKeepsFailing(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.StartBeforeTheEvents()
	d.Workers.Running("ENG-1", "working")
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Alerts.Fail(errors.New("exit status 1: denied"))

	for range 3 {
		res := d.Run("tick")
		require.ErrorContains(t, res.Err, "ENG-1 notify failed")
		assert.ErrorContains(t, res.Err, "denied")
	}

	assert.Empty(t, lines)
	assert.Empty(t, d.Alerts.Shown())

	d.Alerts.Fail(nil)
	d.Tick()

	assert.Contains(t, next(t, lines), "ENG-1 is in Needs Human")
}

func TestTickMessagesEveryFollowerOfATicket(t *testing.T) {
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
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting, Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	got := next(t, lines)
	assert.Contains(t, got, mergedText1)
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
	assert.Equal(t, []string{mergedText1}, d.Alerts.Shown())
}

func TestTickMessagesFollowersAboutAnEscalation(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.LaunchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	// Each tick retries the launch, and the second failure reaches the limit.
	res := d.Run("tick")
	require.ErrorContains(t, res.Err, "pool_error")
	assert.Empty(t, lines)
	// A receipt is told from its retry by its start time, which the fake clock only changes when moved.
	d.Advance(time.Second)
	res = d.Run("tick")
	require.ErrorContains(t, res.Err, "pool_error")

	got := next(t, lines)
	assert.Contains(t, got, "ENG-1 is in Needs Human")
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
	assert.Len(t, d.Alerts.Shown(), 1)
}

func TestTickTellsFollowersAboutAMergeWhenTheAlertFails(t *testing.T) {
	for _, test := range []struct {
		name  string
		fails bool
		want  string
	}{
		{name: "Merged", want: "ENG-1 was approved and merged"},
		{name: "MergeFailed", fails: true, want: "merging"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket, lines := sessionSocket(t)
			d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting, Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
			d.StartBeforeTheEvents()
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
			require.NoError(t, d.Run("watch", "ENG-1").Err)
			d.Alerts.Fail(errors.New("exit status 1"))
			if test.fails {
				d.GitHub.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))
			}

			res := d.Run("tick")
			if test.fails {
				require.ErrorContains(t, res.Err, "merge refused")
			} else {
				require.NoError(t, res.Err)
			}

			// No later tick sends it again, so this tick's message is the only one.
			assert.Contains(t, next(t, lines), test.want)
		})
	}
}

func TestWatchRefusesAMissingTicket(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc-socks/42.sock"}})

	require.ErrorContains(t, d.Run("watch").Err, "watch takes a ticket")
}

func TestWatchUppercasesTheTicket(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc-socks/42.sock"}})

	res := d.Run("watch", "eng-1")

	require.NoError(t, res.Err)
	assert.Equal(t, "watching ENG-1\n", res.Stdout)
}

func TestTickMessagesOnlyTheSessionFollowingTheTicket(t *testing.T) {
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
	assert.Len(t, d.Alerts.Shown(), 2)
}

func TestTickMessagesASessionFollowingSeveralTickets(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
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

func TestTickRaisesTheAlertForATicketNobodyFollows(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-2").Err)

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
	assert.Empty(t, lines)
}

func TestUnwatchStopsMessagesForThatTicketOnly(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
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
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc-socks/42.sock"}})

	require.NoError(t, d.Run("unwatch", "ENG-1").Err)
}

func TestUnwatchFailsOutsideAClaudeSession(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": ""}})

	require.ErrorContains(t, d.Run("unwatch", "ENG-1").Err, "CLAUDE_CODE_MESSAGING_SOCKET is not set")
}

func TestTickDroppingADeadSessionStopsItFollowingEveryTicket(t *testing.T) {
	socket, _ := sessionSocket(t)
	dead := deadSocket(t, socket)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": dead}})
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
	assert.Equal(t, 1, strings.Count(d.LoopStderr(), "dropped watcher socket "+dead))
}

func TestWatchRefusesAnythingButATicketIdentifier(t *testing.T) {
	for _, arg := range []string{"banana", "ENG286", "ENG-", "ENG-1x"} {
		d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc-socks/42.sock"}})

		require.ErrorContains(t, d.Run("watch", arg).Err, "not a ticket identifier")
	}
}

func TestUnwatchAcceptsALowercaseTicket(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.StartBeforeTheEvents()
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))

	require.NoError(t, d.Run("unwatch", "eng-1").Err)
	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
	assert.Empty(t, lines)
}

func TestWatchingATicketTwiceMessagesTheSessionOnce(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
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
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc-socks/42.sock"}})

	require.Error(t, d.Run("subscribe").Err)
}

func TestTickKeepsAReadyNoticeFromFollowersWhenTheAlertFailsAndRunsItAgain(t *testing.T) {
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Alerts.Fail(errors.New("exit status 1"))

	// A notice that did not reach the alert has not reached a follower either.
	require.ErrorContains(t, d.Run("tick").Err, "ENG-1 notify failed")
	assert.Empty(t, d.Alerts.Shown())
	assert.Empty(t, lines)
	assert.Equal(t, []string{"attempted", "failed"}, states(d.attemptsOf("ENG-1", "notify")))

	// The next tick runs it again, and the followers hear it once the alert shows.
	d.Alerts.Fail(nil)
	d.Advance(time.Second)
	d.Tick()

	assert.Contains(t, next(t, lines), "ENG-1 is ready for review")
	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
	assert.Equal(t, []string{"attempted", "failed", "attempted", "done"}, states(d.attemptsOf("ENG-1", "notify")))

	d.Advance(time.Second)
	d.Tick()
	assert.Empty(t, lines)
	assert.Len(t, d.attemptsOf("ENG-1", "notify"), 4)
}
