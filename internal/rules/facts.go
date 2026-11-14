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
	At string `js:"at" doc:"The source's own time for the item, as the source wrote it (RFC 3339)."`
	// SeenAt is when dispatch first saw the item, in milliseconds.
	SeenAt int64 `js:"seenAt" doc:"When dispatch first saw this item, in milliseconds since the epoch. The same item keeps its seenAt across cycles and restarts, so it can key a once."`
}

// Label is a Linear label and the label group it belongs to.
type Label struct {
	Name  string `js:"name" doc:"The label's name, such as ai."`
	Group string `js:"group,optional" doc:"The label group the label belongs to, absent for an ungrouped label."`
}

// Stop is the latest time a worker's turn ended.
type Stop struct {
	At int64 `js:"at" doc:"When the worker's latest turn ended, in milliseconds since the epoch. For an idle worker with no Stop record from the hook it is when dispatch first saw the worker idle, not a real stop time."`
}

// Turn is one reply a worker's Claude session finished. It mirrors sessionlog.Turn, which
// carries no js tags.
type Turn struct {
	At      int64 `js:"at" json:"at" doc:"When the turn ended, in milliseconds since the epoch."`
	Running bool  `js:"running" json:"running" doc:"True when a background task of the session was still running as the turn ended. A Claude Code version that doesn't report background tasks reads as false. Nothing links the task to a command, only the session and the order of at."`
}

// Command is a slash command a worker's Claude session took. It mirrors sessionlog.Command, which
// carries no js tags.
type Command struct {
	Name string `js:"name" json:"name" doc:"The command's name without the slash, such as merge-it for /merge-it 42."`
	Args string `js:"args" json:"args" doc:"The rest of the message, trimmed and cut at 1KB. Whatever the sender typed, so a rule that forwards it should treat it as untrusted text."`
	Cut  bool   `js:"cut" json:"cut" doc:"True when args was longer than 1KB and was cut."`
	At   int64  `js:"at" json:"at" doc:"When Claude took the message, in milliseconds since the epoch. A message sent to a working Claude waits for the turn to end, so at follows that turn's end."`
	By   string `js:"by" json:"by" ts:"\"person\" | \"dispatch\"" doc:"Who typed the command, person or dispatch. Dispatch's kickoff and every nudge that starts with a slash are dispatch's, including one a dispatch tell asked for."`
}

// Blocker is a ticket that blocks another.
type Blocker struct {
	ID        string `js:"id" doc:"The blocking ticket's id."`
	Status    string `js:"status" doc:"The blocking ticket's status name, as its ticket system spells it (not a job key)."`
	Done      bool   `js:"done" doc:"True when the blocking ticket's status does the blocker_done job, so it no longer holds this ticket back."`
	Abandoned bool   `js:"abandoned" doc:"True when the blocking ticket was canceled. An abandoned blocker is not done."`
	Ticket    Ref    `js:"ticket" ref:"ticket" ts:"Ticket | undefined" doc:"The tracked ticket with the blocker's id, undefined when dispatch doesn't track it."`
}

// Stack is where a stacked ticket's branch started.
type Stack struct {
	Parent    string `js:"parent" doc:"The stack parent's ticket id."`
	ForkPoint string `js:"forkPoint" doc:"The full sha of the commit the branch started at."`
}

// Ticket is a ticket dispatch tracks, from Linear or from the local ticket database.
type Ticket struct {
	ID               Fact[string]            `js:"id" doc:"The ticket's identifier, such as ENG-12 for a Linear ticket or LOC-3 for a local one."`
	Title            Fact[string]            `js:"title" doc:"The ticket's title."`
	URL              Fact[string]            `js:"url" doc:"The ticket's address in Linear, empty for a local ticket."`
	Branch           Fact[string]            `js:"branch" doc:"The branch dispatch launches the ticket's worker on and reads its PR by."`
	Stack            Fact[*Stack]            `js:"stack" doc:"The stack record of the ticket's branch, undefined when the branch has none. Unknown when the clone's records could not be read or the record is partial."`
	System           Fact[string]            `js:"system" ts:"\"linear\" | \"local\"" doc:"The ticket system the ticket comes from."`
	Attributes       Fact[map[string]string] `js:"attributes" ts:"Readonly<Record<string, string>>" doc:"A local ticket's key-value pairs; {} for a Linear ticket."`
	Status           Fact[string]            `js:"status" ts:"string" doc:"The job key (todo, in_progress, in_review or needs_human) when the ticket's status does one of those jobs, otherwise the status name as the ticket system spells it."`
	StatusSince      Fact[int64]             `js:"statusSince" doc:"When the ticket got its current status, in milliseconds since the epoch. For a Linear ticket it is when dispatch first saw the status, so a ticket moved while dispatch was stopped reads as moved when dispatch next looked. It changes only when the status fact's value changes, so a move between two statuses that both read as the same job key (todo, in_progress, in_review or needs_human) leaves it. For a local ticket it is when the database recorded the move."`
	PreviousStatus   Fact[*string]           `js:"previousStatus" ts:"string | undefined" doc:"The status the ticket had before its current one, as the status fact spells it, only for a change dispatch saw. Undefined for a ticket dispatch has seen in one status only."`
	OptedIn          Fact[bool]              `js:"optedIn" doc:"True when dispatch works the ticket: it carries the configured ai label, or it is a local ticket. Unknown when Linear cut the label list short and the ai label is not in what was read."`
	Repo             Fact[*string]           `js:"repo" doc:"The repo label of the ticket: for a Linear ticket its label in the repo label group, for a local ticket its repo field. It does not prove a [repos.<label>] table exists, so check ctx.config.hasRepo. Undefined when the ticket has no repo label. Unknown when Linear cut the label list short and what was read holds no repo label."`
	Labels           Fact[[]Label]           `js:"labels" doc:"The ticket's labels. Unknown when Linear cut the label list short, so a missing label can't be told from an unread one."`
	HasChildren      Fact[bool]              `js:"hasChildren" doc:"True when the ticket has child tickets. Read ctx.ticket.children(t) for the children themselves."`
	Finished         Fact[bool]              `js:"finished" doc:"True when the ticket's status does the finished job, such as Done or Canceled."`
	Paused           Fact[bool]              `js:"paused" doc:"True when the ticket's status does the paused job, such as Backlog or Todo."`
	CreatedAt        Fact[*string]           `js:"createdAt" doc:"When the ticket was created, RFC 3339, undefined when its system gave none. Among actions that take a worker slot, the oldest ticket starts first, and a ticket with no createdAt goes last."`
	LastHumanComment Fact[*Seen]             `js:"lastHumanComment" doc:"The newest comment on the ticket, whoever wrote it, dispatch's own included, undefined when it has none. It carries only the comment's times, not its text or author. Despite the name, it is not limited to people's comments."`
}

// Worker is a ticket's place on a tree it holds, and the Claude session there, if any.
type Worker struct {
	State        Fact[string]  `js:"state" ts:"\"none\" | \"working\" | \"idle\" | \"blocked\"" doc:"none when the ticket has no Claude session in a workspace, working when it is running a turn, when dispatch sent it a nudge and no read has yet seen it working or blocked, or when the multiplexer can't tell its status (a worker with no status whose launch is unconfirmed reads idle instead), idle when it is waiting at its prompt, blocked when it waits on a question popup. A worker the ticket never had also reads none."`
	Ticket       Fact[Ref]     `js:"ticket" ref:"ticket" doc:"The ticket the worker serves."`
	Tree         Fact[*string] `js:"tree" doc:"The path of the treehouse tree the worker runs in, undefined when it has none."`
	Workspace    Fact[*string] `js:"workspace" doc:"The id of the multiplexer workspace the worker runs in, undefined when it has none."`
	SeenAt       Fact[int64]   `js:"seenAt" doc:"When the worker last changed between present (any state but none) and absent, in milliseconds since the epoch. It survives a change from working to idle and a restart."`
	GoneSince    Fact[int64]   `js:"goneSince" doc:"The same time as seenAt. Read it as when the worker went none when its state is none."`
	IdleSince    Fact[*int64]  `js:"idleSince" doc:"When the worker became idle, in milliseconds since the epoch, undefined unless it is idle. Dispatch sending a nudge to an idle worker makes it working, so the value then is undefined."`
	BlockedSince Fact[*int64]  `js:"blockedSince" doc:"When the worker became blocked on a popup, in milliseconds since the epoch, undefined unless it is blocked."`
	// UnconfirmedSince is when the latest launch on this tree began, while its
	// kickoff is still unconfirmed. A launch killed before the kickoff leaves it set.
	UnconfirmedSince Fact[*int64]    `js:"unconfirmedSince" doc:"When the latest launch on this tree began, in milliseconds since the epoch, while its kickoff instruction has not yet run. Undefined once the kickoff ran, once the launch returned, and when the worker's state is none. A launch killed before the kickoff leaves it set."`
	LastStop         Fact[*Stop]     `js:"lastStop" doc:"When the worker's latest turn ended, undefined unless the worker is idle. Dispatch reads the Stop hook's record only for an idle worker on an In Progress or Needs Human ticket. For an In Review worker, a worker with no Stop record, or a Stop that did not list background tasks, it holds when dispatch first saw the worker idle instead."`
	Busy             Fact[bool]      `js:"busy" doc:"True when an idle worker's latest Stop listed a background task still running and that Stop is under 60 minutes old. After 60 minutes it reads false even if the task still runs, because a hung task never wakes its worker. Dispatch reads the Stop record only for an idle worker on an In Progress or Needs Human ticket, so busy is false for an In Review worker, for a worker that is not idle, and for a Claude Code version that doesn't report background tasks."`
	Waiting          Fact[bool]      `js:"waiting" doc:"True when the worker has paused: it is idle on an In Progress or Needs Human ticket, its kickoff has run, its latest Stop listed no background task still running (a task listed over 60 minutes ago counts as stopped, as for busy), and its session has taken no message since that Stop, whether a slash command or any other. False while the worker is in a turn, which includes a message typed to an idle worker before herdr reads it working. This is the Stop-record and session-log part of the test that gives its workspace the 💤 label. False for a worker that is not idle, whose kickoff is unconfirmed, with no Stop record or a Stop that did not list background tasks, with a background task still running, and for a worker on a ticket that is not In Progress or Needs Human. A Needs Human workspace keeps its 🔴 label whatever this says."`
	Commands         Fact[[]Command] `js:"commands" doc:"Every slash command the worker's current Claude session has taken, oldest first, for a worker on an In Progress, In Review or Needs Human ticket. A message counts when it starts with /name, so a message that only mentions one does not. A skill the model loads by itself is not a command. Empty for a session that has taken none, and unknown when the multiplexer reports no session or the session's record can't be read. A worker relaunched in a new session starts over."`
	Turns            Fact[[]Turn]    `js:"turns" doc:"Every turn the worker's current Claude session has ended, oldest first, for a worker on an In Progress, In Review or Needs Human ticket. Empty for a session that has ended none, and unknown when the multiplexer reports no session or the session's record can't be read. A worker relaunched in a new session starts over."`
}

// PR is a ticket's pull request: the open PR of an In Review ticket, or the merged PR of a local ticket.
type PR struct {
	Ticket          Fact[Ref]    `js:"ticket" ref:"ticket" doc:"The ticket the PR belongs to."`
	Number          Fact[int64]  `js:"number" doc:"The PR's number in its repo."`
	URL             Fact[string] `js:"url" doc:"The PR's address on GitHub."`
	Head            Fact[string] `js:"head" doc:"The full sha of the PR's head commit. A push moves it, which clears approval, so it keys a once that should run again per push."`
	Closed          Fact[bool]   `js:"closed" doc:"Always the same value as merged, so true only for a local ticket's merged PR. A PR closed without merging has no record."`
	Merged          Fact[bool]   `js:"merged" doc:"The PR merged into the repo's default branch. Dispatch reads merged PRs only for local tickets, so it is false on a Linear ticket's PR."`
	MergedAt        Fact[int64]  `js:"mergedAt" doc:"When the PR merged, in milliseconds since the epoch. 0 for a PR that has not merged. Compare it with the ticket's statusSince to count only a merge newer than the ticket's current status."`
	Draft           Fact[bool]   `js:"draft" doc:"True when the PR has not been opened for review. GitHub refuses to merge a draft."`
	Approved        Fact[bool]   `js:"approved" doc:"True when a reviewer's latest review approves the head commit and no reviewer's latest review requests changes. A push clears it, because an approval of an earlier commit doesn't count. It stays false while the repo's approval_checks are unmet, and reviews by ignore_comment_authors are skipped."`
	Ready           Fact[bool]   `js:"ready" doc:"True when the PR needs no work, has no open review thread, every check has passed (a PR with no checks counts as passed; checks named in approval_checks, and non-required checks when ignore_optional_checks is on, are left out) and GitHub says it merges cleanly. A running check, or a merge state GitHub has not computed yet, is not ready."`
	NeedsWork       Fact[bool]   `js:"needsWork" doc:"True when the PR has a failed check, changes requested or a merge conflict. Changes requested counts only on the head commit and not while a check is still running. An open review thread alone is not needs work."`
	Conflict        Fact[bool]   `js:"conflict" doc:"GitHub reports the PR's merge state as conflicting. needsWork counts a conflict too."`
	Problem         Fact[string] `js:"problem" doc:"The reasons needsWork is true as one phrase, such as a failed check and a merge conflict. Empty when needsWork is false."`
	ActivitySummary Fact[string] `js:"activitySummary" doc:"Describes the item at lastActivity, such as a review by alice."`
	LastActivity    Fact[*Seen]  `js:"lastActivity" doc:"The newest review, conversation comment or comment on an unresolved review thread, undefined when there is none. It skips approvals with no body, dismissals, drafts and anything by ignore_comment_authors. A review with only a body opens no thread, so this is the one way a rule sees it."`
	LastThread      Fact[*Seen]  `js:"lastThread" doc:"The newest comment on a review thread that is still unresolved, undefined when no thread is open. Comments by ignore_comment_authors are skipped."`
	OpenAs          Fact[string] `js:"openAs" ts:"\"open\" | \"draft\"" doc:"How the PR's repo opens its PRs, open or draft, from the repo's open_pr_as setting."`
	AutoMerge       Fact[bool]   `js:"autoMerge" doc:"True when the PR's repo table sets auto_merge. It is only that setting. The shipped auto-merge rule also needs the ticket In Review and the PR not a draft."`
	WaitingOn       Fact[string] `js:"waitingOn" ts:"\"nobody\" | \"you\" | \"approvers\" | \"merge\"" doc:"Who the PR waits on once it is ready. A PR that is not ready waits on nobody. In a repo that opens PRs as drafts, a PR you have opened waits on its approvers until its head is approved, then on whoever merges it. Every other ready PR waits on you."`
}

// Tree is a worktree in a repo's treehouse pool.
type Tree struct {
	Path           Fact[string] `js:"path" doc:"The tree's path on disk."`
	Leased         Fact[bool]   `js:"leased" doc:"True when a ticket holds the tree."`
	Holder         Fact[Ref]    `js:"holder" ref:"ticket" ts:"Ticket | undefined" doc:"The ticket that holds the tree, undefined when it is free. A holder that isn't one of your tickets reads as a ticket with only id known, and finished and paused read false."`
	LeasedSince    Fact[int64]  `js:"leasedSince" doc:"When dispatch first saw the tree with its current holder, in milliseconds since the epoch. While the holder is unknown it reads this cycle's time instead of Unknown, so don't key a once on it then."`
	Pushed         Fact[bool]   `js:"pushed" doc:"True when no commit reachable from HEAD is missing from every remote. Unknown unless dispatch inspected the tree, which it does for a finished ticket's tree and for the tree of a ticket outside In Progress and In Review that has no worker in it, and unknown when the git read failed."`
	TrackedChanges Fact[bool]   `js:"trackedChanges" doc:"True when git tracks a file in the tree with uncommitted changes. Unknown unless dispatch inspected the tree (see pushed), or when the git read failed."`
	Unfinished     Fact[string] `js:"unfinished" doc:"What returning the tree would destroy, as text, empty for a pushed tree with no tracked changes. Unknown unless dispatch inspected the tree (see pushed), or when the git read failed."`
	// Untracked lists the files git has never seen in the tree, one per line. Returning a
	// tree deletes them, but a nested repository survives that and keeps the return refused.
	Untracked Fact[string] `js:"untracked" doc:"The files in the tree that git does not track and .gitignore does not cover, one per line. Returning the tree deletes them, but a nested repository survives that and keeps the return refused. Unknown unless dispatch inspected the tree (see pushed), or when the git read failed."`
	// CleanSince is when pushed, trackedChanges or untracked last changed, so a return refused for
	// late work or a nested repository is tried again once that is gone.
	CleanSince Fact[int64] `js:"cleanSince" doc:"The latest time pushed, trackedChanges or untracked changed value. Lets a return refused for late work or a nested repository be tried again once that is gone. Unknown until pushed, trackedChanges and untracked are all known."`
}

// Escalation is a ticket's move to Needs Human and the comment that explains it.
type Escalation struct {
	Ticket    Fact[Ref]    `js:"ticket" ref:"ticket" doc:"The ticket that was escalated."`
	Reason    Fact[string] `js:"reason" doc:"The reason given to escalate. When the move failed, it is the error text instead."`
	Moved     Fact[string] `js:"moved" ts:"\"done\" | \"failed\" | \"pending\"" doc:"How the move to Needs Human went: done or failed, or pending while the escalation is still running or was cut short before it recorded its steps."`
	Commented Fact[string] `js:"commented" ts:"\"done\" | \"failed\" | \"refused\" | \"attempted\" | \"pending\"" doc:"How the explaining comment went: done or failed, attempted when the move failed so no comment was tried, or pending while the escalation is still running or was cut short before it recorded its steps."`
	Started   Fact[int64]  `js:"started" doc:"When the escalation began, in milliseconds since the epoch."`
}

// Dispatch is dispatch itself, so a rule can run once per cycle.
type Dispatch struct {
	ID        Fact[string] `js:"id" doc:"Always dispatch. The one record of this kind exists so a rule can run once per cycle."`
	StartedAt Fact[int64]  `js:"startedAt" doc:"When this dispatch process began, in milliseconds since the epoch."`
	// A failed inbox read leaves InboxReadAt as it was.
	InboxReadAt Fact[*int64] `js:"inboxReadAt" doc:"Milliseconds since the epoch of the last good read of the GitHub inbox, undefined until the first. A failed read leaves it as it was. Use it for staleness (ctx.now - ctx.dispatch.inboxReadAt), not for newness: a once key on [item.id, item.updatedAt] finds new and changed items."`
}

// InboxItem is one entry in the dispatch owner's GitHub notifications inbox, as GitHub listed it at
// the last good read. Dispatch reports GitHub's values as GitHub gave them.
type InboxItem struct {
	ID          Fact[string]  `js:"id" doc:"GitHub's thread id. It stays the same as notifications arrive on the thread."`
	Reason      Fact[string]  `js:"reason" doc:"GitHub's current reason: review_requested, mention, author, ... GitHub replaces it with the newest one, so a reason it has replaced is gone."`
	Unread      Fact[bool]    `js:"unread" doc:"True when the notification was unread at the last good read. Dispatch reads the newest 50 notifications, read ones included."`
	UpdatedAt   Fact[string]  `js:"updatedAt" doc:"GitHub's updated_at, RFC 3339 as written."`
	RepoName    Fact[string]  `js:"repoName" doc:"owner/name."`
	SubjectType Fact[string]  `js:"subjectType" doc:"GitHub's subject.type: PullRequest, Issue, Release, ..."`
	Title       Fact[string]  `js:"title" doc:"The title of the notification's subject, such as a PR's title."`
	APIURL      Fact[*string] `js:"apiUrl" doc:"GitHub's subject.url, an API URL, undefined when GitHub gives none."`
	Repo        Fact[*string] `js:"repo" doc:"The [repos.<label>] whose chosen remote names repoName. Unknown when more than one clone's chosen remote names it."`
}

// Comment is a ticket comment, the payload of comment.posted.
type Comment struct {
	ID         string `js:"id" doc:"The comment's id in its ticket system."`
	Body       string `js:"body" doc:"The comment's text, as written."`
	Author     string `js:"author" doc:"The author's name as the ticket system gives it."`
	At         string `js:"at" doc:"When the comment was posted, as the ticket system wrote it."`
	ByDispatch bool   `js:"byDispatch" doc:"True when dispatch posted the comment itself."`
	Ticket     Ref    `js:"ticket" ref:"ticket" doc:"The ticket the comment is on."`
}

// CheckRun is a finished check on a pull request, the payload of check.completed.
type CheckRun struct {
	ID         string `js:"id" doc:"The check run's id on GitHub."`
	Name       string `js:"name" doc:"The check's name."`
	Conclusion string `js:"conclusion" doc:"How the check ended, such as success or failure, as GitHub spells it."`
	URL        string `js:"url" doc:"The check's address on GitHub."`
	Head       string `js:"head" doc:"The sha of the commit the check ran on."`
	PR         Ref    `js:"pr" ref:"pr" doc:"The PR the check ran on."`
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
	{"ticket", "Ticket", reflect.TypeFor[Ticket]()},
	{"pr", "PR", reflect.TypeFor[PR]()},
	{"worker", "Worker", reflect.TypeFor[Worker]()},
	{"tree", "Tree", reflect.TypeFor[Tree]()},
	{"escalation", "Escalation", reflect.TypeFor[Escalation]()},
	{"dispatch", "Dispatch", reflect.TypeFor[Dispatch]()},
	{"inbox", "InboxItem", reflect.TypeFor[InboxItem]()},
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
	{"comment.posted", "Comment", reflect.TypeFor[Comment]()},
	{"check.completed", "CheckRun", reflect.TypeFor[CheckRun]()},
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
