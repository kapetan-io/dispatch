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
	"github.com/kapetan-io/dispatch/internal/stops"
)

// stopInput is the part of Claude Code's Stop hook input that dispatch reads.
// BackgroundTasks is a pointer so a missing field, which means Claude Code
// could not read its task registry, differs from an empty list.
type stopInput struct {
	SessionID       string `json:"session_id"`
	CWD             string `json:"cwd"`
	BackgroundTasks *[]struct {
		Status string `json:"status"`
	} `json:"background_tasks"`
}

// recordStop runs as a Stop hook. It stores when the session ended its turn and
// how many background tasks were still running, for the rules to read. It
// succeeds silently everywhere else, because the hook runs in every Claude
// session on the machine and a failure would show in all of them.
func recordStop(ctx context.Context, opts Options) error {
	var in stopInput
	if json.NewDecoder(opts.Stdin).Decode(&in) != nil {
		return nil
	}
	cfg, err := config.Load(opts.ConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch:", err)
		return nil
	}
	tree, ok := worktreeRoot(ctx, in.CWD)
	if !ok {
		return nil
	}
	clone, ok := cloneRoot(ctx, in.CWD)
	// Workers run in treehouse worktrees, never in the clone itself, which
	// is where the supervising session works.
	if !ok || clone == tree {
		return nil
	}
	if _, _, managed := repoAt(ctx, cfg, in.CWD); !managed {
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
