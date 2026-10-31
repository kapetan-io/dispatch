package harnesstest

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kapetan-io/dispatch/internal/harness"
)

// Subject is a harness under test.
type Subject struct {
	Harness harness.Harness
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
}
