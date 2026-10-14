package dispatch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/launcher"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/tooltest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

func TestTickDryRunListsLaunchWithoutActing(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1\n", out)
	assert.Empty(t, h.launches())
	assert.Empty(t, h.linear.mutations())
	assert.NoFileExists(t, filepath.Join(h.state, "cache.json"))
}

func TestTickLaunchesApprovedTicketWithNoWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1: ok\n", out)
	assert.Equal(t, []string{h.repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, h.launches())
	assert.Empty(t, h.linear.mutations())
}

func TestTickLeavesTicketWithLiveWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.launches())
}

func TestTickRelaunchesWhenWorkspaceHasNoClaude(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "")

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.launches(), 1)
}

func TestTickWaitsWhenWorkerCapIsFull(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "dispatch"),
	)
	h.worker("ENG-1", "working")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "wait ENG-2: 1 of 1 workers already running\n", out)
	assert.Empty(t, h.launches())
}

func TestTickLaunchesNothingForTicketsThatAreNotInProgress(t *testing.T) {
	h := newHarness(t)
	h.tickets(
		ticket("ENG-1", "In Review", "dispatch"),
		ticket("ENG-2", "Needs Human", "dispatch"),
	)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "notify ENG-2: entered Needs Human\n", out)
	assert.Empty(t, h.launches())
}

func TestTickEscalatesAfterRepeatedLaunchFailures(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.launchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)

	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.linear.mutations())

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1: launch failed 2 times")
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, lineartest.Move, mutations[0].Kind)
	assert.Equal(t, needsHumanID, mutations[0].StateID)
	assert.Equal(t, lineartest.Comment, mutations[1].Kind)
	assert.Contains(t, mutations[1].Body, "treehouse lease failed")
}

func TestTickNeverEscalatesForAFullPool(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.launchResult(`{"status":"pool_exhausted","detail":"all 4/4 pool trees are taken"}`)

	var out string
	for range 3 {
		var err error
		out, err = h.run("tick")
		require.NoError(t, err)
	}

	assert.Equal(t, "launch ENG-1: pool_exhausted: all 4/4 pool trees are taken\n", out)
	assert.Len(t, h.launches(), 3)
	assert.Empty(t, h.linear.mutations())
}

func TestTickSkipsTicketsItNeverLaunches(t *testing.T) {
	for _, test := range []struct {
		name   string
		ticket fakeTicket
		want   string
	}{
		{
			name:   "ParentTicket",
			ticket: withChildren(ticket("ENG-1", "In Progress", "dispatch")),
			want:   "skip ENG-1: has sub-issues, and a parent never gets a worker\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(test.ticket)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, test.want, out)
			assert.Empty(t, h.launches())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickEscalatesTicketItCannotPlaceInARepo(t *testing.T) {
	for _, test := range []struct {
		name     string
		repo     string
		wantBody string
	}{
		{
			name:     "NoRepoLabel",
			repo:     "",
			wantBody: "has no \"repo\" label",
		},
		{
			name:     "RepoNotConfigured",
			repo:     "tiger",
			wantBody: "its repo \"tiger\" is not in the dispatcher's config",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Progress", test.repo))

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.launches())
			mutations := h.linear.mutations()
			require.Len(t, mutations, 2)
			assert.Equal(t, needsHumanID, mutations[0].StateID)
			assert.Contains(t, mutations[1].Body, test.wantBody)
		})
	}
}

func TestTickLaunchesOtherTicketsWhenAnEscalationFails(t *testing.T) {
	h := newHarness(t)
	h.tickets(
		ticket("ENG-1", "In Progress", ""),
		ticket("ENG-2", "In Progress", "dispatch"),
	)
	h.linear.failMoves()

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-2: ok\n", out)
	assert.Len(t, h.launches(), 1)
	// The failed move left ENG-1 In Progress, so no comment was posted that a
	// later tick would post again.
	assert.Empty(t, h.linear.mutations())
}

func TestTickNeverCommentsWhenLinearRefusesTheMove(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", ""))
	h.linear.unsuccessfulMoves()

	out, stderr, err := h.runContext(context.Background(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Contains(t, stderr, "while escalating ENG-1: linear MoveTicket: Linear reported success=false")
	assert.Empty(t, h.linear.mutations())
}

func TestTickFailsWhenManagedTicketsSpanMoreThanOnePage(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.linear.overflowManaged()

	_, err := h.run("tick")

	require.ErrorContains(t, err, "more than 100 started \"ai\" tickets are assigned to you")
	assert.Empty(t, h.launches())
}

func TestTickHoldsBackOnlyTheRepoWhosePoolCannotBeRead(t *testing.T) {
	h := newHarness(t)
	// A moved clone: treehouse can't run in a directory that doesn't exist.
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "tiger"),
	)

	out, stderr, err := h.runContext(context.Background(), "tick")
	require.NoError(t, err)

	assert.Equal(t, "wait ENG-2: the tiger pool could not be read this tick\nlaunch ENG-1: ok\n", out)
	assert.Contains(t, stderr, "dispatch: while reading the tiger pool:")
	assert.Equal(t, []string{h.repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, h.launches())
	assert.Empty(t, h.linear.mutations())
}

func TestTickCountsTicketsInAnUnreadRepoAgainstTheWorkerCap(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "tiger"),
	)

	out, err := h.run("tick")
	require.NoError(t, err)

	// ENG-2 may have a live worker nobody could see, so ENG-1 must not launch.
	assert.Contains(t, out, "wait ENG-1: 1 of 1 workers already running")
	assert.Empty(t, h.launches())
}

func TestTickPostsAnEscalationCommentThatFailedOnAnEarlierTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", ""))
	h.linear.failComments(true)

	_, stderr, err := h.runContext(context.Background(), "tick")
	require.NoError(t, err)
	assert.Contains(t, stderr, "while escalating ENG-1: moved to Needs Human, but the comment failed")

	// The ticket has since left the started states the dispatcher reads, so
	// the retry can't depend on finding it among managed tickets.
	h.tickets()
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.linear.failComments(false)

	_, err = h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, lineartest.Move, mutations[0].Kind)
	assert.Equal(t, lineartest.Comment, mutations[1].Kind)
	assert.Contains(t, mutations[1].Body, `has no "repo" label`)
	assert.Equal(t, "uuid-ENG-1", mutations[1].TicketID)

	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickEscalatesATicketWhoseLabelsDidNotFitOnePage(t *testing.T) {
	h := newHarness(t)
	// The design label may be among the labels Linear left off the page, so
	// this ticket must not be launched as a build.
	cut := ticket("ENG-1", "In Progress", "dispatch")
	cut.LabelsTruncated = true
	h.tickets(cut)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1: it has more labels than the dispatcher reads")
	assert.Empty(t, h.launches())
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, needsHumanID, mutations[0].StateID)
}

func TestTickExitsWhenAnotherTickHoldsTheLock(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	require.NoError(t, os.MkdirAll(h.state, 0o755))
	held, err := os.OpenFile(filepath.Join(h.state, "tick.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	defer func() { _ = held.Close() }()
	require.NoError(t, syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.launches())
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	h := newHarness(t)

	_, err := h.run("deploy")

	require.ErrorContains(t, err, `unknown command "deploy"`)
}

func TestRunTicksOnEachIntervalUntilCanceled(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var out string

	go func() {
		var err error
		out, _, err = h.runContext(ctx, "run", "--interval", "20ms")
		done <- err
	}()
	// The fake launcher starts no worker herdr could show, so every tick launches ENG-1 again.
	// A 4th launch proves the 3rd tick printed before cancellation could drop it.
	require.Eventually(t, func() bool { return len(h.launches()) >= 4 }, 15*time.Second, 10*time.Millisecond)
	cancel()

	require.NoError(t, <-done)
	assert.Contains(t, out, "ticking every 20ms")
	assert.GreaterOrEqual(t, strings.Count(out, "launch ENG-1: ok"), 3)
	assert.Regexp(t, `(?m)^\d\d:\d\d:\d\d tick: 1 ticket, 1 action, Linear 1 call in `+duration+` \(2400 of 2500 requests left this hour\), took `+duration+`$`, out)
}

func TestRunPrintsASlowTickInSeconds(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tools.Treehouse.DelayTrees(1100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var out string

	go func() {
		var err error
		out, _, err = h.runContext(ctx, "run", "--interval", "20ms")
		done <- err
	}()
	// A second launch proves the slow first tick already printed its line,
	// which cancellation would otherwise drop.
	require.Eventually(t, func() bool { return len(h.launches()) >= 2 }, 15*time.Second, 10*time.Millisecond)
	cancel()

	require.NoError(t, <-done)
	assert.Regexp(t, `(?m)^\d\d:\d\d:\d\d tick: 1 ticket, 1 action, Linear 1 call in `+duration+` \(2400 of 2500 requests left this hour\), took \d+(?:\.\d+)?s$`, out)
}

func TestRunKeepsTickingAfterAFailedTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	// treehouse fails its first call only, so the first tick fails and the
	// second one launches.
	h.tools.Treehouse.FailTrees(errors.New("exit status 1"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var stderr string

	go func() {
		var err error
		_, stderr, err = h.runContext(ctx, "run", "--interval", "20ms")
		done <- err
	}()
	require.Eventually(t, func() bool { return len(h.launches()) >= 1 }, 5*time.Second, 10*time.Millisecond)
	cancel()

	require.NoError(t, <-done)
	assert.Regexp(t, `^\d\d:\d\d:\d\d dispatch: tick failed after `+duration+`: while running treehouse status`, stderr)
}

func TestRunRejectsANonPositiveInterval(t *testing.T) {
	h := newHarness(t)
	h.config("interval_minutes = 0\n")

	_, err := h.run("run")

	require.ErrorContains(t, err, "interval must be positive")
}

// duration matches Go's duration formatting, which prints 0s under a
// millisecond, 12ms under a second, and 3.477s from a second up.
const duration = `(?:\d+h)?(?:\d+m)?\d+(?:\.\d+)?(?:ms|s)`

const (
	needsHumanID = "state-needs-human"
	viewerID     = "viewer-1"
)

// harness runs the dispatcher against a fake Linear server and the in-process
// fakes in tools (herdr, treehouse, pull requests, notifications, launcher),
// all rooted in one temp directory.
type harness struct {
	t             *testing.T
	configuredKey bool
	dir           string
	repo          string
	state         string
	extra         string
	repos         []repoEntry
	linear        *linearHarness
	tools         *tooltest.Tools
	// builtinLauncher leaves Options.Launcher empty, so a tick starts workers
	// with the launcher built into dispatch, over tools.Herdr and tools.Treehouse.
	builtinLauncher bool
	// clock is the time every command sees, and moves only through advance.
	clock time.Time
	// pages is what GitHub was last set to answer, by ticket and cursor.
	pages map[string][]byte
}

func newHarness(t *testing.T) *harness {
	dir := t.TempDir()
	h := &harness{
		t:     t,
		dir:   dir,
		repo:  filepath.Join(dir, "repo"),
		state: filepath.Join(dir, "state"),
		tools: tooltest.New(t),
		clock: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
		pages: map[string][]byte{},
	}
	h.linear = &linearHarness{Workspace: h.tools.Linear}
	h.tools.Linear.Now = func() time.Time { return h.clock }
	require.NoError(t, os.MkdirAll(h.repo, 0o755))
	h.addRepo("dispatch", h.repo)
	return h
}

// useConfiguredKey makes runs find the Linear key the way a user's machine
// does, instead of through Options.LinearKey.
func (h *harness) useConfiguredKey(t *testing.T) {
	h.configuredKey = true
	t.Setenv("LINEAR_API_KEY", "")
}

func (h *harness) config(extra string) {
	h.extra = extra
}

type repoEntry struct{ label, dir, settings string }

// addRepo adds a [repos.<label>] table to the config.
func (h *harness) addRepo(label, dir string) {
	h.repos = append(h.repos, repoEntry{label: label, dir: dir})
}

// resetRepos drops every repo added after the dispatch repo.
func (h *harness) resetRepos() {
	h.repos = h.repos[:1]
}

// repoSettings appends extra TOML lines to the table of a repo, such as
// "auto_merge = true\n". The dispatch repo is always configured.
func (h *harness) repoSettings(label, extra string) {
	for i := range h.repos {
		if h.repos[i].label == label {
			h.repos[i].settings += extra
		}
	}
}

func (h *harness) reposTOML() string {
	var out strings.Builder
	for _, repo := range h.repos {
		fmt.Fprintf(&out, "\n[repos.%s]\npath = %q\n%s", repo.label, repo.dir, repo.settings)
	}
	return out.String()
}

func (h *harness) run(args ...string) (string, error) {
	stdout, _, err := h.runContext(context.Background(), args...)
	return stdout, err
}

func (h *harness) runContext(ctx context.Context, args ...string) (string, string, error) {
	return h.runWith(ctx, dispatch.Options{}, args...)
}

// runWith runs a command with the harness's fakes, over the Options given.
func (h *harness) runWith(ctx context.Context, extra dispatch.Options, args ...string) (string, string, error) {
	h.writeFile("config.toml", h.extra+h.reposTOML())
	var stdout, stderr bytes.Buffer
	extra.ConfigPath = filepath.Join(h.dir, "config.toml")
	extra.StateDir = h.state
	extra.Now = func() time.Time { return h.clock }
	extra.Linear = h.linear.Connect
	if !h.configuredKey {
		extra.LinearKey = lineartest.Key
	}
	extra.Herdr = h.tools.Herdr
	extra.Treehouse = h.tools.Treehouse
	extra.PullRequests = h.tools.PullRequests
	extra.Alerts = h.tools.Alerts
	if !h.builtinLauncher {
		extra.Launcher = h.tools.Launcher
	}
	extra.LaunchPace = time.Millisecond
	extra.Stdout = &stdout
	extra.Stderr = &stderr
	err := dispatch.Run(ctx, args, extra)
	return stdout.String(), stderr.String(), err
}

func (h *harness) tick() string {
	h.t.Helper()
	out, err := h.run("tick")
	require.NoError(h.t, err)
	return out
}

// advance moves the clock forward.
func (h *harness) advance(d time.Duration) {
	h.clock = h.clock.Add(d)
}

func (h *harness) tickets(tickets ...fakeTicket) {
	h.linear.setTickets(tickets)
}

// worker gives a ticket a leased tree and a herdr workspace on it. An empty
// status means the workspace has no Claude agent.
func (h *harness) worker(ticket, status string) {
	h.workerOn(ticket, filepath.Join(h.dir, "trees", ticket), "w-"+ticket, status)
}

// workerOn is worker for a tree at a chosen path and workspace id.
func (h *harness) workerOn(ticket, tree, id, status string) {
	h.tools.Treehouse.Seed(h.repo, treehouse.Tree{Path: tree, Status: "leased", LeaseHolder: ticket})

	h.tools.Herdr.AddWorkspace(herdr.Workspace{ID: id, Label: ticket, CheckoutPath: tree})
	if status == "" {
		return
	}
	h.tools.Herdr.AddTab(herdr.Tab{ID: id + ":t1", Label: ticket, WorkspaceID: id})
	h.tools.Herdr.AddPane(herdr.Pane{ID: id + ":p1", TabID: id + ":t1", WorkspaceID: id})
	h.tools.Herdr.AddAgent(herdr.Agent{Kind: "claude", Status: status, Session: "worker", PaneID: id + ":p1", TabID: id + ":t1", WorkspaceID: id})
}

// setWorkerStatus changes the herdr status of a worker added with worker.
func (h *harness) setWorkerStatus(ticket, status string) {
	h.tools.Herdr.SetStatus("w-"+ticket+":p1", status)
}

// setWorkerSession changes the Claude session id herdr reports for a worker
// added with worker, as a relaunch does.
func (h *harness) setWorkerSession(ticket, session string) {
	h.tools.Herdr.SetSession("w-"+ticket+":p1", session)
}

// blockAfterSnapshot has the worker stop to ask a question after dispatch's
// snapshot, so herdr refuses a prompt the snapshot says is fine.
func (h *harness) blockAfterSnapshot(ticket string) {
	h.tools.Herdr.BlockAfterSnapshot("w-" + ticket + ":p1")
}

// unblock undoes blockAfterSnapshot.
func (h *harness) unblock(ticket string) {
	h.tools.Herdr.Unblock("w-" + ticket + ":p1")
}

// prompts lists what was typed into workers, as "<pane>|<text>".
func (h *harness) prompts() []string {
	return h.tools.Herdr.Prompted()
}

// notifications lists the macOS notification texts shown.
func (h *harness) notifications() []string {
	return h.tools.Alerts.Shown()
}

func (h *harness) lines(name string) []string {
	b, err := os.ReadFile(filepath.Join(h.dir, name))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(h.t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// labelWorkspace sets the label herdr reports for a workspace.
func (h *harness) labelWorkspace(id, label string) {
	h.tools.Herdr.SetWorkspaceLabel(id, label)
}

// renames lists the workspace renames, as "<workspace>|<label>".
func (h *harness) renames() []string {
	return h.tools.Herdr.WorkspaceRenames()
}

// tabRenames lists the tab renames, as "<tab>|<label>".
func (h *harness) tabRenames() []string {
	return h.tools.Herdr.TabRenames()
}

// labelTab sets the label herdr reports for a worker's tab.
func (h *harness) labelTab(ticket, label string) {
	h.tools.Herdr.SetTabLabel("w-"+ticket+":t1", label)
}

// closes lists the herdr workspaces that were closed.
func (h *harness) closes() []string {
	return h.tools.Herdr.ClosedWorkspaces()
}

// returns lists the treehouse return calls, as "<repo>|<tree>|<holder>".
func (h *harness) returns() []string {
	return h.tools.Calls("treehouse", "return")
}

// pool makes the harness's repo a git repo that treehouse pools, with one tree
// in it, and returns the tree's path.
func (h *harness) pool() string {
	tree := filepath.Join(h.dir, "tree")
	require.NoError(h.t, os.WriteFile(filepath.Join(h.repo, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		{"worktree", "add", "-q", "--detach", tree},
	} {
		out, err := exec.Command("git", append([]string{"-C", h.repo}, args...)...).CombinedOutput()
		require.NoError(h.t, err, string(out))
	}
	h.tools.Treehouse.AddTree(h.repo, tree)
	return tree
}

func (h *harness) launchResult(result string) {
	var r launcher.Result
	require.NoError(h.t, json.Unmarshal([]byte(result), &r))
	h.tools.Launcher.Answer(r)
}

// launches lists the launch requests the dispatcher made, as
// "<repo> <ticket> <branch> --model <model>" plus " --kickoff <text>" when one was set.
func (h *harness) launches() []string {
	var found []string
	for _, r := range h.tools.Launcher.Launched() {
		line := fmt.Sprintf("%s %s %s --model %s", r.RepoDir, r.Ticket, r.Branch, r.Model)
		if r.Kickoff != "" {
			line += " --kickoff " + r.Kickoff
		}
		found = append(found, line+"")
	}
	return found
}

func (h *harness) removeFile(name string) {
	require.NoError(h.t, os.Remove(filepath.Join(h.dir, name)))
}

func (h *harness) writeFile(name, content string) {
	require.NoError(h.t, os.WriteFile(filepath.Join(h.dir, name), []byte(content), 0o644))
}

func (h *harness) writeJSON(name string, v any) {
	b, err := json.Marshal(v)
	require.NoError(h.t, err)
	h.writeFile(name, string(b))
}

func (h *harness) readFile(name string) []byte {
	b, err := os.ReadFile(filepath.Join(h.dir, name))
	require.NoError(h.t, err)
	return b
}

// fakeTicket is one ticket in the Linear workspace.
type fakeTicket = lineartest.Issue

// ticket builds an ai-labeled ENG ticket in status, labeled with repo when
// repo is not empty.
func ticket(id, status, repo string) fakeTicket {
	labels := []linear.Label{{Name: "ai"}}
	if repo != "" {
		labels = append(labels, linear.Label{Name: repo, Group: "repo"})
	}
	return fakeTicket{
		Ticket: linear.Ticket{
			ID:         "uuid-" + id,
			Identifier: id,
			Title:      "ticket " + id,
			URL:        "https://linear.app/team/issue/" + id,
			Branch:     "thrawn01/" + strings.ToLower(id),
			Status:     status,
			Team:       "ENG",
			Labels:     labels,
		},
		Assignee: viewerID,
	}
}

func withChildren(t fakeTicket) fakeTicket {
	t.HasChildren = true
	return t
}

func withLabel(t fakeTicket, name string) fakeTicket {
	t.Labels = append(slices.Clone(t.Labels), linear.Label{Name: name})
	return t
}

// linearHarness seeds the Linear workspace from three groups of tickets, so a
// test can replace one group without restating the others.
type linearHarness struct {
	*lineartest.Workspace
	managed, elsewhere, children []fakeTicket
}

func (l *linearHarness) seed() {
	var all []fakeTicket
	seen := map[string]bool{}
	for _, group := range [][]fakeTicket{l.managed, l.elsewhere, l.children} {
		for _, t := range group {
			if !seen[t.Identifier] {
				seen[t.Identifier] = true
				all = append(all, t)
			}
		}
	}
	l.Seed(all...)
}

// setTickets sets the tickets that are started and assigned to the key's owner.
func (l *linearHarness) setTickets(tickets []fakeTicket) {
	l.managed = tickets
	l.seed()
}

// setElsewhere sets the tickets Linear knows that are not started, such as a
// Done or Todo ticket, which only a lookup by identifier finds.
func (l *linearHarness) setElsewhere(tickets ...fakeTicket) {
	l.elsewhere = tickets
	l.seed()
}

// setChildren sets the children Linear returns for open parents.
func (l *linearHarness) setChildren(tickets ...fakeTicket) {
	l.children = tickets
	l.seed()
}

// mutations lists the moves and comments Linear accepted.
func (l *linearHarness) mutations() []lineartest.Change { return l.Changes() }

// calls counts the requests Linear answered.
func (l *linearHarness) calls() int { return l.Calls() }

// lookups counts the lookups of tickets by identifier.
func (l *linearHarness) lookups() int { return l.CallsTo(lineartest.OnLeased) }

// childReadParents lists the parent ids each children read asked for.
func (l *linearHarness) childReadParents() [][]string { return l.Reads() }

func (l *linearHarness) failMoves()           { l.FailOn(lineartest.OnMove, true) }
func (l *linearHarness) unsuccessfulMoves()   { l.FailOn(lineartest.OnUnsuccessfulMove, true) }
func (l *linearHarness) failComments(on bool) { l.FailOn(lineartest.OnComment, on) }
func (l *linearHarness) failLookups()         { l.FailOn(lineartest.OnLeased, true) }
func (l *linearHarness) allowLookups()        { l.FailOn(lineartest.OnLeased, false) }
func (l *linearHarness) failChildren()        { l.FailOn(lineartest.OnChildren, true) }

// overflowManaged adds enough started tickets to push a read of managed
// tickets onto a second page.
func (l *linearHarness) overflowManaged() {
	for n := range linear.PageSize {
		l.managed = append(l.managed, ticket(fmt.Sprintf("ENG-%d", 1000+n), "In Progress", "dispatch"))
	}
	l.seed()
}
