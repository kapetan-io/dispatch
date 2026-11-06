package herdr_test

// Package exception: this package wraps the rex tool, so every test here is an adapter test of it.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

// listener is one Changes call, with the panes it has named.
type listener struct {
	mu    sync.Mutex
	panes []string
	done  chan error
	stop  context.CancelFunc
}

func listen(t *testing.T, rex *herdr.Rex) *listener {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	l := &listener{done: make(chan error, 1), stop: cancel}
	go func() {
		l.done <- rex.Changes(ctx, func(pane string) {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.panes = append(l.panes, pane)
		})
	}()
	t.Cleanup(cancel)
	return l
}

func (l *listener) named() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.panes)
}

func (l *listener) ended(t *testing.T) error {
	t.Helper()
	select {
	case err := <-l.done:
		return err
	case <-time.After(10 * time.Second):
		require.FailNow(t, "Changes did not return")
		return nil
	}
}

// streamRig has one worker block and a stream that is live, which is when the block's pane was named.
func streamRig(t *testing.T) (r *standInRig, pane string, l *listener) {
	t.Helper()
	r = newStandInRig(t)
	tree := shortTree(t)
	r.edit(t, func(s *standInRex) {
		session, block := s.block("repo", "ENG-1", standInBlock{Label: workerLabelOf("01234567", tree), Argv0: "claude"})
		pane = session + "/" + block
	})
	l = listen(t, r.rex)
	require.Eventually(t, func() bool { return slices.Contains(l.named(), pane) }, 10*time.Second, 5*time.Millisecond)
	return r, pane, l
}

func sessionEvent(session, event, name, block string) string {
	return fmt.Sprintf(`{"type":"session_event","payload":{"session_id":%q,"event":{"type":%q,"payload":{"block_id":%q,"name":%q}}}}`, session, event, block, name)
}

func TestRexStreamNamesThePaneOfEveryWorkerOnceItIsLive(t *testing.T) {
	r := newStandInRig(t)
	tree1, tree2 := shortTree(t), shortTree(t)
	var panes []string
	r.edit(t, func(s *standInRex) {
		for _, tree := range []string{tree1, tree2} {
			session, block := s.block("repo", "ENG-1", standInBlock{Label: workerLabelOf("01234567", tree), Argv0: "claude"})
			panes = append(panes, session+"/"+block)
		}
		s.block("repo", "notes", standInBlock{Label: "user shell", Argv0: "zsh"})
	})

	l := listen(t, r.rex)

	require.Eventually(t, func() bool { return len(l.named()) >= 2 }, 10*time.Second, 5*time.Millisecond)
	assert.ElementsMatch(t, panes, l.named())
}

func TestRexStreamNamesThePaneOfOnlyTheLinesThatChangeABlocksAgent(t *testing.T) {
	for _, test := range []struct {
		name string
		line func(session, block string) string
		want bool
	}{
		{"StatusChanged", func(s, b string) string { return sessionEvent(s, "block_event", "program_status_changed", b) }, true},
		{"StatusRemoved", func(s, b string) string { return sessionEvent(s, "block_event", "program_status_removed", b) }, true},
		{"ChildExited", func(s, b string) string { return sessionEvent(s, "block_event", "child_exited", b) }, true},
		{"BlockClosed", func(s, b string) string { return sessionEvent(s, "block_closed", "", b) }, true},
		{"ProcessChanged", func(s, b string) string { return sessionEvent(s, "block_event", "process_changed", b) }, false},
		{"UnknownBlockEvent", func(s, b string) string { return sessionEvent(s, "block_event", "title_changed", b) }, false},
		{"UnknownSessionEvent", func(s, b string) string { return sessionEvent(s, "window_opened", "", b) }, false},
		{"ClientJoined", func(s, b string) string { return `{"type":"client_joined"}` }, false},
		{"BlockEventWithNoBlock", func(s, b string) string { return sessionEvent(s, "block_event", "child_exited", "") }, false},
		{"Malformed", func(s, b string) string { return `{"type":"session_event",` }, false},
		{"NotJSON", func(s, b string) string { return "rex events: subscribed" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, pane, l := streamRig(t)
			session, block, _ := strings.Cut(pane, "/")
			before := len(l.named())

			appendStreamLines(t, r, test.line(session, block), sessionEvent(session, "block_event", "program_status_changed", "sentinel"))

			require.Eventually(t, func() bool { return slices.Contains(l.named()[before:], session+"/sentinel") }, 10*time.Second, 5*time.Millisecond)
			want := []string{session + "/sentinel"}
			if test.want {
				want = []string{pane, session + "/sentinel"}
			}
			assert.Equal(t, want, l.named()[before:])
		})
	}
}

func TestRexStreamReadsALineLongerThanAScreenOfText(t *testing.T) {
	r, pane, l := streamRig(t)
	session, block, _ := strings.Cut(pane, "/")
	before := len(l.named())
	long := make([]byte, 300_000)
	for i := range long {
		long[i] = 'x'
	}

	appendStreamLines(t, r, fmt.Sprintf(`{"type":"session_event","payload":{"session_id":%q,"event":{"type":"block_event","payload":{"block_id":%q,"name":"child_exited","text":%q}}}}`, session, block, long))

	require.Eventually(t, func() bool { return len(l.named()) > before }, 10*time.Second, 5*time.Millisecond)
	assert.Equal(t, pane, l.named()[before])
}

func TestRexStreamSkipsALineOverTheCapAndNamesTheNext(t *testing.T) {
	r, pane, l := streamRig(t)
	session, block, _ := strings.Cut(pane, "/")
	before := len(l.named())

	overCap := strings.Replace(sessionEvent(session, "block_event", "child_exited", "over-cap"), `"name"`, `"text":"`+strings.Repeat("x", 2<<20)+`","name"`, 1)

	appendStreamLines(t, r, overCap, sessionEvent(session, "block_event", "program_status_changed", block))

	require.Eventually(t, func() bool { return len(l.named()) > before }, 10*time.Second, 5*time.Millisecond)
	assert.Equal(t, []string{pane}, l.named()[before:])
}

func TestRexStreamReturnsNilWhenItsContextEnds(t *testing.T) {
	_, _, l := streamRig(t)

	l.stop()

	assert.NoError(t, l.ended(t))
}

func TestRexStreamReportsAServerThatIsNotRunningWhenItStarts(t *testing.T) {
	r := newStandInRig(t)
	r.edit(t, func(s *standInRex) { s.Down = true })

	err := listen(t, r.rex).ended(t)

	require.ErrorIs(t, err, herdr.ErrRexNotRunning)
}

func TestRexStreamReportsAnEndTheServerCaused(t *testing.T) {
	for _, test := range []struct {
		name string
		exit int
		is   error
	}{
		{"ServerStopped", 4, herdr.ErrRexNotRunning},
		{"CleanEnd", 0, herdr.ErrRexEventsEnded},
		{"FailedEnd", 1, herdr.ErrRexEventsEnded},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, _, l := streamRig(t)

			appendStreamLines(t, r, standInExitEvent(test.exit))

			require.ErrorIs(t, l.ended(t), test.is)
		})
	}
}

func appendStreamLines(t *testing.T, r *standInRig, lines ...string) {
	t.Helper()
	for _, line := range lines {
		appendEvent(r.state, line)
	}
}
