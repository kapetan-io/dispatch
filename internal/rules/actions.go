package rules

// Param is one positional argument of an action method.
type Param struct {
	Name string
	TS   string
}

// ActionSpec declares one action a plugin lets a rule queue. The runtime
// registers ctx.<plugin>.<method> from it and dispatch.d.ts is generated from it.
type ActionSpec struct {
	Plugin string
	Method string
	// Receipt names the action in receipts and in rule scenarios.
	Receipt string
	// Subject is the record the action is about. It is the first argument.
	Subject Param
	// Params follow the subject.
	Params []Param
	// Options is the TypeScript of the options beyond once.
	Options string
	// Capped marks an action that takes a worker slot under max_in_progress
	// unless its options say uncapped: true. A move takes one only when it
	// moves a ticket into In Progress, because that is what starts a worker.
	Capped bool
}

// takesSlot reports whether the queued action counts against max_in_progress.
func (q *queued) takesSlot() bool {
	if q.spec.Receipt == "move" {
		return q.spec.Capped && q.args["status"] == "in_progress"
	}
	return q.spec.Capped
}

// Actions lists every action a rule can queue. Adding one also takes a subject
// kind in queueFn (call.go), unless its subject is a name and not a record, as a post's is, and, for a new plugin, a case in ctxPlugins (declarations.go)
// and in ctx (call.go); the generated declarations then follow from this table.
var Actions = []ActionSpec{
	{Plugin: "ticket", Method: "queueMove", Receipt: "move", Subject: Param{"t", "Ticket"},
		Params: []Param{{"status", "string"}, {"why", "string"}}, Options: "uncapped?: boolean", Capped: true},
	{Plugin: "ticket", Method: "queueComment", Receipt: "comment", Subject: Param{"t", "Ticket"},
		Params: []Param{{"text", "string"}}},
	{Plugin: "ticket", Method: "queueEscalate", Receipt: "escalate", Subject: Param{"t", "Ticket"},
		Params: []Param{{"why", "string"}}},
	{Plugin: "worker", Method: "queueLaunch", Receipt: "launch", Subject: Param{"t", "Ticket"},
		Options: "model?: string; kickoff?: string; uncapped?: boolean; stackOn?: PR", Capped: true},
	{Plugin: "worker", Method: "queueNudge", Receipt: "nudge", Subject: Param{"t", "Ticket"},
		Params: []Param{{"text", "string"}}},
	{Plugin: "worker", Method: "queueClose", Receipt: "close", Subject: Param{"w", "Worker"}},
	{Plugin: "tree", Method: "queueReturn", Receipt: "return", Subject: Param{"tree", "Tree"}},
	{Plugin: "pr", Method: "queueMerge", Receipt: "merge", Subject: Param{"pr", "PR"}},
	{Plugin: "notify", Method: "queueSend", Receipt: "notify", Subject: Param{"subject", "Ticket"},
		Params: []Param{{"text", "string"}}, Options: "followersAlways?: boolean; reason?: string"},
	// The subject of a post is the service it goes to, a name from [http.<name>].
	{Plugin: "http", Method: "queuePost", Receipt: "post", Subject: Param{"service", "string"},
		Params: []Param{{"path", "string"}, {"body", "unknown"}}},
}
