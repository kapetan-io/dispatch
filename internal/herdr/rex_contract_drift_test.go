//go:build drift

package herdr_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// rexContract sets up the situations the host-neutral scenarios start from as worker blocks on the
// scratch server: a shell in each block, which agent replaces with the stand-in Claude.
type rexContract struct {
	rig      *rexRig
	instance string
	session  string
	count    int
	started  map[string]bool
}

func (c *rexContract) call(t *testing.T, session, method string, args any) []byte {
	t.Helper()
	body, err := json.Marshal(args)
	require.NoError(t, err)
	argv := []string{"api", "call"}
	if session != "" {
		argv = append(argv, "-s", session)
	}
	out, err := c.rig.server.Output(append(argv, method, string(body))...)
	require.NoError(t, err)
	return []byte(out)
}

func (c *rexContract) layout(tree string) map[string]any {
	options := map[string]any{"shell": "none", "login": false, "cwd": tree, "command": []string{"sh"}}
	return map[string]any{"block": map[string]any{"flavor": "com.superlogical.terminal.shell", "label": "dispatch - " + c.instance + " " + tree, "options": options}}
}

func (c *rexContract) workspace(t *testing.T, label string) herdrtest.Layout {
	t.Helper()
	repo, tree := herdrtest.NewShortCheckout(t)
	space := filepath.Base(repo) + "|" + tree
	if c.session == "" {
		var made struct {
			Session string `json:"session_id"`
			Windows []struct {
				ID     string   `json:"window_id"`
				Blocks []string `json:"block_ids"`
			} `json:"initial_windows"`
		}
		require.NoError(t, json.Unmarshal(c.call(t, "", "session.create", map[string]any{
			"label": filepath.Base(repo), "initial_windows": []any{map[string]any{"window_label": label, "layout": c.layout(tree)}},
		}), &made))
		c.session = made.Session
		return herdrtest.Layout{Workspace: space, Tab: made.Session + "/" + made.Windows[0].ID, Pane: made.Session + "/" + made.Windows[0].Blocks[0]}
	}
	return c.window(t, space, tree, label)
}

func (c *rexContract) tab(t *testing.T, workspace, label string) herdrtest.Layout {
	t.Helper()
	_, tree, ok := strings.Cut(workspace, "|")
	require.True(t, ok)
	return c.window(t, workspace, tree, label)
}

func (c *rexContract) window(t *testing.T, space, tree, label string) herdrtest.Layout {
	t.Helper()
	var made struct {
		Window string   `json:"window_id"`
		Blocks []string `json:"block_ids"`
	}
	require.NoError(t, json.Unmarshal(c.call(t, c.session, "session.new_window", map[string]any{
		"session_id": c.session, "window_label": label, "focus": false, "layout": c.layout(tree),
	}), &made))
	return herdrtest.Layout{Workspace: space, Tab: c.session + "/" + made.Window, Pane: c.session + "/" + made.Blocks[0]}
}

func (c *rexContract) write(t *testing.T, pane, data string) {
	t.Helper()
	session, block, _ := strings.Cut(pane, "/")
	c.call(t, session, "block.method", map[string]any{"session_id": session, "block_id": block, "method": "write", "args": map[string]any{"data": data}})
}

// agent has the shell in pane become the stand-in Claude on first use, and moves it to status.
func (c *rexContract) agent(t *testing.T, pane, status string) {
	t.Helper()
	if !c.started[pane] {
		c.started[pane] = true
		claude := filepath.Join(herdrtest.StandInClaude(t), "claude")
		c.write(t, pane, "exec env STANDIN_NO_DIALOGS=1 "+claude+"\r")
		c.await(t, pane, "idle")
		if status == "idle" {
			return
		}
		c.write(t, pane, "kickoff\r")
		c.await(t, pane, "working")
	}
	if status != "working" || c.started[pane+"/moved"] {
		c.write(t, pane, "!"+status+"\r")
	}
	c.started[pane+"/moved"] = true
	c.await(t, pane, status)
}

func (c *rexContract) await(t *testing.T, pane, status string) {
	t.Helper()
	require.Eventually(t, func() bool {
		snap, err := c.rig.rex.Read(context.Background())
		require.NoError(t, err)
		for _, a := range snap.Agents {
			if a.PaneID == pane {
				return a.Status == status
			}
		}
		return false
	}, 20*time.Second, 100*time.Millisecond)
}

func TestRexKeepsTheHostNeutralHerdrContract(t *testing.T) {
	for _, scenario := range herdrtest.Scenarios {
		if scenario.TUIOnly {
			continue
		}
		t.Run(scenario.Name, func(t *testing.T) {
			rig := newRexRig(t)
			subject := &rexContract{rig: rig, started: map[string]bool{}}
			var status struct {
				Instance string `json:"instance_id"`
			}
			require.NoError(t, json.Unmarshal(subject.call(t, "", "server.status", map[string]any{}), &status))
			subject.instance = status.Instance[:8]
			scenario.Run(t, herdrtest.Subject{
				Multiplexer: rig.rex,
				PluginRoot:  rig.worker.PluginRoot,
				Program:     rig.worker.Program,
				Checkout:    func() (string, string) { return herdrtest.NewShortCheckout(t) },
				Workspace:   func(label string) herdrtest.Layout { return subject.workspace(t, label) },
				Tab:         func(workspace, label string) herdrtest.Layout { return subject.tab(t, workspace, label) },
				Agent:       func(pane, status string) { subject.agent(t, pane, status) },
			})
		})
	}
}

func TestRexKeepsTheStreamContract(t *testing.T) {
	for _, scenario := range herdrtest.StreamScenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			rig := newRexRig(t)
			subject := &rexContract{rig: rig, started: map[string]bool{}}
			var status struct {
				Instance string `json:"instance_id"`
			}
			require.NoError(t, json.Unmarshal(subject.call(t, "", "server.status", map[string]any{}), &status))
			subject.instance = status.Instance[:8]
			scenario.Run(t, herdrtest.StreamSubject{
				Stream: rig.rex,
				Exit:   func(pane string) { subject.write(t, pane, "\x04") },
				Subject: herdrtest.Subject{
					Multiplexer: rig.rex,
					PluginRoot:  rig.worker.PluginRoot,
					Program:     rig.worker.Program,
					Checkout:    func() (string, string) { return herdrtest.NewShortCheckout(t) },
					Workspace:   func(label string) herdrtest.Layout { return subject.workspace(t, label) },
					Tab:         func(workspace, label string) herdrtest.Layout { return subject.tab(t, workspace, label) },
					Agent:       func(pane, status string) { subject.agent(t, pane, status) },
				},
			})
		})
	}
}
