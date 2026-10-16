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
	// Kind names the action in plans. Receipt is the shorter name its receipts
	// carry, which rules filter receipts by.
	Kind    string
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
	if q.spec.Kind == kindLinearMove {
		return q.spec.Capped && q.args["status"] == "in_progress"
	}
	return q.spec.Capped
}

const (
	kindLinearMove     = "linear.move"
	kindLinearComment  = "linear.comment"
	kindLinearEscalate = "linear.escalate"
	kindHerdrLaunch    = "herdr.launch"
	kindHerdrNudge     = "herdr.nudge"
	kindHerdrClose     = "herdr.close"
	kindTreehouseRet   = "treehouse.return"
	kindGitHubMerge    = "github.merge"
	kindNotifySend     = "notify.send"
	kindHTTPPost       = "http.post"
)

// Actions lists every action a rule can queue. Adding one also takes a subject
// kind in queueFn (call.go), unless its subject is a name and not a record, as a post's is, and, for a new plugin, a case in ctxPlugins (declarations.go)
// and in ctx (call.go); the generated declarations then follow from this table.
var Actions = []ActionSpec{
	{Plugin: "linear", Method: "queueMove", Kind: kindLinearMove, Receipt: "move", Subject: Param{"t", "Ticket"},
		Params: []Param{{"status", "string"}, {"why", "string"}}, Options: "uncapped?: boolean", Capped: true},
	{Plugin: "linear", Method: "queueComment", Kind: kindLinearComment, Receipt: "comment", Subject: Param{"t", "Ticket"},
		Params: []Param{{"text", "string"}}},
	{Plugin: "linear", Method: "queueEscalate", Kind: kindLinearEscalate, Receipt: "escalate", Subject: Param{"t", "Ticket"},
		Params: []Param{{"why", "string"}}},
	{Plugin: "herdr", Method: "queueLaunch", Kind: kindHerdrLaunch, Receipt: "launch", Subject: Param{"t", "Ticket"},
		Options: "model?: string; kickoff?: string; uncapped?: boolean", Capped: true},
	{Plugin: "herdr", Method: "queueNudge", Kind: kindHerdrNudge, Receipt: "nudge", Subject: Param{"t", "Ticket"},
		Params: []Param{{"text", "string"}}},
	{Plugin: "herdr", Method: "queueClose", Kind: kindHerdrClose, Receipt: "close", Subject: Param{"w", "Worker"}},
	{Plugin: "treehouse", Method: "queueReturn", Kind: kindTreehouseRet, Receipt: "return", Subject: Param{"tree", "Tree"}},
	{Plugin: "github", Method: "queueMerge", Kind: kindGitHubMerge, Receipt: "merge", Subject: Param{"pr", "PR"}},
	{Plugin: "notify", Method: "queueSend", Kind: kindNotifySend, Receipt: "notify", Subject: Param{"subject", "Ticket"},
		Params: []Param{{"text", "string"}}, Options: "followersAlways?: boolean"},
	// The subject of a post is the service it goes to, a name from [http.<name>].
	{Plugin: "http", Method: "queuePost", Kind: kindHTTPPost, Receipt: "post", Subject: Param{"service", "string"},
		Params: []Param{{"path", "string"}, {"body", "unknown"}}},
}
