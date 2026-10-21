package loop

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
	// OpWatch and OpUnwatch add or remove a Claude Code session on a ticket's followers.
	OpWatch   = "watch"
	OpUnwatch = "unwatch"
	// OpWake reads herdr and the pools again and runs a cycle, without a resync, and
	// answers at once. A hook asks for it, so it must not retry what failed or wait.
	OpWake = "wake"
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
	// Socket is the inbox of the Claude Code session that watches or unwatches Ticket.
	Socket string `json:"socket,omitempty"`
	// Stop is what the Stop hook saw.
	Stop *Stop `json:"stop,omitempty"`
}

// Stop is a Stop hook's report of one turn a Claude session ended in a worker's tree.
type Stop struct {
	Tree    string `json:"tree"`
	Session string `json:"session"`
	// At is when the turn ended, in milliseconds.
	At int64 `json:"at"`
	// Reported is false when the hook input carried no background_tasks field.
	Reported bool `json:"reported"`
	// Running counts the background tasks the hook listed as running.
	Running int `json:"running"`
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
