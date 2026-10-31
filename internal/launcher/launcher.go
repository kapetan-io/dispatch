// Package launcher leases a tree, opens a herdr workspace and starts a
// worker for one ticket.
package launcher

import (
	"context"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

// Launcher starts a worker for a ticket.
type Launcher interface {
	// Launch leases a tree for the ticket's branch and starts a worker on
	// it. The Result says what happened, and never fails to carry a status.
	Launch(ctx context.Context, r Request) Result
}

// Request is one ticket to start a worker for.
type Request struct {
	// RepoDir is where the launch starts looking for the target repo. Defaults
	// to the working directory.
	RepoDir string
	Ticket  string
	// Title is the ticket's title. The worker's tab is labeled "<Ticket>
	// <Title>", or just <Ticket> when it is empty.
	Title  string
	Branch string
	// Base is the ref a new branch starts from. Defaults to the repo's default
	// branch.
	Base string
	// Stack starts a new branch at Stack.Head and records the stack parent and fork point. It
	// overrides Base. An existing branch gets neither the start point nor the record.
	Stack *Stack
	// Program is what herdr runs for the worker, as a harness describes it. It
	// has no default.
	Program herdr.Program
	// Kickoff is the first command typed into the worker. Defaults to
	// "/dispatch:build <Ticket>".
	Kickoff string
}

// Stack is the PR a stacked ticket's branch starts at.
type Stack struct {
	// Ticket is the stack parent's id and Branch its PR's branch. Head is the PR's head sha as
	// the rule read it. It is the fork point and is never updated, even if the PR moves.
	Ticket, Branch, Head string
}

// Launcher statuses that the dispatcher treats specially.
const (
	StatusOK             = "ok"
	StatusAlreadyRunning = "already_running"
	StatusPoolExhausted  = "pool_exhausted"
)

// Result is the one JSON object a launch prints, status first.
type Result struct {
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Ticket    string `json:"ticket,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Pane      string `json:"pane,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Kickoff   string `json:"kickoff,omitempty"`
	// Stacked is true only when this launch wrote the stack record. A relaunch, or a launch on a
	// branch that already exists, leaves it false.
	Stacked bool `json:"stacked,omitempty"`
}

// Succeeded reports whether the launch leaves a worker running, which is when
// `dispatch launch` exits 0.
func (r Result) Succeeded() bool {
	return r.Status == StatusOK || r.Status == StatusAlreadyRunning
}

// Failed reports whether the launch counts toward the escalation limit. A
// full pool is waiting, not failing, and an existing worker is success.
func (r Result) Failed() bool {
	return !r.Succeeded() && r.Status != StatusPoolExhausted
}
