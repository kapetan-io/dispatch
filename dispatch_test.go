package dispatch_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdr"
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
	assert.Equal(t, []string{h.repo + " ENG-1 thrawn01/eng-1 --model sonnet --no-focus"}, h.launches())
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
	assert.Equal(t, "MoveTicket", mutations[0].operation)
	assert.Equal(t, needsHumanID, mutations[0].variables["state"])
	assert.Equal(t, "CommentTicket", mutations[1].operation)
	assert.Contains(t, mutations[1].variables["body"], "treehouse lease failed")
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
			assert.Equal(t, needsHumanID, mutations[0].variables["state"])
			assert.Contains(t, mutations[1].variables["body"], test.wantBody)
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
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "MoveTicket", mutations[0].operation)
}

func TestTickFailsWhenManagedTicketsSpanMoreThanOnePage(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.linear.hasMorePages()

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
	assert.Equal(t, []string{h.repo + " ENG-1 thrawn01/eng-1 --model sonnet --no-focus"}, h.launches())
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
	h.linear.failComments(false)

	_, err = h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, "MoveTicket", mutations[0].operation)
	assert.Equal(t, "CommentTicket", mutations[1].operation)
	assert.Contains(t, mutations[1].variables["body"], `has no "repo" label`)
	assert.Equal(t, "uuid-ENG-1", mutations[1].variables["id"])

	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickEscalatesATicketWhoseLabelsDidNotFitOnePage(t *testing.T) {
	h := newHarness(t)
	// The design label may be among the labels Linear left off the page, so
	// this ticket must not be launched as a build.
	cut := ticket("ENG-1", "In Progress", "dispatch")
	cut["labels"].(map[string]any)["pageInfo"] = map[string]bool{"hasNextPage": true}
	h.tickets(cut)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1: it has more labels than the dispatcher reads")
	assert.Empty(t, h.launches())
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, needsHumanID, mutations[0].variables["state"])
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
	// The fake herdr never shows a worker, so every tick launches ENG-1 again.
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

// harness runs the dispatcher against a fake Linear server, the in-process
// fakes in tools (herdr, treehouse, notifications) and gh and launcher scripts,
// all rooted in one temp directory.
type harness struct {
	t      *testing.T
	dir    string
	repo   string
	state  string
	extra  string
	repos  string
	linear *fakeLinear
	tools  *tooltest.Tools
	// builtinLauncher leaves Options.Launcher empty, so a tick starts workers
	// with the launcher built into dispatch. That launcher still runs the herdr
	// script from fakeHerdr, so it shares no state with tools.Herdr.
	builtinLauncher bool
	// clock is the time every command sees, and moves only through advance.
	clock time.Time
}

func newHarness(t *testing.T) *harness {
	dir := t.TempDir()
	h := &harness{
		t:      t,
		dir:    dir,
		repo:   filepath.Join(dir, "repo"),
		state:  filepath.Join(dir, "state"),
		linear: newFakeLinear(t),
		tools:  tooltest.New(t),
		clock:  time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
	}
	require.NoError(t, os.MkdirAll(h.repo, 0o755))
	h.writeFile("launch.json", `{"status":"ok"}`)
	// gh's branch= argument names the PR file to print, and each call is logged.
	h.script("gh", `[ -f "`+dir+`/gh-fails" ] && exit 1
case "$1 $2" in
"repo view")
  echo "repo-view" >> "`+dir+`/repo-views.log"
  cat "`+dir+`/repo-name" 2>/dev/null || echo kapetan-io/dispatch
  exit 0 ;;
"pr merge")
  [ -f "`+dir+`/merge-fails" ] && { echo "Pull request is not mergeable: the base branch policy prohibits the merge" >&2; exit 1; }
  echo "$*" >> "`+dir+`/merges.log"
  rm -f "`+dir+`"/pr-*.json
  exit 0 ;;
esac
for arg; do case "$arg" in branch=*) branch="${arg#branch=}" ;; after=*) after=".${arg#after=}" ;; esac; done
echo "$branch$after" >> "`+dir+`/gh.log"
cat "`+dir+`/pr-${branch##*/}$after.json" 2>/dev/null || echo '{"data":{"repository":{"pullRequests":{"nodes":[]}}}}'`)
	h.script("launcher", `echo "$PWD $*" >> "`+dir+`/launches.log"
cat "`+dir+`/launch.json"`)
	return h
}

func (h *harness) config(extra string) {
	h.extra = extra
}

// addRepo adds a repo to the config's [repos] table.
func (h *harness) addRepo(label, dir string) {
	h.repos += fmt.Sprintf("%s = %q\n", label, dir)
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
	h.writeFile("config.toml", h.extra+fmt.Sprintf("\n[repos]\ndispatch = %q\n", h.repo)+h.repos)
	var stdout, stderr bytes.Buffer
	extra.ConfigPath = filepath.Join(h.dir, "config.toml")
	extra.StateDir = h.state
	extra.Now = func() time.Time { return h.clock }
	extra.LinearURL = h.linear.server.URL
	extra.LinearKey = "test-key"
	extra.Herdr = h.tools.Herdr
	extra.HerdrCommand = filepath.Join(h.dir, "herdr")
	extra.Treehouse = h.tools.Treehouse
	extra.GH = filepath.Join(h.dir, "gh")
	extra.Alerts = h.tools.Alerts
	if !h.builtinLauncher {
		extra.Launcher = filepath.Join(h.dir, "launcher")
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

func (h *harness) launchResult(result string) {
	h.writeFile("launch.json", result)
}

func (h *harness) launches() []string {
	b, err := os.ReadFile(filepath.Join(h.dir, "launches.log"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(h.t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (h *harness) script(name, body string) {
	writeFake(h.t, filepath.Join(h.dir, name), h.dir, body)
}

// standInTreehouse is the name a test symlinks this test binary under, so a
// subprocess that runs `treehouse` gets runStandInTreehouse instead of the
// installed tool or its real pool.
const standInTreehouse = "treehouse"

// standInTreeEnv names the variable holding the path the stand-in leases.
const standInTreeEnv = "DISPATCH_STAND_IN_TREE"

// runStandInTreehouse answers the commands a launch makes from a subprocess,
// which the in-process fake can't reach. It tracks no leases, so only the
// wrapper test uses it, and the drift check covers the wrapper against the
// real tools.
func runStandInTreehouse(args []string, stdout io.Writer) int {
	switch {
	case len(args) > 0 && args[0] == "status":
		_, _ = fmt.Fprintln(stdout, "[]")
	case len(args) > 0 && args[0] == "get":
		_, _ = fmt.Fprintf(stdout, "{\"path\":%q}\n", os.Getenv(standInTreeEnv))
	case len(args) > 0 && args[0] == "return":
	default:
		return 2
	}
	return 0
}

// fakeDirEnv names the variable that tells a shared fake which test's files to use.
const fakeDirEnv = "DISPATCH_TEST_DIR"

// sharedFakes holds one copy of each distinct fake script for the whole run.
var sharedFakes string

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == standInTreehouse {
		os.Exit(runStandInTreehouse(os.Args[1:], os.Stdout))
	}
	var err error
	if sharedFakes, err = os.MkdirTemp("", "dispatch-fakes"); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(sharedFakes)
	os.Exit(code)
}

// writeFake installs a fake command at dest. macOS pays about a second for the
// first run of every new executable file, so identical scripts are shared across
// tests and find the test's own files through fakeDirEnv instead of a baked-in path.
func writeFake(t *testing.T, dest, dir, body string) {
	t.Helper()
	body = "#!/usr/bin/env bash\n" + strings.ReplaceAll(body, dir, "$"+fakeDirEnv) + "\n"
	sum := sha256.Sum256([]byte(body))
	shared := filepath.Join(sharedFakes, hex.EncodeToString(sum[:8]))
	if _, err := os.Stat(shared); err != nil {
		tmp := shared + ".tmp"
		require.NoError(t, os.WriteFile(tmp, []byte(body), 0o755))
		require.NoError(t, os.Rename(tmp, shared))
	}
	_ = os.Remove(dest)
	require.NoError(t, os.Symlink(shared, dest))
	t.Setenv(fakeDirEnv, dir)
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

// fakeTicket is one node of Linear's assignedIssues response.
type fakeTicket map[string]any

// ticket builds an ai-labeled ENG ticket in status, labeled with repo when
// repo is not empty.
func ticket(id, status, repo string) fakeTicket {
	labels := []map[string]any{{"name": "ai", "parent": nil}}
	if repo != "" {
		labels = append(labels, map[string]any{"name": repo, "parent": map[string]string{"name": "repo"}})
	}
	return fakeTicket{
		"id":         "uuid-" + id,
		"identifier": id,
		"title":      "ticket " + id,
		"url":        "https://linear.app/team/issue/" + id,
		"branchName": "thrawn01/" + strings.ToLower(id),
		"state":      map[string]string{"name": status},
		"assignee":   map[string]string{"id": viewerID},
		"team":       map[string]string{"key": "ENG"},
		"labels":     map[string]any{"nodes": labels},
		"children":   map[string]any{"nodes": []any{}},
	}
}

func withChildren(t fakeTicket) fakeTicket {
	t["children"] = map[string]any{"nodes": []any{map[string]string{"id": "child"}}}
	return t
}

func withLabel(t fakeTicket, name string) fakeTicket {
	labels := t["labels"].(map[string]any)["nodes"].([]map[string]any)
	t["labels"] = map[string]any{"nodes": append(labels, map[string]any{"name": name, "parent": nil})}
	return t
}

type mutation struct {
	operation string
	variables map[string]any
}

// fakeLinear answers the dispatcher's GraphQL operations by name and records
// every mutation.
type fakeLinear struct {
	server            *httptest.Server
	mu                sync.Mutex
	nodes             []fakeTicket
	elsewhere         []fakeTicket
	changes           []mutation
	noMoves           bool
	movesUnsuccessful bool
	noComments        bool
	morePages         bool
	lookupCalls       int
	noLookups         bool
	children          []fakeTicket
	childReads        [][]any
	noChildren        bool
}

// setChildren sets the children Linear returns for open parents.
func (f *fakeLinear) setChildren(children ...fakeTicket) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.children = children
}

// failChildren makes Linear refuse the read of a parent's children.
func (f *fakeLinear) failChildren() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noChildren = true
}

// childReadParents lists the parent ids each children read asked for.
func (f *fakeLinear) childReadParents() [][]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]any(nil), f.childReads...)
}

// failLookups makes Linear refuse the lookup of tickets by identifier.
func (f *fakeLinear) failLookups() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noLookups = true
}

// allowLookups undoes failLookups.
func (f *fakeLinear) allowLookups() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noLookups = false
}

// unsuccessfulMoves makes Linear answer a move with HTTP 200 and success:false.
func (f *fakeLinear) unsuccessfulMoves() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.movesUnsuccessful = true
}

func (f *fakeLinear) failComments(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noComments = on
}

func (f *fakeLinear) hasMorePages() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.morePages = true
}

func (f *fakeLinear) failMoves() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noMoves = true
}

func newFakeLinear(t *testing.T) *fakeLinear {
	f := &fakeLinear{}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeLinear) setTickets(nodes []fakeTicket) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes = nodes
}

// setElsewhere sets the tickets Linear knows that are not started, such as a
// Done or Todo ticket, which only a lookup by identifier finds.
func (f *fakeLinear) setElsewhere(nodes ...fakeTicket) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.elsewhere = nodes
}

func (f *fakeLinear) lookups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookupCalls
}

func (f *fakeLinear) mutations() []mutation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mutation(nil), f.changes...)
}

func (f *fakeLinear) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	w.Header().Set("X-RateLimit-Requests-Limit", "2500")
	w.Header().Set("X-RateLimit-Requests-Remaining", "2400")
	if r.Header.Get("Authorization") != "test-key" {
		http.Error(w, "bad key", http.StatusUnauthorized)
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var data any
	switch req.OperationName {
	case "ManagedTickets":
		data = map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{
			"nodes":    f.nodes,
			"pageInfo": map[string]bool{"hasNextPage": f.morePages},
		}}}
	case "LeasedTickets":
		f.lookupCalls++
		if f.noLookups {
			http.Error(w, "lookup refused", http.StatusInternalServerError)
			return
		}
		// Answer like Linear: an "or" element that is not a lone "and" of
		// two filters is ignored and matches every ticket.
		var asked []string
		ignored := false
		filter, _ := req.Variables["filter"].(map[string]any)
		for _, c := range filter["or"].([]any) {
			c := c.(map[string]any)
			clauses, ok := c["and"].([]any)
			if len(c) != 1 || !ok || len(clauses) != 2 {
				ignored = true
				continue
			}
			key := clauses[0].(map[string]any)["team"].(map[string]any)["key"].(map[string]any)["eq"].(string)
			asked = append(asked, fmt.Sprintf("%s-%v", key, clauses[1].(map[string]any)["number"].(map[string]any)["eq"]))
		}
		found := []fakeTicket{}
		for _, n := range f.elsewhere {
			if ignored || slices.Contains(asked, n["identifier"].(string)) {
				found = append(found, n)
			}
		}
		data = map[string]any{
			"viewer": map[string]string{"id": viewerID},
			"issues": map[string]any{"nodes": found, "pageInfo": map[string]bool{"hasNextPage": ignored}},
		}
	case "ParentChildren":
		if f.noChildren {
			http.Error(w, "children refused", http.StatusInternalServerError)
			return
		}
		asked, _ := req.Variables["parents"].([]any)
		f.childReads = append(f.childReads, asked)
		data = map[string]any{
			"viewer": map[string]string{"id": viewerID},
			"issues": map[string]any{"nodes": f.children, "pageInfo": map[string]bool{"hasNextPage": false}},
		}
	case "TeamStates":
		data = map[string]any{"teams": map[string]any{"nodes": []any{map[string]any{
			"key": "ENG",
			"states": map[string]any{"nodes": []any{
				map[string]string{"id": "state-todo", "name": "Todo"},
				map[string]string{"id": "state-in-progress", "name": "In Progress"},
				map[string]string{"id": "state-done", "name": "Done"},
				map[string]string{"id": needsHumanID, "name": "Needs Human"},
			}},
		}}}}
	case "MoveTicket":
		if f.noMoves {
			http.Error(w, "move refused", http.StatusInternalServerError)
			return
		}
		f.changes = append(f.changes, mutation{operation: req.OperationName, variables: req.Variables})
		data = map[string]any{"issueUpdate": map[string]bool{"success": !f.movesUnsuccessful}}
	case "CommentTicket":
		if f.noComments {
			http.Error(w, "comment refused", http.StatusInternalServerError)
			return
		}
		f.changes = append(f.changes, mutation{operation: req.OperationName, variables: req.Variables})
		data = map[string]any{"commentCreate": map[string]bool{"success": true}}
	default:
		http.Error(w, "unknown operation "+req.OperationName, http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
