package dispatch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/kapetan-io/dispatch/internal/loop"
)

// headPrefix is the shape of the head `dispatch review start` takes: 7 to 40 hex digits.
var headPrefix = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// reviewCommand reports a PR review to the running dispatch: start records the ticket and head a
// session reviews, done ends the ticket's review whoever started it. It checks only the shape of its
// arguments.
func reviewCommand(ctx context.Context, opts Options, args []string) error {
	req, err := reviewRequest(args)
	if err != nil {
		_, _ = fmt.Fprintln(opts.Stderr, usage)
		return err
	}
	resp, err := loop.Ask(ctx, opts.StateDir, req)
	if errors.Is(err, loop.ErrNotRunning) {
		return notRunning()
	}
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if req.Op == loop.OpReviewStart {
		_, _ = fmt.Fprintf(opts.Stdout, "reviewing %s at %s\n", req.Ticket, req.Head)
		return nil
	}
	_, _ = fmt.Fprintf(opts.Stdout, "review of %s done\n", req.Ticket)
	return nil
}

func reviewRequest(args []string) (loop.Request, error) {
	switch {
	case len(args) == 3 && args[0] == "start":
		ticket, head := strings.ToUpper(args[1]), strings.ToLower(args[2])
		if !ticketID.MatchString(ticket) {
			return loop.Request{}, fmt.Errorf("%q is not a ticket identifier such as ENG-123", args[1])
		}
		if !headPrefix.MatchString(head) {
			return loop.Request{}, fmt.Errorf("%q is not 7 to 40 hex digits of the PR's head commit", args[2])
		}
		return loop.Request{Op: loop.OpReviewStart, Ticket: ticket, Head: head}, nil
	case len(args) == 2 && args[0] == "done":
		ticket := strings.ToUpper(args[1])
		if !ticketID.MatchString(ticket) {
			return loop.Request{}, fmt.Errorf("%q is not a ticket identifier such as ENG-123", args[1])
		}
		return loop.Request{Op: loop.OpReviewDone, Ticket: ticket}, nil
	}
	return loop.Request{}, errors.New("review takes start, a ticket and the PR's head, or done and a ticket")
}
