//go:build drift

package herdr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// TestRealHerdrKeepsTheHerdrContract runs the fake's scenarios against the
// installed herdr, in a headless session of its own, so the maintainer's
// default session is never touched. A missing herdr fails the test.
// Run with: go test -tags drift ./internal/herdr/
func TestRealHerdrKeepsTheHerdrContract(t *testing.T) {
	t.Cleanup(func() { assert.Empty(t, herdrtest.Leftovers(t, os.Getpid())) })

	for _, scenario := range herdrtest.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			session := herdrtest.StartSession(t)
			ctx := context.Background()
			cwd := t.TempDir()
			// herdr tells an agent by the name of the process in the pane, so a
			// program named claude is enough to make a pane hold one.
			cat, err := exec.LookPath("cat")
			require.NoError(t, err)
			claude := filepath.Join(t.TempDir(), "claude")
			require.NoError(t, os.Symlink(cat, claude))
			started := map[string]bool{}

			scenario.Run(t, herdrtest.Subject{
				Herdr: session.CLI(),
				Workspace: func(label string) herdrtest.Layout {
					out, err := session.Output(ctx, "workspace", "create", "--cwd", cwd, "--label", label, "--no-focus")
					require.NoError(t, err)
					var created struct {
						Result struct {
							Workspace struct {
								ID string `json:"workspace_id"`
							} `json:"workspace"`
							Tab struct {
								ID string `json:"tab_id"`
							} `json:"tab"`
							Pane struct {
								ID string `json:"pane_id"`
							} `json:"root_pane"`
						} `json:"result"`
					}
					require.NoError(t, json.Unmarshal(out, &created))
					return herdrtest.Layout{Workspace: created.Result.Workspace.ID, Tab: created.Result.Tab.ID, Pane: created.Result.Pane.ID}
				},
				Tab: func(workspace, label string) herdrtest.Layout {
					out, err := session.Output(ctx, "tab", "create", "--workspace", workspace, "--cwd", cwd, "--label", label, "--no-focus")
					require.NoError(t, err)
					var created struct {
						Result struct {
							Tab struct {
								ID string `json:"tab_id"`
							} `json:"tab"`
							Pane struct {
								ID string `json:"pane_id"`
							} `json:"root_pane"`
						} `json:"result"`
					}
					require.NoError(t, json.Unmarshal(out, &created))
					return herdrtest.Layout{Workspace: workspace, Tab: created.Result.Tab.ID, Pane: created.Result.Pane.ID}
				},
				Agent: func(pane, status string) {
					if !started[pane] {
						started[pane] = true
						// A command typed before the shell shows its prompt can be lost,
						// and a shell with a heavy profile takes over 10 seconds to show it.
						require.Eventually(t, func() bool {
							out, err := session.Output(ctx, "pane", "read", pane)
							return err == nil && len(bytes.TrimSpace(out)) > 0
						}, 60*time.Second, 50*time.Millisecond)
						require.NoError(t, session.Run("pane", "run", pane, claude))
						require.Eventually(t, func() bool {
							out, err := session.Output(ctx, "agent", "list")
							return err == nil && json.Valid(out) && listsAgent(out, pane)
						}, 10*time.Second, 50*time.Millisecond)
					}
					require.NoError(t, session.Run("pane", "report-agent", "--source", "drift", "--agent", "claude", "--state", status, pane))
				},
			})
		})
	}
}

func listsAgent(out []byte, pane string) bool {
	var agents struct {
		Result struct {
			Agents []struct {
				PaneID string `json:"pane_id"`
			} `json:"agents"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &agents) != nil {
		return false
	}
	for _, a := range agents.Result.Agents {
		if a.PaneID == pane {
			return true
		}
	}
	return false
}
