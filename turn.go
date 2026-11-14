package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/loop"
)

// turnCommand prints the turn a ticket's worker waits at and its closing message, read through
// the running loop. The message is written as the worker wrote it, with nothing added.
func turnCommand(ctx context.Context, opts Options, ticket string, asJSON bool) error {
	ticket = strings.ToUpper(ticket)
	resp, err := loop.Ask(ctx, opts.StateDir, loop.Request{Op: loop.OpTurn, Ticket: ticket})
	if errors.Is(err, loop.ErrNotRunning) {
		return notRunning()
	}
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if resp.Turn == nil {
		return fmt.Errorf("the loop answered %s with no turn", ticket)
	}
	if asJSON {
		return json.NewEncoder(opts.Stdout).Encode(struct {
			Ticket  string `json:"ticket"`
			At      int64  `json:"at"`
			Message string `json:"message"`
			Cut     bool   `json:"cut"`
		}{ticket, resp.Turn.At, resp.Turn.Message, resp.Turn.Cut})
	}
	_, err = fmt.Fprintf(opts.Stdout, "%s turn %d ended %s\n%s", ticket, resp.Turn.At,
		time.UnixMilli(resp.Turn.At).UTC().Format(time.RFC3339), resp.Turn.Message)
	return err
}
