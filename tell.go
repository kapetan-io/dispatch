package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/delivery"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// tell types text into a ticket's worker. It fails on a blocked worker, whose
// question the text would otherwise answer, and on an absent one.
func tell(ctx context.Context, opts Options, ticket, text string) error {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	snap, err := opts.Herdr.Read(ctx)
	if err != nil {
		return err
	}
	var trees []treehouse.Tree
	var unread []string
	for label, dir := range cfg.Repos {
		t, err := opts.Treehouse.Trees(ctx, dir)
		if err != nil {
			err = treehouse.StatusError(dir, err)
			unread = append(unread, fmt.Sprintf("%s (%v)", label, err))
			continue
		}
		trees = append(trees, t...)
	}
	w := worker.Find(ticket, trees, snap)
	switch w.State {
	case worker.None:
		sort.Strings(unread)
		msg := fmt.Sprintf("%s has no worker", ticket)
		if len(unread) > 0 {
			msg += "; could not read the pool of " + strings.Join(unread, ", ")
		}
		return fmt.Errorf("%s", msg)
	}
	err = delivery.Post{Herdr: opts.Herdr}.Nudge(ctx, w, text)
	if errors.Is(err, delivery.ErrBlocked) {
		return fmt.Errorf("%s's worker is blocked on a question; answer it in its herdr workspace", ticket)
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(opts.Stdout, "told %s\n", ticket)
	return nil
}
