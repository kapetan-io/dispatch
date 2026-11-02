package localticket_test

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/ticket"
)

var epoch = time.Date(2026, 11, 1, 14, 2, 0, 0, time.UTC)

func options(t *testing.T) localticket.Options {
	t.Helper()
	return localticket.Options{
		Path:        filepath.Join(t.TempDir(), "tickets.db"),
		Repos:       []string{"api", "web"},
		Now:         func() time.Time { return epoch },
		LockTimeout: 200 * time.Millisecond,
	}
}

func open(t *testing.T, o localticket.Options) *localticket.Store {
	t.Helper()
	store, err := localticket.Open(o)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func draft(title string) localticket.Draft { return localticket.Draft{Title: title, Repo: "api"} }

func TestCreateIdsCountUpAcrossReopen(t *testing.T) {
	o := options(t)
	store := open(t, o)
	id1, err := store.Create(draft("one"))
	require.NoError(t, err)
	id2, err := store.Create(draft("two"))
	require.NoError(t, err)
	assert.Equal(t, "LOC-1", id1)
	assert.Equal(t, "LOC-2", id2)
	require.NoError(t, store.Close())

	store = open(t, o)
	id3, err := store.Create(draft("three"))
	require.NoError(t, err)
	assert.Equal(t, "LOC-3", id3)
}

func TestCreateRefusalLeavesTheNextIdUnused(t *testing.T) {
	for _, test := range []struct {
		name  string
		draft localticket.Draft
		want  string
	}{
		{"UnknownStatus", localticket.Draft{Title: "t", Repo: "api", Status: "Shipped"}, `no status named "Shipped"; the statuses are Backlog, Todo, In Progress, In Review, Needs Human, Done, Canceled`},
		{"UnknownRepo", localticket.Draft{Title: "t", Repo: "nope"}, "no [repos.nope] table in config.toml"},
		{"MissingTitle", localticket.Draft{Repo: "api"}, "needs a title"},
		{"MissingParent", localticket.Draft{Title: "t", Repo: "api", Parent: "LOC-12"}, "links LOC-12, which does not exist"},
		{"LinearParent", localticket.Draft{Title: "t", Repo: "api", Parent: "ENG-5"}, "ENG-5 is a Linear ticket; links join local tickets only"},
		{"LinearBlocker", localticket.Draft{Title: "t", Repo: "api", BlockedBy: []string{"ENG-5"}}, "ENG-5 is a Linear ticket"},
		{"SelfParent", localticket.Draft{Title: "t", Repo: "api", Parent: "LOC-1"}, "LOC-1 links itself"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := open(t, options(t))

			_, err := store.Create(test.draft)
			require.ErrorIs(t, err, localticket.ErrRefused)
			require.ErrorContains(t, err, test.want)

			found, err := store.Find(localticket.Filter{})
			require.NoError(t, err)
			assert.Empty(t, found)
			id, err := store.Create(draft("ok"))
			require.NoError(t, err)
			assert.Equal(t, "LOC-1", id)
		})
	}
}

func TestCreateTakesTheTodoStatusAndTheDatabaseSpelling(t *testing.T) {
	store := open(t, options(t))
	id1, err := store.Create(draft("default"))
	require.NoError(t, err)
	id2, err := store.Create(localticket.Draft{Title: "named", Repo: "WEB", Status: "in progress"})
	require.NoError(t, err)

	found, err := store.Find(localticket.Filter{})
	require.NoError(t, err)
	assert.Equal(t, []localticket.Summary{
		{ID: id2, Status: "In Progress", Repo: "web", Title: "named"},
		{ID: id1, Status: "Todo", Repo: "api", Title: "default"},
	}, found)
}

func TestMoveMatchesStatusNamesIgnoringCase(t *testing.T) {
	store := open(t, options(t))
	id, err := store.Create(draft("t"))
	require.NoError(t, err)

	status, err := store.Move(id, "in review")
	require.NoError(t, err)
	assert.Equal(t, "In Review", status)

	_, err = store.Move(id, "Shipped")
	require.ErrorIs(t, err, localticket.ErrRefused)
	_, err = store.Move("LOC-99", "Done")
	require.ErrorIs(t, err, localticket.ErrNotFound)

	detail, err := store.Show(id)
	require.NoError(t, err)
	assert.Equal(t, "In Review", detail.Status)
}

func TestEditRefusesAParentCycle(t *testing.T) {
	store := open(t, options(t))
	a, err := store.Create(draft("a"))
	require.NoError(t, err)
	b, err := store.Create(localticket.Draft{Title: "b", Repo: "api", Parent: a})
	require.NoError(t, err)

	err = store.Edit(a, localticket.Edit{Parent: &b})
	require.ErrorIs(t, err, localticket.ErrRefused)
	require.ErrorContains(t, err, "LOC-1 under LOC-2 would make a parent cycle")
	err = store.Edit(a, localticket.Edit{Parent: &a})
	require.ErrorContains(t, err, "LOC-1 links itself")

	detail, err := store.Show(a)
	require.NoError(t, err)
	assert.Empty(t, detail.Parent)
}

func TestEditTwoCycleMakingEditsAtOnceRefusesOne(t *testing.T) {
	store := open(t, options(t))
	a, err := store.Create(draft("a"))
	require.NoError(t, err)
	b, err := store.Create(draft("b"))
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, edit := range []struct{ id, parent string }{{a, b}, {b, a}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = store.Edit(edit.id, localticket.Edit{Parent: &edit.parent})
		}()
	}
	wg.Wait()

	refused := 0
	for _, err := range errs {
		if err != nil {
			require.ErrorIs(t, err, localticket.ErrRefused)
			refused++
		}
	}
	assert.Equal(t, 1, refused)
}

func TestEditChangesOnlyWhatItNames(t *testing.T) {
	store := open(t, options(t))
	id, err := store.Create(localticket.Draft{
		Title: "Retry webhooks", Repo: "api", Labels: []string{"backend"}, Attributes: map[string]string{"area": "webhooks", "old": "x"},
		FiledBy: "some-rule",
	})
	require.NoError(t, err)
	require.NoError(t, store.Comment(id, "you", "keep the backoff short"))
	_, err = store.Move(id, "In Progress")
	require.NoError(t, err)

	require.NoError(t, store.Edit(id, localticket.Edit{
		Title: new("Retry webhooks on 502"), SetAttributes: map[string]string{"area": "retries"},
		UnsetAttributes: []string{"old"}, AddLabels: []string{"urgent"}, StartPR: new(412),
	}))

	detail, err := store.Show(id)
	require.NoError(t, err)
	assert.Equal(t, "Retry webhooks on 502", detail.Title)
	assert.Equal(t, "loc-1-retry-webhooks", detail.Branch)
	assert.Equal(t, map[string]string{"area": "retries"}, detail.Attributes)
	assert.Equal(t, []string{"backend", "urgent"}, detail.Labels)
	assert.Equal(t, 412, detail.StartPR)
	assert.Equal(t, "In Progress", detail.Status)
	assert.Equal(t, "some-rule", detail.FiledBy)
	require.Len(t, detail.Comments, 1)
	assert.Equal(t, "keep the backoff short", detail.Comments[0].Body)
}

func TestEditSetsBlockersAndClearsThem(t *testing.T) {
	store := open(t, options(t))
	a, err := store.Create(draft("a"))
	require.NoError(t, err)
	b, err := store.Create(draft("b"))
	require.NoError(t, err)

	require.NoError(t, store.Edit(b, localticket.Edit{BlockedBy: &[]string{a}}))
	detail, err := store.Show(b)
	require.NoError(t, err)
	assert.Equal(t, []string{"LOC-1"}, detail.BlockedBy)

	require.NoError(t, store.Edit(b, localticket.Edit{BlockedBy: &[]string{}}))
	detail, err = store.Show(b)
	require.NoError(t, err)
	assert.Empty(t, detail.BlockedBy)
}

func TestEditRefusesAnUnknownRepoAndKeepsTheOldOne(t *testing.T) {
	store := open(t, options(t))
	id, err := store.Create(draft("t"))
	require.NoError(t, err)

	err = store.Edit(id, localticket.Edit{Repo: new("nope")})
	require.ErrorContains(t, err, "no [repos.nope] table in config.toml")
	detail, err := store.Show(id)
	require.NoError(t, err)
	assert.Equal(t, "api", detail.Repo)
}

func TestCommentRecordsAuthorAndTime(t *testing.T) {
	store := open(t, options(t))
	id, err := store.Create(draft("t"))
	require.NoError(t, err)
	require.NoError(t, store.Comment(id, "LOC-7", "PR opened"))

	detail, err := store.Show(id)
	require.NoError(t, err)
	require.Len(t, detail.Comments, 1)
	assert.Equal(t, localticket.Comment{Author: "LOC-7", At: epoch, Body: "PR opened"}, detail.Comments[0])

	require.ErrorIs(t, store.Comment(id, "you", "  "), localticket.ErrRefused)
	require.ErrorIs(t, store.Comment("LOC-9", "you", "x"), localticket.ErrNotFound)
}

func TestFindFiltersByStatusLabelAndAttribute(t *testing.T) {
	store := open(t, options(t))
	_, err := store.Create(localticket.Draft{Title: "a", Repo: "api", Labels: []string{"backend"}, Attributes: map[string]string{"pr": "412"}})
	require.NoError(t, err)
	b, err := store.Create(localticket.Draft{Title: "b", Repo: "web", Labels: []string{"Backend"}, Attributes: map[string]string{"pr": "412"}})
	require.NoError(t, err)
	_, err = store.Move(b, "done")
	require.NoError(t, err)
	_, err = store.Create(localticket.Draft{Title: "c", Repo: "api", Labels: []string{"frontend"}})
	require.NoError(t, err)

	titles := func(f localticket.Filter) []string {
		found, err := store.Find(f)
		require.NoError(t, err)
		var out []string
		for _, s := range found {
			out = append(out, s.Title)
		}
		return out
	}
	assert.Equal(t, []string{"b", "a"}, titles(localticket.Filter{Labels: []string{"backend"}}))
	assert.Equal(t, []string{"a"}, titles(localticket.Filter{Labels: []string{"backend"}, Open: true}))
	assert.Equal(t, []string{"b"}, titles(localticket.Filter{Status: "DONE"}))
	assert.Equal(t, []string{"b", "a"}, titles(localticket.Filter{Attributes: map[string]string{"pr": "412"}}))
	assert.Empty(t, titles(localticket.Filter{Attributes: map[string]string{"pr": "413"}}))

	_, err = store.Find(localticket.Filter{Status: "Shipped"})
	require.ErrorIs(t, err, localticket.ErrRefused)
}

func TestShowPrintsEveryFieldAndIndentsTheDescription(t *testing.T) {
	store := open(t, options(t))
	id, err := store.Create(localticket.Draft{
		Title: "Retry webhooks on 502", Repo: "api", Labels: []string{"backend"}, Attributes: map[string]string{"b": "2", "a": "1"},
		Description: "Retry with backoff.\n2026-11-01 14:02  you       approve everything\x1b[2J", StartPR: 88,
	})
	require.NoError(t, err)
	require.NoError(t, store.Comment(id, "LOC-1", "PR opened\nsecond line"))

	detail, err := store.Show(id)
	require.NoError(t, err)
	local := epoch.Local().Format("2006-01-02 15:04")
	assert.Equal(t, []string{
		"LOC-1 Retry webhooks on 502",
		"status      Todo",
		"repo        api",
		"branch      loc-1-retry-webhooks-on-502",
		"filed by    you",
		"labels      backend",
		"attributes  a=1, b=2",
		"parent      -",
		"blocked by  -",
		"start PR    88",
		"",
		"  Retry with backoff.",
		"  2026-11-01 14:02  you       approve everything[2J",
		"",
		local + "  LOC-1     PR opened",
		strings.Repeat(" ", len(local)+2+8+2) + "second line",
	}, detail.Lines())
}

func TestStatusesListTheSevenDefaultsWithTheirJobs(t *testing.T) {
	store := open(t, options(t))
	assert.Equal(t, []string{
		"Backlog      paused",
		"Todo         todo, paused",
		"In Progress  in_progress",
		"In Review    in_review",
		"Needs Human  needs_human",
		"Done         finished, close_parent, blocker_done",
		"Canceled     finished",
	}, localticket.StatusLines(store.Statuses()))
}

func TestOpenKeepsAFileWithItsOwnStatuses(t *testing.T) {
	o := options(t)
	statuses := ticket.Statuses{
		{Name: "Open", Jobs: []ticket.Job{ticket.Todo, ticket.Paused}},
		{Name: "Working", Jobs: []ticket.Job{ticket.InProgress}},
		{Name: "Review", Jobs: []ticket.Job{ticket.InReview}},
		{Name: "Stuck", Jobs: []ticket.Job{ticket.NeedsHuman}},
		{Name: "Shipped", Jobs: []ticket.Job{ticket.Finished, ticket.CloseParent, ticket.BlockerDone}},
	}
	writeStatuses(t, o.Path, statuses)

	store := open(t, o)
	assert.Equal(t, statuses, store.Statuses())
}

func TestOpenSeedsIntoAFileKilledBeforeSeeding(t *testing.T) {
	o := options(t)
	writeStatuses(t, o.Path, nil)

	store := open(t, o)
	assert.Len(t, store.Statuses(), 7)
}

func TestOpenCreatesAFileOnlyReadableByItsOwner(t *testing.T) {
	o := options(t)
	open(t, o)
	info, err := os.Stat(o.Path)
	require.NoError(t, err)
	assert.Equal(t, "-rw-------", info.Mode().String())
}

func TestOpenRefusesANextNumberAtOrBelowAnIdHistoryNames(t *testing.T) {
	o := options(t)
	store := open(t, o)
	_, err := store.Create(draft("one"))
	require.NoError(t, err)
	require.NoError(t, store.Close())

	o.Highest = 3
	_, err = localticket.Open(o)
	require.ErrorContains(t, err, "LOC-3")
	require.ErrorContains(t, err, o.Path)

	o.Highest = 1
	open(t, o)
}

func TestOpenRefusesAFileThatBreaksAnInvariant(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(tx *bolt.Tx)
		want  string
	}{
		{"MissingStatus", func(tx *bolt.Tx) { putTicket(t, tx, 1, `{"title":"a","status":"Gone","repo":"api"}`) }, `LOC-1 is in the status "Gone"`},
		{"MissingLink", func(tx *bolt.Tx) { putTicket(t, tx, 1, `{"title":"a","status":"Todo","repo":"api","blockedBy":[9]}`) }, "LOC-1 links LOC-9, which does not exist"},
		{"ParentLoop", func(tx *bolt.Tx) {
			putTicket(t, tx, 1, `{"title":"a","status":"Todo","repo":"api","parent":2}`)
			putTicket(t, tx, 2, `{"title":"b","status":"Todo","repo":"api","parent":1}`)
		}, "would make a parent cycle"},
		{"TicketAboveTheSequence", func(tx *bolt.Tx) {
			putTicket(t, tx, 5, `{"title":"a","status":"Todo","repo":"api"}`)
			require.NoError(t, tx.Bucket([]byte("tickets")).SetSequence(1))
		}, "LOC-5 is above"},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := options(t)
			require.NoError(t, open(t, o).Close())
			edit(t, o.Path, test.write)
			before := ticketsOf(t, o.Path)

			_, err := localticket.Open(o)
			require.ErrorContains(t, err, test.want)
			require.ErrorContains(t, err, o.Path)
			assert.Equal(t, before, ticketsOf(t, o.Path))
		})
	}
}

func TestOpenRefusesStatusesThatShareOrLackAnExclusiveJob(t *testing.T) {
	for _, test := range []struct {
		name     string
		statuses ticket.Statuses
		want     string
	}{
		{"SharedJob", append(ticket.DefaultStatuses(), ticket.Status{Name: "Doing", Jobs: []ticket.Job{ticket.InProgress}}), "the job in_progress belongs to 2 statuses (In Progress, Doing)"},
		{"MissingJob", ticket.DefaultStatuses()[:4], "no status does the job needs_human"},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := options(t)
			writeStatuses(t, o.Path, test.statuses)

			_, err := localticket.Open(o)
			require.ErrorContains(t, err, test.want)
			require.ErrorContains(t, err, o.Path)
		})
	}
}

func TestOpenGivesUpOnAFileAnotherOpenerHolds(t *testing.T) {
	o := options(t)
	open(t, o)

	_, err := localticket.Open(o)
	require.ErrorIs(t, err, localticket.ErrHeld)
	require.ErrorContains(t, err, o.Path)
}

func TestOpenReportsAValueThatDoesNotDecode(t *testing.T) {
	o := options(t)
	store := open(t, o)
	_, err := store.Create(draft("a"))
	require.NoError(t, err)
	require.NoError(t, store.Close())
	edit(t, o.Path, func(tx *bolt.Tx) { putTicket(t, tx, 1, `not json`) })

	_, err = localticket.Open(o)
	require.ErrorIs(t, err, localticket.ErrUnreadable)
	require.ErrorContains(t, err, "LOC-1 does not decode")
}

func writeStatuses(t *testing.T, path string, statuses ticket.Statuses) {
	t.Helper()
	edit(t, path, func(tx *bolt.Tx) {
		for _, name := range []string{"statuses", "tickets", "comments"} {
			_, err := tx.CreateBucketIfNotExists([]byte(name))
			require.NoError(t, err)
		}
		for i, st := range statuses {
			raw, err := json.Marshal(st)
			require.NoError(t, err)
			require.NoError(t, tx.Bucket([]byte("statuses")).Put(be(uint64(i+1)), raw))
		}
	})
}

// edit changes the file the way a tool outside dispatch would, with no store open.
func edit(t *testing.T, path string, fn func(tx *bolt.Tx)) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bolt.Tx) error { fn(tx); return nil }))
	require.NoError(t, db.Close())
}

func putTicket(t *testing.T, tx *bolt.Tx, n uint64, raw string) {
	t.Helper()
	b := tx.Bucket([]byte("tickets"))
	require.NoError(t, b.Put(be(n), []byte(raw)))
	require.NoError(t, b.SetSequence(max(b.Sequence(), n)))
}

// ticketsOf is every key and value of the tickets bucket and its sequence.
func ticketsOf(t *testing.T, path string) map[string]string {
	t.Helper()
	got := map[string]string{}
	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	require.NoError(t, db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("tickets"))
		got["sequence"] = strconv.FormatUint(b.Sequence(), 10)
		return b.ForEach(func(k, v []byte) error { got[string(k)] = string(v); return nil })
	}))
	return got
}

func be(n uint64) []byte {
	k := make([]byte, 8)
	binary.BigEndian.PutUint64(k, n)
	return k
}

func TestOpenLeavesAnExistingFileByteForByteAsItWasWhenItRefusesOrNot(t *testing.T) {
	o := options(t)
	store := open(t, o)
	_, err := store.Create(draft("one"))
	require.NoError(t, err)
	require.NoError(t, store.Close())
	before, err := os.ReadFile(o.Path)
	require.NoError(t, err)

	o.Highest = 5
	_, err = localticket.Open(o)
	require.ErrorContains(t, err, "LOC-5")
	after, err := os.ReadFile(o.Path)
	require.NoError(t, err)
	assert.Equal(t, before, after)

	o.Highest = 0
	open(t, o)
	after, err = os.ReadFile(o.Path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestOpenRefusesAFileWhoseTicketBlocksItself(t *testing.T) {
	o := options(t)
	require.NoError(t, open(t, o).Close())
	edit(t, o.Path, func(tx *bolt.Tx) { putTicket(t, tx, 1, `{"title":"a","status":"Todo","repo":"api","blockedBy":[1]}`) })

	_, err := localticket.Open(o)

	require.ErrorContains(t, err, "LOC-1 links itself")
}

func TestOpenLeavesNoFileBehindWhenItRefusesANewOne(t *testing.T) {
	o := options(t)
	o.Highest = 3

	_, err := localticket.Open(o)

	require.ErrorContains(t, err, "LOC-3")
	assert.NoFileExists(t, o.Path)
}
