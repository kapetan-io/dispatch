package loop

import (
	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/notify"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// Operations a client asks of the running loop over its socket.
const (
	OpTick = "tick"
	// OpSettle waits until the loop is idle and reports every action since it started,
	// without forcing a read.
	OpSettle  = "settle"
	OpTell    = "tell"
	OpIdle    = "idle"
	OpPending = "pending"
	// OpStop carries a Stop hook's report, which the loop stores.
	OpStop = "stop"
	// OpWatch and OpUnwatch add or remove a session on a ticket's followers.
	OpWatch   = "watch"
	OpUnwatch = "unwatch"
	// OpWake reads herdr and the pools again and runs a cycle, without a resync, and
	// answers at once. A hook asks for it, so it must not retry what failed or wait.
	OpWake = "wake"
	// OpPrompt carries a UserPromptSubmit hook's report of a slash command, which the loop
	// stores and then wakes as OpWake does.
	OpPrompt = "prompt"
	// OpTyped tells the loop dispatch is about to type text into a worker, or clears what it
	// typed there, from a process that is not the loop.
	OpTyped = "typed"
)

// Exit codes a client reports for a response.
const (
	CodeSettled = 0
	CodeFailed  = 1
	// CodeNotRunning is the exit code of a command that needs a loop when none runs.
	CodeNotRunning = 3
)

// Request is one question to the running loop.
type Request struct {
	Op     string `json:"op"`
	Ticket string `json:"ticket,omitempty"`
	Text   string `json:"text,omitempty"`
	// Follower is the session that watches or unwatches Ticket.
	Follower *notify.Follower `json:"follower,omitempty"`
	// Stop is the turn end the session's harness reported.
	Stop *sessionlog.Stop `json:"stop,omitempty"`
	// Prompt is the message the session's harness reported it took.
	Prompt *sessionlog.Prompt `json:"prompt,omitempty"`
	// Typed is what a launch typed.
	Typed *Typed `json:"typed,omitempty"`
	// Call is the verb of a ticket request.
	Call *TicketCall `json:"call,omitempty"`
}

// Typed is a launch's note that it typed Text into the session in Tree, or with Clear set that
// it closed that session.
type Typed struct {
	Tree  string `json:"tree"`
	Text  string `json:"text,omitempty"`
	Clear bool   `json:"clear,omitempty"`
}

// Response is the loop's answer. Lines are printed to stdout, one per ticket.
type Response struct {
	Lines []string `json:"lines,omitempty"`
	// Code is 0 when things settled and 1 when an action failed.
	Code int `json:"code"`
	// Error is set when the request itself failed, such as a tell to a ticket with no worker.
	Error string `json:"error,omitempty"`
	// Idle counts the times the loop went idle, and Busy says whether it is idle now.
	Idle uint64 `json:"idle,omitempty"`
	Busy bool   `json:"busy,omitempty"`
}

// OpTicket carries a `dispatch ticket` verb to the loop, the only opener of the local ticket database.
const OpTicket = "ticket"

// Verbs of a TicketCall.
const (
	VerbCreate   = "create"
	VerbShow     = "show"
	VerbList     = "list"
	VerbMove     = "move"
	VerbComment  = "comment"
	VerbEdit     = "edit"
	VerbStatuses = "statuses"
)

// TicketCall is one `dispatch ticket` verb.
type TicketCall struct {
	Verb   string              `json:"verb"`
	ID     string              `json:"id,omitempty"`
	Draft  *localticket.Draft  `json:"draft,omitempty"`
	Edit   *localticket.Edit   `json:"edit,omitempty"`
	Status string              `json:"status,omitempty"` // move's status name
	Body   string              `json:"body,omitempty"`   // comment's body
	Filter *localticket.Filter `json:"filter,omitempty"`
	// Tree is the worker tree the CLI ran in, "" elsewhere. It decides a comment's author.
	Tree string `json:"tree,omitempty"`
}
