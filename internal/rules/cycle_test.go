package rules_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/world"
)

// busyWorld is a world the size of a busy day: 60 tickets across every status,
// 40 workers, 20 PRs and 40 trees.
func busyWorld(cfg config.Config, now time.Time) world.World {
	st := cfg.Plugins.Linear.Statuses
	w := world.World{
		At: now, Workers: map[string]world.Worker{}, Stops: map[string]stops.Record{}, Leases: map[string][]world.Worker{},
		Leased: map[string]bool{}, Children: map[string][]linear.Ticket{}, Unfinished: map[string]string{},
		Work: map[string]treehouse.Work{}, CheckErrors: map[string]error{}, Untracked: map[string][]string{},
		PRs: map[string]pullrequest.PullRequest{}, PRReadFailed: map[string]bool{},
		PoolErrs: map[string]error{}, UnreadRepos: map[string]bool{},
	}
	statuses := []string{st.InProgress, st.InReview, st.NeedsHuman}
	states := []world.WorkerState{world.Working, world.Idle, world.Blocked, world.NoWorker}
	for i := range 60 {
		id := fmt.Sprintf("ENG-%d", i+1)
		t := linear.Ticket{
			ID: "id-" + id, Identifier: id, Title: "ticket " + id, Status: statuses[i%3], Team: "ENG", Mine: true,
			Labels: []linear.Label{{Name: "ai"}, {Name: "dispatch", Group: "repo"}}, LastComment: "2026-10-14T10:00:00Z",
			CreatedAt: fmt.Sprintf("2026-10-%02dT00:00:00Z", i%28+1),
		}
		w.Tickets = append(w.Tickets, t)
		wk := world.Worker{State: states[i%4]}
		if wk.State != world.NoWorker && i < 40 {
			wk.TreePath = "/trees/" + id
			w.Trees = append(w.Trees, treehouse.Tree{Path: wk.TreePath, LeaseHolder: id})
			w.Leased[id] = true
		} else {
			wk = world.Worker{State: world.NoWorker}
		}
		w.Workers[id] = wk
		if t.Status == st.InReview && i < 40 {
			w.PRs[id] = pullrequest.PullRequest{Number: i, URL: "https://github.test/" + id, Head: "sha", Ready: i%2 == 0}
		}
	}
	return w
}

func shipped(t *testing.T, timeouts rules.Timeouts) *rules.Rules {
	t.Helper()
	loaded, err := rules.Load("../../rules", "dispatch.ts", timeouts)
	require.NoError(t, err)
	return loaded
}

func shippedConfig() config.Config {
	cfg := config.Config{
		Dispatch: config.Dispatch{MaxInProgress: 4},
		Plugins:  config.Plugins{Linear: config.Linear{Teams: []string{"ENG"}, AILabel: "ai", RepoLabelGroup: "repo"}},
		Repos:    config.Repos{"dispatch": {Path: "/work/dispatch", OpenPRAs: config.OpenPRAsOpen}},
		Rules:    config.ShippedRules(),
	}
	cfg.Plugins.Linear.Statuses = config.Statuses{
		Todo: "Todo", CloseParent: "Done", BlockerDone: "Done", InProgress: "In Progress", InReview: "In Review",
		NeedsHuman: "Needs Human", Finished: []string{"Done", "Canceled", "Duplicate"}, Paused: []string{"Backlog", "Todo"},
	}
	return cfg
}

// TestCycleCostWithGoBackedRecords measures what Open Question 1 asks: whether
// a cycle over records that throw on unknown values and track every read is
// fast enough to run once a second.
func TestCycleCostWithGoBackedRecords(t *testing.T) {
	cfg := shippedConfig()
	loaded := shipped(t, rules.Timeouts{})
	now := time.Now()
	w := busyWorld(cfg, now)

	const runs = 5
	var total time.Duration
	var result rules.Result
	for range runs {
		state := rules.FromWorld(w, cfg, nil, "you")
		start := time.Now()
		result = loaded.Cycle(state, nil)
		total += time.Since(start)
	}
	per := total / runs
	t.Logf("a cycle over %d tickets, %d workers and %d PRs takes %s with %d rules", len(w.Tickets), len(w.Workers), len(w.PRs), per, len(loaded.Defs()))
	assert.Empty(t, result.Throws)
	// About 300ms without the race detector and 7 times that with it, so the bound only
	// catches a cycle that has become pathologically slow.
	assert.Less(t, per, 15*time.Second)
}
