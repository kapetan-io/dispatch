// Package ticket is what dispatch knows of a ticket whichever ticket system it came from: the
// system an id belongs to, and the statuses a system has and the jobs they do.
package ticket

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Ticket systems, as a ticket's id names them.
const (
	SystemLinear = "linear"
	SystemLocal  = "local"
)

// LocalPrefix starts the id of every local ticket.
const LocalPrefix = "LOC-"

// SystemOf is the ticket system an id belongs to. The prefix is decisive, because a Linear
// read that returns a LOC- id fails.
func SystemOf(id string) string {
	if strings.HasPrefix(id, LocalPrefix) {
		return SystemLocal
	}
	return SystemLinear
}

// LocalID is the id of local ticket number n.
func LocalID(n uint64) string { return LocalPrefix + strconv.FormatUint(n, 10) }

// LocalNumber is the number in a local ticket id such as LOC-7. It is false for any other text,
// a Linear id included, and for a number written with a sign or leading zero.
func LocalNumber(id string) (uint64, bool) {
	digits, ok := strings.CutPrefix(id, LocalPrefix)
	if !ok || digits == "" || digits[0] == '0' {
		return 0, false
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != digits {
		return 0, false
	}
	return n, true
}

// Job is what dispatch does with a status.
type Job string

const (
	Todo        Job = "todo"
	InProgress  Job = "in_progress"
	InReview    Job = "in_review"
	NeedsHuman  Job = "needs_human"
	CloseParent Job = "close_parent"
	BlockerDone Job = "blocker_done"
	Finished    Job = "finished"
	Paused      Job = "paused"
)

// Jobs is every job, in the order docs list them.
var Jobs = []Job{Todo, InProgress, InReview, NeedsHuman, CloseParent, BlockerDone, Finished, Paused}

// Exclusive are the jobs that belong to exactly one status. Finished and Paused may belong to several.
var Exclusive = []Job{Todo, InProgress, InReview, NeedsHuman, CloseParent, BlockerDone}

// Known is true when j is one of Jobs.
func (j Job) Known() bool {
	return slices.Contains(Jobs, j)
}

// Status is a status name and the jobs it does.
type Status struct {
	Name string `json:"name"`
	Jobs []Job  `json:"jobs"`
}

// Does is true when the status does job.
func (s Status) Does(job Job) bool {
	return slices.Contains(s.Jobs, job)
}

// Statuses is one ticket system's statuses, in the order it lists them.
type Statuses []Status

// Find returns the status named name, matching ignoring case.
func (s Statuses) Find(name string) (Status, bool) {
	for _, st := range s {
		if strings.EqualFold(st.Name, name) {
			return st, true
		}
	}
	return Status{}, false
}

// Has is true when the status named name does job.
func (s Statuses) Has(name string, job Job) bool {
	st, ok := s.Find(name)
	return ok && st.Does(job)
}

// With returns the statuses that do job, in order.
func (s Statuses) With(job Job) []Status {
	var out []Status
	for _, st := range s {
		if st.Does(job) {
			out = append(out, st)
		}
	}
	return out
}

// Names lists the status names in order.
func (s Statuses) Names() []string {
	out := make([]string, len(s))
	for i, st := range s {
		out[i] = st.Name
	}
	return out
}

// Check returns an error naming the job and its statuses when an exclusive job belongs to two
// statuses or to none, and one naming the problem when a status has no name, a repeated name or
// an unknown job.
func (s Statuses) Check() error {
	seen := map[string]bool{}
	for _, st := range s {
		key := strings.ToLower(st.Name)
		switch {
		case st.Name == "":
			return fmt.Errorf("a status has no name")
		case seen[key]:
			return fmt.Errorf("two statuses are named %q", st.Name)
		}
		seen[key] = true
		for _, j := range st.Jobs {
			if !j.Known() {
				return fmt.Errorf("status %q has the job %q, which dispatch does not know", st.Name, j)
			}
		}
	}
	for _, job := range Exclusive {
		have := s.With(job)
		switch len(have) {
		case 1:
		case 0:
			return fmt.Errorf("no status does the job %s; exactly one must", job)
		default:
			names := make([]string, len(have))
			for i, st := range have {
				names[i] = st.Name
			}
			return fmt.Errorf("the job %s belongs to %d statuses (%s); exactly one must do it", job, len(have), strings.Join(names, ", "))
		}
	}
	return nil
}

// DefaultStatuses are the statuses a new local ticket database is seeded with.
func DefaultStatuses() Statuses {
	out := make(Statuses, len(defaults))
	for i, st := range defaults {
		out[i] = Status{Name: st.Name, Jobs: append([]Job(nil), st.Jobs...)}
	}
	return out
}

var defaults = Statuses{
	{Name: "Backlog", Jobs: []Job{Paused}},
	{Name: "Todo", Jobs: []Job{Todo, Paused}},
	{Name: "In Progress", Jobs: []Job{InProgress}},
	{Name: "In Review", Jobs: []Job{InReview}},
	{Name: "Needs Human", Jobs: []Job{NeedsHuman}},
	{Name: "Done", Jobs: []Job{Finished, CloseParent, BlockerDone}},
	{Name: "Canceled", Jobs: []Job{Finished}},
}

func init() {
	if err := defaults.Check(); err != nil {
		panic("the default local statuses are invalid: " + err.Error())
	}
}
