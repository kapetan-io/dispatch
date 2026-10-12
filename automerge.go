package dispatch

import (
	"context"
	"fmt"
	"strings"

	"github.com/kapetan-io/dispatch/internal/config"
)

func autoMergeSet(repos []string) map[string]bool {
	set := map[string]bool{}
	for _, r := range repos {
		set[strings.ToLower(r)] = true
	}
	return set
}

// autoMergeCheck warns once per process about an auto_merge entry that names
// none of the managed repos. It remembers each repo's name, because the name
// only changes when someone edits the clone's remotes.
type autoMergeCheck struct {
	names  map[string]string
	warned map[string]bool
}

func (a *autoMergeCheck) warn(ctx context.Context, opts Options, cfg config.Config) {
	if len(cfg.AutoMerge) == 0 {
		return
	}
	if a.names == nil {
		a.names, a.warned = map[string]string{}, map[string]bool{}
	}
	known := map[string]bool{}
	for label, dir := range cfg.Repos {
		name, ok := a.names[dir]
		if !ok {
			var err error
			if name, err = opts.PullRequests.RepoName(ctx, dir); err != nil {
				// An unresolved repo might be the one the entry names, so nothing is reported this tick.
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while resolving the GitHub repo of %s for auto_merge: %v\n", label, err)
				return
			}
			a.names[dir] = name
		}
		known[strings.ToLower(name)] = true
	}
	for _, entry := range cfg.AutoMerge {
		if known[strings.ToLower(entry)] || a.warned[entry] {
			continue
		}
		a.warned[entry] = true
		_, _ = fmt.Fprintf(opts.Stderr, "dispatch: warning: auto_merge entry %q matches no repo under [repos]\n", entry)
	}
}
