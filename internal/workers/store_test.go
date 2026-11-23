package workers_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/dispatchdb"
	"github.com/kapetan-io/dispatch/internal/workers"
)

func store(t *testing.T) *workers.Store {
	t.Helper()
	file, err := dispatchdb.Open(dispatchdb.Options{StateDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	return workers.New(file)
}

func at(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// Package exception: unreachable from the product, where a Stop comes from a session of a worker
// that exists.
func TestAStopForATreeNoWorkerRowNamesIsDropped(t *testing.T) {
	rows := store(t)

	stored, err := rows.RecordStop("/trees/1", "s1", workers.Record{At: at(2000), Reported: true}, workers.NewClosing(2000, "done", ""), at(3000))

	require.NoError(t, err)
	assert.False(t, stored)
	found, err := rows.Snapshot()
	require.NoError(t, err)
	assert.Empty(t, found)
}

// Package exception: unreachable from the product, because the fakes' sessions report a Stop at
// the loop's clock, which is never behind the row it just made.
func TestAStopThatEndedBeforeTheWorkerRowWasMadeIsDropped(t *testing.T) {
	rows := store(t)
	require.NoError(t, rows.Ensure("ENG-1", "app", at(5000)))
	require.NoError(t, rows.RecordTree("ENG-1", "app", "/trees/1"))

	earlier, err := rows.RecordStop("/trees/1", "s1", workers.Record{At: at(4999), Reported: true}, workers.NewClosing(4999, "old", ""), at(6000))
	require.NoError(t, err)
	later, err := rows.RecordStop("/trees/1", "s2", workers.Record{At: at(5000), Reported: true}, workers.NewClosing(5000, "new", ""), at(6000))
	require.NoError(t, err)

	assert.False(t, earlier)
	assert.True(t, later)
	worker, found, err := rows.OnTree("/trees/1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Len(t, worker.Stops, 1)
	_, kept := worker.Stop("s2")
	assert.True(t, kept)
}

// Package exception: unreachable from the product, where the loop runs one launch for a ticket at
// a time and treehouse leases a tree to one holder.
func TestATreeIsRecordedForOneWorker(t *testing.T) {
	rows := store(t)
	require.NoError(t, rows.Ensure("ENG-1", "app", at(1)))
	require.NoError(t, rows.Ensure("ENG-2", "app", at(1)))
	require.NoError(t, rows.RecordTree("ENG-1", "app", "/trees/1"))

	err := rows.RecordTree("ENG-2", "app", "/trees/1")

	require.ErrorIs(t, err, workers.ErrTreeTaken)
	require.ErrorContains(t, err, "/trees/1 is recorded as the tree of ENG-1 in app")
	found, err := rows.Snapshot()
	require.NoError(t, err)
	require.Len(t, found.Of("ENG-2"), 1)
	assert.Empty(t, found.Of("ENG-2")[0].Row.Tree)
}

// Package exception: unreachable from the product, where deleting a worker row happens only at a
// return or a resync and neither says what the row held.
func TestDeletingAWorkerRowDeletesItsRecords(t *testing.T) {
	rows := store(t)
	require.NoError(t, rows.Ensure("ENG-1", "app", at(1)))
	require.NoError(t, rows.RecordTree("ENG-1", "app", "/trees/1"))
	require.NoError(t, rows.SetKickoff("ENG-1", "app", &workers.Kickoff{Text: "/dispatch:build ENG-1", At: 2, Open: true}))
	_, err := rows.RecordStop("/trees/1", "s1", workers.Record{At: at(3), Reported: true}, workers.NewClosing(3, "done", ""), at(4))
	require.NoError(t, err)

	require.NoError(t, rows.Delete("ENG-1", "app"))
	require.NoError(t, rows.Ensure("ENG-1", "app", at(5)))
	require.NoError(t, rows.RecordTree("ENG-1", "app", "/trees/1"))

	worker, found, err := rows.OnTree("/trees/1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Empty(t, worker.Stops)
	assert.Empty(t, worker.Closing)
	assert.Nil(t, worker.Row.Kickoff)
}

// Package exception: a rule about the source tree, which no command line shows. Only the packages
// that own a part of dispatch.db open bbolt, so every write goes through an owner's checks.
func TestOnlyTheDatabasesOwnersImportBbolt(t *testing.T) {
	owners := []string{"internal/dispatchdb", "internal/localticket", "internal/prreview", "internal/ticketbranch", "internal/workers"}
	root := filepath.Join("..", "..")

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, spec := range parsed.Imports {
			name, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			if name == "go.etcd.io/bbolt" {
				rel, err := filepath.Rel(root, filepath.Dir(path))
				require.NoError(t, err)
				assert.Contains(t, owners, filepath.ToSlash(rel), path)
			}
		}
		return nil
	})

	require.NoError(t, err)
}

// Package exception: unreachable from the product, because the fakes' sessions report a Stop at
// the loop's clock, which is never behind the launch that follows it.
func TestARowAFailedLaunchLeftIsDatedAgainSoAStopOfTheTreesLastWorkerIsDropped(t *testing.T) {
	rows := store(t)
	require.NoError(t, rows.Ensure("ENG-1", "app", at(1000)))
	require.NoError(t, rows.Ensure("ENG-1", "app", at(5000)))
	require.NoError(t, rows.RecordTree("ENG-1", "app", "/trees/1"))

	stored, err := rows.RecordStop("/trees/1", "s1", workers.Record{At: at(3000), Reported: true}, workers.NewClosing(3000, "old", ""), at(6000))

	require.NoError(t, err)
	assert.False(t, stored)
}

// Package exception: unreachable from the product, because the fakes' sessions report a Stop at
// the loop's clock, which is never behind the launch that follows it.
func TestARelaunchOfAWorkerWithATreeKeepsTheStopsItsSessionReportsLate(t *testing.T) {
	rows := store(t)
	require.NoError(t, rows.Ensure("ENG-1", "app", at(1000)))
	require.NoError(t, rows.RecordTree("ENG-1", "app", "/trees/1"))
	require.NoError(t, rows.Ensure("ENG-1", "app", at(5000)))

	stored, err := rows.RecordStop("/trees/1", "s1", workers.Record{At: at(3000), Reported: true}, workers.NewClosing(3000, "done", ""), at(6000))

	require.NoError(t, err)
	assert.True(t, stored)
}

// Package exception: unreachable from the product, where a ticket holds one lease in a pool and
// a tree is in one pool.
func TestAMovedRowKeepsItsTreeWhenItsTicketHoldsASecondLeaseOrTwoPoolsShowTheTree(t *testing.T) {
	rows := store(t)
	require.NoError(t, rows.Ensure("ENG-1", "old", at(1000)))
	require.NoError(t, rows.RecordTree("ENG-1", "old", "/trees/1"))
	require.NoError(t, rows.Ensure("ENG-1", "app", at(2000)))
	leases := []workers.Lease{{Tree: "/trees/1", Holder: "ENG-1"}, {Tree: "/trees/2", Holder: "ENG-1"}}
	idle := func(string, string) bool { return false }

	_, err := rows.Resync([]workers.Pool{workers.ReadPool("app", leases), workers.ReadPool("lib", leases)},
		idle, func(string) bool { return false }, []string{"app", "lib"})

	require.NoError(t, err)
	worker, found, err := rows.OnTree("/trees/1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "app", worker.Repo)
}
