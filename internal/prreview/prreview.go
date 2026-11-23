// Package prreview owns the PR review rows in dispatch.db. A PR review is a session's report, made
// with `dispatch review start`, that it is reviewing one head of an In Review ticket's PR. A ticket
// has at most one row: a later start replaces it and `dispatch review done` ends it, whoever
// started it.
//
// The store reads no PR. Whether a row still applies, to the PR's current head and within the hour,
// is the workspace label's call.
package prreview

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch/internal/dispatchdb"
)

// Expiry is how long a review shows without a done.
const Expiry = time.Hour

var reviewsBucket = []byte("reviews")

// Review is one ticket's row.
type Review struct {
	// Head is the hex prefix of the head commit the session reviews, lowercase.
	Head string `json:"head"`
	// Started is when the review started, in milliseconds since the epoch.
	Started int64 `json:"started"`
}

// Covers is true when the review is of head, the PR's current head, and started under Expiry before now.
func (r Review) Covers(head string, now time.Time) bool {
	return r.Head != "" && strings.HasPrefix(strings.ToLower(head), r.Head) && r.Fresh(now)
}

// Fresh is true when the review started under Expiry before now.
func (r Review) Fresh(now time.Time) bool {
	return now.Sub(time.UnixMilli(r.Started)) < Expiry
}

// Store reads and writes the PR review rows. Every write is one transaction.
type Store struct {
	file *dispatchdb.File
}

// New returns the store over the open database.
func New(file *dispatchdb.File) *Store { return &Store{file: file} }

func key(ticket string) []byte { return []byte(strings.ToUpper(ticket)) }

// Start records that a session began reviewing head of the ticket's PR at at, replacing any row
// the ticket had.
func (s *Store) Start(ticket, head string, at time.Time) error {
	raw, err := json.Marshal(Review{Head: strings.ToLower(head), Started: at.UnixMilli()})
	if err != nil {
		return err
	}
	return s.file.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(reviewsBucket)
		if err != nil {
			return err
		}
		return bucket.Put(key(ticket), raw)
	})
}

// Done deletes the ticket's row, false when it had none.
func (s *Store) Done(ticket string) (bool, error) {
	found := false
	err := s.file.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(reviewsBucket)
		if bucket == nil || bucket.Get(key(ticket)) == nil {
			return nil
		}
		found = true
		return bucket.Delete(key(ticket))
	})
	return found, err
}

// Of is the ticket's row, false when it has none.
func (s *Store) Of(ticket string) (Review, bool, error) {
	var (
		review Review
		found  bool
	)
	err := s.file.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(reviewsBucket)
		if bucket == nil {
			return nil
		}
		raw := bucket.Get(key(ticket))
		if raw == nil {
			return nil
		}
		found = true
		if err := json.Unmarshal(raw, &review); err != nil {
			return fmt.Errorf("the PR review row of %s is not readable: %w", ticket, err)
		}
		return nil
	})
	return review, found, err
}
