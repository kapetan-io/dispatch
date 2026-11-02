package localticket

import (
	"context"
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch/internal/ticket"
)

// Author is who a comment dispatch writes names.
const Author = "dispatch"

// System is the store as the executor writes through it. It is a separate type because the store's
// own Move takes a status name, with no expected status, and its Comment takes an author, where this
// one takes a job and writes as dispatch.
type System struct{ s *Store }

var _ ticket.System = System{}

// System returns the store as a ticket.System.
func (s *Store) System() System { return System{s} }

// Move changes the ticket to the status doing job when it still has the status seen had. The note,
// when there is one, is written in the same transaction, so both land or neither does.
func (y System) Move(_ context.Context, seen ticket.Ticket, job ticket.Job, note string) (status string, err error) {
	defer recoverTo(&err)
	target := y.s.statuses.With(job)
	if len(target) == 0 {
		return "", refuse("no status does the job %s", job)
	}
	err = y.s.db.Update(func(tx *bolt.Tx) error {
		tb := tx.Bucket(ticketBucket)
		n, t, err := get(tb, seen.Identifier)
		if err != nil {
			return err
		}
		if t.Status != seen.Status {
			return fmt.Errorf("%s is %s now, not the %s the cycle saw", seen.Identifier, t.Status, seen.Status)
		}
		t.Status, t.StatusAt = target[0].Name, y.s.stamp()
		if note != "" {
			if err := y.s.addComment(tx, n, &t, Author, note); err != nil {
				return err
			}
		}
		return put(tb, n, t)
	})
	return target[0].Name, err
}

// Comment adds body as a comment by dispatch.
func (y System) Comment(_ context.Context, ref ticket.Ticket, body string) error {
	return y.s.Comment(ref.Identifier, Author, body)
}

// Unchanged compares the ticket's status and newest comment with what seen read.
func (y System) Unchanged(_ context.Context, seen ticket.Ticket) (err error) {
	defer recoverTo(&err)
	return y.s.db.View(func(tx *bolt.Tx) error {
		_, t, err := get(tx.Bucket(ticketBucket), seen.Identifier)
		if err != nil {
			return err
		}
		if t.Status != seen.Status || t.LastComment != seen.LastComment {
			return fmt.Errorf("%s: %w", seen.Identifier, ticket.ErrChanged)
		}
		return nil
	})
}

// addComment writes a comment on ticket n inside tx and notes its time on t, which the caller puts.
func (s *Store) addComment(tx *bolt.Tx, n uint64, t *stored, author, body string) error {
	cb, err := tx.Bucket(commentBucket).CreateBucketIfNotExists(key(n))
	if err != nil {
		return err
	}
	c, err := cb.NextSequence()
	if err != nil {
		return err
	}
	at := s.stamp()
	raw, err := json.Marshal(storedComment{Author: author, At: at, Body: body})
	if err != nil {
		return err
	}
	if err := cb.Put(key(c), raw); err != nil {
		return err
	}
	t.LastComment = at
	return nil
}
