package dispatch_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnknownConfigKeyFailsEveryCommand(t *testing.T) {
	for _, test := range []struct {
		name   string
		config string
		args   []string
		want   string
	}{
		{"TickTopLevel", "max_in_progres = 9\n", []string{"tick"}, `unknown setting max_in_progres`},
		{"TickDryRun", "max_in_progres = 9\n", []string{"tick", "--dry-run"}, `unknown setting max_in_progres`},
		{"RunWithoutInterval", "max_in_progres = 9\n", []string{"run"}, `unknown setting max_in_progres`},
		{"Tell", "max_in_progres = 9\n", []string{"tell", "ENG-1", "hello"}, `unknown setting max_in_progres`},
		{"BlueprintNeedsHumanTable", "[linear.needs_human]\nstatus = \"Needs Human\"\n", []string{"tick"}, `unknown setting linear.needs_human`},
		{"StatusesTodoTypo", "[linear.statuses]\ntodoo = \"Todo\"\n", []string{"tick"}, `unknown setting linear.statuses.todoo`},
		{"TwoUnknownKeys", "nudge_limt = 1\nworker_modle = \"opus\"\n", []string{"tick"}, `unknown setting nudge_limt, worker_modle`},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.config(test.config)

			_, err := h.run(test.args...)

			require.ErrorContains(t, err, test.want)
			require.ErrorContains(t, err, filepath.Join(h.dir, "config.toml"))
		})
	}
}

func TestUnknownConfigKeyTakesNoAction(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progres = 9\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick")

	require.Error(t, err)
	assert.Empty(t, h.launches())
	assert.Empty(t, h.linear.mutations())
}

func TestKnownConfigKeysBehaveAsBefore(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n[linear.statuses]\nin_progress = \"In Progress\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1\n", out)
}
