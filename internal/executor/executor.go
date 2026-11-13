// Package executor carries out the actions a cycle wants. Each action checks the
// source right before it acts, so a decision made on a stale read fails instead
// of landing, and ends as a receipt outcome the ledger keeps. The cycle decides
// what is wanted; the only veto here is a launch while a Claude session runs in the
// worker, herdr was not read, or the ticket's repo pool was not read.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kapetan-io/dispatch/internal/clock"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/httpservice"
	"github.com/kapetan-io/dispatch/internal/kickoffs"
	"github.com/kapetan-io/dispatch/internal/launcher"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/notify"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/typed"
	"github.com/kapetan-io/dispatch/internal/world"
)

// Deadlines. A launch takes 8 to 13 seconds when it works. One that lands after its
// deadline shows up as a worker on the next read, which makes the launch rule false.
// A launch's deadline stops while it waits behind another launch into the same repo, and the
// wait has its own limit of QueueDeadlines launch deadlines, so a launch that hangs while it
// holds the repo fails the ones behind it instead of stalling them.
const (
	QueueDeadlines  = 4
	LaunchDeadline  = 25 * time.Second
	DefaultDeadline = time.Minute
)

// Env is the tools an action drives.
type Env struct {
	Config       config.Config
	Systems      Systems
	Herdr        herdr.Herdr
	Treehouse    treehouse.Pool
	PullRequests pullrequest.Repository
	Launcher     launcher.Launcher
	// Harnesses holds the adapter for each kind a harness table can name.
	Harnesses harness.Registry
	HTTP      httpservice.Services
	// Followers is the sessions following each ticket, which a notification is sent to. Nil starts with none.
	Followers *notify.Followers
	// Environment is what the git commands that return a tree run with.
	Environment environment.Environment
	StateDir    string
	Stderr      io.Writer
	// Typed is what dispatch sent or typed into workers, shared with the launcher and the loop.
	Typed *typed.Log
	// LaunchDeadline and Deadline override the default budgets, which run on Clock.
	LaunchDeadline time.Duration
	Deadline       time.Duration
	// Clock runs the deadlines. Defaults to the wall clock.
	Clock clock.Clock
}

// Systems are the ticket systems the executor writes to. A nil Local means local tickets are off.
type Systems struct {
	Linear ticket.System
	Local  *localticket.Store
}

// Of returns the system the id belongs to. It checks the nil store itself, because a nil *Store in a
// ticket.System is not a nil interface.
func (y Systems) Of(id string) (ticket.System, error) {
	if ticket.SystemOf(id) != ticket.SystemLocal {
		return y.Linear, nil
	}
	if y.Local == nil {
		return nil, fmt.Errorf("%s is a local ticket, and local tickets are off", id)
	}
	return y.Local.System(), nil
}

// UseLocal gives the executor the local ticket database once the loop has opened it, before any action runs.
func (e *Executor) UseLocal(store *localticket.Store) { e.Systems.Local = store }

// rereadOf is the part of the world to read again after a write to the ticket's own system.
func rereadOf(id string) world.Part {
	if ticket.SystemOf(id) == ticket.SystemLocal {
		return world.FromLocalTickets
	}
	return world.FromLinear
}

// Result is how an action ended.
type Result struct {
	Outcome ledger.Outcome
	Detail  string
	// Note is shown in the run window instead of Detail when it says more.
	Note     string
	Steps    []ledger.Step
	TicketID string
	// Reread names the sources to read again before the next cycle.
	Reread world.Part
	// Nudged is the ticket whose worker was sent a nudge.
	Nudged string
}

// Executor runs actions.
type Executor struct {
	Env
}

// New returns an executor over env.
func New(env Env) *Executor {
	if env.Clock == nil {
		env.Clock = clock.Wall{}
	}
	if env.Followers == nil {
		env.Followers = &notify.Followers{}
	}
	return &Executor{Env: env}
}

// WhyNotLaunch says why a launch must not start whatever the rule asked, or "" when it
// may. A Claude session running in the worker, or a herdr or repo pool that was not
// read, means a second session could start beside it.
func (e *Executor) WhyNotLaunch(w rules.Want, wd world.World) string {
	if w.Receipt != "launch" {
		return ""
	}
	if wd.HerdrErr != nil {
		return e.Herdr.Multiplexer() + " was not read"
	}
	if wk := world.FindWorker(w.Subject, wd.Trees, wd.Snapshot); wk.State != world.NoWorker {
		return "a worker is running"
	}
	if t, ok := ticketOf(wd, w.Subject); ok {
		label, _ := world.ScopeOf(e.Config).RepoOf(t)
		if repo, _, _ := e.Config.Repos.Find(label); wd.UnreadRepos[repo] {
			return "the pool of " + repo + " was not read"
		}
	}
	return ""
}

// ticketOf finds a ticket by identifier in everything the world read.
func ticketOf(wd world.World, id string) (ticket.Ticket, bool) {
	for _, t := range wd.Tickets {
		if t.Identifier == id {
			return t, true
		}
	}
	for _, t := range wd.LeaseHolders {
		if t.Identifier == id {
			return t, true
		}
	}
	for _, kids := range wd.Children {
		for _, t := range kids {
			if t.Identifier == id {
				return t, true
			}
		}
	}
	return ticket.Ticket{}, false
}

func (e *Executor) deadline(receipt string) time.Duration {
	if receipt == "launch" {
		if e.LaunchDeadline > 0 {
			return e.LaunchDeadline
		}
		return LaunchDeadline
	}
	if e.Deadline > 0 {
		return e.Deadline
	}
	return DefaultDeadline
}

// Run carries out w against what wd read when the cycle ran.
func (e *Executor) Run(ctx context.Context, w rules.Want, wd world.World) Result {
	ctx, cancel, queued := clock.WithPausableTimeout(ctx, e.Clock, w.Receipt+" "+w.Subject, e.deadline(w.Receipt))
	defer cancel()
	var res Result
	switch w.Receipt {
	case "launch":
		res = e.launch(ctx, w, wd, queued)
	case "nudge":
		res = e.nudge(ctx, w, wd)
	case "close":
		res = e.closeWorkspace(ctx, w)
	case "move":
		res = e.move(ctx, w, wd)
	case "comment":
		res = e.comment(ctx, w, wd)
	case "escalate":
		res = e.escalate(ctx, w, wd)
	case "return":
		res = e.returnTree(ctx, w, wd)
	case "merge":
		res = e.merge(ctx, w, wd)
	case "notify":
		res = e.notify(ctx, w, wd)
	case "post":
		res = e.post(ctx, w)
	case "create":
		res = e.create(w)
	case "edit":
		res = e.edit(w)
	default:
		res = Result{Outcome: ledger.Failed, Detail: "no executor for " + w.Receipt}
	}
	// A notify or comment that timed out may have landed, and a retry could say it twice.
	// Nudge, post and create decide for themselves: a nudge that failed sent nothing, because
	// Deliver returns nil once its write completes.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && res.Outcome == ledger.Failed && (w.Receipt == "notify" || w.Receipt == "comment") {
		res.Outcome, res.Detail = ledger.Done, "timeout"
	}
	return res
}

// SafeToRetry reports whether an action with this receipt name can run again after
// its outcome was lost. A message whose outcome was lost may have gone out, and a
// retry could say it twice. A create whose outcome was lost may have filed its ticket,
// and a retry would file a second. Post is on the list, but Run's timeout rule leaves it
// out, because post decides itself whether a timeout went out, and nudge and create fail with nothing sent.
func SafeToRetry(receipt string) bool {
	return !slices.Contains([]string{"nudge", "notify", "comment", "post", "create"}, receipt)
}

func failed(format string, args ...any) Result {
	return Result{Outcome: ledger.Failed, Detail: fmt.Sprintf(format, args...)}
}

func (e *Executor) launch(ctx context.Context, w rules.Want, wd world.World, queued func() func()) Result {
	t, ok := ticketOf(wd, w.Subject)
	if !ok {
		return failed("%s was not read", w.Subject)
	}
	cfg := e.Config
	label, labeled := world.ScopeOf(cfg).RepoOf(t)
	_, repo, _ := cfg.Repos.Find(label)
	// A rule that skips hasRepo would otherwise launch in dispatch's own directory.
	if !labeled || repo.Path == "" {
		return Result{Outcome: ledger.Refused, Detail: t.Identifier + " has no configured repo"}
	}
	model, _ := w.Options["model"].(string)
	ref, _ := w.Options["harness"].(string)
	// Without a harness table, a launch runs Claude Code.
	kind := config.KindClaudeCode
	switch {
	case ref != "" && model != "":
		return Result{Outcome: ledger.Refused, Detail: fmt.Sprintf("launch of %s passes both harness %q and model %q: pass one", t.Identifier, ref, model)}
	case ref != "":
		table, err := cfg.Table(ref)
		if err != nil {
			return Result{Outcome: ledger.Refused, Detail: fmt.Sprintf("launch of %s passes harness %q: %v", t.Identifier, ref, err)}
		}
		kind, model = table.Kind, table.Model
	case model == "":
		model = cfg.WorkerModel()
	}
	program, err := e.Harnesses.Program(kind, model, cfg.Plugins.PluginRoot())
	if err != nil {
		return Result{Outcome: ledger.Refused, Detail: fmt.Sprintf("launch of %s: %v", t.Identifier, err)}
	}
	kickoff, _ := w.Options["kickoff"].(string)
	req := launcher.Request{
		RepoDir: repo.Path, Ticket: t.Identifier, Title: t.Title, Branch: t.Branch, StartPR: t.StartPR, Program: program, Kickoff: kickoff, WhileQueued: queued, QueueLimit: QueueDeadlines * e.deadline(w.Receipt),
	}
	if on, ok := w.Options["stackOn"].(map[string]any); ok {
		ticket, _ := on["ticket"].(string)
		branch, _ := on["branch"].(string)
		head, _ := on["head"].(string)
		if ticket == "" || branch == "" || head == "" {
			return Result{Outcome: ledger.Refused, Detail: "stackOn names no PR head to start " + t.Identifier + " at"}
		}
		req.Stack = &launcher.Stack{Ticket: ticket, Branch: branch, Head: head}
	}
	r := e.Launcher.Launch(ctx, req)
	res := Result{Reread: world.FromLocal}
	switch {
	case r.Status == launcher.StatusOK:
		res.Outcome, res.Detail = ledger.Done, "ok"
		if r.Workspace != "" {
			res.Detail = "ok: workspace " + r.Workspace
		}
		if r.Stacked {
			res.Detail += fmt.Sprintf(", stacked on %s at %s (%s)", req.Stack.Ticket, shortSHA(req.Stack.Head), req.Stack.Branch)
		}
	case r.Status == launcher.StatusAlreadyRunning:
		res.Outcome, res.Detail = ledger.Done, launcher.StatusAlreadyRunning
	case r.Status == launcher.StatusPoolExhausted:
		res.Outcome, res.Detail = ledger.Refused, launcher.StatusPoolExhausted
		res.Note = launcher.StatusPoolExhausted + ": " + r.Detail
	default:
		res.Outcome, res.Detail = ledger.Failed, r.Status
		if r.Detail != "" {
			res.Detail = r.Status + ": " + r.Detail
		}
	}
	return res
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (e *Executor) nudge(ctx context.Context, w rules.Want, wd world.World) Result {
	wk := world.FindWorker(w.Subject, wd.Trees, wd.Snapshot)
	switch {
	case wk.State == world.NoWorker:
		return failed("%s has no worker", w.Subject)
	case wk.State == world.Blocked:
		return failed("blocked")
	case !wk.StatusKnown():
		return failed("status unknown, nothing sent")
	case wk.Session == "":
		return failed("nothing sent: session not known yet")
	}
	h, ok := e.Harnesses.ForAgent(wk.Kind)
	if !ok {
		return failed("no harness for agent kind %q, nothing sent", wk.Kind)
	}
	text, _ := w.Args["text"].(string)
	e.Typed.Note(wk.TreePath, text)
	if err := h.Deliver(ctx, wk.Session, text); err != nil {
		e.Typed.Withdraw(wk.TreePath, text)
		return failed("nothing sent: %v", err)
	}
	return Result{Outcome: ledger.Done, Nudged: w.Subject}
}

func (e *Executor) closeWorkspace(ctx context.Context, w rules.Want) Result {
	id := w.Target["workspace"]
	if id == "" {
		return Result{Outcome: ledger.Done, Detail: "no workspace"}
	}
	if err := e.Herdr.CloseWorkspace(ctx, id); err != nil {
		return failed("while closing workspace %s: %v", id, err)
	}
	res := Result{Outcome: ledger.Done, Reread: world.FromLocal}
	// A closed workspace ends any launch on its tree, and a mark left behind would be read
	// onto the next Claude there.
	if tree := w.Target["tree"]; tree != "" {
		kickoffs.Clear(e.StateDir, tree)
		e.Typed.Clear(tree)
	}
	if len(w.Once) > 0 && w.Once[0] == "close-unconfirmed" {
		res.Detail = "closed workspace " + id + ": its launch died before the kickoff ran"
	}
	return res
}

func (e *Executor) move(ctx context.Context, w rules.Want, wd world.World) Result {
	t, ok := ticketOf(wd, w.Subject)
	if !ok {
		return failed("%s was not read", w.Subject)
	}
	key, _ := w.Args["status"].(string)
	sys, err := e.Systems.Of(t.Identifier)
	if err != nil {
		return failed("%v", err)
	}
	name, err := sys.Move(ctx, t, ticket.Job(key), "")
	if err != nil {
		return failed("%v", err)
	}
	why, _ := w.Args["why"].(string)
	return Result{Outcome: ledger.Done, Detail: name + ": " + why, Reread: rereadOf(t.Identifier)}
}

func (e *Executor) comment(ctx context.Context, w rules.Want, wd world.World) Result {
	text, _ := w.Args["text"].(string)
	sys, err := e.Systems.Of(w.Subject)
	if err != nil {
		return failed("%v", err)
	}
	ref, ok := ticketOf(wd, w.Subject)
	if !ok {
		ref = ticket.Ticket{Identifier: w.Subject, ID: w.Target["linearID"]}
	}
	if err := sys.Comment(ctx, ref, text); err != nil {
		return failed("while commenting on %s: %v", w.Subject, err)
	}
	res := Result{Outcome: ledger.Done}
	// A Linear comment waits for the next resync, as it always has. A local one is a cheap read, and the merge check compares comments.
	if ticket.SystemOf(w.Subject) == ticket.SystemLocal {
		res.Reread = world.FromLocalTickets
	}
	return res
}

// escalate moves a ticket to Needs Human and comments why. A system that cannot do both at once
// reports the lost comment, and the receipt keeps the two steps apart so it can be finished later.
func (e *Executor) escalate(ctx context.Context, w rules.Want, wd world.World) Result {
	t, ok := ticketOf(wd, w.Subject)
	if !ok {
		return failed("%s was not read", w.Subject)
	}
	why, _ := w.Args["why"].(string)
	res := Result{Detail: why, TicketID: t.ID, Steps: []ledger.Step{{Name: "move"}, {Name: "comment"}}}
	sys, err := e.Systems.Of(t.Identifier)
	if err == nil {
		_, err = sys.Move(ctx, t, ticket.NeedsHuman, "The dispatcher moved this ticket to Needs Human because "+why)
	}
	switch {
	case errors.Is(err, ticket.ErrNoteLost):
		res.Steps[0].Outcome, res.Steps[1].Outcome, res.Reread, res.Outcome = ledger.Done, ledger.Failed, rereadOf(t.Identifier), ledger.Done
		if e.Stderr != nil {
			_, _ = fmt.Fprintf(e.Stderr, "dispatch: %v\n", err)
		}
	case err != nil:
		res.Outcome, res.Detail = ledger.Failed, err.Error()
		res.Steps[0].Outcome = ledger.Failed
	default:
		res.Steps[0].Outcome, res.Steps[1].Outcome, res.Reread, res.Outcome = ledger.Done, ledger.Done, rereadOf(t.Identifier), ledger.Done
	}
	return res
}

// returnTree closes the tree's workspace, looks at the tree again, removes
// untracked files and gives the tree back. Work found on the second look keeps
// the tree, because the worker could have written to it since the cycle ran.
func (e *Executor) returnTree(ctx context.Context, w rules.Want, wd world.World) Result {
	path, holder := w.Target["path"], w.Target["holder"]
	var repoDir string
	for _, tr := range wd.Trees {
		if tr.Path == path {
			repoDir = tr.RepoDir
		}
	}
	if repoDir == "" {
		return failed("%s is not in a pool dispatch read", path)
	}
	res := Result{Reread: world.FromLocal}
	if id := e.workspaceOn(path, wd); id != "" {
		if err := e.Herdr.CloseWorkspace(ctx, id); err != nil {
			return failed("while closing workspace %s: %v", id, err)
		}
	}
	why, err := treehouse.Unfinished(ctx, e.Environment, path)
	if err != nil {
		return failed("%v", err)
	}
	if why != "" {
		res.Outcome, res.Detail = ledger.Refused, "the worker left "+why+" in "+path
		return res
	}
	files, err := treehouse.Untracked(ctx, e.Environment, path)
	if err != nil {
		return failed("%v", err)
	}
	detail := "returned " + path
	if len(files) > 0 {
		if err := treehouse.RemoveUntracked(ctx, e.Environment, path); err != nil {
			return failed("%v", err)
		}
		left, err := treehouse.Untracked(ctx, e.Environment, path)
		if err != nil {
			return failed("%v", err)
		}
		// git clean skips a nested repository, and returning the tree would discard it.
		if len(left) > 0 {
			res.Outcome, res.Detail = ledger.Refused, "untracked "+nameList(left)+" in "+path+" survived git clean"
			return res
		}
		detail += ", removed untracked " + nameList(files)
	}
	if err := e.Treehouse.Return(ctx, repoDir, path, holder); err != nil {
		return failed("while returning %s to the pool: %v", path, err)
	}
	stops.MarkReturned(e.StateDir, path, e.Clock.Now())
	sessionlog.Remove(e.StateDir, path)
	kickoffs.Clear(e.StateDir, path)
	res.Outcome, res.Detail = ledger.Done, detail
	return res
}

func (e *Executor) workspaceOn(path string, wd world.World) string {
	for _, leases := range wd.Leases {
		for _, wk := range leases {
			if wk.TreePath == path {
				return wk.WorkspaceID
			}
		}
	}
	for _, wk := range wd.Workers {
		if wk.TreePath == path {
			return wk.WorkspaceID
		}
	}
	return ""
}

// nameList joins file names with spaces and quotes a name that holds whitespace
// or a control character, so a list never reads ambiguously.
func nameList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = name
		if strings.ContainsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			quoted[i] = strconv.Quote(name)
		}
	}
	return strings.Join(quoted, " ")
}

// merge merges the PR at the head the rule saw. GitHub refuses a head that moved,
// and the ticket is read again first so a comment posted since the last read is
// seen before anything merges.
func (e *Executor) merge(ctx context.Context, w rules.Want, wd world.World) Result {
	head, url := w.Target["head"], w.Target["url"]
	t, ok := ticketOf(wd, w.Subject)
	if !ok {
		return failed("%s was not read", w.Subject)
	}
	sys, err := e.Systems.Of(t.Identifier)
	if err != nil {
		return failed("%v", err)
	}
	if err := sys.Unchanged(ctx, t); err != nil {
		if !errors.Is(err, ticket.ErrChanged) {
			return failed("%v", err)
		}
		res := failed("%s changed since the cycle, so the merge waits for a new decision", t.Identifier)
		res.Reread = rereadOf(t.Identifier)
		return res
	}
	if err := e.PullRequests.Merge(ctx, url, head); err != nil {
		msg, _, _ := strings.Cut(err.Error(), "\n")
		// No re-read: the notice for a refused head has to see that head, and the next resync reads the new one.
		return Result{Outcome: ledger.Refused, Detail: msg}
	}
	return Result{Outcome: ledger.Done, Reread: world.FromGitHub}
}

func (e *Executor) notify(ctx context.Context, w rules.Want, wd world.World) Result {
	text, _ := w.Args["text"].(string)
	t, _ := ticketOf(wd, w.Subject)
	if t.Identifier == "" {
		t.Identifier = w.Subject
	}
	reason, ok := w.Options["reason"].(string)
	if !ok && len(w.Once) > 0 {
		reason, _ = w.Once[0].(string)
	}
	notify.Post{Followers: e.Followers, Harnesses: e.Harnesses, Stderr: e.Stderr}.Tell(ctx, notify.Notification{Ticket: t, Reason: reason, Text: text})
	return Result{Outcome: ledger.Done}
}

// TellAll sends text, which is about dispatch rather than a ticket, to every session
// following a ticket, and returns how many sessions took it.
func (e *Executor) TellAll(ctx context.Context, text string) int {
	return notify.Post{Followers: e.Followers, Harnesses: e.Harnesses, Stderr: e.Stderr}.TellAll(ctx, text)
}

// post sends a rule's body to its service. A POST that timed out may have landed, so it counts as sent.
func (e *Executor) post(ctx context.Context, w rules.Want) Result {
	path, _ := w.Args["path"].(string)
	body, err := json.Marshal(w.Args["body"])
	if err != nil {
		return Result{Outcome: ledger.Refused, Detail: "the body is not JSON"}
	}
	if e.HTTP == nil {
		return failed("no services to call")
	}
	res, err := e.HTTP.Post(ctx, w.Subject, path, body)
	var unavailable *httpservice.Unavailable
	switch {
	case errors.As(err, &unavailable) && unavailable.Timeout:
		// Only a timeout after the request went out may have landed. A token_command that ran
		// out of time sent nothing, so that one stays Failed and is tried again.
		return Result{Outcome: ledger.Done, Detail: "timeout"}
	case errors.Is(err, httpservice.ErrUnknownService), errors.Is(err, httpservice.ErrPath):
		// The config changed since the rule queued it, and trying again can't fix that.
		return Result{Outcome: ledger.Refused, Detail: err.Error()}
	case err != nil:
		return failed("%v", err)
	}
	return Result{Outcome: ledger.Done, Detail: fmt.Sprintf("POST %s %s: %d", w.Subject, path, res.Status)}
}

// create files the ticket a rule drafted. A refusal of the store, such as an unknown repo or a link to a
// missing ticket, ends the receipt Refused, because asking again would be refused again.
func (e *Executor) create(w rules.Want) Result {
	if e.Systems.Local == nil {
		return Result{Outcome: ledger.Refused, Detail: "local tickets are off"}
	}
	var draft localticket.Draft
	if err := decodeArgs(w.Args["draft"], &draft); err != nil {
		return failed("while reading the draft: %v", err)
	}
	if job, ok := w.Args["draft"].(map[string]any)["status"].(string); ok {
		name, found := e.Systems.Local.Statuses().NameOf(ticket.Job(job))
		if !found {
			return Result{Outcome: ledger.Refused, Detail: "no status does the job " + job}
		}
		draft.Status = name
	}
	draft.FiledBy = w.Rule
	id, err := e.Systems.Local.Create(draft)
	if err != nil {
		return storeFailure(err)
	}
	return Result{Outcome: ledger.Done, Detail: id, Reread: world.FromLocalTickets}
}

// edit changes the local ticket a rule named.
func (e *Executor) edit(w rules.Want) Result {
	if e.Systems.Local == nil {
		return Result{Outcome: ledger.Refused, Detail: "local tickets are off"}
	}
	var changes localticket.Edit
	if err := decodeArgs(w.Args["changes"], &changes); err != nil {
		return failed("while reading the changes: %v", err)
	}
	if err := e.Systems.Local.Edit(w.Subject, changes); err != nil {
		return storeFailure(err)
	}
	return Result{Outcome: ledger.Done, Reread: world.FromLocalTickets}
}

// storeFailure ends a receipt for an error of the local store: Refused for a request that breaks an
// invariant, and Failed for anything else.
func storeFailure(err error) Result {
	if errors.Is(err, localticket.ErrRefused) {
		return Result{Outcome: ledger.Refused, Detail: err.Error()}
	}
	return failed("%v", err)
}

// decodeArgs turns an action's argument, plain values from a rule or from history, into the store's type.
func decodeArgs(in any, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
