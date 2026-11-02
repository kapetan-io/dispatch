package dispatch

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/ticket"
)

// repeated is a flag that may be given several times.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

// pairs reads KEY=VALUE arguments, splitting at the first =.
func pairs(args []string) (map[string]string, error) {
	var out map[string]string
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%q is not KEY=VALUE", a)
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	return out, nil
}

// ticketCommand sends one `dispatch ticket` verb to the running dispatch, the only process that opens
// the local ticket database.
func ticketCommand(ctx context.Context, opts Options, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(opts.Stderr, usage)
		return errors.New("ticket takes a verb: create, show, list, move, comment, edit or statuses")
	}
	verb, args := args[0], args[1:]
	fs := flag.NewFlagSet("ticket "+verb, flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	call := loop.TicketCall{Verb: verb}

	// Verbs that name a ticket take its id first, so the flags after it still parse.
	switch verb {
	case loop.VerbShow, loop.VerbMove, loop.VerbComment, loop.VerbEdit:
		if len(args) == 0 {
			return fmt.Errorf("ticket %s takes a ticket id such as LOC-3", verb)
		}
		call.ID, args = strings.ToUpper(args[0]), args[1:]
		if ticket.SystemOf(call.ID) != ticket.SystemLocal {
			return fmt.Errorf("%s is a Linear ticket; use the Linear tools", call.ID)
		}
	}

	var (
		repo, title, description, status, parent *string
		labels, attrs, blockers                  repeated
		startPR                                  *int
		all                                      *bool
		addLabels, removeLabels, unsetAttrs      repeated
	)
	switch verb {
	case loop.VerbCreate:
		repo, title, description = fs.String("repo", "", "the [repos.<label>] label of the ticket's repo"), fs.String("title", "", "the title"), fs.String("description", "", "the description")
		status, parent = fs.String("status", "", "the status to start in; defaults to the status doing todo"), fs.String("parent", "", "the parent ticket, LOC-<n>")
		fs.Var(&labels, "label", "a label; repeatable")
		fs.Var(&attrs, "attr", "KEY=VALUE; repeatable")
		fs.Var(&blockers, "blocked-by", "a ticket that blocks this one, LOC-<n>; repeatable")
		startPR = fs.Int("start-pr", 0, "a PR in the ticket's repo; stored and shown, and does nothing yet")
	case loop.VerbList:
		status = fs.String("status", "", "only tickets in this status")
		fs.Var(&labels, "label", "only tickets with this label; repeatable")
		fs.Var(&attrs, "attr", "only tickets with KEY=VALUE; repeatable")
		all = fs.Bool("all", false, "include tickets in a finished status")
	case loop.VerbEdit:
		title, description, repo = fs.String("title", "", "the title"), fs.String("description", "", "the description"), fs.String("repo", "", "the [repos.<label>] label of the ticket's repo")
		fs.Var(&addLabels, "add-label", "add a label; repeatable")
		fs.Var(&removeLabels, "remove-label", "remove a label; repeatable")
		fs.Var(&attrs, "attr", "set KEY=VALUE; repeatable")
		fs.Var(&unsetAttrs, "unset-attr", "remove an attribute; repeatable")
		parent = fs.String("parent", "", `the parent ticket, LOC-<n>; "" clears`)
		fs.Var(&blockers, "blocked-by", `replaces the blockers with this one, LOC-<n>; repeatable; "" clears`)
		startPR = fs.Int("start-pr", 0, "a PR in the ticket's repo; 0 clears")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	rest := fs.Args()

	want := func(n int) error {
		if len(rest) != n {
			return fmt.Errorf("ticket %s takes %d argument(s), got %d", verb, n, len(rest))
		}
		return nil
	}
	switch verb {
	case loop.VerbStatuses, loop.VerbShow:
		if err := want(0); err != nil {
			return err
		}
	case loop.VerbMove:
		if err := want(1); err != nil {
			return err
		}
		call.Status = rest[0]
	case loop.VerbComment:
		if err := want(1); err != nil {
			return err
		}
		call.Body = rest[0]
		call.Tree, _ = workerTree(ctx, opts, opts.Dir)
	case loop.VerbCreate:
		if err := want(0); err != nil {
			return err
		}
		if *repo == "" || *title == "" {
			return errors.New("ticket create needs --repo and --title")
		}
		attributes, err := pairs(attrs)
		if err != nil {
			return err
		}
		call.Draft = &localticket.Draft{
			Title: *title, Description: *description, Repo: *repo, Status: *status, Labels: labels, Attributes: attributes,
			Parent: *parent, BlockedBy: blockers, StartPR: *startPR,
		}
	case loop.VerbList:
		if err := want(0); err != nil {
			return err
		}
		attributes, err := pairs(attrs)
		if err != nil {
			return err
		}
		// A status that names a finished status lists those tickets, so --all only matters without one.
		call.Filter = &localticket.Filter{Status: *status, Labels: labels, Attributes: attributes, Open: !*all && *status == ""}
	case loop.VerbEdit:
		if err := want(0); err != nil {
			return err
		}
		set, err := pairs(attrs)
		if err != nil {
			return err
		}
		e := localticket.Edit{AddLabels: addLabels, RemoveLabels: removeLabels, SetAttributes: set, UnsetAttributes: unsetAttrs}
		if given["title"] {
			e.Title = title
		}
		if given["description"] {
			e.Description = description
		}
		if given["repo"] {
			e.Repo = repo
		}
		if given["parent"] {
			e.Parent = parent
		}
		if given["start-pr"] {
			e.StartPR = startPR
		}
		if given["blocked-by"] {
			list := []string{}
			for _, b := range blockers {
				if b != "" {
					list = append(list, b)
				}
			}
			e.BlockedBy = &list
		}
		call.Edit = &e
	default:
		_, _ = fmt.Fprintln(opts.Stderr, usage)
		return fmt.Errorf("unknown ticket verb %q", verb)
	}

	resp, err := loop.Ask(ctx, opts.StateDir, loop.Request{Op: loop.OpTicket, Call: &call})
	if errors.Is(err, loop.ErrNotRunning) {
		return notRunning()
	}
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	for _, line := range resp.Lines {
		_, _ = fmt.Fprintln(opts.Stdout, line)
	}
	return nil
}
