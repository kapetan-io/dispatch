package herdr_test

// Package exception: this package wraps the herdr tool, so every test here is an adapter or contract test of it.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

const envReplies = "HERDR_TEST_REPLIES"

type reply struct {
	Stdout string
	Stderr string
	Exit   int
}

// The test binary stands in for the herdr command when envReplies is set, so
// the adapter's reading of herdr output is tested without a shell script. It
// answers each command line with the reply the test gave for it.
func TestMain(m *testing.M) {
	// The race detector sleeps 1s at exit by default. The stand-in processes this binary re-execs inherit this setting.
	_ = os.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	if dir, ok := os.LookupEnv(envState); ok {
		os.Exit(runStatefulStandIn(dir, os.Args[1:]))
	}
	if raw, ok := os.LookupEnv(envReplies); ok {
		var replies map[string]reply
		if err := json.Unmarshal([]byte(raw), &replies); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		r, ok := replies[strings.Join(os.Args[1:], " ")]
		if !ok {
			fmt.Fprintln(os.Stderr, "unexpected command line:", os.Args[1:])
			os.Exit(2)
		}
		_, _ = fmt.Fprint(os.Stdout, r.Stdout)
		_, _ = fmt.Fprint(os.Stderr, r.Stderr)
		os.Exit(r.Exit)
	}
	os.Exit(m.Run())
}

func standIn(t *testing.T, replies map[string]reply, global ...string) *herdr.CLI {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	raw, err := json.Marshal(replies)
	require.NoError(t, err)
	t.Setenv(envReplies, string(raw))
	return herdr.NewCLI(exe, global...)
}

const (
	workspaces = `{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"ENG-1","worktree":{"checkout_path":"/t/1"}},{"workspace_id":"w2","label":"bare"}]}}`
	tabs       = `{"id":"cli:tab:list","result":{"tabs":[{"tab_id":"w1:t1","label":"ENG-1","workspace_id":"w1"}],"type":"tab_list"}}`
	panes      = `{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1"}],"type":"pane_list"}}`
	agents     = `{"id":"cli:agent:list","result":{"agents":[{"agent":"claude","agent_status":"idle","pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","agent_session":{"value":"abc"}}],"type":"agent_list"}}`
)

func TestCLIReadsWhatHerdrLists(t *testing.T) {
	cli := standIn(t, map[string]reply{
		"workspace list": {Stdout: workspaces},
		"tab list":       {Stdout: tabs},
		"pane list":      {Stdout: panes},
		"agent list":     {Stdout: agents},
	})

	snap, err := cli.Read(context.Background())

	require.NoError(t, err)
	assert.Equal(t, herdr.Snapshot{
		Workspaces: []herdr.Workspace{{ID: "w1", Label: "ENG-1", CheckoutPath: "/t/1"}, {ID: "w2", Label: "bare"}},
		Tabs:       []herdr.Tab{{ID: "w1:t1", Label: "ENG-1", WorkspaceID: "w1"}},
		Panes:      []herdr.Pane{{ID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1"}},
		Agents:     []herdr.Agent{{Kind: "claude", Status: "idle", PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1", Session: "abc"}},
	}, snap)
}

func TestCLIReadStillWorksWhenTabsOrPanesCannotBeListed(t *testing.T) {
	cli := standIn(t, map[string]reply{
		"workspace list": {Stdout: workspaces},
		"tab list":       {Exit: 2, Stderr: "usage"},
		"pane list":      {Exit: 2, Stderr: "usage"},
		"agent list":     {Stdout: agents},
	})

	snap, err := cli.Read(context.Background())

	require.NoError(t, err)
	assert.Len(t, snap.Workspaces, 2)
	assert.Len(t, snap.Agents, 1)
	assert.Empty(t, snap.Tabs)
	assert.Empty(t, snap.Panes)
}

func TestCLIReadFailsWhenWorkspacesOrAgentsCannotBeListed(t *testing.T) {
	for _, test := range []struct {
		name    string
		replies map[string]reply
		wantErr string
	}{
		{
			name:    "WorkspaceListFails",
			replies: map[string]reply{"workspace list": {Exit: 1}},
			wantErr: "while running herdr [workspace list]",
		},
		{
			name:    "AgentListFails",
			replies: map[string]reply{"workspace list": {Stdout: workspaces}, "agent list": {Exit: 1}},
			wantErr: "while running herdr [agent list]",
		},
		{
			name:    "WorkspaceListIsNotJSON",
			replies: map[string]reply{"workspace list": {Stdout: "not json"}},
			wantErr: "while reading herdr [workspace list] output",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := standIn(t, test.replies).Read(context.Background())

			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestCLIReportsWhatHerdrRefuses(t *testing.T) {
	const blocked = `{"error":{"code":"agent_blocked","message":"agent w1:p1 is blocked and requires interactive input"},"id":"cli:agent:prompt"}`
	cli := standIn(t, map[string]reply{
		"agent prompt w1:p1 hi":         {Exit: 1, Stderr: blocked + "\n"},
		"agent prompt w9:p9 hi":         {Exit: 1, Stderr: `{"error":{"code":"agent_not_found","message":"agent target w9:p9 not found"},"id":"cli:agent:prompt"}`},
		"workspace rename w1 x":         {Exit: 2, Stderr: "error: unrecognized subcommand"},
		"workspace close w9":            {Exit: 1, Stderr: `{"error":{"code":"workspace_not_found","message":"workspace w9 not found"},"id":"cli:workspace:close"}`},
		"tab rename w1:t1 y":            {},
		"tab close w1:t1":               {},
		"tab close w1:t9":               {Exit: 1, Stderr: `{"error":{"code":"tab_not_found","message":"tab w1:t9 not found"},"id":"cli:tab:close"}`},
		"workspace rename w1 renamed":   {},
		"agent prompt w1:p1 keep going": {},
	})
	ctx := context.Background()

	err := cli.Prompt(ctx, "w1:p1", "hi")
	require.ErrorIs(t, err, herdr.ErrAgentBlocked)
	assert.EqualError(t, err, blocked)
	var refusal *herdr.Refusal
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, "agent_blocked", refusal.Code)

	err = cli.Prompt(ctx, "w9:p9", "hi")
	require.ErrorContains(t, err, "agent_not_found")
	assert.NotErrorIs(t, err, herdr.ErrAgentBlocked)

	err = cli.RenameWorkspace(ctx, "w1", "x")
	require.ErrorContains(t, err, "unrecognized subcommand")
	assert.NotErrorIs(t, err, herdr.ErrAgentBlocked)

	require.ErrorContains(t, cli.CloseWorkspace(ctx, "w9"), "workspace_not_found")
	require.ErrorContains(t, cli.CloseTab(ctx, "w1:t9"), "tab_not_found")
	require.NoError(t, cli.RenameTab(ctx, "w1:t1", "y"))
	require.NoError(t, cli.CloseTab(ctx, "w1:t1"))
	require.NoError(t, cli.RenameWorkspace(ctx, "w1", "renamed"))
	require.NoError(t, cli.Prompt(ctx, "w1:p1", "keep going"))
}

func TestCLIPutsItsGlobalArgumentsBeforeTheSubcommand(t *testing.T) {
	cli := standIn(t, map[string]reply{"--session drift workspace close w1": {}}, "--session", "drift")

	require.NoError(t, cli.CloseWorkspace(context.Background(), "w1"))
}
