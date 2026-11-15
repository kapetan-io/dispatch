package dispatch

import (
	"context"

	"github.com/kapetan-io/dispatch/internal/clock"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/panerecords"
)

// RexPanes is the user's panes on the Rex server.
type RexPanes interface {
	// Instance is the start of the id of the running Rex server process.
	Instance(ctx context.Context) (string, error)
	// Panes lists every block in a window of every session.
	Panes(ctx context.Context) ([]herdr.RexPane, error)
	// TypeLine types line into the pane and presses Enter.
	TypeLine(ctx context.Context, pane, line string) error
}

// userPane is the Rex pane the hook runs in, when it is one of the user's. A worker's pane is
// not, because the next launch for its tree resumes the session in its Worker label.
func userPane(opts Options) (string, bool) {
	session, block := opts.Env.Get("REX_SESSION"), opts.Env.Get("REX_BLOCK")
	if session == "" || block == "" || opts.Env.Get("AUTONOMOUS_WORKER") == "1" {
		return "", false
	}
	return session + "/" + block, true
}

// recordStart runs as a SessionStart hook. It writes the pane record of the Rex pane the session
// runs in, so a Rex restart can resume it there. It always writes, because a session resumed
// after a restart keeps its id and only the Rex server's instance says the record is current.
// It succeeds silently everywhere, because the hook runs in every session on the machine.
func recordStart(ctx context.Context, opts Options) error {
	input, ok := hookInput(opts)
	if !ok {
		return nil
	}
	ctx, cancel := clock.WithTimeout(ctx, opts.Clock, "record-start", stopTimeout)
	defer cancel()
	recordPane(ctx, opts, input, true)
	return nil
}

// recordPane writes the pane record from a SessionStart or Stop hook's input. Without always it
// leaves a record that already names the session, so a turn end costs one file read.
func recordPane(ctx context.Context, opts Options, input []byte, always bool) {
	pane, ok := userPane(opts)
	if !ok {
		return
	}
	session, cwd, ok := harness.ClaudeCode{}.Started(input)
	if !ok {
		return
	}
	if existing, found := panerecords.Read(opts.StateDir, pane); found && existing.Session == session && !always {
		return
	}
	instance, err := opts.RexPanes.Instance(ctx)
	if err != nil {
		return
	}
	_ = panerecords.Write(opts.StateDir, pane, panerecords.Record{Session: session, CWD: cwd, Instance: instance, At: opts.Clock.Now().UTC()})
}

// recordEnd runs as a SessionEnd hook. It removes the pane record when the user quit the session
// it names, so a later restore does not bring back a conversation the user closed. Any other
// end keeps the record, since a Rex server that dies ends the session too.
func recordEnd(opts Options) error {
	input, ok := hookInput(opts)
	if !ok {
		return nil
	}
	pane, ok := userPane(opts)
	if !ok {
		return nil
	}
	if session, quit, ok := (harness.ClaudeCode{}).Ended(input); ok && quit {
		_ = panerecords.RemoveIf(opts.StateDir, pane, session)
	}
	return nil
}
