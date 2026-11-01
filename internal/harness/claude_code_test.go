package harness_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/harnesstest"
)

// openInbox listens on a socket the way a Claude Code session's inbox does and records each
// message frame it receives.
func openInbox(t *testing.T) harnesstest.Session {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ds")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "1.sock")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	// A crashed session leaves its socket file behind.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	var got []string
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
					var frame struct {
						Message struct {
							Content string `json:"content"`
						} `json:"message"`
					}
					if json.Unmarshal(sc.Bytes(), &frame) == nil {
						mu.Lock()
						got = append(got, frame.Message.Content)
						mu.Unlock()
					}
				}
			}()
		}
	}()
	return harnesstest.Session{
		Env:     environment.Environment{"CLAUDE_CODE_MESSAGING_SOCKET=" + path},
		Address: path,
		Delivered: func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), got...)
		},
		Exit: func() { _ = ln.Close() },
	}
}

func TestClaudeCodeKeepsTheHarnessContract(t *testing.T) {
	for _, scenario := range harnesstest.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			scenario.Run(t, harnesstest.Subject{
				Harness:     harness.ClaudeCode{},
				OpenSession: openInbox,
				StopInput: func(session, dir string, tasks *int) []byte {
					in := map[string]any{"hook_event_name": "Stop", "session_id": session, "cwd": dir}
					if tasks != nil {
						running := make([]map[string]string, *tasks)
						for i := range running {
							running[i] = map[string]string{"status": "running"}
						}
						in["background_tasks"] = running
					}
					b, _ := json.Marshal(in)
					return b
				},
				PromptInput: func(session, dir, text string) []byte {
					in := map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": session, "cwd": dir}
					if text == "" {
						in = map[string]any{"hook_event_name": "Notification", "notification_type": "idle_prompt", "session_id": session, "cwd": dir}
					} else {
						in["prompt"] = text
					}
					b, _ := json.Marshal(in)
					return b
				},
			})
		})
	}
}

func TestClaudeCodeProgramRunsTheWorkerFlagsThroughTheShims(t *testing.T) {
	program := harness.ClaudeCode{}.Program(harness.Start{Model: "opus", PluginRoot: "/plugin"})

	assert.Equal(t, "claude", program.AgentKind)
	assert.Equal(t, "Claude", program.Name)
	assert.Equal(t, []string{
		"--model", "opus", "--plugin-dir", "/plugin",
		"--disallowedTools", "EnterPlanMode,ExitPlanMode", "--settings", `{"crossSessionInbound":"accept"}`,
		"--dangerously-skip-permissions",
	}, program.Argv)
	assert.Equal(t, []string{"/plugin/scripts/shims"}, program.PathFirst)
}

func TestClaudeCodeStopCountsOnlyTheBackgroundTasksThatRun(t *testing.T) {
	input := `{"hook_event_name":"Stop","session_id":"s1","cwd":"/t","background_tasks":[{"status":"running"},{"status":"completed"},{"status":"running"}]}`

	report, _, ok := harness.ClaudeCode{}.Stop([]byte(input))

	assert.True(t, ok)
	assert.True(t, report.Reported)
	assert.Equal(t, 2, report.Running)
}

func TestClaudeCodeStopWithAnEmptyTaskListIsReportedAsRunningNone(t *testing.T) {
	report, _, ok := harness.ClaudeCode{}.Stop([]byte(`{"session_id":"s1","cwd":"/t","background_tasks":[]}`))

	assert.True(t, ok)
	assert.True(t, report.Reported)
	assert.Zero(t, report.Running)
}

func TestClaudeCodeFollowerOutsideASessionSaysWhereToRunWatch(t *testing.T) {
	_, err := harness.ClaudeCode{}.Follower(environment.Environment{})

	assert.ErrorIs(t, err, harness.ErrNotThisHarness)
	assert.ErrorContains(t, err, "CLAUDE_CODE_MESSAGING_SOCKET is not set, so this is not a Claude Code session; run watch from one")
}

func TestClaudeCodeFollowerRefusesARelativeSocket(t *testing.T) {
	_, err := harness.ClaudeCode{}.Follower(environment.Environment{"CLAUDE_CODE_MESSAGING_SOCKET=1.sock"})

	assert.ErrorContains(t, err, "not absolute")
	assert.NotErrorIs(t, err, harness.ErrNotThisHarness)
}
