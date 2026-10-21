package dispatch_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	noTasks      = `"background_tasks":[]`
	runningTasks = `"background_tasks":[{"type":"shell","status":"running"}]`
)

// recordStop runs the Stop hook as the worker of ticket, in a real worktree of the
// harness repo. tasks is the background_tasks member of the hook input, or ""
// to leave it out.
func (h *harness) recordStop(ticket, tasks string) {
	h.t.Helper()
	h.recordStopFrom("worker", ticket, tasks)
}

// recordStopFrom is recordStop for the Claude session with the given id.
func (h *harness) recordStopFrom(session, ticket, tasks string) {
	h.t.Helper()
	_, _, err := h.runWith(context.Background(), dispatch.Options{Stdin: strings.NewReader(h.stopInput(session, ticket, tasks))}, "record-stop")
	require.NoError(h.t, err)
}

// stopInput is what Claude Code hands the Stop hook for a session in the ticket's tree.
func (h *harness) stopInput(session, ticket, tasks string) string {
	h.t.Helper()
	tree := filepath.Join(h.dir, "trees", ticket)
	if _, err := os.Stat(filepath.Join(h.repo, ".git")); err != nil {
		hintGit(h.t, h.repo, "init", "-q")
		hintGit(h.t, h.repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	}
	if _, err := os.Stat(tree); err != nil {
		hintGit(h.t, h.repo, "worktree", "add", "-q", "-b", "br-"+ticket, tree)
	}
	input := fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q,"cwd":%q`, session, tree)
	if tasks != "" {
		input += "," + tasks
	}
	return input + "}"
}

// failingPR puts ENG-1 In Review with an idle worker and a PR whose check failed.
func (h *harness) failingPR() {
	h.t.Helper()
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
}

func TestTickNudgesAgainAtTheNextResyncWhenTheWorkerLeftNoStopRecord(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(h *harness)
	}{
		{"NoRecord", func(h *harness) {}},
		{"NoBackgroundTasksField", func(h *harness) { h.recordStop("ENG-1", "") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.config("[rules]\nnudge_limit = 10\n")
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			test.stop(h)
			reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

			h.tick()
			assert.Len(t, h.prompts(), 1)

			// A nudged worker shows as working until the next resync, so a resync that
			// finds it idle again starts a new stretch of idling, and a new nudge.
			h.advance(5 * time.Minute)
			assert.Len(t, h.prompts(), 2)

			// A tick on the same clock starts no new stretch.
			h.tick()
			assert.Len(t, h.prompts(), 2)

			h.setWorkerStatus("ENG-1", "working")
			h.advance(5 * time.Minute)
			assert.Len(t, h.prompts(), 2)
			h.setWorkerStatus("ENG-1", "idle")
			h.advance(5 * time.Minute)
			assert.Len(t, h.prompts(), 3)
		})
	}
}

func TestTickNudgesAnIdleWorkerAfterAnotherSessionStopsBusyInItsTree(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 10\n")
	h.failingPR()
	h.recordStopFrom("worker", "ENG-1", noTasks)
	h.tick()
	assert.Len(t, h.prompts(), 1)

	h.advance(time.Minute)
	h.recordStopFrom("stranger", "ENG-1", runningTasks)
	h.advance(5 * time.Minute)
	h.recordStopFrom("worker", "ENG-1", noTasks)
	h.tick()

	assert.Len(t, h.prompts(), 2)
}

func TestTickSendsTheResumeNudgeWhateverTheStopRecordSays(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	h.tick()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.recordStop("ENG-1", runningTasks)

	h.tick()
	h.tick()

	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, h.prompts())
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
}

func TestTickForgetsTheStopRecordOfATreeItReturns(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", runningTasks)
	require.Len(t, stopRecords(t, h.state), 1)
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	remote := filepath.Join(h.dir, "remotes", "ENG-1.git")
	require.NoError(t, os.MkdirAll(filepath.Dir(remote), 0o755))
	hintGit(t, h.dir, "init", "-q", "--bare", remote)
	tree := filepath.Join(h.dir, "trees", "ENG-1")
	hintGit(t, tree, "remote", "add", "origin", remote)
	hintGit(t, tree, "push", "-q", "--set-upstream", "origin", "br-ENG-1")

	h.tick()

	assert.Len(t, h.returns(), 1)
	assert.Empty(t, stopRecords(t, h.state))
}

func TestRecordStopDoesNothingOutsideAWorkerTree(t *testing.T) {
	h := newHarness(t)
	h.recordStop("ENG-1", noTasks)
	other := newHintRepo(t)
	stateBefore := stopRecords(t, h.state)

	for _, test := range []struct {
		name  string
		cwd   string
		input string
	}{
		{"MainCheckout", h.repo, ""},
		{"UnmanagedRepo", other, ""},
		{"NotARepo", t.TempDir(), ""},
		{"NotJSON", "", "not json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := test.input
			if input == "" {
				input = fmt.Sprintf(`{"cwd":%q,%s}`, test.cwd, noTasks)
			}

			stdout, stderr, err := h.runWith(t.Context(), dispatch.Options{Stdin: strings.NewReader(input)}, "record-stop")

			require.NoError(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Equal(t, stateBefore, stopRecords(t, h.state))
		})
	}
}

func TestRecordStopStaysSilentWithoutAConfig(t *testing.T) {
	var stdout, stderr strings.Builder

	err := dispatch.Run(t.Context(), []string{"record-stop"}, dispatch.Options{
		ConfigPath: filepath.Join(t.TempDir(), "missing.toml"),
		StateDir:   t.TempDir(),
		Stdin:      strings.NewReader(`{"cwd":"/tmp","background_tasks":[]}`),
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	require.NoError(t, err)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}

func TestRecordStopDropsItsReportWhenNoDispatchRuns(t *testing.T) {
	h := newHarness(t)
	var stdout, stderr strings.Builder
	opts := h.options(dispatch.Options{Stdin: strings.NewReader(h.stopInput("worker", "ENG-1", noTasks))})
	opts.Stdout, opts.Stderr = &stdout, &stderr

	err := dispatch.Run(t.Context(), []string{"record-stop"}, opts)

	require.NoError(t, err)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
	assert.Empty(t, stopRecords(t, h.state))
	assert.NoDirExists(t, filepath.Join(h.state, "sessions"))
}

func TestRecordStopDoesNotWaitOnAStuckDispatch(t *testing.T) {
	h := newHarness(t)
	input := h.stopInput("worker", "ENG-1", noTasks)
	ln, err := net.Listen("unix", loop.SocketPath(h.state))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	var stdout, stderr strings.Builder
	opts := h.options(dispatch.Options{Stdin: strings.NewReader(input)})
	opts.Stdout, opts.Stderr = &stdout, &stderr
	start := time.Now()

	err = dispatch.Run(t.Context(), []string{"record-stop"}, opts)

	require.NoError(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}

func TestRecordStopTellsTheRunningDispatch(t *testing.T) {
	h := newHarness(t)
	h.startBeforeTheEvents()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "done")

	h.recordStop("ENG-1", runningTasks)

	for range 3 {
		h.advance(5 * time.Minute)
		h.tick()
	}
	assert.NotEmpty(t, stopRecords(t, h.state))
	assert.Empty(t, h.prompts())
}

func TestStopHookRunsRecordStop(t *testing.T) {
	body, err := os.ReadFile("hooks/hooks.json")
	require.NoError(t, err)

	assert.Contains(t, string(body), `"Stop"`)
	assert.Contains(t, string(body), "exec dispatch record-stop")
}

func stopRecords(t *testing.T, stateDir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(stateDir, "stops"), func(path string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		out = append(out, e.Name()+string(b))
		return err
	})
	if !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	return out
}
