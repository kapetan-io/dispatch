package dispatch_test

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionSocket stands in for a Claude Code session's inbox and collects the
// lines written to it. The path is short because macOS caps socket paths.
func sessionSocket(t *testing.T) (string, <-chan string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ds")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "1.sock")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	lines := make(chan string, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				sc := bufio.NewScanner(conn)
				for sc.Scan() {
					lines <- sc.Text()
				}
			}()
		}
	}()
	return path, lines
}

func next(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line := <-lines:
		return line
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no message arrived")
		return ""
	}
}

func TestWatchFailsOutsideAClaudeSession(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")

	_, err := h.run("watch", "ENG-1")

	require.ErrorContains(t, err, "CLAUDE_CODE_MESSAGING_SOCKET is not set")
	assert.NoFileExists(t, filepath.Join(h.state, "watchers"))
}

func TestWatchRecordsTheSessionsSocket(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/cc-socks/42.sock")

	out, err := h.run("watch", "ENG-1")

	require.NoError(t, err)
	assert.Equal(t, "watching ENG-1\n", out)
	b, err := os.ReadFile(filepath.Join(h.state, "watchers"))
	require.NoError(t, err)
	assert.Equal(t, "ENG-1 /tmp/cc-socks/42.sock\n", string(b))
}

func TestWatchRefusesARelativeSocket(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "cc-socks/42.sock")

	_, err := h.run("watch", "ENG-1")

	require.ErrorContains(t, err, "not absolute")
}

func TestTickMessagesFollowersAboutAReadyPR(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)

	_, err = h.run("tick")
	require.NoError(t, err)

	got := next(t, lines)
	assert.Contains(t, got, `"type":"user"`)
	assert.Contains(t, got, "dispatch event (PR ready for review): ENG-1 is ready for review")
	assert.Contains(t, got, prURL1)
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
	assert.Equal(t, []string{readyText1}, h.notifications())

	// The next tick has nothing new to say.
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, lines)
}

func TestTickMessagesFollowersThatReviewsAreHandledNotThatAPRIsReady(t *testing.T) {
	h := draftHarness(t)
	h.pr("ENG-1", cleanPR("a1"))
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)

	h.tick()

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
			h := newHarness(t)
			h.tickets(ticket("ENG-1", test.status, "dispatch"))
			h.worker("ENG-1", test.worker)
			socket, lines := sessionSocket(t)
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
			_, err := h.run("watch", "ENG-1")
			require.NoError(t, err)

			_, err = h.run("tick")
			require.NoError(t, err)

			got := next(t, lines)
			assert.Contains(t, got, test.want)
			assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
		})
	}
}

func TestTickDropsAFollowerWhoseSessionExited(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	socket, _ := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)
	// A crashed session leaves its socket file behind with nothing listening.
	dead := filepath.Join(filepath.Dir(socket), "dead.sock")
	ln, err := net.Listen("unix", dead)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", dead)
	_, err = h.run("watch", "ENG-1")
	require.NoError(t, err)

	_, err = h.run("tick")

	require.NoError(t, err)
	assert.Equal(t, []string{readyText1}, h.notifications())
	assert.Contains(t, h.loopErr(), "dropped watcher socket "+dead)
	b, err := os.ReadFile(filepath.Join(h.state, "watchers"))
	require.NoError(t, err)
	assert.Equal(t, "ENG-1 "+socket+"\n", string(b))
}

func TestTickMessagesEveryFollowerOfATicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	socket1, lines1 := sessionSocket(t)
	socket2, lines2 := sessionSocket(t)
	for _, socket := range []string{socket1, socket2} {
		t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
		_, err := h.run("watch", "ENG-1")
		require.NoError(t, err)
	}

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, next(t, lines1), "ENG-1 is ready for review")
	assert.Contains(t, next(t, lines2), "ENG-1 is ready for review")
}

func TestTickMessagesFollowersAboutAMerge(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)

	_, err = h.run("tick")
	require.NoError(t, err)

	got := next(t, lines)
	assert.Contains(t, got, mergedText1)
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
	assert.Equal(t, []string{mergedText1}, h.notifications())
}

func TestTickMessagesFollowersAboutAnEscalation(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.launchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)

	// Each tick retries the launch, and the second failure reaches the limit.
	_, err = h.run("tick")
	require.ErrorContains(t, err, "pool_error")
	assert.Empty(t, lines)
	// A receipt is told from its retry by its start time, which the fake clock only changes when moved.
	h.advance(time.Second)
	_, err = h.run("tick")
	require.ErrorContains(t, err, "pool_error")

	got := next(t, lines)
	assert.Contains(t, got, "ENG-1 is in Needs Human")
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
	assert.Len(t, h.notifications(), 1)
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
			h := newHarness(t)
			h.repoSettings("dispatch", autoMergeSetting)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", approvedPR("a1"))
			socket, lines := sessionSocket(t)
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
			_, err := h.run("watch", "ENG-1")
			require.NoError(t, err)
			h.tools.Alerts.Fail(errors.New("exit status 1"))
			if test.fails {
				h.tools.PullRequests.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))
			}

			_, err = h.run("tick")
			if test.fails {
				require.ErrorContains(t, err, "merge refused")
			} else {
				require.NoError(t, err)
			}

			// No later tick sends it again, so this tick's message is the only one.
			assert.Contains(t, next(t, lines), test.want)
		})
	}
}

func TestWatchRefusesAMissingTicket(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/cc-socks/42.sock")

	_, err := h.run("watch")

	require.ErrorContains(t, err, "watch takes a ticket")
	assert.NoFileExists(t, filepath.Join(h.state, "watchers"))
}

func TestWatchUppercasesTheTicket(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/cc-socks/42.sock")

	out, err := h.run("watch", "eng-1")

	require.NoError(t, err)
	assert.Equal(t, "watching ENG-1\n", out)
}

func TestTickMessagesOnlyTheSessionFollowingTheTicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.pr("ENG-2", cleanPR("a2"))
	socket1, lines1 := sessionSocket(t)
	socket2, lines2 := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket1)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket2)
	_, err = h.run("watch", "ENG-2")
	require.NoError(t, err)

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, next(t, lines1), "ENG-1 is ready for review")
	assert.Contains(t, next(t, lines2), "ENG-2 is ready for review")
	assert.Empty(t, lines1)
	assert.Empty(t, lines2)
	assert.Len(t, h.notifications(), 2)
}

func TestTickMessagesASessionFollowingSeveralTickets(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.pr("ENG-2", cleanPR("a2"))
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	for _, id := range []string{"ENG-1", "ENG-2"} {
		_, err := h.run("watch", id)
		require.NoError(t, err)
	}

	_, err := h.run("tick")
	require.NoError(t, err)

	got := next(t, lines) + next(t, lines)
	assert.Contains(t, got, "ENG-1 is ready for review")
	assert.Contains(t, got, "ENG-2 is ready for review")
}

func TestTickRaisesTheAlertForATicketNobodyFollows(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-2")
	require.NoError(t, err)

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{readyText1}, h.notifications())
	assert.Empty(t, lines)
}

func TestUnwatchStopsMessagesForThatTicketOnly(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.pr("ENG-2", cleanPR("a2"))
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	for _, id := range []string{"ENG-1", "ENG-2"} {
		_, err := h.run("watch", id)
		require.NoError(t, err)
	}

	out, err := h.run("unwatch", "ENG-1")
	require.NoError(t, err)
	assert.Equal(t, "unwatching ENG-1\n", out)
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, next(t, lines), "ENG-2 is ready for review")
	assert.Empty(t, lines)
}

func TestUnwatchATicketNeverWatchedSucceeds(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/cc-socks/42.sock")

	_, err := h.run("unwatch", "ENG-1")

	require.NoError(t, err)
}

func TestUnwatchFailsOutsideAClaudeSession(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")

	_, err := h.run("unwatch", "ENG-1")

	require.ErrorContains(t, err, "CLAUDE_CODE_MESSAGING_SOCKET is not set")
}

func TestTickDroppingADeadSessionStopsItFollowingEveryTicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	socket, _ := sessionSocket(t)
	dead := filepath.Join(filepath.Dir(socket), "dead.sock")
	ln, err := net.Listen("unix", dead)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", dead)
	for _, id := range []string{"ENG-1", "ENG-2"} {
		_, err = h.run("watch", id)
		require.NoError(t, err)
	}

	_, err = h.run("tick")
	require.NoError(t, err)

	b, err := os.ReadFile(filepath.Join(h.state, "watchers"))
	require.NoError(t, err)
	assert.Equal(t, "\n", string(b))
}

func TestWatchRefusesAnythingButATicketIdentifier(t *testing.T) {
	for _, arg := range []string{"banana", "ENG286", "ENG-", "ENG-1x"} {
		h := newHarness(t)
		t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/cc-socks/42.sock")

		_, err := h.run("watch", arg)

		require.ErrorContains(t, err, "not a ticket identifier")
		assert.NoFileExists(t, filepath.Join(h.state, "watchers"))
	}
}

func TestUnwatchAcceptsALowercaseTicket(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/cc-socks/42.sock")
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)

	_, err = h.run("unwatch", "eng-1")
	require.NoError(t, err)

	b, err := os.ReadFile(filepath.Join(h.state, "watchers"))
	require.NoError(t, err)
	assert.Equal(t, "\n", string(b))
}

func TestWatchingATicketTwiceRecordsItOnce(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/cc-socks/42.sock")

	for range 2 {
		_, err := h.run("watch", "ENG-1")
		require.NoError(t, err)
	}

	b, err := os.ReadFile(filepath.Join(h.state, "watchers"))
	require.NoError(t, err)
	assert.Equal(t, "ENG-1 /tmp/cc-socks/42.sock\n", string(b))
}

func TestSubscribeIsGone(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/cc-socks/42.sock")

	_, err := h.run("subscribe")

	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(h.state, "supervisors"))
}
