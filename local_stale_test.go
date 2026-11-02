package dispatch_test

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const commentWithStatusSince = `whenever({ name: "since", each: "ticket", if: (t) => t.system === "local" && t.status === "needs_human",
  then: (t, ctx) => ctx.ticket.queueComment(t, "since=" + t.statusSince, { once: ["since", t.id] }) });`

const commentOnLocalByAttribute = `whenever({ name: "ask", each: "ticket", if: (t) => t.system === "local" && t.attributes.area === "webhooks",
  then: (t, ctx) => ctx.ticket.queueComment(t, "seen", { once: ["ask", t.id] }) });`

// The two actions run side by side, so either may land first. Whichever does, the other sees a status
// it did not read and fails without writing.
func TestTickKeepsALocalTicketsStatusSinceFromTheDatabase(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(commentWithStatusSince)})
	d.StartBeforeTheEvents()
	d.fileLocal("Todo")
	moved := d.Clock.Now()
	d.Stop()

	// The file says LOC-1 entered Needs Human at the time of the move, an hour before dispatch first reads that status.
	d.Advance(time.Hour)
	d.setLocalStatus("LOC-1", "Needs Human", moved)
	d.Restart()
	d.Tick()

	assert.Contains(t, d.ticketOut("show", "LOC-1"), fmt.Sprintf("since=%d", moved.UnixMilli()))
}

// setLocalStatus edits the database file while dispatch is stopped, as a tool outside dispatch could.
func (d *Dispatch) setLocalStatus(id, status string, at time.Time) {
	d.t.Helper()
	db, err := bolt.Open(filepath.Join(d.state, "tickets.db"), 0o600, &bolt.Options{Timeout: time.Second})
	require.NoError(d.t, err)
	defer func() { _ = db.Close() }()
	n, err := strconv.ParseUint(strings.TrimPrefix(id, "LOC-"), 10, 64)
	require.NoError(d.t, err)
	key := binary.BigEndian.AppendUint64(nil, n)
	require.NoError(d.t, db.Update(func(tx *bolt.Tx) error {
		tickets := tx.Bucket([]byte("tickets"))
		var stored map[string]any
		if err := json.Unmarshal(tickets.Get(key), &stored); err != nil {
			return err
		}
		stored["status"], stored["statusAt"] = status, at.UTC().Format("2006-01-02T15:04:05.000Z")
		raw, err := json.Marshal(stored)
		if err != nil {
			return err
		}
		return tickets.Put(key, raw)
	}))
}

func TestTickRulesReadTheSystemAndAttributesOfALocalTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(commentOnLocalByAttribute)})
	d.Linear.Tickets(ticket("ENG-1", "Todo", "dispatch"))
	d.StartBeforeTheEvents()
	d.fileLocal("Todo", "--attr", "area=webhooks")
	d.fileLocal("Todo", "--attr", "area=billing")

	d.Tick()

	assert.Contains(t, d.ticketOut("show", "LOC-1"), "seen")
	assert.NotContains(t, d.ticketOut("show", "LOC-2"), "seen")
}
