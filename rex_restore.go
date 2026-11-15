package dispatch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/clock"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/panerecords"
)

// restoreWait is how long restore waits for the sessions it resumed to take their panes.
const restoreWait = 15 * time.Second

// sessionID is what a harness session id may hold, so a record can never type shell syntax.
var sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func rexCommand(ctx context.Context, opts Options, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "restore":
		return rexRestore(ctx, opts, false)
	case len(args) == 2 && args[0] == "restore" && args[1] == "--dry-run":
		return rexRestore(ctx, opts, true)
	}
	_, _ = fmt.Fprintln(opts.Stderr, usage)
	return errors.New("rex takes restore, or restore --dry-run")
}

// resumeLine is what restore types at a pane's prompt. claude is whatever the pane's shell
// finds, which for a session started through dispatch's shim adds the plugin back. ok is false
// when the line cannot be quoted the same way for every shell.
func resumeLine(r panerecords.Record) (line string, ok bool) {
	line = "cd '" + r.CWD + "' && claude --resume " + r.Session
	return line, r.CWD != "" && !strings.ContainsAny(r.CWD, "'\\\n") && sessionID.MatchString(r.Session)
}

// rexRestore resumes each recorded session in the pane it ran in, after a Rex restart brought
// the pane back as a shell. It types only into a pane at its prompt whose record is older than
// the running server, so a second run, or a run with no restart, types nothing.
func rexRestore(ctx context.Context, opts Options, dryRun bool) error {
	instance, err := opts.RexPanes.Instance(ctx)
	if err != nil {
		return err
	}
	panes, err := opts.RexPanes.Panes(ctx)
	if err != nil {
		return err
	}
	records, err := panerecords.All(opts.StateDir)
	if err != nil {
		return err
	}
	byID := map[string]herdr.RexPane{}
	for _, p := range panes {
		byID[p.ID] = p
	}
	say := func(what, where, rest string) {
		_, _ = fmt.Fprintln(opts.Stdout, strings.TrimRight(fmt.Sprintf("%-13s %s  %s", what, where, rest), " "))
	}
	failed := false
	var typed []panerecords.Stored
	for _, rec := range records {
		if rec.Err != nil {
			say("skipped", rec.Path, "unreadable: "+rec.Err.Error())
			continue
		}
		line, quotable := resumeLine(rec.Record)
		pane, found := byID[rec.Pane]
		where := pane.Session + " / " + pane.Window
		switch {
		case !found:
			say("gone", rec.Pane, "run: "+line)
			if !dryRun {
				_ = panerecords.Remove(opts.StateDir, rec.Pane)
			}
		case pane.Worker:
			say("worker", where, "")
		case !pane.AtPrompt:
			say("busy", where, "")
		case rec.Instance == instance:
			say("idle", where, "run: "+line)
		case !quotable:
			say("skipped", where, "run: "+line)
		case dryRun:
			say("would resume", where, rec.Session)
		default:
			if err := opts.RexPanes.TypeLine(ctx, rec.Pane, line); err != nil {
				say("failed", where, err.Error())
				failed = true
				continue
			}
			typed = append(typed, rec)
		}
	}
	for _, rec := range typed {
		pane := byID[rec.Pane]
		if started(ctx, opts, rec.Pane) {
			say("resumed", pane.Session+" / "+pane.Window, rec.Session)
			continue
		}
		say("not started", pane.Session+" / "+pane.Window, rec.Session)
		failed = true
	}
	if failed {
		return errors.New("some sessions were not resumed")
	}
	return nil
}

// started waits until the pane is no longer at its prompt, which is the typed command running.
func started(ctx context.Context, opts Options, pane string) bool {
	ctx, cancel := clock.WithTimeout(ctx, opts.Clock, "rex-restore", restoreWait)
	defer cancel()
	for {
		panes, err := opts.RexPanes.Panes(ctx)
		if err == nil {
			for _, p := range panes {
				if p.ID == pane && !p.AtPrompt {
					return true
				}
			}
		}
		pace := opts.Clock.NewTimer(max(opts.LaunchPace, 100*time.Millisecond))
		select {
		case <-ctx.Done():
			pace.Stop()
			return false
		case <-pace.C():
		}
	}
}
