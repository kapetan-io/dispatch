package dispatch_test

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/linear"
)

const defaultStatusLines = `Backlog      paused
Todo         todo, paused
In Progress  in_progress
In Review    in_review
Needs Human  needs_human
Done         finished, close_parent, blocker_done
Canceled     finished
`

// dbStatus is a status as the database file stores it.
type dbStatus struct {
	Name string   `json:"name"`
	Jobs []string `json:"jobs"`
}

func defaultStatuses() []dbStatus {
	return []dbStatus{
		{Name: "Backlog", Jobs: []string{"paused"}},
		{Name: "Todo", Jobs: []string{"todo", "paused"}},
		{Name: "In Progress", Jobs: []string{"in_progress"}},
		{Name: "In Review", Jobs: []string{"in_review"}},
		{Name: "Needs Human", Jobs: []string{"needs_human"}},
		{Name: "Done", Jobs: []string{"finished", "close_parent", "blocker_done"}},
		{Name: "Canceled", Jobs: []string{"finished"}},
	}
}

// databasePath is where dispatch keeps the local ticket database when [plugins.local] sets no path.
func (d *Dispatch) databasePath() string { return filepath.Join(d.state, "tickets.db") }

// writeDatabase writes the file the way a tool outside dispatch would, before dispatch run starts.
func (d *Dispatch) writeDatabase(statuses []dbStatus, tickets map[uint64]string) {
	d.t.Helper()
	db, err := bolt.Open(d.databasePath(), 0o600, nil)
	require.NoError(d.t, err)
	require.NoError(d.t, db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"statuses", "tickets", "comments"} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		for i, st := range statuses {
			raw, err := json.Marshal(st)
			if err != nil {
				return err
			}
			if err := tx.Bucket([]byte("statuses")).Put(bigEndian(uint64(i+1)), raw); err != nil {
				return err
			}
		}
		var highest uint64
		for n, raw := range tickets {
			if err := tx.Bucket([]byte("tickets")).Put(bigEndian(n), []byte(raw)); err != nil {
				return err
			}
			highest = max(highest, n)
		}
		return tx.Bucket([]byte("tickets")).SetSequence(highest)
	}))
	require.NoError(d.t, db.Close())
}

// databaseContents is every key and value of every bucket, which a refused start must leave as it was.
func (d *Dispatch) databaseContents() map[string]string {
	d.t.Helper()
	got := map[string]string{}
	db, err := bolt.Open(d.databasePath(), 0o600, &bolt.Options{ReadOnly: true})
	require.NoError(d.t, err)
	require.NoError(d.t, db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			got[string(name)+"/sequence"] = string(binary.BigEndian.AppendUint64(nil, b.Sequence()))
			return b.ForEach(func(k, v []byte) error { got[string(name)+"/"+string(k)] = string(v); return nil })
		})
	}))
	require.NoError(d.t, db.Close())
	return got
}

func bigEndian(n uint64) []byte { return binary.BigEndian.AppendUint64(nil, n) }

func TestTicketStatusesListsAFilesOwnStatusesAfterAStart(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn})
	d.writeDatabase([]dbStatus{
		{Name: "Open", Jobs: []string{"todo", "paused"}},
		{Name: "Working", Jobs: []string{"in_progress"}},
		{Name: "Review", Jobs: []string{"in_review"}},
		{Name: "Stuck", Jobs: []string{"needs_human"}},
		{Name: "Shipped", Jobs: []string{"finished", "close_parent", "blocker_done"}},
	}, nil)
	d.StartBeforeTheEvents()

	assert.Equal(t, `Open     todo, paused
Working  in_progress
Review   in_review
Stuck    needs_human
Shipped  finished, close_parent, blocker_done
`, d.ticketOut("statuses"))
	d.Restart()
	assert.Contains(t, d.ticketOut("statuses"), "Working  in_progress\n")
}

func TestTicketStatusesListsAllSevenDefaultsAfterAFirstStartKilledWhileSeeding(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn})
	// A seeding that did not commit leaves the buckets and no statuses.
	d.writeDatabase(nil, nil)
	d.StartBeforeTheEvents()

	assert.Equal(t, defaultStatusLines, d.ticketOut("statuses"))
}

func TestTicketSaysLocalTicketsAreOffForEveryVerb(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.StartBeforeTheEvents()

	for _, args := range [][]string{
		{"statuses"},
		{"list"},
		{"create", "--repo", "dispatch", "--title", "x"},
		{"show", "LOC-1"},
		{"move", "LOC-1", "Done"},
		{"comment", "LOC-1", "x"},
		{"edit", "LOC-1", "--title", "y"},
	} {
		res := d.Run(append([]string{"ticket"}, args...)...)

		assert.Equal(t, 1, res.Code)
		require.EqualError(t, res.Err, "local tickets are off: config.toml has no [plugins.local] table")
	}
	assert.NoFileExists(t, d.databasePath())
}

func TestRunWithoutThePluginTableRecordsNoLocalTicketsInHistory(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.StartBeforeTheEvents()
	d.Tick()
	d.Restart()

	for _, body := range d.StateFiles() {
		assert.NotContains(t, body, "LOC-")
		assert.NotContains(t, body, "localHighest")
	}
}

func TestTicketRefusalsLeaveEveryTicketAsItWas(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.ticketOut("create", "--repo", "dispatch", "--title", "a", "--label", "backend", "--attr", "area=webhooks")
	d.ticketOut("create", "--repo", "dispatch", "--title", "b", "--parent", "LOC-1")
	d.ticketOut("comment", "LOC-2", "keep me")
	showAll := func() string { return d.ticketOut("show", "LOC-1") + d.ticketOut("show", "LOC-2") }
	before := showAll()

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"EditWithAValidTitleAndACycle", []string{"edit", "LOC-1", "--title", "renamed", "--add-label", "urgent", "--parent", "LOC-2"}, "would make a parent cycle"},
		{"EditWithAValidTitleAndAMissingBlocker", []string{"edit", "LOC-2", "--title", "renamed", "--blocked-by", "LOC-12"}, "links LOC-12, which does not exist"},
		{"EditWithAValidTitleAndALinearBlocker", []string{"edit", "LOC-2", "--title", "renamed", "--blocked-by", "ENG-5"}, "ENG-5 is a Linear ticket"},
		{"EditWithAValidTitleAndAnUnknownRepo", []string{"edit", "LOC-2", "--title", "renamed", "--repo", "web"}, "no [repos.web] table in config.toml"},
		{"EditToItself", []string{"edit", "LOC-2", "--title", "renamed", "--blocked-by", "LOC-2"}, "LOC-2 links itself"},
		{"MoveToUnknownStatus", []string{"move", "LOC-2", "Shipped"}, `no status named "Shipped"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			res := d.Run(append([]string{"ticket"}, test.args...)...)

			require.ErrorContains(t, res.Err, test.want)
			assert.Equal(t, before, showAll())
		})
	}
}

func TestTicketEditLeavesTheWorkersCommentAsWritten(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn})
	d.Workers.Running("ENG-1", "")
	tree := d.worktreeOf("ENG-1")
	d.StartBeforeTheEvents()
	d.ticketOut("create", "--repo", "dispatch", "--title", "t")
	require.NoError(t, d.RunIn(tree, "ticket", "comment", "LOC-1", "from the worker").Err)
	comments := func() []string {
		lines := strings.Split(d.ticketOut("show", "LOC-1"), "\n")
		i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "start PR") })
		return lines[i+1:]
	}
	before := comments()

	d.Advance(2 * time.Hour)
	d.ticketOut("edit", "LOC-1", "--title", "renamed", "--add-label", "urgent")

	assert.Equal(t, before, comments())
}

func TestTicketLinearReadClashNamesTheIdOncePerFailedRead(t *testing.T) {
	t.Parallel()
	own := ticket("LOC-12", "In Progress", "dispatch")
	blocked := ticket("ENG-1", "In Progress", "dispatch")
	blocked.Blockers = []linear.Blocker{{Identifier: "LOC-12", Status: "Todo"}}

	for _, test := range []struct {
		name    string
		tickets []fakeTicket
	}{
		{"TicketIdItself", []fakeTicket{own}},
		{"BlockerId", []fakeTicket{blocked}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: localOn})
			d.Linear.Tickets(test.tickets...)
			d.StartBeforeTheEvents()
			first := strings.Count(d.loopErr(), "Linear returned LOC-12, which is a local ticket id; rename the Linear team")

			d.Tick()

			assert.Equal(t, 1, first)
			assert.Equal(t, 2, strings.Count(d.loopErr(), "Linear returned LOC-12, which is a local ticket id; rename the Linear team"))
		})
	}
}

func TestRulesHaveNoDeleteAction(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{ExactRules: true})
	dir := filepath.Join(t.TempDir(), "rules")
	require.NoError(t, d.Run("rules", "init", dir).Err)

	declarations, err := os.ReadFile(filepath.Join(dir, "types", "dispatch.d.ts"))
	require.NoError(t, err)

	assert.NotRegexp(t, regexp.MustCompile(`(?i)\bqueue\w*delete|\bdelete\w*\(`), string(declarations))
}

func TestRunStopsAtStartWhenALeaseNamesAnIdTheDatabaseWouldReuse(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn})
	d.Workers.Running("LOC-5", "")
	d.worktreeOf("LOC-5")
	d.StartBeforeTheEvents()
	d.ticketOut("create", "--repo", "dispatch", "--title", "one")
	d.Tick()
	d.Stop()

	err := d.startLoop(dispatch.Options{})

	require.ErrorContains(t, err, "LOC-5")
	require.ErrorContains(t, err, d.databasePath())
}

func TestRunStopsAtStartOnAFileThatBreaksAnInvariantAndLeavesItAsItWas(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		tickets map[uint64]string
		want    []string
	}{
		{"StatusTheFileLacks", map[uint64]string{1: `{"title":"a","status":"Gone","repo":"dispatch"}`}, []string{"LOC-1", `"Gone"`}},
		{"LinkToAMissingTicket", map[uint64]string{1: `{"title":"a","status":"Todo","repo":"dispatch","blockedBy":[9]}`}, []string{"LOC-1", "LOC-9", "does not exist"}},
		{"ParentLoop", map[uint64]string{
			1: `{"title":"a","status":"Todo","repo":"dispatch","parent":2}`,
			2: `{"title":"b","status":"Todo","repo":"dispatch","parent":1}`,
		}, []string{"parent cycle"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: localOn})
			d.writeDatabase(defaultStatuses(), test.tickets)
			before := d.databaseContents()

			err := d.startLoop(dispatch.Options{})

			require.Error(t, err)
			for _, want := range test.want {
				require.ErrorContains(t, err, want)
			}
			assert.Equal(t, before, d.databaseContents())
		})
	}
}

func TestRunStopsAtStartOnStatusesThatShareOrLackAJob(t *testing.T) {
	t.Parallel()
	for _, job := range []string{"todo", "in_progress", "in_review", "needs_human", "close_parent", "blocker_done"} {
		t.Run("Shared"+job, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: localOn})
			d.writeDatabase(append(defaultStatuses(), dbStatus{Name: "Extra", Jobs: []string{job}}), nil)
			before := d.databaseContents()

			err := d.startLoop(dispatch.Options{})

			require.ErrorContains(t, err, job)
			require.ErrorContains(t, err, "Extra")
			assert.Equal(t, before, d.databaseContents())
		})
		t.Run("Lacked"+job, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: localOn})
			statuses := defaultStatuses()
			for i := range statuses {
				statuses[i].Jobs = slices.DeleteFunc(slices.Clone(statuses[i].Jobs), func(j string) bool { return j == job })
			}
			d.writeDatabase(statuses, nil)
			before := d.databaseContents()

			err := d.startLoop(dispatch.Options{})

			require.ErrorContains(t, err, job)
			assert.Equal(t, before, d.databaseContents())
		})
	}
}
