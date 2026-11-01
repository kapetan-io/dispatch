package harness_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/harnesstest"
)

func TestClaudeCodeKeepsTheHarnessContract(t *testing.T) {
	for _, scenario := range harnesstest.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			scenario.Run(t, harnesstest.Subject{
				Harness: harness.ClaudeCode{},
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
