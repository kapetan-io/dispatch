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
	// Doc is the description of the method in dispatch.d.ts.
	Doc string
}

// takesSlot reports whether the queued action counts against max_in_progress.
func (q *queued) takesSlot() bool {
	if q.spec.Receipt == "move" {
		return q.spec.Capped && q.args["status"] == "in_progress"
	}
	return q.spec.Capped
}

// Actions lists every action a rule can queue. Adding one also takes a subject
// kind in queueFn (call.go), unless its subject is a name and not a record, as a post's and a create's are, and, for a new plugin, a case in ctxPlugins (declarations.go)
// and in ctx (call.go); the generated declarations then follow from this table.
var Actions = []ActionSpec{
	{Plugin: "ticket", Method: "queueMove", Doc: "Queues moving t to the status that does the job key status, with why as the reason. Takes a worker slot under max_in_progress when it moves a ticket into in_progress, unless uncapped is true.", Receipt: "move", Subject: Param{"t", "Ticket"},
		Params: []Param{{"status", "string"}, {"why", "string"}}, Options: "uncapped?: boolean", Capped: true},
	{Plugin: "ticket", Method: "queueComment", Doc: "Queues a comment on t.", Receipt: "comment", Subject: Param{"t", "Ticket"},
		Params: []Param{{"text", "string"}}},
	{Plugin: "ticket", Method: "queueEscalate", Doc: "Queues moving t to Needs Human and commenting why.", Receipt: "escalate", Subject: Param{"t", "Ticket"},
		Params: []Param{{"why", "string"}}},
	// The subject of a create is the ticket system it files in, a name and not a record.
	{Plugin: "ticket", Method: "queueCreate", Doc: "Queues filing a local ticket from draft.", Receipt: "create", Subject: Param{"system", `"local"`},
		Params: []Param{{"draft", "TicketDraft"}}},
	{Plugin: "ticket", Method: "queueEdit", Doc: "Queues changes to t.", Receipt: "edit", Subject: Param{"t", "Ticket"},
		Params: []Param{{"changes", "TicketChanges"}}},
	{Plugin: "worker", Method: "queueLaunch", Doc: "Queues starting a worker for t, taking a worker slot under max_in_progress unless uncapped is true. harness names a [harness.<name>] table, model a Claude Code model, kickoff the first command typed into the worker, and stackOn starts t's branch at that PR's head.", Receipt: "launch", Subject: Param{"t", "Ticket"},
		Options: "harness?: string; model?: string; kickoff?: string; uncapped?: boolean; stackOn?: PR", Capped: true},
	{Plugin: "worker", Method: "queueNudge", Doc: "Queues typing text into t's worker.", Receipt: "nudge", Subject: Param{"t", "Ticket"},
		Params: []Param{{"text", "string"}}},
	{Plugin: "worker", Method: "queueClose", Doc: "Queues closing the worker's workspace.", Receipt: "close", Subject: Param{"w", "Worker"}},
	{Plugin: "tree", Method: "queueReturn", Doc: "Queues returning the tree to its repo's pool. Dispatch refuses it while the tree holds work it would destroy.", Receipt: "return", Subject: Param{"tree", "Tree"}},
	{Plugin: "pr", Method: "queueMerge", Doc: "Queues merging the PR, only if its head hasn't moved.", Receipt: "merge", Subject: Param{"pr", "PR"}},
	{Plugin: "notify", Method: "queueSend", Doc: "Queues a notification about subject. reason names the kind of notice and defaults to the first element of the once key.", Receipt: "notify", Subject: Param{"subject", "Ticket"},
		Params: []Param{{"text", "string"}}, Options: "reason?: string"},
	// The subject of a post is the service it goes to, a name from [http.<name>].
	{Plugin: "http", Method: "queuePost", Doc: "Queues a POST of body as JSON to path on the service named in [http.<name>].", Receipt: "post", Subject: Param{"service", "string"},
		Params: []Param{{"path", "string"}, {"body", "unknown"}}},
}
