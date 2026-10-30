package world_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/stackrecord"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/world"
)

// full sets every field of a World, so a field added later without history recording it fails the test.
func full() world.World {
	ticket := linear.Ticket{Identifier: "ENG-1", Blockers: []linear.Blocker{{Identifier: "ENG-2", Status: "Todo"}}, BlockersTruncated: true}
	worker := world.Worker{State: world.Working, TreePath: "/trees/1", RepoDir: "/repo", WorkspaceID: "w1"}
	return world.World{
		At:           time.UnixMilli(1760450411000).UTC(),
		Tickets:      []linear.Ticket{ticket},
		LinearErr:    errors.New("linear is down"),
		Snapshot:     herdr.Snapshot{Workspaces: []herdr.Workspace{{ID: "w1", CheckoutPath: "/trees/1"}}},
		HerdrErr:     errors.New("herdr is down"),
		Trees:        []treehouse.Tree{{Path: "/trees/1", Status: "leased", LeaseHolder: "ENG-1", RepoDir: "/repo"}},
		PoolErrs:     map[string]error{"tiger": errors.New("pool tiger unread")},
		Workers:      map[string]world.Worker{"ENG-1": worker},
		Stops:        map[string]stops.Record{"ENG-1": {At: time.UnixMilli(1760450000000).UTC(), Reported: true, Running: 2}},
		Sessions:     map[string]sessionlog.Log{"ENG-1": {Turns: []sessionlog.Turn{{At: 1760450000000, Running: true}}}},
		SessionErrs:  map[string]error{"ENG-6": errors.New("session unread")},
		LeaseHolders: []linear.Ticket{{Identifier: "ENG-3"}},
		HoldersErr:   errors.New("holders unread"),
		Leases:       map[string][]world.Worker{"ENG-3": {worker}},
		Leased:       map[string]bool{"ENG-3": true},
		Children:     map[string][]linear.Ticket{"ENG-9": {{Identifier: "ENG-10"}}},
		ChildrenErr:  errors.New("children unread"),
		Unfinished:   map[string]string{"/trees/1": "2 commits"},
		Work:         map[string]treehouse.Work{"/trees/1": {Unpushed: 2, TrackedChanges: true}},
		CheckErrors:  map[string]error{"/trees/2": errors.New("status failed")},
		Untracked:    map[string][]string{"/trees/1": {"a.txt"}},
		PRs:          map[string]pullrequest.PullRequest{"ENG-4": {Number: 7, Head: "abc", Problems: []string{"a failed check"}}},
		PRReadFailed: map[string]bool{"ENG-5": true},
		UnreadRepos:  map[string]bool{"tiger": true},
		Nudged:       map[string]bool{"ENG-1": true},
		StackRecords: map[string]stackrecord.Records{"tiger": {Whole: map[string]stackrecord.Record{"eng-1": {Parent: "ENG-2", ForkPoint: "abc"}}, Partial: map[string]bool{"eng-7": true}}},
		StackErrs:    map[string]error{"lion": errors.New("stack records unread")},
	}
}

// Package exception: codec round trip over every field of World, which a CLI scenario would cover only by accident.
func TestWorldSurvivesBeingWrittenToHistory(t *testing.T) {
	before := full()
	for i, f := range reflect.VisibleFields(reflect.TypeFor[world.World]()) {
		require.False(t, reflect.ValueOf(before).Field(i).IsZero(), f.Name)
	}

	b, err := json.Marshal(before)
	require.NoError(t, err)
	var after world.World
	require.NoError(t, json.Unmarshal(b, &after))

	assert.Equal(t, before, after)
	assert.Equal(t, "/repo", after.Trees[0].RepoDir)
}
