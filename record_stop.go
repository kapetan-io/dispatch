package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kapetan-io/dispatch/internal/config"
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
// how many background tasks were still running, and adds the turn to the
// session's log, for the rules to read. It
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

// workerTree returns the managed worktree a worker runs in when dir is inside one. The
// plugin's hooks run in every Claude session on the machine, so a session outside a pool
// tree of a repo listed under [repos] gets false and the hook stays silent.
func workerTree(ctx context.Context, opts Options, dir string) (string, bool) {
	cfg, err := config.Load(opts.ConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	if err != nil {
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch:", err)
		return "", false
	}
	tree, ok := worktreeRoot(ctx, dir)
	if !ok {
		return "", false
	}
	clone, ok := cloneRoot(ctx, dir)
	// Workers run in treehouse worktrees, never in the clone itself, which
	// is where the supervising session works.
	if !ok || clone == tree {
		return "", false
	}
	if _, _, managed := repoAt(ctx, cfg, dir); !managed {
		return "", false
	}
	return tree, true
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
