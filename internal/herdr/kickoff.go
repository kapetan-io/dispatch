package herdr

import (
	"context"
	"fmt"
	"strings"
)

// observed is what a host shows of the worker's terminal at one read.
type observed struct {
	// Alive is false once the terminal is gone or its program finished.
	Alive bool
	// Agent is true when a program of an agent kind runs there.
	Agent bool
	// Status is idle, done, working, blocked or unknown.
	Status string
	// Turn is a token that only grows when the program takes a turn. A host with no turn
	// counter uses the time of the program's last report.
	Turn uint64
}

// sendOutcome is what a host knows of a send of the kickoff text.
type sendOutcome int

const (
	// sendStarted means the host saw a turn begin.
	sendStarted sendOutcome = iota
	// sendNotStarted means nothing was typed, because the status moved before the text went in.
	sendNotStarted
	// sendUnknown means the text may be typed and submitted, and the host can't tell whether a turn began.
	sendUnknown
)

type sent struct {
	Outcome sendOutcome
	// Turn is the host's turn token as of the send, so a later read past it shows the text ran.
	Turn uint64
}

// kickoffHost is the four operations a host gives the kickoff driver. The driver never learns which host it drives.
type kickoffHost interface {
	// read shows the terminal. An error leaves the terminal's state unknown and the driver reads again.
	read(ctx context.Context) (observed, error)
	// screen is the visible screen plus history lines above it, joined on one line, or "" when it can't be read.
	screen(ctx context.Context, history int) string
	// press sends one list of keys together, named as a startup Try names them.
	press(ctx context.Context, keys []string)
	// send types text and submits it when status, the status last read, still holds.
	send(ctx context.Context, text, status string) (sent, error)
}

// kickoff types text into the program once its input box is ready, answering the startup
// dialogs, and returns once the host shows the program running it.
func (w workLog) kickoff(ctx context.Context, h kickoffHost, text string, startup Startup) error {
	sightings := make([]int, len(startup.Answers))
	var seen observed
	ready := false
	for range kickoffChecks {
		if ctx.Err() != nil {
			return ErrKickoffUnconfirmed
		}
		o, err := h.read(ctx)
		if err == nil && !o.Alive {
			return ErrKickoffUnconfirmed
		}
		if err == nil && o.Agent {
			switch o.Status {
			case "working":
				return nil
			case "blocked":
				return ErrKickoffUnconfirmed
			case "idle", "done":
				seen, ready = o, true
			default:
				w.answer(ctx, h, startup, sightings)
			}
		}
		if ready {
			break
		}
		w.sleep(ctx, w.pace)
	}
	if !ready {
		return ErrKickoffUnconfirmed
	}
	for try := 1; try <= kickoffTries; try++ {
		w.record("typing", fmt.Sprintf("%s (try %d)", text, try))
		status := seen.Status
		res, err := h.send(ctx, text, status)
		if err != nil {
			return ErrKickoffUnconfirmed
		}
		switch res.Outcome {
		case sendStarted:
			return nil
		case sendNotStarted:
			// Nothing was typed, so the worker leaving idle is no confirmation. Another try is
			// safe unless the worker now needs a person or says nothing.
			if seen, err = h.read(ctx); err != nil || !seen.Alive {
				return ErrKickoffUnconfirmed
			}
			if seen.Status == "blocked" || seen.Status == "unknown" {
				return ErrKickoffUnconfirmed
			}
			continue
		}
		// The host could not tell whether the text started a turn, so give the status time to move before judging.
		if seen, err = w.awaitLeft(ctx, h, status); err != nil || !seen.Alive {
			return ErrKickoffUnconfirmed
		}
		now := seen.Status
		// A turn token past the send's own means the text ran, whatever the status reads now.
		ran := seen.Turn > res.Turn
		// Done before the text went in and still done is no sign it ran, so treat it like an idle prompt.
		switch {
		case ran || now == "working" || now == "blocked" || (now == "done" && status == "idle"):
			return nil
		case now == "idle" || now == "done":
			// Still at the prompt: press Enter if the text sits in the input box, else retype.
			if strings.Contains(h.screen(ctx, 30), probeOf(text)) {
				h.press(ctx, []string{"Enter"})
				if seen, err = w.awaitLeft(ctx, h, now); err != nil || !seen.Alive {
					return ErrKickoffUnconfirmed
				}
				after := seen.Status
				if seen.Turn > res.Turn || after == "working" || after == "blocked" || (after == "done" && now == "idle") {
					return nil
				}
				return ErrKickoffUnconfirmed
			}
		default:
			return ErrKickoffUnconfirmed
		}
	}
	return ErrKickoffUnconfirmed
}

// awaitLeft reads until the status is no longer from, for up to kickoffWaits reads, and returns the last read.
func (w workLog) awaitLeft(ctx context.Context, h kickoffHost, from string) (observed, error) {
	var o observed
	var err error
	for range kickoffWaits {
		if o, err = h.read(ctx); err != nil || !o.Alive || o.Status != from {
			return o, err
		}
		w.sleep(ctx, w.pace)
	}
	return o, err
}

// answer sends the next try for the startup dialog the screen shows, and nothing when it shows none.
func (w workLog) answer(ctx context.Context, h kickoffHost, startup Startup, sightings []int) {
	screen := h.screen(ctx, 0)
	w.record("dialogs", screen)
	try, ok := startup.Answer(screen, sightings)
	if !ok {
		return
	}
	for _, keys := range try {
		h.press(ctx, keys)
	}
}
