package dispatch

import (
	"context"
	"fmt"
	"os"

	"github.com/kapetan-io/dispatch/internal/config"
)

// repoSettings prints the settings table of the configured repo containing the
// working directory. Every failure is an error, because a worker acting on the
// wrong settings is worse than one that stops.
func repoSettings(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	label, repo, ok := repoAt(ctx, cfg, "")
	if !ok {
		dir, _ := os.Getwd()
		return fmt.Errorf("%s is not inside a repo under [repos] in %s", dir, opts.ConfigPath)
	}
	table, err := repo.Encode(label)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(opts.Stdout, table)
	return err
}
