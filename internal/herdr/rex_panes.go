package herdr

import (
	"context"
	"errors"
)

// RexPane is one block of the Rex server as `dispatch rex restore` reads it.
type RexPane struct {
	// ID is the pane id, `<session id>/<block id>`.
	ID string
	// Session and Window are the labels of the Rex session and window that hold the block.
	Session, Window string
	// AtPrompt is true when the block's foreground process is the block's own shell, so text
	// typed into it is read as a command.
	AtPrompt bool
	// Worker is true when the block's label is a Worker label.
	Worker bool
}

// Instance is the start of the id of the running Rex server process, as a Worker label holds it.
func (r *Rex) Instance(ctx context.Context) (string, error) {
	var status struct {
		Instance string `json:"instance_id"`
	}
	if err := r.api(ctx, "", "server.status", nil, &status); err != nil {
		return "", err
	}
	if status.Instance == "" {
		return "", errors.New("rex server.status named no instance id")
	}
	if len(status.Instance) > instanceLen {
		return status.Instance[:instanceLen], nil
	}
	return status.Instance, nil
}

// Panes lists every block in a window of every session. A block that goes away during the read is left out.
func (r *Rex) Panes(ctx context.Context) ([]RexPane, error) {
	sessions, err := r.sessions(ctx)
	if err != nil {
		return nil, err
	}
	var out []RexPane
	for _, s := range sessions {
		for _, w := range s.Windows {
			for _, b := range w.Blocks {
				proc, err := r.process(ctx, s.ID, b.ID)
				if rexGone(err) {
					continue
				}
				if err != nil {
					return nil, err
				}
				_, worker := parseWorkerLabel(b.Label)
				out = append(out, RexPane{ID: rexPane(s.ID, b.ID), Session: s.Label, Window: w.Label, AtPrompt: proc.atPrompt(), Worker: worker})
			}
		}
	}
	return out, nil
}

// atPrompt reports whether the foreground process is the block's own shell: the block's child
// itself, or the one process the child started, as a login shell is started by login.
func (p rexProcess) atPrompt() bool {
	if p.LastExit != nil || p.Foreground == nil || p.Child == nil {
		return false
	}
	if p.Foreground.PID == p.Child.PID {
		return true
	}
	return len(p.Ancestors) == 1 && p.Ancestors[0].PID == p.Child.PID
}

// TypeLine types line into the block at pane and presses Enter, as a person at its prompt does.
func (r *Rex) TypeLine(ctx context.Context, pane, line string) error {
	session, block, ok := splitRexPane(pane)
	if !ok {
		return errors.New(pane + " is not a Rex pane id")
	}
	for _, data := range []string{line, "\r"} {
		if err := r.blockCall(ctx, session, block, "write", map[string]any{"data": data}, nil); err != nil {
			return err
		}
	}
	return nil
}
