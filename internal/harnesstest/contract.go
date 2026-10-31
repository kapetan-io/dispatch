package harnesstest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/harness"
)

// Subject is a harness under test.
type Subject struct {
	Harness harness.Harness
	// StopInput is what the program's own hook hands dispatch at a turn end. A nil tasks
	// leaves out the count of running background tasks, as a program that can't say does.
	StopInput func(session, dir string, tasks *int) []byte
	// PromptInput is what the program's own hook hands dispatch when text was typed into
	// the session. Empty text is a session that went idle.
	PromptInput func(session, dir, text string) []byte
}

// Scenario is one situation with the outcome every adapter must give.
type Scenario struct {
	Name string
	Run  func(t *testing.T, s Subject)
}

// Scenarios is the contract for harness.Harness. The fake and the Claude Code
// adapter run it in every test run.
var Scenarios = []Scenario{
	{Name: "ProgramRunsTheModelInTheAgentKindItIsDetectedAs", Run: func(t *testing.T, s Subject) {
		program := s.Harness.Program(harness.Start{Model: "opus", PluginRoot: "/plugin"})

		assert.Equal(t, s.Harness.AgentKind(), program.AgentKind)
		assert.NotEmpty(t, program.Name)
		assert.Contains(t, program.Argv, "opus")
	}},
	{Name: "ProgramListsAReadyScreenSoTheKickoffHasWhereToLand", Run: func(t *testing.T, s Subject) {
		program := s.Harness.Program(harness.Start{Model: "opus", PluginRoot: "/plugin"})

		assert.NotNil(t, program.Startup.Ready)
	}},
	{Name: "ProgramIsTheSameForTheSameStart", Run: func(t *testing.T, s Subject) {
		start := harness.Start{Model: "opus", PluginRoot: "/plugin"}

		assert.Equal(t, s.Harness.Program(start), s.Harness.Program(start))
	}},
	{Name: "StopReadsTheSessionAndWhereItRan", Run: func(t *testing.T, s Subject) {
		tasks := 2

		report, dir, ok := s.Harness.Stop(s.StopInput("s1", "/work/tree", &tasks))

		require.True(t, ok)
		assert.Equal(t, "s1", report.Session)
		assert.Equal(t, "/work/tree", dir)
		assert.True(t, report.Reported)
		assert.Equal(t, 2, report.Running)
	}},
	{Name: "StopWithoutATaskCountIsNotReportedAndRunsNone", Run: func(t *testing.T, s Subject) {
		report, _, ok := s.Harness.Stop(s.StopInput("s1", "/work/tree", nil))

		require.True(t, ok)
		assert.False(t, report.Reported)
		assert.Zero(t, report.Running)
	}},
	{Name: "StopLeavesTheTreeAndTimeForTheCaller", Run: func(t *testing.T, s Subject) {
		tasks := 0

		report, _, ok := s.Harness.Stop(s.StopInput("s1", "/work/tree", &tasks))

		require.True(t, ok)
		assert.Empty(t, report.Tree)
		assert.Zero(t, report.At)
	}},
	{Name: "StopOfInputThatIsNotAReportIsDropped", Run: func(t *testing.T, s Subject) {
		_, _, ok := s.Harness.Stop([]byte("not json"))

		assert.False(t, ok)
	}},
	{Name: "PromptKeepsTheTextExactlyAsTyped", Run: func(t *testing.T, s Subject) {
		const typed = "/dispatch:pr-respond  42\nand then stop \t"

		report, dir, ok := s.Harness.Prompt(s.PromptInput("s1", "/work/tree", typed))

		require.True(t, ok)
		assert.Equal(t, "s1", report.Session)
		assert.Equal(t, "/work/tree", dir)
		assert.Equal(t, typed, report.Text)
	}},
	{Name: "PromptOfASessionThatWentIdleHasNoText", Run: func(t *testing.T, s Subject) {
		report, _, ok := s.Harness.Prompt(s.PromptInput("s1", "/work/tree", ""))

		require.True(t, ok)
		assert.Equal(t, "s1", report.Session)
		assert.Empty(t, report.Text)
	}},
	{Name: "PromptOfInputThatIsNotAReportIsDropped", Run: func(t *testing.T, s Subject) {
		_, _, ok := s.Harness.Prompt([]byte("not json"))

		assert.False(t, ok)
	}},
}
