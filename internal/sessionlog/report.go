package sessionlog

// Prompt is a harness's report of a message its session took in a worker's tree.
type Prompt struct {
	Tree    string `json:"tree"`
	Session string `json:"session"`
	// At is when the session took the message, in milliseconds.
	At   int64  `json:"at"`
	Text string `json:"text"`
	// Task is true when the harness reads the message as a task notice, one its session's own
	// background task sent. A loop older than the field ignores it, and the notice ends an ask.
	Task bool `json:"task,omitempty"`
}

// Stop is a harness's report of one turn its session ended in a worker's tree.
type Stop struct {
	Tree    string `json:"tree"`
	Session string `json:"session"`
	// At is when the turn ended, in milliseconds.
	At int64 `json:"at"`
	// Reported is false when the program did not say whether background tasks run.
	Reported bool `json:"reported"`
	// Running counts the background tasks the program listed as running.
	Running int `json:"running"`
	// Message is the turn's closing message as the program reported it, empty when it gave none.
	Message string `json:"message,omitempty"`
	// Transcript names the program's own record of the session, which its harness reads to tell
	// whether the session took a message after this turn. Empty when the program gave none.
	Transcript string `json:"transcript,omitempty"`
}

// AskReport is a `dispatch ask` run in a worker's tree, by the session that ran it.
type AskReport struct {
	Tree    string `json:"tree"`
	Session string `json:"session"`
	// At is when the command ran, in milliseconds.
	At int64 `json:"at"`
}
