package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

func (c *CLI) OpenWorkspace(ctx context.Context, w NewWorkspace) (string, error) {
	var list struct {
		Result struct {
			Workspaces []struct {
				WorkspaceID string `json:"workspace_id"`
				Worktree    *struct {
					CheckoutPath string `json:"checkout_path"`
				} `json:"worktree"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	_ = c.listJSON(ctx, &list, "workspace", "list")
	for _, ws := range list.Result.Workspaces {
		if ws.Worktree != nil && ws.Worktree.CheckoutPath == w.Path {
			return ws.WorkspaceID, nil
		}
	}

	// The checkout opens as a worktree so its workspace nests under the repo in
	// herdr's sidebar. There is no bare-workspace fallback, since herdr reports
	// a bare workspace with no worktree path, so the dispatcher could never find
	// its worker and would launch another one on every cycle.
	out, _ := c.combined(ctx, "worktree", "open", "--cwd", w.Repo, "--path", w.Path, "--label", w.Label, "--no-focus", "--json")
	var opened struct {
		Result struct {
			Worktree struct {
				OpenWorkspaceID string `json:"open_workspace_id"`
			} `json:"worktree"`
			Workspace struct {
				WorkspaceID string `json:"workspace_id"`
			} `json:"workspace"`
		} `json:"result"`
	}
	_ = json.Unmarshal([]byte(out), &opened)
	id := opened.Result.Worktree.OpenWorkspaceID
	if id == "" {
		id = opened.Result.Workspace.WorkspaceID
	}
	if id == "" {
		return "", fmt.Errorf("herdr worktree open failed for %s: %s", w.Path, out)
	}
	return id, nil
}

type listedPane struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Agent       string `json:"agent"`
}

type listedAgent struct {
	Name   string `json:"name"`
	PaneID string `json:"pane_id"`
	Agent  string `json:"agent"`
	Status string `json:"agent_status"`
}

func (c *CLI) listPanes(ctx context.Context) []listedPane {
	var r struct {
		Result struct {
			Panes []listedPane `json:"panes"`
		} `json:"result"`
	}
	_ = c.listJSON(ctx, &r, "pane", "list")
	return r.Result.Panes
}

func (c *CLI) listAgents(ctx context.Context) []listedAgent {
	var r struct {
		Result struct {
			Agents []listedAgent `json:"agents"`
		} `json:"result"`
	}
	_ = c.listJSON(ctx, &r, "agent", "list")
	return r.Result.Agents
}

func (c *CLI) StartWorker(ctx context.Context, w Worker) (Started, error) {
	// worktree open carries no --env but tab create does, so the worker runs in a
	// fresh stamped tab and the agent-less tabs the workspace opened with are
	// closed. The program's PathFirst directories hold wrappers that re-add its
	// flags when herdr restarts it, since herdr does not replay agent argv.
	path := strings.Join(append(slices.Clone(w.Program.PathFirst), c.env.Get("PATH")), ":")
	args := []string{"tab", "create", "--workspace", w.Workspace, "--cwd", w.Dir, "--label", w.Label,
		"--env", "PATH=" + path,
		"--env", "AUTONOMOUS_WORKER=1",
		"--env", "DISPATCH_PLUGIN_ROOT=" + w.PluginRoot}
	// A wrapper runs DISPATCH_BIN, so a dispatch on PATH that is older than the
	// plugin, or absent, cannot break the worker's start or restart.
	if c.dispatchBin != "" {
		args = append(args, "--env", "DISPATCH_BIN="+c.dispatchBin)
	}
	for _, key := range slices.Sorted(maps.Keys(w.Program.Env)) {
		args = append(args, "--env", key+"="+w.Program.Env[key])
	}
	out, _ := c.combined(ctx, append(args, "--no-focus")...)
	var created struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	_ = json.Unmarshal([]byte(out), &created)
	pane := created.Result.RootPane.PaneID
	if pane == "" {
		return Started{}, fmt.Errorf("herdr tab create failed in %s: %s", w.Workspace, out)
	}
	c.closeAgentlessTabs(ctx, w.Workspace, pane)

	started := Started{Pane: pane}
	// A timed-out start may still bring the program up, so retry only into a pane
	// that has no agent of its kind.
	ok := c.startAgent(ctx, w, &started)
	if !ok {
		c.sleep(ctx, 3*c.pace)
		ok = c.paneHasAgent(ctx, pane, w.Program.AgentKind) || c.startAgent(ctx, w, &started)
	}
	if !ok && !c.paneHasAgent(ctx, pane, w.Program.AgentKind) {
		return started, fmt.Errorf("herdr agent start failed in pane %s", pane)
	}
	return started, nil
}

func (c *CLI) closeAgentlessTabs(ctx context.Context, workspace, keep string) {
	stale := map[string]bool{}
	for _, p := range c.listPanes(ctx) {
		if p.WorkspaceID == workspace && p.PaneID != keep && p.Agent == "" {
			stale[p.TabID] = true
		}
	}
	tabs := make([]string, 0, len(stale))
	for id := range stale {
		if id != "" {
			tabs = append(tabs, id)
		}
	}
	sort.Strings(tabs)
	for _, id := range tabs {
		_ = c.run(ctx, "tab", "close", id)
	}
}

var shellPrompt = regexp.MustCompile(`[>❯$%]`)

func (c *CLI) startAgent(ctx context.Context, w Worker, started *Started) bool {
	started.Agent = w.Name
	for _, a := range c.listAgents(ctx) {
		if a.Name == started.Agent {
			started.Agent = fmt.Sprintf("%s-r%d", w.Name, rand.IntN(32768))
			break
		}
	}
	// A fresh pane rejects agent start until its shell has drawn a prompt.
	for try := 1; try <= 15; try++ {
		if out, err := c.output(ctx, "pane", "read", started.Pane); err == nil && shellPrompt.MatchString(out) {
			break
		}
		c.log("pane %s shell not ready yet (%d/15)", started.Pane, try)
		c.sleep(ctx, c.pace)
	}
	args := []string{"agent", "start", started.Agent, "--kind", w.Program.AgentKind, "--pane", started.Pane, "--timeout", "90000", "--"}
	out, err := c.combined(ctx, append(args, w.Program.Argv...)...)
	if err != nil {
		c.log("agent start failed: %s", out)
		return false
	}
	return true
}

func (c *CLI) paneHasAgent(ctx context.Context, pane, kind string) bool {
	for _, a := range c.listAgents(ctx) {
		if a.PaneID == pane && a.Agent == kind {
			return true
		}
	}
	return false
}

// Kickoff confirms a submitted kickoff by herdr's agent status leaving idle,
// never by finding its text on screen. While a skill runs, the screen shows
// only the spinner and an empty input box, and a text probe misread that as a
// lost kickoff and typed it again. The kickoff is retyped only while the agent
// is still idle with the text gone from its input box.
func (c *CLI) Kickoff(ctx context.Context, pane, text string, startup Startup) error {
	// A prefix is enough to find the typed kickoff, which the program may wrap.
	probe := text
	if r := []rune(text); len(r) > 24 {
		probe = string(r[:24])
	}
	visible := func() string {
		out, _ := c.output(ctx, "agent", "read", pane, "--source", "visible", "--lines", "30")
		return strings.ReplaceAll(out, "\n", " ")
	}
	_ = c.run(ctx, "agent", "wait", pane, "--until", "idle", "--timeout", "15000")
	ready := false
	unrun := false // the launch command was last seen unexecuted at the shell prompt
	entered := 0   // Enters herdr accepted while the launch command showed at the shell prompt
	sightings := make([]int, len(startup.Answers))
screens:
	for range 30 {
		screen := visible()
		c.record("dialogs", screen)
		for i, answer := range startup.Answers {
			if !answer.shows(screen) {
				continue
			}
			try := answer.Tries[sightings[i]%len(answer.Tries)]
			sightings[i]++
			for _, keys := range try {
				_ = c.run(ctx, append([]string{"pane", "send-keys", pane}, keys...)...)
			}
			c.sleep(ctx, c.pace)
			continue screens
		}
		// A shell prompt can match Ready too. When Unrun matches, the launch command is
		// still on the line and Enter was lost, so press it rather than type the
		// kickoff onto that line.
		if startup.Unrun != nil && startup.Unrun.MatchString(screen) {
			unrun = true
			if err := c.run(ctx, "pane", "send-keys", pane, "Enter"); err != nil {
				c.record("enter-failed", err.Error())
			} else {
				entered++
				c.record("enter", "launch command at the shell prompt")
			}
			c.sleep(ctx, c.pace)
			continue
		}
		if startup.Ready != nil && startup.Ready.MatchString(screen) {
			unrun = false
			ready = true
			break
		}
		c.sleep(ctx, c.pace)
	}
	if !ready {
		return c.unconfirmed(unrun, entered)
	}
	wait := func() bool {
		return c.run(ctx, "agent", "wait", pane, "--until", "working", "--until", "blocked", "--timeout", "15000") == nil
	}
	for try := 1; try <= 3; try++ {
		c.record("typing", fmt.Sprintf("%s (try %d)", text, try))
		_ = c.run(ctx, "pane", "run", pane, text)
		_ = c.run(ctx, "pane", "send-keys", pane, "Enter")
		if wait() {
			return nil
		}
		screen := visible()
		c.record("not-working", screen)
		// Retype only while the agent sits idle. done means a fast kickoff
		// already ran, and working or blocked means it is running, so never type
		// it again.
		var status string
		for _, a := range c.listAgents(ctx) {
			if a.PaneID == pane {
				status = a.Status
				break
			}
		}
		switch status {
		case "idle": // fall through to the retype
		case "working", "blocked", "done":
			return nil
		default:
			return c.unconfirmed(unrun, entered)
		}
		// Typed but never submitted, so press Enter rather than type it twice.
		if strings.Contains(screen, probe) {
			_ = c.run(ctx, "pane", "send-keys", pane, "Enter")
			if wait() {
				return nil
			}
			return c.unconfirmed(unrun, entered)
		}
	}
	return c.unconfirmed(unrun, entered)
}

func (c *CLI) unconfirmed(unrun bool, entered int) error {
	if unrun && entered == 0 {
		return ErrEnterNotSent
	}
	if unrun {
		return ErrNeverStarted
	}
	return ErrKickoffUnconfirmed
}

// shows reports whether every pattern of the dialog matches the screen.
func (a Answer) shows(screen string) bool {
	return len(a.Shows) > 0 && len(a.Tries) > 0 && !slices.ContainsFunc(a.Shows, func(re *regexp.Regexp) bool { return !re.MatchString(screen) })
}

func (c *CLI) record(label, body string) {
	if c.trace == "" {
		return
	}
	f, err := os.OpenFile(c.trace, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "=== %s %s\n%s\n", time.Now().Format("15:04:05"), label, body)
}

func (c *CLI) log(format string, args ...any) {
	_, _ = fmt.Fprintf(c.progress, "dispatch launch: "+format+"\n", args...)
}

func (c *CLI) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

func (c *CLI) run(ctx context.Context, args ...string) error {
	return c.command(ctx, args...).Run()
}

func (c *CLI) output(ctx context.Context, args ...string) (string, error) {
	out, err := c.command(ctx, args...).Output()
	return string(out), err
}

func (c *CLI) combined(ctx context.Context, args ...string) (string, error) {
	out, err := c.command(ctx, args...).CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

func (c *CLI) listJSON(ctx context.Context, into any, args ...string) error {
	out, err := c.output(ctx, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), into)
}
