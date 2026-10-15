// Package executor carries out the actions a cycle wants. Each action checks the
// source right before it acts, so a decision made on a stale read fails instead
// of landing, and ends as a receipt outcome the ledger keeps. It never decides
// anything: whether an action runs at all is the cycle's and the loop's call.
package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/delivery"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/launcher"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/worker"
	"github.com/kapetan-io/dispatch/internal/world"
)

// Deadlines. A launch has to end before the loop's own 30 second patience, and a
// launch that lands after its deadline shows up as a worker, which makes the
// launch rule false.
const (
	LaunchDeadline  = 25 * time.Second
	DefaultDeadline = time.Minute
)

// Env is the tools an action drives.
type Env struct {
	Config       config.Config
	Linear       func() linear.Workspace
	Herdr        herdr.Herdr
	Treehouse    treehouse.Pool
	PullRequests pullrequest.Repository
	Launcher     launcher.Launcher
	Alerts       delivery.Alerts
	StateDir     string
	Stderr       io.Writer
	// LaunchDeadline and Deadline override the defaults, so a test can see a launch time out.
	LaunchDeadline time.Duration
	Deadline       time.Duration
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
	// Nudged is the ticket whose worker was typed into.
	Nudged string
	// Status is the status a Linear write moved a ticket to.
	Status string
}

// Executor runs actions.
type Executor struct{ Env }

// New returns an executor over env.
func New(env Env) *Executor { return &Executor{Env: env} }

// Guard says why a launch must not start whatever the rule asked, or "" when it
// may. A worker that is present or unknown means a second one could start beside it.
func (e *Executor) Guard(w rules.Want, wd world.World) string {
	if w.Kind != "herdr.launch" {
		return ""
	}
	if wd.HerdrErr != nil {
		return "herdr was not read"
	}
	if wk := worker.Find(w.Subject, wd.Trees, wd.Snapshot); wk.State != worker.None {
		return "a worker is running"
	}
	if t, ok := ticketOf(wd, w.Subject); ok {
		repo, _ := t.LabelIn(e.Config.Plugins.Linear.RepoLabelGroup)
		if wd.UnreadRepos[repo] {
			return "the pool of " + repo + " was not read"
		}
	}
	return ""
}

// ticketOf finds a ticket by identifier in everything the world read.
func ticketOf(wd world.World, id string) (linear.Ticket, bool) {
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
	return linear.Ticket{}, false
}

func (e *Executor) deadline(kind string) time.Duration {
	if kind == "herdr.launch" {
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
	ctx, cancel := context.WithTimeout(ctx, e.deadline(w.Kind))
	defer cancel()
	var res Result
	switch w.Kind {
	case "herdr.launch":
		res = e.launch(ctx, w, wd)
	case "herdr.nudge":
		res = e.nudge(ctx, w, wd)
	case "herdr.close":
		res = e.closeWorkspace(ctx, w)
	case "linear.move":
		res = e.move(ctx, w, wd)
	case "linear.comment":
		res = e.comment(ctx, w, wd)
	case "linear.escalate":
		res = e.escalate(ctx, w, wd)
	case "treehouse.return":
		res = e.returnTree(ctx, w, wd)
	case "github.merge":
		res = e.merge(ctx, w, wd)
	case "notify.send":
		res = e.notify(ctx, w, wd)
	default:
		res = Result{Outcome: ledger.Failed, Detail: "no executor for " + w.Kind}
	}
	// A message that timed out may have landed, and a retry could say it twice.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && res.Outcome == ledger.Failed && isMessage(w.Kind) {
		res.Outcome, res.Detail = ledger.Done, "timeout"
	}
	return res
}

func isMessage(kind string) bool {
	return slices.Contains([]string{"herdr.nudge", "notify.send", "linear.comment"}, kind)
}

func failed(format string, args ...any) Result {
	return Result{Outcome: ledger.Failed, Detail: fmt.Sprintf(format, args...)}
}

func (e *Executor) launch(ctx context.Context, w rules.Want, wd world.World) Result {
	t, ok := ticketOf(wd, w.Subject)
	if !ok {
		return failed("%s was not read", w.Subject)
	}
	cfg := e.Config
	repo, _ := t.LabelIn(cfg.Plugins.Linear.RepoLabelGroup)
	model, _ := w.Options["model"].(string)
	if model == "" {
		model = cfg.WorkerModel()
	}
	kickoff, _ := w.Options["kickoff"].(string)
	r := e.Launcher.Launch(ctx, launcher.Request{
		RepoDir: cfg.Repos[repo].Path, Ticket: t.Identifier, Title: t.Title, Branch: t.Branch, Model: model, Kickoff: kickoff,
	})
	res := Result{Reread: world.FromLocal}
	switch {
	case r.Status == launcher.StatusOK:
		res.Outcome, res.Detail = ledger.Done, "ok"
		if r.Workspace != "" {
			res.Detail = "ok: workspace " + r.Workspace
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

func (e *Executor) nudge(ctx context.Context, w rules.Want, wd world.World) Result {
	wk := worker.Find(w.Subject, wd.Trees, wd.Snapshot)
	if wk.State == worker.None {
		return failed("%s has no worker", w.Subject)
	}
	text, _ := w.Args["text"].(string)
	err := delivery.Post{Herdr: e.Herdr}.Nudge(ctx, wk, text)
	switch {
	case errors.Is(err, delivery.ErrBlocked), errors.Is(err, herdr.ErrAgentBlocked):
		return failed("blocked")
	case err != nil:
		return failed("%v", err)
	}
	return Result{Outcome: ledger.Done, Nudged: w.Subject}
}

func (e *Executor) closeWorkspace(ctx context.Context, w rules.Want) Result {
	id := w.Target["workspace"]
	if id == "" {
		return Result{Outcome: ledger.Done, Detail: "no workspace"}
	}
	if err := e.Herdr.CloseWorkspace(ctx, id); err != nil {
		return failed("while closing herdr workspace %s: %v", id, err)
	}
	return Result{Outcome: ledger.Done, Reread: world.FromLocal}
}

// fresh reads one ticket again from Linear, whatever its status, so a decision
// made on an old read fails instead of landing.
func (e *Executor) fresh(ctx context.Context, id string) (linear.Ticket, error) {
	cfg := e.Config.Plugins.Linear
	got, err := e.Linear().LeasedTickets(ctx, cfg.AILabel, cfg.Teams, []string{id})
	if err != nil {
		return linear.Ticket{}, err
	}
	for _, t := range got {
		if t.Identifier == id {
			return t, nil
		}
	}
	return linear.Ticket{}, fmt.Errorf("%s was not found in Linear", id)
}

func (e *Executor) move(ctx context.Context, w rules.Want, wd world.World) Result {
	t, ok := ticketOf(wd, w.Subject)
	if !ok {
		return failed("%s was not read", w.Subject)
	}
	key, _ := w.Args["status"].(string)
	name, ok := rules.StatusName(key, e.Config.Plugins.Linear.Statuses)
	if !ok {
		return failed("%q is not a status key", key)
	}
	if err := e.moveTo(ctx, t, name); err != nil {
		return failed("%v", err)
	}
	why, _ := w.Args["why"].(string)
	return Result{Outcome: ledger.Done, Detail: name + ": " + why, Reread: world.FromLinear, Status: name}
}

// moveTo sets a ticket's status when it still has the status the cycle saw.
func (e *Executor) moveTo(ctx context.Context, t linear.Ticket, name string) error {
	now, err := e.fresh(ctx, t.Identifier)
	if err != nil {
		return err
	}
	if now.Status != t.Status {
		return fmt.Errorf("%s is %s now, not the %s the cycle saw", t.Identifier, now.Status, t.Status)
	}
	lin := e.Linear()
	state, err := lin.StateID(ctx, t.Team, name)
	if err != nil {
		return err
	}
	return lin.Move(ctx, t.ID, state)
}

func (e *Executor) comment(ctx context.Context, w rules.Want, wd world.World) Result {
	text, _ := w.Args["text"].(string)
	id := e.linearID(w.Subject, wd)
	if id == "" {
		id = w.Target["linearID"]
	}
	if id == "" {
		return failed("%s was not read", w.Subject)
	}
	if err := e.Linear().Comment(ctx, id, text); err != nil {
		return failed("while commenting on %s: %v", w.Subject, err)
	}
	return Result{Outcome: ledger.Done}
}

// linearID is Linear's id for a ticket identifier, from anything the world read.
func (e *Executor) linearID(identifier string, wd world.World) string {
	if t, ok := ticketOf(wd, identifier); ok {
		return t.ID
	}
	return ""
}

// escalate moves a ticket to Needs Human, then comments why. Move first: a move
// that fails leaves the ticket where it was, and a comment sent first would be
// posted again by every retry. The receipt keeps the two steps apart so a lost
// comment can be finished later.
func (e *Executor) escalate(ctx context.Context, w rules.Want, wd world.World) Result {
	t, ok := ticketOf(wd, w.Subject)
	if !ok {
		return failed("%s was not read", w.Subject)
	}
	why, _ := w.Args["why"].(string)
	res := Result{Detail: why, TicketID: t.ID, Steps: []ledger.Step{{Name: "move"}, {Name: "comment"}}}
	if err := e.moveTo(ctx, t, e.Config.Plugins.Linear.Statuses.NeedsHuman); err != nil {
		res.Outcome, res.Detail = ledger.Failed, fmt.Sprintf("%v", err)
		res.Steps[0].Outcome = ledger.Failed
		return res
	}
	res.Steps[0].Outcome, res.Reread, res.Status = ledger.Done, world.FromLinear, e.Config.Plugins.Linear.Statuses.NeedsHuman
	res.Outcome = ledger.Done
	body := "The dispatcher moved this ticket to Needs Human because " + why
	if err := e.Linear().Comment(ctx, t.ID, body); err != nil {
		res.Steps[1].Outcome = ledger.Failed
		fmt.Fprintf(e.Stderr, "dispatch: while commenting on %s: %v\n", t.Identifier, err)
		return res
	}
	res.Steps[1].Outcome = ledger.Done
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
			return failed("while closing herdr workspace %s: %v", id, err)
		}
	}
	why, err := treehouse.Unfinished(ctx, path)
	if err != nil {
		return failed("%v", err)
	}
	if why != "" {
		res.Outcome, res.Detail = ledger.Refused, "kept: the worker left "+why+" in "+path
		return res
	}
	files, err := treehouse.Untracked(ctx, path)
	if err != nil {
		return failed("%v", err)
	}
	detail := "returned " + path
	if len(files) > 0 {
		if err := treehouse.RemoveUntracked(ctx, path); err != nil {
			return failed("%v", err)
		}
		left, err := treehouse.Untracked(ctx, path)
		if err != nil {
			return failed("%v", err)
		}
		// git clean skips a nested repository, and returning the tree would discard it.
		if len(left) > 0 {
			res.Outcome, res.Detail = ledger.Refused, "kept: untracked "+nameList(left)+" in "+path+" survived git clean"
			return res
		}
		detail += ", removed untracked " + nameList(files)
	}
	if err := e.Treehouse.Return(ctx, repoDir, path, holder); err != nil {
		return failed("while returning %s to the pool: %v", path, err)
	}
	stops.Remove(e.StateDir, path)
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
	head, _ := w.Once[len(w.Once)-1].(string)
	if len(w.Once) > 1 {
		head, _ = w.Once[1].(string)
	}
	url := w.Target["url"]
	t, ok := ticketOf(wd, w.Subject)
	if !ok {
		return failed("%s was not read", w.Subject)
	}
	cfg := e.Config.Plugins.Linear
	tickets, err := e.Linear().ManagedTickets(ctx, cfg.AILabel, cfg.Teams)
	if err != nil {
		return failed("%v", err)
	}
	for _, now := range tickets {
		if now.Identifier == t.Identifier && (now.LastComment != t.LastComment || now.Status != t.Status) {
			res := failed("%s changed in Linear since the cycle, so the merge waits for a new decision", t.Identifier)
			res.Reread = world.FromLinear
			return res
		}
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
	post := delivery.Post{Herdr: e.Herdr, Alerts: e.Alerts, StateDir: e.StateDir, Stderr: e.Stderr}
	ev := delivery.Event{Ticket: t, Reason: Reason(w), Text: text}
	if always, _ := w.Options["followersAlways"].(bool); always {
		// Nothing retries a merge notice, so followers hear it whether or not the alert showed.
		res := Result{Outcome: ledger.Done}
		if err := post.NotifyOnce(ctx, ev); err != nil {
			res.Detail = "alert failed: " + err.Error()
		}
		return res
	}
	if err := post.Notify(ctx, ev); err != nil {
		return failed("%v", err)
	}
	return Result{Outcome: ledger.Done}
}

// Reason names why a notice went out, the way followers read it.
func Reason(w rules.Want) string {
	topic := ""
	if len(w.Once) > 0 {
		topic, _ = w.Once[0].(string)
	}
	switch topic {
	case "needs-human":
		return "entered Needs Human"
	case "blocked":
		return "blocked"
	case "ready":
		return "ready for review"
	case "draft-ready":
		return "draft ready"
	case "stall-count":
		return "stalled"
	case "unresponsive":
		return "unresponsive"
	case "merged", "merge-failed":
		return "merge"
	}
	return topic
}
