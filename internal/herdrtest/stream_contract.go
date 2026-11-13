package herdrtest

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

// StreamSubject is a Subject whose herdr also reports its changes.
type StreamSubject struct {
	Subject
	Stream herdr.Stream
	// Exit ends the agent in the pane, as Claude quitting does.
	Exit func(pane string)
}

// StreamScenario is one situation with the panes both the fake and the real stream must name.
type StreamScenario struct {
	Name string
	Run  func(t *testing.T, s StreamSubject)
}

// open starts the stream and returns the panes it has named so far and a stop that ends it and
// returns what Changes returned.
func open(t *testing.T, stream herdr.Stream) (named func() []string, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var panes []string
	done := make(chan error, 1)
	go func() {
		done <- stream.Changes(ctx, func(pane string) {
			mu.Lock()
			defer mu.Unlock()
			panes = append(panes, pane)
		})
	}()
	var ended bool
	stop = func() error {
		cancel()
		if ended {
			return nil
		}
		ended = true
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			require.FailNow(t, "the stream did not end when its context ended")
			return nil
		}
	}
	t.Cleanup(func() { _ = stop() })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(panes)
	}, stop
}

func count(panes []string, pane string) int {
	return len(slices.DeleteFunc(slices.Clone(panes), func(p string) bool { return p != pane }))
}

const streamWait = 30 * time.Second

// StreamScenarios is the contract for herdr.Stream. The fake runs it in every test run, and the Rex
// adapter runs it under the drift build tag against a scratch Rex server.
var StreamScenarios = []StreamScenario{
	{Name: "StreamNamesThePaneOfEachAgentOnceItIsLive", Run: func(t *testing.T, s StreamSubject) {
		first, second := s.Workspace("alpha"), s.Workspace("beta")
		s.Agent(first.Pane, "idle")
		s.Agent(second.Pane, "working")

		named, _ := open(t, s.Stream)

		require.Eventually(t, func() bool {
			return count(named(), first.Pane) > 0 && count(named(), second.Pane) > 0
		}, streamWait, 10*time.Millisecond)
	}},
	{Name: "StreamNamesThePaneOfAStatusChange", Run: func(t *testing.T, s StreamSubject) {
		ws := s.Workspace("alpha")
		s.Agent(ws.Pane, "working")
		named, _ := open(t, s.Stream)
		require.Eventually(t, func() bool { return count(named(), ws.Pane) > 0 }, streamWait, 10*time.Millisecond)
		live := count(named(), ws.Pane)

		s.Agent(ws.Pane, "idle")

		require.Eventually(t, func() bool { return count(named(), ws.Pane) > live }, streamWait, 10*time.Millisecond)
	}},
	{Name: "StreamNamesThePaneOfAClosedTab", Run: func(t *testing.T, s StreamSubject) {
		ws := s.Workspace("alpha")
		tab := s.Tab(ws.Workspace, "beta")
		s.Agent(tab.Pane, "idle")
		named, _ := open(t, s.Stream)
		require.Eventually(t, func() bool { return count(named(), tab.Pane) > 0 }, streamWait, 10*time.Millisecond)
		live := count(named(), tab.Pane)

		require.NoError(t, s.Multiplexer.CloseTab(context.Background(), tab.Tab))

		require.Eventually(t, func() bool { return count(named(), tab.Pane) > live }, streamWait, 10*time.Millisecond)
	}},
	{Name: "StreamNamesThePaneOfAnAgentThatExits", Run: func(t *testing.T, s StreamSubject) {
		ws := s.Workspace("alpha")
		s.Agent(ws.Pane, "idle")
		named, _ := open(t, s.Stream)
		require.Eventually(t, func() bool { return count(named(), ws.Pane) > 0 }, streamWait, 10*time.Millisecond)
		live := count(named(), ws.Pane)

		s.Exit(ws.Pane)

		require.Eventually(t, func() bool { return count(named(), ws.Pane) > live }, streamWait, 10*time.Millisecond)
		_, found, err := s.Multiplexer.AgentAt(context.Background(), ws.Pane)
		require.NoError(t, err)
		assert.False(t, found)
	}},
	{Name: "AgentAtShowsThePaneAStatusChangeNamed", Run: func(t *testing.T, s StreamSubject) {
		ws := s.Workspace("alpha")
		s.Agent(ws.Pane, "working")
		named, _ := open(t, s.Stream)
		require.Eventually(t, func() bool { return count(named(), ws.Pane) > 0 }, streamWait, 10*time.Millisecond)
		live := count(named(), ws.Pane)

		s.Agent(ws.Pane, "idle")
		require.Eventually(t, func() bool { return count(named(), ws.Pane) > live }, streamWait, 10*time.Millisecond)

		agent, found, err := s.Multiplexer.AgentAt(context.Background(), ws.Pane)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "idle", agent.Status)
		snap, err := s.Multiplexer.Read(context.Background())
		require.NoError(t, err)
		require.Len(t, snap.Agents, 1)
		assert.Equal(t, agent.Status, snap.Agents[0].Status)
	}},
	{Name: "StreamEndsWithNothingWhenItsContextEnds", Run: func(t *testing.T, s StreamSubject) {
		ws := s.Workspace("alpha")
		s.Agent(ws.Pane, "idle")
		named, stop := open(t, s.Stream)
		require.Eventually(t, func() bool { return count(named(), ws.Pane) > 0 }, streamWait, 10*time.Millisecond)

		assert.NoError(t, stop())
	}},
}
