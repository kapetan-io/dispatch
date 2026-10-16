package world

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// recorded is World as history writes it. A read's errors become their text, which is
// all a rule can see of one, and a tree keeps the clone it came from, which treehouse's
// own JSON leaves out.
type recorded struct {
	At           time.Time                          `json:"at"`
	Tickets      []linear.Ticket                    `json:"tickets,omitempty"`
	LinearErr    string                             `json:"linearErr,omitempty"`
	Snapshot     herdr.Snapshot                     `json:"snapshot"`
	HerdrErr     string                             `json:"herdrErr,omitempty"`
	Trees        []recordedTree                     `json:"trees,omitempty"`
	PoolErrs     map[string]string                  `json:"poolErrs,omitempty"`
	Workers      map[string]Worker                  `json:"workers,omitempty"`
	Stops        map[string]stops.Record            `json:"stops,omitempty"`
	LeaseHolders []linear.Ticket                    `json:"leaseHolders,omitempty"`
	HoldersErr   string                             `json:"holdersErr,omitempty"`
	Leases       map[string][]Worker                `json:"leases,omitempty"`
	Leased       map[string]bool                    `json:"leased,omitempty"`
	Children     map[string][]linear.Ticket         `json:"children,omitempty"`
	ChildrenErr  string                             `json:"childrenErr,omitempty"`
	Unfinished   map[string]string                  `json:"unfinished,omitempty"`
	Work         map[string]treehouse.Work          `json:"work,omitempty"`
	CheckErrors  map[string]string                  `json:"checkErrors,omitempty"`
	Untracked    map[string][]string                `json:"untracked,omitempty"`
	PRs          map[string]pullrequest.PullRequest `json:"prs,omitempty"`
	PRReadFailed map[string]bool                    `json:"prReadFailed,omitempty"`
	UnreadRepos  map[string]bool                    `json:"unreadRepos,omitempty"`
	Nudged       map[string]bool                    `json:"nudged,omitempty"`
}

type recordedTree struct {
	treehouse.Tree
	RepoDir string `json:"repoDir,omitempty"`
}

// MarshalJSON writes the read in the form history keeps.
func (w World) MarshalJSON() ([]byte, error) {
	r := recorded{
		At: w.At, Tickets: w.Tickets, LinearErr: errText(w.LinearErr), Snapshot: w.Snapshot, HerdrErr: errText(w.HerdrErr),
		PoolErrs: errTexts(w.PoolErrs), Workers: w.Workers, Stops: w.Stops, LeaseHolders: w.LeaseHolders,
		HoldersErr: errText(w.HoldersErr), Leases: w.Leases, Leased: w.Leased, Children: w.Children,
		ChildrenErr: errText(w.ChildrenErr), Unfinished: w.Unfinished, Work: w.Work, CheckErrors: errTexts(w.CheckErrors),
		Untracked: w.Untracked, PRs: w.PRs, PRReadFailed: w.PRReadFailed, UnreadRepos: w.UnreadRepos, Nudged: w.Nudged,
	}
	for _, t := range w.Trees {
		r.Trees = append(r.Trees, recordedTree{Tree: t, RepoDir: t.RepoDir})
	}
	return json.Marshal(r)
}

// UnmarshalJSON reads a read back. An error returns as one with the recorded text.
func (w *World) UnmarshalJSON(b []byte) error {
	var r recorded
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*w = World{
		At: r.At, Tickets: r.Tickets, LinearErr: asError(r.LinearErr), Snapshot: r.Snapshot, HerdrErr: asError(r.HerdrErr),
		PoolErrs: asErrors(r.PoolErrs), Workers: r.Workers, Stops: r.Stops, LeaseHolders: r.LeaseHolders,
		HoldersErr: asError(r.HoldersErr), Leases: r.Leases, Leased: r.Leased, Children: r.Children,
		ChildrenErr: asError(r.ChildrenErr), Unfinished: r.Unfinished, Work: r.Work, CheckErrors: asErrors(r.CheckErrors),
		Untracked: r.Untracked, PRs: r.PRs, PRReadFailed: r.PRReadFailed, UnreadRepos: r.UnreadRepos, Nudged: r.Nudged,
	}
	for _, t := range r.Trees {
		t.Tree.RepoDir = t.RepoDir
		w.Trees = append(w.Trees, t.Tree)
	}
	return nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func errTexts(m map[string]error) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, err := range m {
		out[k] = errText(err)
	}
	return out
}

func asError(text string) error {
	if text == "" {
		return nil
	}
	return errors.New(text)
}

func asErrors(m map[string]string) map[string]error {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]error, len(m))
	for k, text := range m {
		out[k] = asError(text)
	}
	return out
}
