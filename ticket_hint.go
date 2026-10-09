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
func ticketHint(_ context.Context, opts Options) error {
	cfg, err := config.Load(opts.ConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		// A broken config must not put a hook error in every session.
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch:", err)
		return nil
	}
	clone, ok := cloneRoot()
	if !ok {
		return nil
	}
	for _, dir := range cfg.Repos {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil && resolved == clone {
			_, _ = fmt.Fprintln(opts.Stdout, ticketHintText)
			return nil
		}
	}
	return nil
}

// cloneRoot returns the directory holding the .git of the repo containing the
// working directory. A worktree shares its clone's git common dir, so both
// resolve to the clone. A bare repo has no such directory and never matches.
func cloneRoot() (string, bool) {
	out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", false
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(strings.TrimSpace(string(out))))
	return root, err == nil
}
