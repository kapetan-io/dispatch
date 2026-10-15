package loop

// Operations a client asks of the running loop over its socket.
const (
	OpTick    = "tick"
	OpTell    = "tell"
	OpIdle    = "idle"
	OpPending = "pending"
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
