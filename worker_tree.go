package dispatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/kapetan-io/dispatch/internal/config"
)

// workerTree returns the managed worktree a worker runs in when dir is inside one. The
// plugin's hooks run in every Claude session on the machine, so a session outside a pool
// tree of a repo listed under [repos] gets false and the hook stays silent.
func workerTree(ctx context.Context, opts Options, dir string) (string, bool) {
	cfg, err := config.Load(opts.ConfigPath, opts.Env)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	if err != nil {
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch:", err)
		return "", false
	}
	tree, ok := worktreeRoot(ctx, opts, dir)
	if !ok {
		return "", false
	}
	clone, ok := cloneRoot(ctx, opts, dir)
	// Workers run in treehouse worktrees, never in the clone itself, which
	// is where the supervising session works.
	if !ok || clone == tree {
		return "", false
	}
	if _, _, managed := repoAt(ctx, opts, cfg, dir); !managed {
		return "", false
	}
	return tree, true
}
