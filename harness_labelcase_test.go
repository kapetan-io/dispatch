package dispatch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kapetan-io/dispatch/internal/linear"
)

func TestTickLaunchesOnTheTableOfTheRepoSpelledExactlyLikeTheLabel(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: harnessTables + "[rules]\nworker = \"harness.claude-sonnet\"\n"})
	d.repoSettings("dispatch", "worker = \"harness.claude-opus\"\n")
	d.addRepo("Dispatch", t.TempDir())
	d.repoSettings("Dispatch", "worker = \"harness.claude-sonnet\"\n")
	d.Linear.Tickets(labeledAs("ENG-1", linear.Label{Name: "ai"}, linear.Label{Name: "dispatch", Group: "repo"}))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model opus"}, d.Workers.Launched())
}
