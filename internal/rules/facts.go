// Package rules runs the TypeScript rules that decide what dispatch does. It
// loads the rules, runs one cycle of them over the world and the ledger, and
// runs their scenario tests. The types in this file are the one place the
// facts a rule can read are named: the runtime exposes them and dispatch.d.ts
// is generated from them, so the two cannot drift.
package rules

import (
	"reflect"
)

// Fact is one value in the world with the time it was first seen at that
// value, or unknown with the reason the read failed. A value and an unknown
// reason never coexist: Unknown is set only when the read failed.
type Fact[T any] struct {
	Value T
	// Unknown is the reason the read failed or came back cut short. Reading
	// the fact then throws.
	Unknown string
	// Since is when dispatch first saw the value, in milliseconds since the epoch.
	Since int64
}

// Known is a fact with a value.
func Known[T any](v T, since int64) Fact[T] { return Fact[T]{Value: v, Since: since} }

// Unread is a fact whose read failed for reason.
func Unread[T any](reason string) Fact[T] { return Fact[T]{Unknown: reason} }

// Ref names another record. A field holding one reads as that record.
type Ref struct {
	Kind string
	ID   string
}

// Seen is an item from a source, such as a comment, and when dispatch first saw it.
type Seen struct {
	// At is the source's own time for the item, as the source wrote it.
	At string `js:"at"`
	// SeenAt is when dispatch first saw the item, in milliseconds.
	SeenAt int64 `js:"seenAt"`
}

// Label is a Linear label and the label group it belongs to.
type Label struct {
	Name  string `js:"name"`
	Group string `js:"group,optional"`
}

// Stop is the latest time a worker's turn ended.
type Stop struct {
	At int64 `js:"at"`
}

// Blocker is a ticket that blocks another.
type Blocker struct {
	ID        string `js:"id"`
	Status    string `js:"status"`
	Done      bool   `js:"done"`
	Abandoned bool   `js:"abandoned"`
}

// Ticket is a Linear ticket dispatch tracks.
type Ticket struct {
	ID               Fact[string]  `js:"id"`
	Title            Fact[string]  `js:"title"`
	URL              Fact[string]  `js:"url"`
	Status           Fact[string]  `js:"status" ts:"string"`
	StatusSince      Fact[int64]   `js:"statusSince"`
	PreviousStatus   Fact[*string] `js:"previousStatus" ts:"string | undefined"`
	OptedIn          Fact[bool]    `js:"optedIn"`
	Repo             Fact[*string] `js:"repo"`
	Labels           Fact[[]Label] `js:"labels"`
	HasChildren      Fact[bool]    `js:"hasChildren"`
	Finished         Fact[bool]    `js:"finished"`
	Paused           Fact[bool]    `js:"paused"`
	CreatedAt        Fact[*string] `js:"createdAt"`
	LastHumanComment Fact[*Seen]   `js:"lastHumanComment"`
}

// Worker is the Claude session serving a ticket, or the lack of one.
type Worker struct {
	State        Fact[string]  `js:"state" ts:"\"none\" | \"working\" | \"idle\" | \"blocked\""`
	Ticket       Fact[Ref]     `js:"ticket" ref:"linear.ticket"`
	Tree         Fact[*string] `js:"tree"`
	Workspace    Fact[*string] `js:"workspace"`
	SeenAt       Fact[int64]   `js:"seenAt"`
	GoneSince    Fact[int64]   `js:"goneSince"`
	IdleSince    Fact[*int64]  `js:"idleSince"`
	BlockedSince Fact[*int64]  `js:"blockedSince"`
	LastStop     Fact[*Stop]   `js:"lastStop"`
	Busy         Fact[bool]    `js:"busy"`
}

// PR is the open pull request of an In Review ticket.
type PR struct {
	Ticket          Fact[Ref]    `js:"ticket" ref:"linear.ticket"`
	Number          Fact[int64]  `js:"number"`
	URL             Fact[string] `js:"url"`
	Head            Fact[string] `js:"head"`
	Closed          Fact[bool]   `js:"closed"`
	Draft           Fact[bool]   `js:"draft"`
	Approved        Fact[bool]   `js:"approved"`
	Ready           Fact[bool]   `js:"ready"`
	NeedsWork       Fact[bool]   `js:"needsWork"`
	Problem         Fact[string] `js:"problem"`
	ActivitySummary Fact[string] `js:"activitySummary"`
	LastActivity    Fact[*Seen]  `js:"lastActivity"`
	LastThread      Fact[*Seen]  `js:"lastThread"`
	OpenAs          Fact[string] `js:"openAs" ts:"\"open\" | \"draft\""`
	AutoMerge       Fact[bool]   `js:"autoMerge"`
}

// Tree is a worktree in a repo's treehouse pool.
type Tree struct {
	Path           Fact[string] `js:"path"`
	Leased         Fact[bool]   `js:"leased"`
	Holder         Fact[Ref]    `js:"holder" ref:"linear.ticket" ts:"Ticket | undefined"`
	LeasedSince    Fact[int64]  `js:"leasedSince"`
	Pushed         Fact[bool]   `js:"pushed"`
	TrackedChanges Fact[bool]   `js:"trackedChanges"`
	Unfinished     Fact[string] `js:"unfinished"`
	// Untracked lists the files git has never seen in the tree, one per line. Returning a
	// tree deletes them, but a nested repository survives that and keeps the return refused.
	Untracked Fact[string] `js:"untracked"`
	// CleanSince is when the tree last became safe to return, so a return refused for
	// late work or a nested repository is tried again once that is gone.
	CleanSince Fact[int64] `js:"cleanSince"`
}

// Escalation is a ticket's move to Needs Human and the comment that explains it.
type Escalation struct {
	Ticket    Fact[Ref]    `js:"ticket" ref:"linear.ticket"`
	Reason    Fact[string] `js:"reason"`
	Moved     Fact[string] `js:"moved" ts:"\"done\" | \"failed\" | \"refused\" | \"attempted\""`
	Commented Fact[string] `js:"commented" ts:"\"done\" | \"failed\" | \"refused\" | \"attempted\" | \"pending\""`
	Started   Fact[int64]  `js:"started"`
}

// Dispatch is dispatch itself, so a rule can run once per cycle.
type Dispatch struct {
	ID        Fact[string] `js:"id"`
	StartedAt Fact[int64]  `js:"startedAt"`
}

// Comment is a ticket comment, the payload of linear.comment.posted.
type Comment struct {
	ID         string `js:"id"`
	Body       string `js:"body"`
	Author     string `js:"author"`
	At         string `js:"at"`
	ByDispatch bool   `js:"byDispatch"`
	Ticket     Ref    `js:"ticket" ref:"linear.ticket"`
}

// CheckRun is a finished check on a pull request, the payload of github.check.completed.
type CheckRun struct {
	ID         string `js:"id"`
	Name       string `js:"name"`
	Conclusion string `js:"conclusion"`
	URL        string `js:"url"`
	Head       string `js:"head"`
	PR         Ref    `js:"pr" ref:"github.pr"`
}

// Kind describes one kind of record a whenever rule can run over.
type Kind struct {
	// Name is the string a rule gives as `each`.
	Name string
	// TS is the TypeScript name of the record.
	TS   string
	Type reflect.Type
}

// Kinds lists the records rules run over, in the order a check prints them.
var Kinds = []Kind{
	{"linear.ticket", "Ticket", reflect.TypeFor[Ticket]()},
	{"github.pr", "PR", reflect.TypeFor[PR]()},
	{"herdr.worker", "Worker", reflect.TypeFor[Worker]()},
	{"treehouse.tree", "Tree", reflect.TypeFor[Tree]()},
	{"ledger.escalation", "Escalation", reflect.TypeFor[Escalation]()},
	{"dispatch", "Dispatch", reflect.TypeFor[Dispatch]()},
}

// EventKind describes one event type an on rule can match.
type EventKind struct {
	// Name is the string a rule gives as `event`.
	Name string
	// TS is the TypeScript name of the payload.
	TS   string
	Type reflect.Type
}

// Events lists the event types rules can match.
var Events = []EventKind{
	{"linear.comment.posted", "Comment", reflect.TypeFor[Comment]()},
	{"github.check.completed", "CheckRun", reflect.TypeFor[CheckRun]()},
}

// kindOf returns the kind named name.
func kindOf(name string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

// refTS maps a record kind name to its TypeScript record name.
func refTS(kind string) string {
	if k, ok := kindOf(kind); ok {
		return k.TS
	}
	return "unknown"
}
