package harness_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/harnesstest"
)

func TestClaudeCodeKeepsTheHarnessContract(t *testing.T) {
	for _, scenario := range harnesstest.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			scenario.Run(t, harnesstest.Subject{Harness: harness.ClaudeCode{}})
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
