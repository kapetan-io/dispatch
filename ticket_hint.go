package dispatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kapetan-io/dispatch/internal/config"
)

const ticketHintText = "This repo is managed by dispatch. Before you create or rewrite a Linear ticket, load the dispatch:ticket skill and use it instead of linear-create, whether the user asked for the ticket or you are filing it yourself."

// ticketHint runs as a SessionStart hook. With a long skill list Claude Code
// shows some skills by name only, so the dispatch:ticket description alone
// cannot make it fire. It prints nothing and succeeds everywhere else, even with
// a missing or broken config, because the hook runs in every session on the machine.
func ticketHint(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.ConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		// A broken config must not put a hook error in every session.
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch:", err)
		return nil
	}
	if _, _, ok := repoAt(ctx, cfg, ""); ok {
		_, _ = fmt.Fprintln(opts.Stdout, ticketHintText)
	}
	return nil
}

// repoAt returns the label and settings of the configured repo whose clone
// holds dir, or the working directory when dir is empty. A worktree of the
// clone counts as inside it.
func repoAt(ctx context.Context, cfg config.Config, dir string) (string, config.Repo, bool) {
	clone, ok := cloneRoot(ctx, dir)
	if !ok {
		return "", config.Repo{}, false
	}
	for _, label := range cfg.Repos.Labels() {
		resolved, err := filepath.EvalSymlinks(cfg.Repos[label].Path)
		if err == nil && resolved == clone {
			return label, cfg.Repos[label], true
		}
	}
	return "", config.Repo{}, false
}

// cloneRoot returns the directory holding the .git of the repo containing dir,
// or of the working directory when dir is empty. A worktree shares its clone's
// git common dir, so both resolve to the clone. A bare repo has no such
// directory and never matches.
func cloneRoot(ctx context.Context, dir string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(strings.TrimSpace(string(out))))
	return root, err == nil
}
