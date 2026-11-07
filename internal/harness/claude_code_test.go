package harness_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/harnesstest"
)

var sessionSeq atomic.Int64

// listEntry writes the session list entry Claude Code keeps for a running session.
func listEntry(t *testing.T, sessions, id, socket string, startedAt int64) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"sessionId": id, "messagingSocketPath": socket, "startedAt": startedAt})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sessions, fmt.Sprintf("%d.json", sessionSeq.Add(1))), body, 0o600))
}

// openInbox listens on a socket the way a Claude Code session's inbox does, lists the
// session in sessions, and records each message frame it receives.
func openInbox(t *testing.T, sessions string) harnesstest.Session {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ds")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "1.sock")
	id := fmt.Sprintf("session-%d", sessionSeq.Add(1))
	listEntry(t, sessions, id, path, 1)
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
		Env:     environment.Environment{"CLAUDE_CODE_SESSION_ID=" + id},
		Address: id,
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
			sessions := t.TempDir()
			scenario.Run(t, harnesstest.Subject{
				Harness:     harness.ClaudeCode{Sessions: sessions},
				OpenSession: func(t *testing.T) harnesstest.Session { return openInbox(t, sessions) },
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
	assert.ErrorContains(t, err, "CLAUDE_CODE_SESSION_ID is not set, so this is not a Claude Code session; run watch from one")
}

func TestClaudeCodeFollowerIsTheSessionID(t *testing.T) {
	id, err := harness.ClaudeCode{}.Follower(environment.Environment{"CLAUDE_CODE_SESSION_ID=3f2a"})

	require.NoError(t, err)
	assert.Equal(t, "3f2a", id)
}

func TestClaudeCodeDeliverReachesTheNewestEntryOfASession(t *testing.T) {
	sessions := t.TempDir()
	old := openInbox(t, sessions)
	listEntry(t, sessions, old.Address, "/tmp/dead-socket-that-does-not-exist.sock", 0)

	require.NoError(t, harness.ClaudeCode{Sessions: sessions}.Deliver(context.Background(), old.Address, "hello"))

	require.Eventually(t, func() bool { return slices.Contains(old.Delivered(), "hello") }, 5*time.Second, 10*time.Millisecond)
}

func TestClaudeCodeDeliverToAnIDWithNoEntryIsGone(t *testing.T) {
	err := harness.ClaudeCode{Sessions: t.TempDir()}.Deliver(context.Background(), "missing", "hello")

	assert.ErrorIs(t, err, harness.ErrGone)
}

func TestClaudeCodeDeliverToARefusedSocketIsGone(t *testing.T) {
	sessions := t.TempDir()
	session := openInbox(t, sessions)
	session.Exit()

	err := harness.ClaudeCode{Sessions: sessions}.Deliver(context.Background(), session.Address, "hello")

	assert.ErrorIs(t, err, harness.ErrGone)
}

func TestClaudeCodeDeliverWithoutASessionListIsNotGone(t *testing.T) {
	err := harness.ClaudeCode{}.Deliver(context.Background(), "s1", "hello")

	require.Error(t, err)
	assert.NotErrorIs(t, err, harness.ErrGone)
}

func TestClaudeCodeDeliverThatBreaksMidWriteIsNotGone(t *testing.T) {
	sessions := t.TempDir()
	dir, err := os.MkdirTemp("/tmp", "ds")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "1.sock")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	listEntry(t, sessions, "s1", path, 1)

	err = harness.ClaudeCode{Sessions: sessions}.Deliver(context.Background(), "s1", strings.Repeat("x", 4<<20))

	require.Error(t, err)
	assert.NotErrorIs(t, err, harness.ErrGone)
}

func TestClaudeCodeDeliverWithOnlyAnUnreadableListEntryIsNotGone(t *testing.T) {
	sessions := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sessions, "1.json"), []byte(`{"sessionId":"s1","messagingSo`), 0o600))

	err := harness.ClaudeCode{Sessions: sessions}.Deliver(context.Background(), "s1", "hello")

	require.Error(t, err)
	assert.NotErrorIs(t, err, harness.ErrGone)
}
