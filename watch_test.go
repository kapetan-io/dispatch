package dispatch_test

import (
	"bufio"
	"errors"
	"fmt"
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
	assert.Contains(t, got, "ENG-1 is ready for review")
	assert.Contains(t, got, prURL1)
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
	assert.Equal(t, []string{readyText1 + ""}, h.notifications())

	// The next tick has nothing new to say.
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, lines)
}

func TestTickMessagesFollowersAboutNeedsHumanAndBlockedWorkers(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		worker string
		want   string
	}{
		{name: "NeedsHuman", status: "Needs Human", worker: "working", want: "ENG-1 is in Needs Human"},
		{name: "Blocked", status: "In Progress", worker: "blocked", want: "ENG-1 is waiting on your answer"},
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

	out, stderr, err := h.runContext(t.Context(), "tick")

	require.NoError(t, err)
	assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
	assert.Contains(t, stderr, "dropped watcher socket "+dead)
	b, err := os.ReadFile(filepath.Join(h.state, "watchers"))
	require.NoError(t, err)
	assert.Equal(t, "ENG-1 "+socket+"\n", string(b))
}

func TestTickDryRunPrintsTheMessagesWithoutSending(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "notify ENG-1: PR ready for review\n"+
		"would message "+socket+": dispatch event (PR ready for review): "+readyText1+" Ticket: https://linear.app/team/issue/ENG-1\n", out)
	assert.Empty(t, lines)
	assert.Empty(t, h.notifications())
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
	h.config(autoMergeConfig)
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
	assert.Equal(t, []string{mergedText1 + ""}, h.notifications())
}

func TestTickMessagesFollowersAboutAnUnresponsiveWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)
	h.tick()
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)

	h.advance(61 * time.Minute)
	h.tick()

	got := next(t, lines)
	assert.Contains(t, got, "ENG-1: the worker hasn't responded to its nudge in an hour")
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
}

func TestTickMessagesFollowersAboutStallNudgeCounts(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)

	for range 6 {
		h.advance(5 * time.Minute)
		h.recordStop("ENG-1", noTasks)
		h.tick()
	}

	got := next(t, lines)
	assert.Contains(t, got, "ENG-1 has had 6 stall nudges with no status change")
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
}

func TestTickMessagesFollowersAboutAnEscalation(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.launchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)

	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, lines)
	_, err = h.run("tick")
	require.NoError(t, err)

	got := next(t, lines)
	assert.Contains(t, got, "ENG-1 is in Needs Human")
	assert.Contains(t, got, "https://linear.app/team/issue/ENG-1")
	assert.Len(t, h.notifications(), 1)
}

func TestTickDryRunPrintsEscalationAndMergeMessages(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(h *harness)
		want  string
	}{
		{
			name: "Merge",
			setup: func(h *harness) {
				h.config(autoMergeConfig)
				h.tickets(ticket("ENG-1", "In Review", "dispatch"))
				h.worker("ENG-1", "idle")
				h.pr("ENG-1", approvedPR("a1"))
			},
			want: "dispatch event (PR approved): " + mergedText1,
		},
		{
			name: "Escalate",
			setup: func(h *harness) {
				h.tickets(ticket("ENG-1", "In Progress", "nosuchrepo"))
			},
			want: "dispatch event: ENG-1 is in Needs Human",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			test.setup(h)
			socket, lines := sessionSocket(t)
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
			_, err := h.run("watch", "ENG-1")
			require.NoError(t, err)

			out, err := h.run("tick", "--dry-run")
			require.NoError(t, err)

			assert.Contains(t, out, fmt.Sprintf("would message %s: %s", socket, test.want))
			assert.Empty(t, lines)
			assert.Empty(t, h.notifications())
		})
	}
}

func TestTickTellsFollowersAboutAMergeWhenTheAlertFails(t *testing.T) {
	for _, test := range []struct {
		name  string
		fails string
		want  string
	}{
		{name: "Merged", want: "ENG-1 was approved and merged"},
		{name: "MergeFailed", fails: "merge-fails", want: "merging"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.config(autoMergeConfig)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", approvedPR("a1"))
			socket, lines := sessionSocket(t)
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
			_, err := h.run("watch", "ENG-1")
			require.NoError(t, err)
			h.tools.Alerts.Fail(errors.New("exit status 1"))
			if test.fails != "" {
				h.writeFile(test.fails, "")
			}

			_, err = h.run("tick")
			require.NoError(t, err)

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

	assert.Equal(t, []string{readyText1 + ""}, h.notifications())
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

func TestTickDryRunListsOnlyTheFollowersOfEachEvent(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.pr("ENG-2", cleanPR("a2"))
	socket1, _ := sessionSocket(t)
	socket2, _ := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket1)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket2)
	_, err = h.run("watch", "ENG-2")
	require.NoError(t, err)

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Contains(t, out, "would message "+socket1+": dispatch event (PR ready for review): ENG-1 is ready")
	assert.Contains(t, out, "would message "+socket2+": dispatch event (PR ready for review): ENG-2 is ready")
	assert.NotContains(t, out, "would message "+socket1+": dispatch event (PR ready for review): ENG-2")
	assert.NotContains(t, out, "would message "+socket2+": dispatch event (PR ready for review): ENG-1")
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
