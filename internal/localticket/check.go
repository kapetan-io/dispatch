package localticket

import (
	"github.com/kapetan-io/dispatch/internal/ticket"
	bolt "go.etcd.io/bbolt"
)

// check refuses a file whose tickets break an invariant, which only a file written outside
// dispatch can do, and a file whose next number a ticket id already used elsewhere would reuse.
func check(tx *bolt.Tx, statuses ticket.Statuses, highest uint64) error {
	tb := tx.Bucket(ticketBucket)
	if seq := tb.Sequence(); seq < highest {
		return refuse("the next ticket number is %d, but history or a tree's lease names %s; this is not the file that issued it", seq+1, ticket.LocalID(highest))
	}
	c := tb.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		id := ticket.LocalID(number(k))
		t, err := decode(k, v)
		if err != nil {
			return err
		}
		if number(k) > tb.Sequence() {
			return refuse("%s is above the file's last issued number %d, so the next create would overwrite it", id, tb.Sequence())
		}
		if _, ok := statuses.Find(t.Status); !ok {
			return refuse("%s is in the status %q, which the file lacks", id, t.Status)
		}
		for _, ref := range append([]uint64{t.Parent}, t.BlockedBy...) {
			if ref != 0 && ref == number(k) {
				return refuse("%s links itself", id)
			}
			if ref != 0 && tb.Get(key(ref)) == nil {
				return refuse("%s links %s, which does not exist", id, ticket.LocalID(ref))
			}
		}
		if t.Parent != 0 {
			if err := noLoop(tb, id, number(k), t.Parent); err != nil {
				return err
			}
		}
	}
	return nil
}
