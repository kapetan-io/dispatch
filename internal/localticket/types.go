// Package localticket is dispatch's own ticket system: tickets kept in one database file that
// only the holder of dispatch.lock opens.
package localticket

import (
	"errors"
	"time"
)

// The four kinds of failure a store call reports, matched with errors.Is.
var (
	// ErrRefused means the request breaks an invariant. Nothing changed.
	ErrRefused = errors.New("refused")
	// ErrNotFound means the ticket does not exist.
	ErrNotFound = errors.New("not found")
	// ErrUnreadable means the file holds a value that does not decode, or bbolt panicked.
	ErrUnreadable = errors.New("unreadable")
	// ErrHeld means another process holds the file.
	ErrHeld = errors.New("held")
)

// Error is a failure of one kind with a message that names the problem.
type Error struct {
	Kind error
	Msg  string
}

func (e *Error) Error() string        { return e.Msg }
func (e *Error) Is(target error) bool { return target == e.Kind }

// timeForm is the form of every stored time: UTC, to the millisecond, as Linear writes them, so
// two tickets order by creation when their strings compare.
const timeForm = "2006-01-02T15:04:05.000Z"

// Draft is a new ticket.
type Draft struct {
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Repo        string            `json:"repo"`
	Status      string            `json:"status,omitempty"` // a status name; unset means the status doing todo
	Labels      []string          `json:"labels,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Parent      string            `json:"parent,omitempty"`    // LOC-<n>
	BlockedBy   []string          `json:"blockedBy,omitempty"` // LOC-<n>
	StartPR     int               `json:"startPR,omitempty"`
	FiledBy     string            `json:"filedBy,omitempty"` // set by the loop, never trusted from the CLI
}

// Edit changes what it names. It has no status, comment or filed-by field, so an edit cannot change them.
type Edit struct {
	Title           *string           `json:"title,omitempty"`
	Description     *string           `json:"description,omitempty"`
	Repo            *string           `json:"repo,omitempty"`
	AddLabels       []string          `json:"addLabels,omitempty"`
	RemoveLabels    []string          `json:"removeLabels,omitempty"`
	SetAttributes   map[string]string `json:"setAttributes,omitempty"`
	UnsetAttributes []string          `json:"unsetAttributes,omitempty"`
	Parent          *string           `json:"parent,omitempty"`    // "" clears
	BlockedBy       *[]string         `json:"blockedBy,omitempty"` // replaces
	StartPR         *int              `json:"startPR,omitempty"`   // 0 clears
}

// Filter selects tickets. Every field set must match.
type Filter struct {
	Status     string            `json:"status,omitempty"`
	Labels     []string          `json:"labels,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	// Open leaves out the tickets in a status that does the finished job.
	Open bool `json:"open,omitempty"`
}

// Summary is one line of a list.
type Summary struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Repo   string `json:"repo"`
	Title  string `json:"title"`
}

// Comment is a note on a ticket. Once written it never changes.
type Comment struct {
	Author string    `json:"author"` // dispatch, you, or a worker's ticket id
	At     time.Time `json:"at"`
	Body   string    `json:"body"`
}

// Detail is everything show prints.
type Detail struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Status      string            `json:"status"`
	Repo        string            `json:"repo"`
	Branch      string            `json:"branch"`
	FiledBy     string            `json:"filedBy"`
	Labels      []string          `json:"labels,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Parent      string            `json:"parent,omitempty"`
	BlockedBy   []string          `json:"blockedBy,omitempty"`
	StartPR     int               `json:"startPR,omitempty"`
	Comments    []Comment         `json:"comments,omitempty"`
}

// stored is the value in the tickets bucket.
type stored struct {
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Status      string            `json:"status"`
	Repo        string            `json:"repo"`
	Branch      string            `json:"branch"`
	Labels      []string          `json:"labels,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Parent      uint64            `json:"parent,omitempty"`
	BlockedBy   []uint64          `json:"blockedBy,omitempty"`
	StartPR     int               `json:"startPR,omitempty"`
	FiledBy     string            `json:"filedBy"`
	CreatedAt   string            `json:"createdAt"`
	LastComment string            `json:"lastComment"`
	StatusAt    string            `json:"statusAt"`
}

// storedComment is the value in a ticket's comments bucket.
type storedComment struct {
	Author string `json:"author"`
	At     string `json:"at"`
	Body   string `json:"body"`
}
