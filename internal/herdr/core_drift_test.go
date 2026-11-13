//go:build drift

package herdr_test

// Package exception: this package wraps the hc tool, so every test here is an adapter test of it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

var (
	builtOnce sync.Once
	builtPath string
	builtErr  error
)

// dispatchBinary is a built dispatch, because a worker's claude shim runs DISPATCH_BIN and the
// test binary that calls the adapter in-process is not one.
func dispatchBinary(t *testing.T) string {
	t.Helper()
	builtOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dispatch-core-drift-")
		if err != nil {
			builtErr = err
			return
		}
		builtPath = filepath.Join(dir, "dispatch")
		if out, err := exec.Command("go", "build", "-o", builtPath, "github.com/kapetan-io/dispatch/cmd/dispatch").CombinedOutput(); err != nil {
			builtErr = fmt.Errorf("go build: %w: %s", err, out)
		}
	})
	require.NoError(t, builtErr)
	return builtPath
}

// coreRig is the app adapter over a scratch core, with the stand-in Claude on the workers' PATH.
type coreRig struct {
	core    *herdr.Core
	session *herdrtest.CoreSession
	worker  herdr.Worker
	space   string
}

func newCoreRig(t *testing.T) *coreRig {
	t.Helper()
	t.Cleanup(func() {
		left, err := herdrtest.CoreLeftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
	})
	session := herdrtest.StartCoreSession(t)
	standin := herdrtest.StandInClaude(t)
	session.Env = session.Env.With("PATH", standin+string(os.PathListSeparator)+session.Env.Get("PATH"))
	plugin, err := filepath.Abs("../..")
	require.NoError(t, err)
	_, tree := herdrtest.NewCheckout(t)
	core := session.Core().WithKinds("claude").WithDispatchBin(dispatchBinary(t)).WithPace(100 * time.Millisecond)
	space, err := core.OpenWorkspace(context.Background(), herdr.NewWorkspace{Path: tree})
	require.NoError(t, err)
	program := harness.ClaudeCode{}.Program(harness.Start{Model: "sonnet", PluginRoot: plugin, Session: "6f0c1e2a-0000-4000-8000-000000000001"})
	return &coreRig{core: core, session: session, space: space, worker: herdr.Worker{
		Workspace: space, Dir: tree, Label: "ENG-1", Name: "eng-1", PluginRoot: plugin, Program: program,
	}}
}

type listed struct {
	Terminals []struct {
		Address string            `json:"address"`
		State   string            `json:"state"`
		Reason  string            `json:"reason"`
		Parent  *string           `json:"parent"`
		Labels  map[string]string `json:"labels"`
	} `json:"terminals"`
}

// terminals lists the scratch core's terminals with their labels, which `hc ls` leaves out.
func (r *coreRig) terminals(t *testing.T, all bool) listed {
	t.Helper()
	var reply struct {
		Result listed `json:"result"`
	}
	require.NoError(t, json.Unmarshal(r.call(t, "terminal.list", map[string]any{"all": all}), &reply))
	return reply.Result
}

// agent is the worker's agent as Read shows it, or false once Claude is gone.
func (r *coreRig) agent(t *testing.T) (herdr.Agent, bool) {
	t.Helper()
	snap, err := r.core.Read(context.Background())
	require.NoError(t, err)
	if len(snap.Agents) == 0 {
		return herdr.Agent{}, false
	}
	return snap.Agents[0], true
}

func (r *coreRig) awaitStatus(t *testing.T, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		a, ok := r.agent(t)
		return ok && a.Status == want
	}, 20*time.Second, 100*time.Millisecond)
}

// launched starts the worker and gives it its kickoff, as a launch does.
func (r *coreRig) launched(t *testing.T) herdr.Started {
	t.Helper()
	started, err := r.core.StartWorker(context.Background(), r.worker)
	require.NoError(t, err)
	require.NoError(t, r.core.Kickoff(context.Background(), started.Pane, "/dispatch:build ENG-1", r.worker.Program.Startup))
	return started
}

func TestCoreStartsAWorkerAnswersItsDialogsAndTypesTheKickoff(t *testing.T) {
	r := newCoreRig(t)

	started := r.launched(t)

	live := r.terminals(t, false)
	require.Len(t, live.Terminals, 1)
	labels := live.Terminals[0].Labels
	assert.Equal(t, r.worker.Dir, labels["dispatch.tree"])
	assert.Equal(t, r.worker.Program.Session, labels["dispatch.session"])
	assert.Equal(t, r.space, labels["herdr.space"])
	assert.NotEmpty(t, labels["herdr.tab"])
	a, ok := r.agent(t)
	require.True(t, ok)
	assert.Equal(t, herdr.Agent{Kind: "claude", Status: "working", PaneID: started.Pane, TabID: labels["herdr.tab"], WorkspaceID: r.space, Session: r.worker.Program.Session}, a)
	found, ok, err := r.core.AgentAt(context.Background(), started.Pane)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, a, found)
	var recorded struct {
		Args []string `json:"args"`
	}
	b, err := os.ReadFile(filepath.Join(r.worker.Dir, "standin-claude.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &recorded))
	assert.Contains(t, recorded.Args, "--session-id")
	assert.Contains(t, recorded.Args, r.worker.Program.Session)
}

func TestCoreRefusesASecondSessionOnTheTreeAndClosesNothing(t *testing.T) {
	r := newCoreRig(t)
	r.launched(t)

	_, err := r.core.StartWorker(context.Background(), r.worker)

	require.ErrorIs(t, err, herdr.ErrSessionRunning)
	assert.Len(t, r.terminals(t, false).Terminals, 1)
	r.awaitStatus(t, "working")
}

func TestCoreLeavesOneTerminalWhenTwoStartsRace(t *testing.T) {
	r := newCoreRig(t)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = r.core.StartWorker(context.Background(), r.worker)
		}()
	}
	wg.Wait()

	assert.Len(t, r.terminals(t, false).Terminals, 1)
	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		assert.ErrorIs(t, err, herdr.ErrSessionRunning)
	}
	assert.Equal(t, 1, succeeded)
}

func TestCoreRelaunchesInTheSameSpaceAfterClaudeWasKilled(t *testing.T) {
	r := newCoreRig(t)
	first := r.launched(t)
	r.session.Run(t, "kill", first.Pane)
	require.Eventually(t, func() bool { _, ok := r.agent(t); return !ok }, 10*time.Second, 100*time.Millisecond)

	second, err := r.core.StartWorker(context.Background(), r.worker)

	require.NoError(t, err)
	assert.NotEqual(t, first.Pane, second.Pane)
	all := r.terminals(t, true)
	require.Len(t, all.Terminals, 1)
	assert.Equal(t, "live", all.Terminals[0].State)
	assert.Equal(t, r.space, all.Terminals[0].Labels["herdr.space"])
}

func TestCoreRefusesToStartBesideAClaudeTheUserStartedInTheSpace(t *testing.T) {
	r := newCoreRig(t)
	claude := filepath.Join(herdrtest.StandInClaude(t), "claude")
	r.session.Run(t, "run", "--bg", "--name", "users", "--", claude)
	r.session.Run(t, "label", "users", "herdr.space="+r.space)
	require.Eventually(t, func() bool {
		return bytes.Contains([]byte(r.session.Run(t, "get", "users", "--json")), []byte(`"name":"claude"`))
	}, 10*time.Second, 100*time.Millisecond)

	_, err := r.core.StartWorker(context.Background(), r.worker)

	require.ErrorIs(t, err, herdr.ErrSessionRunning)
	assert.Len(t, r.terminals(t, false).Terminals, 1)
}

func TestCoreNamesAWorkerTerminalThatRunsNoClaudeAndLeavesItRunning(t *testing.T) {
	r := newCoreRig(t)
	r.session.Run(t, "run", "--bg", "--name", "stuck", "--", "sleep", "300")
	r.session.Run(t, "label", "stuck", "herdr.space="+r.space, "dispatch.tree="+r.worker.Dir)

	_, err := r.core.StartWorker(context.Background(), r.worker)

	var held *herdr.TerminalWithoutAgent
	require.ErrorAs(t, err, &held)
	assert.NotEmpty(t, held.Address)
	live := r.terminals(t, false)
	require.Len(t, live.Terminals, 1)
	assert.Equal(t, held.Address, live.Terminals[0].Address)
}

func TestCoreReadsAWorkerWhoseClaudeWasKilledAsHavingNoAgent(t *testing.T) {
	r := newCoreRig(t)
	started := r.launched(t)

	r.session.Run(t, "kill", started.Pane)

	require.Eventually(t, func() bool { _, ok := r.agent(t); return !ok }, 10*time.Second, 100*time.Millisecond)
	snap, err := r.core.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Workspaces, 1)
	assert.Equal(t, r.worker.Dir, snap.Workspaces[0].CheckoutPath)
}

func TestCoreCloseWorkspaceEndsEveryWorkerTerminalAndLeavesTheUsersOwn(t *testing.T) {
	r := newCoreRig(t)
	r.launched(t)
	r.session.Run(t, "run", "--bg", "--name", "users", "--", "sleep", "300")
	r.session.Run(t, "label", "users", "herdr.space="+r.space)

	require.NoError(t, r.core.CloseWorkspace(context.Background(), r.space))

	all := r.terminals(t, true)
	require.Len(t, all.Terminals, 1)
	assert.Empty(t, all.Terminals[0].Labels["dispatch.tree"])
	assert.Equal(t, "live", all.Terminals[0].State)
	require.NoError(t, r.core.CloseWorkspace(context.Background(), r.space))
}

func TestCoreRemovesTheFinishedWorkerTerminalsOfAClosedSpaceBeforeANewStart(t *testing.T) {
	r := newCoreRig(t)
	started := r.launched(t)
	r.session.Run(t, "kill", started.Pane)

	snap, err := r.core.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Workspaces, 1)
	require.NoError(t, r.core.CloseWorkspace(context.Background(), snap.Workspaces[0].ID))

	assert.Empty(t, r.terminals(t, true).Terminals)
	snap, err = r.core.Read(context.Background())
	require.NoError(t, err)
	assert.Empty(t, snap.Workspaces)
}

// call sends one request straight to the scratch core, as the adapter does through `hc connect`.
func (r *coreRig) call(t *testing.T, method string, params map[string]any) []byte {
	t.Helper()
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	hc, err := exec.LookPath("hc")
	require.NoError(t, err)
	cmd := exec.Command(hc, "connect")
	cmd.Env = environment.Environment(r.session.Env)
	cmd.Stdin = bytes.NewReader(append(request, '\n'))
	out, err := cmd.Output()
	require.NoError(t, err)
	return bytes.TrimSpace(out)
}

// rpc is call for a request that fails, and returns the kind hc gives the failure.
func (r *coreRig) rpc(t *testing.T, method string, params map[string]any) (kind string) {
	t.Helper()
	var reply struct {
		Error *struct {
			Data struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.call(t, method, params), &reply))
	if reply.Error == nil {
		return ""
	}
	return reply.Error.Data.Kind
}

func TestHcAnswersWithTheRefusalKindsCoreTranslates(t *testing.T) {
	r := newCoreRig(t)
	started := r.launched(t)

	assert.Equal(t, "status_mismatch", r.rpc(t, "terminal.send", map[string]any{"terminal": started.Pane, "text": "x", "if": "idle"}))
	r.session.Type(t, started.Pane, "!blocked")
	r.awaitStatus(t, "blocked")
	assert.Equal(t, "blocked", r.rpc(t, "terminal.send", map[string]any{"terminal": started.Pane, "text": "x"}))
	assert.Equal(t, "not_found", r.rpc(t, "terminal.get", map[string]any{"terminal": "t999"}))
	r.session.Run(t, "kill", started.Pane)
	assert.Equal(t, "finished", r.rpc(t, "terminal.send", map[string]any{"terminal": started.Pane, "text": "x"}))
}

func TestCoreWorkersOutliveTheTerminalDispatchRunsIn(t *testing.T) {
	r := newCoreRig(t)
	r.session.Run(t, "run", "--bg", "--name", "outer", "--", "cat")
	// Dispatch run from inside an app terminal hands hc that terminal's id.
	env := r.session.Env.With("HC_TERMINAL_ID", "outer")
	r.session.Env = env
	core := r.session.Core().WithKinds("claude").WithDispatchBin(dispatchBinary(t)).WithPace(100 * time.Millisecond)
	started, err := core.StartWorker(context.Background(), r.worker)
	require.NoError(t, err)
	require.NoError(t, core.Kickoff(context.Background(), started.Pane, "/dispatch:build ENG-1", r.worker.Program.Startup))

	for _, terminal := range r.terminals(t, false).Terminals {
		if terminal.Labels["dispatch.tree"] != "" {
			assert.Nil(t, terminal.Parent)
		}
	}
	r.session.Run(t, "kill", "outer")

	time.Sleep(time.Second)
	a, ok := r.agent(t)
	require.True(t, ok)
	assert.Equal(t, "working", a.Status)
	outer := r.session.Run(t, "read", "outer")
	assert.NotContains(t, outer, "dispatch")
}

// contractSubject sets up the situations the host-neutral scenarios start from as worker
// terminals of the scratch core: a shell for each pane, which Agent replaces with the stand-in Claude.
type contractSubject struct {
	rig   *coreRig
	count int
	exec  map[string]bool
}

func (c *contractSubject) terminal(t *testing.T, space, tab string) string {
	t.Helper()
	c.count++
	out := c.rig.session.Run(t, "run", "--bg", "--cwd", t.TempDir(), "--name", fmt.Sprintf("pane-%d", c.count), "--", "sh")
	var run struct {
		Address string `json:"address"`
	}
	line, _, _ := strings.Cut(out, "\n")
	require.NoError(t, json.Unmarshal([]byte(line), &run))
	c.rig.session.Run(t, "label", run.Address, "dispatch.tree="+t.TempDir(), "herdr.space="+space, "herdr.tab="+tab)
	return run.Address
}

func (c *contractSubject) workspace(t *testing.T, label string) herdrtest.Layout {
	c.count++
	space, tab := fmt.Sprintf("sp-%d", c.count), fmt.Sprintf("tb-%d", c.count)
	return herdrtest.Layout{Workspace: space, Tab: tab, Pane: c.terminal(t, space, tab)}
}

func (c *contractSubject) tab(t *testing.T, workspace, label string) herdrtest.Layout {
	c.count++
	tab := fmt.Sprintf("tb-%d", c.count)
	return herdrtest.Layout{Workspace: workspace, Tab: tab, Pane: c.terminal(t, workspace, tab)}
}

// agent has the shell in pane become the stand-in Claude on first use, and moves it to status.
func (c *contractSubject) agent(t *testing.T, pane, status string) {
	t.Helper()
	if !c.exec[pane] {
		c.exec[pane] = true
		claude := filepath.Join(herdrtest.StandInClaude(t), "claude")
		c.rig.session.Run(t, "send", pane, "exec env STANDIN_NO_DIALOGS=1 "+claude, "--force")
		c.await(t, pane, "idle")
		if status == "idle" {
			return
		}
		c.rig.session.Run(t, "send", pane, "kickoff", "--force")
		c.await(t, pane, "working")
	}
	if status != "working" || c.exec[pane+"/moved"] {
		c.rig.session.Run(t, "send", pane, "!"+status, "--force")
	}
	c.exec[pane+"/moved"] = true
	c.await(t, pane, status)
}

func (c *contractSubject) await(t *testing.T, pane, status string) {
	t.Helper()
	require.Eventually(t, func() bool {
		snap, err := c.rig.core.Read(context.Background())
		require.NoError(t, err)
		for _, a := range snap.Agents {
			if a.PaneID == pane {
				return a.Status == status
			}
		}
		return false
	}, 20*time.Second, 100*time.Millisecond)
}

func TestCoreKeepsTheHostNeutralHerdrContract(t *testing.T) {
	for _, scenario := range herdrtest.Scenarios {
		if scenario.TUIOnly {
			continue
		}
		t.Run(scenario.Name, func(t *testing.T) {
			rig := newCoreRig(t)
			subject := &contractSubject{rig: rig, exec: map[string]bool{}}
			scenario.Run(t, herdrtest.Subject{
				Multiplexer: rig.core,
				PluginRoot:  rig.worker.PluginRoot,
				Program:     rig.worker.Program,
				Checkout:    func() (string, string) { return herdrtest.NewCheckout(t) },
				Workspace:   func(label string) herdrtest.Layout { return subject.workspace(t, label) },
				Tab:         func(workspace, label string) herdrtest.Layout { return subject.tab(t, workspace, label) },
				Agent:       func(pane, status string) { subject.agent(t, pane, status) },
			})
		})
	}
}

func TestCoreRunsWorkersOfTwoTreesAndClosesOnlyTheTreeItIsAskedFor(t *testing.T) {
	r := newCoreRig(t)
	first := r.launched(t)
	_, tree := herdrtest.NewCheckout(t)
	other := r.worker
	other.Dir = tree
	other.Program.Session = "6f0c1e2a-0000-4000-8000-000000000002"
	otherSpace, err := r.core.OpenWorkspace(context.Background(), herdr.NewWorkspace{Path: tree})
	require.NoError(t, err)
	require.NotEqual(t, r.space, otherSpace)

	second, err := r.core.StartWorker(context.Background(), other)
	require.NoError(t, err)
	require.NoError(t, r.core.Kickoff(context.Background(), second.Pane, "/dispatch:build ENG-2", other.Program.Startup))
	require.Len(t, r.terminals(t, false).Terminals, 2)

	require.NoError(t, r.core.CloseWorkspace(context.Background(), r.space))

	left := r.terminals(t, true)
	require.Len(t, left.Terminals, 1)
	assert.Equal(t, second.Pane, left.Terminals[0].Address)
	assert.NotEqual(t, first.Pane, left.Terminals[0].Address)
	assert.Equal(t, "live", left.Terminals[0].State)
}

func TestHcRefusesAnIdleOnlySendToATerminalThatReportsNoStatus(t *testing.T) {
	r := newCoreRig(t)
	started, err := r.core.StartWorker(context.Background(), r.worker)
	require.NoError(t, err)
	require.Eventually(t, func() bool { a, ok := r.agent(t); return ok && a.Status == "unknown" }, 20*time.Second, 100*time.Millisecond)

	kind := r.rpc(t, "terminal.send", map[string]any{"terminal": started.Pane, "text": "x", "if": "idle"})

	assert.Equal(t, "status_mismatch", kind)
}

// recordedArgs is the argv the stand-in Claude saw when it last started in the worker's tree.
func (r *coreRig) recordedArgs(t *testing.T) []string {
	t.Helper()
	var recorded struct {
		Args []string `json:"args"`
	}
	b, err := os.ReadFile(filepath.Join(r.worker.Dir, "standin-claude.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &recorded))
	return recorded.Args
}

func TestCoreStartWorkerResumesAfterACoreRestart(t *testing.T) {
	r := newCoreRig(t)
	first := r.launched(t)
	require.NotEmpty(t, first.Pane)

	r.session.Restart(t)

	require.Eventually(t, func() bool {
		for _, term := range r.terminals(t, true).Terminals {
			if term.State == "finished" && term.Reason == "server_stop" {
				return true
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond)
	r.launched(t)
	all := r.terminals(t, true)
	require.Len(t, all.Terminals, 1)
	assert.Equal(t, r.worker.Program.Session, all.Terminals[0].Labels["dispatch.session"])
	args := r.recordedArgs(t)
	assert.Contains(t, args, "--model")
	assert.Contains(t, args, "--resume")
	assert.Contains(t, args, r.worker.Program.Session)
	assert.NotContains(t, args, "--session-id")
}

func TestCoreStartWorkerStartsFreshAfterACrash(t *testing.T) {
	r := newCoreRig(t)
	first := r.launched(t)
	r.session.Run(t, "kill", first.Pane)
	require.Eventually(t, func() bool { _, ok := r.agent(t); return !ok }, 10*time.Second, 100*time.Millisecond)
	r.worker.Program.Session = "6f0c1e2a-0000-4000-8000-000000000002"

	r.launched(t)

	all := r.terminals(t, true)
	require.Len(t, all.Terminals, 1)
	assert.Equal(t, "6f0c1e2a-0000-4000-8000-000000000002", all.Terminals[0].Labels["dispatch.session"])
	args := r.recordedArgs(t)
	assert.Contains(t, args, "--session-id")
	assert.NotContains(t, args, "--resume")
}

func TestCoreCloseWorkspaceRemovesFinishedWorkerTerminalsAfterACoreRestart(t *testing.T) {
	r := newCoreRig(t)
	r.launched(t)
	r.session.Restart(t)
	require.Eventually(t, func() bool {
		snap, err := r.core.Read(context.Background())
		return err == nil && len(snap.Workspaces) == 1 && len(snap.Agents) == 0
	}, 10*time.Second, 100*time.Millisecond)

	require.NoError(t, r.core.CloseWorkspace(context.Background(), r.space))

	assert.Empty(t, r.terminals(t, true).Terminals)
	r.worker.Program.Session = "6f0c1e2a-0000-4000-8000-000000000003"
	r.launched(t)
	args := r.recordedArgs(t)
	assert.Contains(t, args, "--session-id")
	assert.Contains(t, args, "6f0c1e2a-0000-4000-8000-000000000003")
	assert.NotContains(t, args, "--resume")
}

// killHolder kills the scratch core's holder process of the worker's terminal, which leaves
// the terminal finished with reason holder_lost, and removes the socket the holder leaves behind.
func (r *coreRig) killHolder(t *testing.T) {
	t.Helper()
	prefix := strings.TrimSuffix(r.session.Socket, ".sock") + ".holder."
	out, err := exec.Command("ps", "-axo", "pid,command").Output()
	require.NoError(t, err)
	killed := 0
	for _, line := range strings.Split(string(out), "\n") {
		_, command, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found || !strings.Contains(command, "hold --socket "+prefix) {
			continue
		}
		require.NoError(t, exec.Command("kill", "-9", strings.Fields(line)[0]).Run())
		_, socket, _ := strings.Cut(command, "--socket ")
		require.NoError(t, os.Remove(strings.TrimSpace(socket)))
		killed++
	}
	require.Equal(t, 1, killed)
}

func TestCoreStartWorkerResumesAfterItsHolderWasLost(t *testing.T) {
	r := newCoreRig(t)
	r.launched(t)

	r.killHolder(t)

	require.Eventually(t, func() bool {
		for _, term := range r.terminals(t, true).Terminals {
			if term.State == "finished" && term.Reason == "holder_lost" {
				return true
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond)
	r.launched(t)
	all := r.terminals(t, true)
	require.Len(t, all.Terminals, 1)
	assert.Equal(t, r.worker.Program.Session, all.Terminals[0].Labels["dispatch.session"])
	args := r.recordedArgs(t)
	assert.Contains(t, args, "--resume")
	assert.Contains(t, args, r.worker.Program.Session)
	assert.NotContains(t, args, "--session-id")
}
