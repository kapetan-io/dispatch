package lineartest_test

import (
	"testing"

	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/lineartest"
)

func TestWorkspaceKeepsTheLinearContract(t *testing.T) {
	for _, scenario := range lineartest.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			workspace := lineartest.New(&journal.Journal{})
			scenario.Run(t, lineartest.Subject{
				Connect: workspace.Connect,
				Key:     lineartest.Key,
				Team:    "ENG",
				Seed:    workspace.Seed,
				Viewer:  workspace.Viewer(),
			})
		})
	}
}
