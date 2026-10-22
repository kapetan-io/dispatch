package dispatch

import (
	"context"
	"fmt"

	"github.com/kapetan-io/dispatch/internal/config"
)

// repoSettings prints the settings table of the configured repo containing the
// working directory. Every failure is an error, because a worker acting on the
// wrong settings is worse than one that stops.
func repoSettings(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.ConfigPath, opts.Env)
	if err != nil {
		return err
	}
	labels, clone := reposAt(ctx, opts, cfg, "")
	if len(labels) > 1 {
		return fmt.Errorf("repos.%s and repos.%s both resolve to %s: give each repo its own clone", labels[0], labels[1], clone)
	}
	label, repo, ok := repoAt(ctx, opts, cfg, "")
	if !ok {
		return fmt.Errorf("%s is not inside a repo under [repos] in %s", opts.Dir, opts.ConfigPath)
	}
	table, err := repo.Encode(label)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(opts.Stdout, table)
	return err
}
