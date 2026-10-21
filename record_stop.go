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
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/loop"
)

// stopTimeout caps the wait on dispatch because the hook runs after every turn. A report lost
// to it costs little, since a worker with no record is treated as stopped when first seen idle.
const stopTimeout = time.Second

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

// recordStop runs as a Stop hook. It tells the running dispatch when the session ended its
// turn and how many background tasks were still running, for the rules to read. It
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
	report := loop.Stop{Tree: tree, Session: in.SessionID, At: opts.Clock.Now().UnixMilli(), Reported: in.BackgroundTasks != nil}
	if report.Reported {
		for _, task := range *in.BackgroundTasks {
			if task.Status == "running" {
				report.Running++
			}
		}
	}
	askCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	// The hook runs in every session, so a report dispatch can't take is dropped without a word
	// whether dispatch is not running, stuck or refuses it.
	_, _ = loop.Ask(askCtx, opts.StateDir, loop.Request{Op: loop.OpStop, Stop: &report})
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
