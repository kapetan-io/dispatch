package dispatch_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// fakeDispatch listens where the loop would and hands each connection to answer.
func fakeDispatch(t *testing.T, stateDir string, answer func(net.Conn)) {
	t.Helper()
	ln, err := net.Listen("unix", loop.SocketPath(stateDir))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				answer(conn)
			}()
		}
	}()
}

func TestRecordStopReportsTheTurnAndWritesNothingToTheStateDir(t *testing.T) {
	h := newHarness(t)
	input := h.stopInput("worker", "ENG-1", runningTasks)
	got := make(chan loop.Request, 1)
	fakeDispatch(t, h.state, func(conn net.Conn) {
		var req loop.Request
		if json.NewDecoder(conn).Decode(&req) == nil {
			got <- req
			_ = json.NewEncoder(conn).Encode(loop.Response{})
		}
	})
	entries, err := os.ReadDir(h.state)
	require.NoError(t, err)

	err = dispatch.Run(t.Context(), []string{"record-stop"}, h.options(dispatch.Options{Stdin: strings.NewReader(input)}))

	require.NoError(t, err)
	req := <-got
	assert.Equal(t, loop.OpStop, req.Op)
	require.NotNil(t, req.Stop)
	assert.Equal(t, "worker", req.Stop.Session)
	tree, err := filepath.EvalSymlinks(filepath.Join(h.dir, "trees", "ENG-1"))
	require.NoError(t, err)
	assert.Equal(t, tree, req.Stop.Tree)
	assert.True(t, req.Stop.Reported)
	assert.Equal(t, 1, req.Stop.Running)
	after, err := os.ReadDir(h.state)
	require.NoError(t, err)
	assert.Equal(t, len(entries), len(after))
	assert.Empty(t, stopRecords(t, h.state))
	assert.NoDirExists(t, filepath.Join(h.state, "sessions"))
}

func TestRecordStopStaysSilentHoweverTheConnectionFails(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, h *harness)
	}{
		{"StaleSocketFile", func(t *testing.T, h *harness) {
			ln, err := net.Listen("unix", loop.SocketPath(h.state))
			require.NoError(t, err)
			ln.(*net.UnixListener).SetUnlinkOnClose(false)
			require.NoError(t, ln.Close())
		}},
		{"ClosesWithoutAnswering", func(t *testing.T, h *harness) {
			fakeDispatch(t, h.state, func(conn net.Conn) {})
		}},
		{"AnswersGarbage", func(t *testing.T, h *harness) {
			fakeDispatch(t, h.state, func(conn net.Conn) { _, _ = conn.Write([]byte("not json\n")) })
		}},
		{"AnswersWithAnError", func(t *testing.T, h *harness) {
			fakeDispatch(t, h.state, func(conn net.Conn) {
				_ = json.NewEncoder(conn).Encode(loop.Response{Code: loop.CodeFailed, Error: "disk full"})
			})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			test.setup(t, h)
			var stdout, stderr strings.Builder
			opts := h.options(dispatch.Options{Stdin: strings.NewReader(h.stopInput("worker", "ENG-1", noTasks))})
			opts.Stdout, opts.Stderr = &stdout, &stderr
			start := time.Now()

			err := dispatch.Run(t.Context(), []string{"record-stop"}, opts)

			require.NoError(t, err)
			assert.Empty(t, stdout.String())
			assert.Empty(t, stderr.String())
			assert.Less(t, time.Since(start), 5*time.Second)
			assert.Empty(t, stopRecords(t, h.state))
		})
	}
}

func TestRecordStopExitsCleanlyOnABadConfig(t *testing.T) {
	h := newHarness(t)
	input := h.stopInput("worker", "ENG-1", noTasks)
	opts := h.options(dispatch.Options{Stdin: strings.NewReader(input)})
	require.NoError(t, os.WriteFile(opts.ConfigPath, []byte("this is = = not toml"), 0o644))
	var stdout strings.Builder
	opts.Stdout = &stdout

	err := dispatch.Run(t.Context(), []string{"record-stop"}, opts)

	require.NoError(t, err)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stopRecords(t, h.state))
}

func TestRulesReadTurnsAfterTheLoopRestarts(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.startBeforeTheEvents()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", runningTasks)
	h.advance(time.Minute)
	h.recordStop("ENG-1", noTasks)

	h.restart()
	h.tick()

	assert.Equal(t, []string{"turns running,idle of 2"}, h.turnComments())
}

func TestWatchWritesNoFileToTheStateDir(t *testing.T) {
	h := newHarness(t)
	h.startBeforeTheEvents()
	socket, _ := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	entries, err := os.ReadDir(h.state)
	require.NoError(t, err)

	_, err = h.run("watch", "ENG-1")
	require.NoError(t, err)
	_, err = h.run("unwatch", "ENG-1")
	require.NoError(t, err)

	after, err := os.ReadDir(h.state)
	require.NoError(t, err)
	assert.Equal(t, len(entries), len(after))
}

func TestFollowersAreForgottenWhenDispatchRestarts(t *testing.T) {
	h := newHarness(t)
	h.startBeforeTheEvents()
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)

	h.restart()
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.tick()

	assert.Empty(t, lines)
	assert.Equal(t, []string{readyText1}, h.notifications())
}

func TestALateStopReportDoesNotReplaceANewerOne(t *testing.T) {
	h := newHarness(t)
	h.startBeforeTheEvents()
	const tree, session = "/trees/ENG-1", "worker"
	newer := loop.Stop{Tree: tree, Session: session, At: 2000, Reported: true, Running: 1}
	older := loop.Stop{Tree: tree, Session: session, At: 1000, Reported: true}

	for _, report := range []loop.Stop{newer, older} {
		resp, err := loop.Ask(t.Context(), h.state, loop.Request{Op: loop.OpStop, Stop: &report})
		require.NoError(t, err)
		require.Empty(t, resp.Error)
	}

	records := stopRecords(t, h.state)
	require.Len(t, records, 1)
	assert.Contains(t, records[0], `"running":1`)
	log, err := sessionlog.Read(h.state, tree, session)
	require.NoError(t, err)
	assert.Equal(t, []sessionlog.Turn{{At: 2000, Running: true}}, log.Turns)
}

func TestAStopReportFromBeforeAClockStepIsKept(t *testing.T) {
	h := newHarness(t)
	h.startBeforeTheEvents()
	const tree, session = "/trees/ENG-1", "worker"
	ahead := h.clock.Now().Add(time.Hour).UnixMilli()
	stale := loop.Stop{Tree: tree, Session: session, At: ahead, Reported: true, Running: 1}
	fresh := loop.Stop{Tree: tree, Session: session, At: h.clock.Now().UnixMilli(), Reported: true}

	for _, report := range []loop.Stop{stale, fresh} {
		resp, err := loop.Ask(t.Context(), h.state, loop.Request{Op: loop.OpStop, Stop: &report})
		require.NoError(t, err)
		require.Empty(t, resp.Error)
	}

	records := stopRecords(t, h.state)
	require.Len(t, records, 1)
	assert.Contains(t, records[0], `"running":0`)
}
