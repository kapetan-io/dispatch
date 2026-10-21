package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/stops"
)

// stopInput is the part of Claude Code's Stop hook input that dispatch reads.
// BackgroundTasks is a pointer so a missing field, which means a Claude Code
// version that predates it, differs from an empty list.
type stopInput struct {
	SessionID       string `json:"session_id"`
	CWD             string `json:"cwd"`
	BackgroundTasks *[]struct {
		Status string `json:"status"`
	} `json:"background_tasks"`
}

// recordStop runs as a Stop hook. It stores when the session ended its turn and
// how many background tasks were still running, and adds the turn to the session's log,
// for the nudge rules, the workspace label and the rules' turn log to read. It
// succeeds silently everywhere else, because the hook runs in every Claude
// session on the machine and a failure would show in all of them.
func recordStop(ctx context.Context, opts Options) error {
	var in stopInput
	if json.NewDecoder(opts.Stdin).Decode(&in) != nil {
		return nil
	}
	tree, ok := workerTree(ctx, opts, in.CWD)
	if !ok {
		return nil
	}
	rec := stops.Record{At: opts.Clock.Now().UTC(), Reported: in.BackgroundTasks != nil}
	if rec.Reported {
		for _, task := range *in.BackgroundTasks {
			if task.Status == "running" {
				rec.Running++
			}
		}
	}
	if err := stops.Write(opts.StateDir, tree, in.SessionID, rec); err != nil {
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch:", err)
	}
	turn := sessionlog.Turn{At: rec.At.UnixMilli(), Running: rec.Running > 0}
	if err := sessionlog.AppendTurn(opts.StateDir, tree, in.SessionID, turn); err != nil {
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch:", err)
	}
	return nil
}

// worktreeRoot returns the top of the working tree containing dir.
func worktreeRoot(ctx context.Context, dir string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	root, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	return root, err == nil
}
