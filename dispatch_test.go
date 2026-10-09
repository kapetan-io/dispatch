package dispatch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
		{
			name:   "DesignTicket",
			ticket: withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design"),
			want:   "skip ENG-1: carries \"design\", which this dispatcher does not launch yet\n",
		},
		{
			name:   "ReviewTicket",
			ticket: withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"),
			want:   "skip ENG-1: carries \"design-review\", which this dispatcher does not launch yet\n",
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

	_, err := h.run("launch")

	require.ErrorContains(t, err, `unknown command "launch"`)
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
	require.Eventually(t, func() bool { return len(h.launches()) >= 3 }, 5*time.Second, 10*time.Millisecond)
	cancel()

	require.NoError(t, <-done)
	assert.Contains(t, out, "ticking every 20ms")
	assert.GreaterOrEqual(t, strings.Count(out, "launch ENG-1: ok"), 3)
	assert.Regexp(t, `(?m)^\d\d:\d\d:\d\d tick: 1 ticket, 1 action, Linear 1 call in `+duration+` \(2400 of 2500 requests left this hour\), took `+duration+`$`, out)
}

func TestRunPrintsASlowTickInSeconds(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.writeFile("treehouse-slow", "")
	h.script("treehouse", `if [ -f "`+h.dir+`/treehouse-slow" ]; then rm "`+h.dir+`/treehouse-slow"; sleep 1.1; fi
cat "`+h.dir+`/trees.json"`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var out string

	go func() {
		var err error
		out, _, err = h.runContext(ctx, "run", "--interval", "20ms")
		done <- err
	}()
	require.Eventually(t, func() bool { return len(h.launches()) >= 1 }, 5*time.Second, 10*time.Millisecond)
	cancel()

	require.NoError(t, <-done)
	assert.Regexp(t, `(?m)^\d\d:\d\d:\d\d tick: 1 ticket, 1 action, Linear 1 call in `+duration+` \(2400 of 2500 requests left this hour\), took \d+(?:\.\d+)?s$`, out)
}

func TestRunKeepsTickingAfterAFailedTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	// treehouse fails its first call only, so the first tick fails and the
	// second one launches.
	h.writeFile("treehouse-fails", "")
	h.script("treehouse", `if [ -f "`+h.dir+`/treehouse-fails" ]; then rm "`+h.dir+`/treehouse-fails"; exit 1; fi
cat "`+h.dir+`/trees.json"`)
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

// harness runs the dispatcher against a fake Linear server and fake herdr,
// treehouse and launcher commands, all rooted in one temp directory.
type harness struct {
	t      *testing.T
	dir    string
	repo   string
	state  string
	extra  string
	repos  string
	linear *fakeLinear
}

func newHarness(t *testing.T) *harness {
	dir := t.TempDir()
	h := &harness{
		t:      t,
		dir:    dir,
		repo:   filepath.Join(dir, "repo"),
		state:  filepath.Join(dir, "state"),
		linear: newFakeLinear(t),
	}
	require.NoError(t, os.MkdirAll(h.repo, 0o755))
	h.writeFile("workspaces.json", `{"result":{"workspaces":[]}}`)
	h.writeFile("agents.json", `{"result":{"agents":[]}}`)
	h.writeFile("trees.json", `[]`)
	h.writeFile("launch.json", `{"status":"ok"}`)
	h.script("herdr", `case "$1 $2" in
"workspace list") cat "`+dir+`/workspaces.json" ;;
"workspace close")
  [ -f "`+dir+`/close-fails" ] && exit 1
  echo "$3" >> "`+dir+`/closes.log"
  jq --arg id "$3" '.result.workspaces |= map(select(.workspace_id != $id))' "`+dir+`/workspaces.json" > "`+dir+`/workspaces.tmp"
  mv "`+dir+`/workspaces.tmp" "`+dir+`/workspaces.json" ;;
"agent list") cat "`+dir+`/agents.json" ;;
"agent prompt")
  [ -f "`+dir+`/prompt-fails" ] && { echo '{"error":{"code":"agent_blocked"}}'; exit 1; }
  echo "$3|$4" >> "`+dir+`/prompts.log" ;;
*) exit 2 ;;
esac`)
	// The notification message and title are osascript's last two arguments.
	h.script("osascript", `[ -f "`+dir+`/notify-fails" ] && exit 1
echo "${@: -2:1}|${@: -1}" >> "`+dir+`/notifications.log"`)
	// gh's branch= argument names the PR file to print, and each call is logged.
	h.script("gh", `[ -f "`+dir+`/gh-fails" ] && exit 1
for arg; do case "$arg" in branch=*) branch="${arg#branch=}" ;; esac; done
echo "$branch" >> "`+dir+`/gh.log"
cat "`+dir+`/pr-${branch##*/}.json" 2>/dev/null || echo '{"data":{"repository":{"pullRequests":{"nodes":[]}}}}'`)
	// return marks the tree available, so later ticks see the slot free.
	h.script("treehouse", `case "$1" in
return)
  [ -f "`+dir+`/return-fails" ] && exit 1
  echo "$PWD|$*" >> "`+dir+`/returns.log"
  jq --arg p "$2" 'map(if .path == $p then .lease_holder = "" | .status = "available" else . end)' "`+dir+`/trees.json" > "`+dir+`/trees.tmp"
  mv "`+dir+`/trees.tmp" "`+dir+`/trees.json" ;;
*) cat "`+dir+`/trees.json" ;;
esac`)
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
	extra.LinearURL = h.linear.server.URL
	extra.LinearKey = "test-key"
	extra.Herdr = filepath.Join(h.dir, "herdr")
	extra.Treehouse = filepath.Join(h.dir, "treehouse")
	extra.GH = filepath.Join(h.dir, "gh")
	extra.Osascript = filepath.Join(h.dir, "osascript")
	extra.Launcher = filepath.Join(h.dir, "launcher")
	extra.Stdout = &stdout
	extra.Stderr = &stderr
	err := dispatch.Run(ctx, args, extra)
	return stdout.String(), stderr.String(), err
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
	trees := []map[string]string{}
	require.NoError(h.t, json.Unmarshal(h.readFile("trees.json"), &trees))
	trees = append(trees, map[string]string{"path": tree, "status": "leased", "lease_holder": ticket})
	h.writeJSON("trees.json", trees)

	var ws struct {
		Result struct {
			Workspaces []map[string]any `json:"workspaces"`
		} `json:"result"`
	}
	require.NoError(h.t, json.Unmarshal(h.readFile("workspaces.json"), &ws))
	ws.Result.Workspaces = append(ws.Result.Workspaces, map[string]any{
		"workspace_id": id,
		"label":        ticket,
		"worktree":     map[string]string{"checkout_path": tree},
	})
	h.writeJSON("workspaces.json", ws)

	if status == "" {
		return
	}
	var agents struct {
		Result struct {
			Agents []map[string]string `json:"agents"`
		} `json:"result"`
	}
	require.NoError(h.t, json.Unmarshal(h.readFile("agents.json"), &agents))
	agents.Result.Agents = append(agents.Result.Agents, map[string]string{
		"agent":        "claude",
		"agent_status": status,
		"pane_id":      id + ":p1",
		"workspace_id": id,
	})
	h.writeJSON("agents.json", agents)
}

// setWorkerStatus changes the herdr status of a worker added with worker.
func (h *harness) setWorkerStatus(ticket, status string) {
	var agents struct {
		Result struct {
			Agents []map[string]string `json:"agents"`
		} `json:"result"`
	}
	require.NoError(h.t, json.Unmarshal(h.readFile("agents.json"), &agents))
	for _, a := range agents.Result.Agents {
		if a["workspace_id"] == "w-"+ticket {
			a["agent_status"] = status
		}
	}
	h.writeJSON("agents.json", agents)
}

// prompts lists what was typed into workers, as "<pane>|<text>".
func (h *harness) prompts() []string {
	return h.lines("prompts.log")
}

// notifications lists the macOS notifications shown, as "<message>|<title>".
func (h *harness) notifications() []string {
	return h.lines("notifications.log")
}

func (h *harness) lines(name string) []string {
	b, err := os.ReadFile(filepath.Join(h.dir, name))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(h.t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// closes lists the herdr workspaces that were closed.
func (h *harness) closes() []string {
	return h.lines("closes.log")
}

// returns lists the treehouse return calls, as "<directory>|<arguments>".
func (h *harness) returns() []string {
	return h.lines("returns.log")
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
	require.NoError(h.t, os.WriteFile(filepath.Join(h.dir, name), []byte("#!/usr/bin/env bash\n"+body+"\n"), 0o755))
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
	case "TeamStates":
		data = map[string]any{"teams": map[string]any{"nodes": []any{map[string]any{
			"key": "ENG",
			"states": map[string]any{"nodes": []any{
				map[string]string{"id": "state-in-progress", "name": "In Progress"},
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
