package herdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync/atomic"
	"time"
)

var _ Stream = (*Rex)(nil)

// ErrRexEventsEnded means the rex events process ended on its own, with the server still expected to run.
var ErrRexEventsEnded = errors.New("rex events ended: the server disconnected")

const (
	// eventLineMax bounds one event line. A session event can carry a screen's worth of record text.
	eventLineMax = 1 << 20
	// liveProbeEvery spaces the calls that prove the subscription is live.
	liveProbeEvery = 50 * time.Millisecond
	// liveWait bounds how long Changes waits for that proof before it names the panes anyway.
	liveWait = 3 * time.Second
)

// rexEvent is one line of `rex events --all --json`.
type rexEvent struct {
	Type    string `json:"type"`
	Payload struct {
		Session string `json:"session_id"`
		Event   struct {
			Type    string `json:"type"`
			Payload struct {
				Block string `json:"block_id"`
				Name  string `json:"name"`
			} `json:"payload"`
		} `json:"event"`
	} `json:"payload"`
}

// pane is the pane the line names, or "" when the line is not a change of a block's agent.
func (e rexEvent) pane() string {
	if e.Type != "session_event" || e.Payload.Session == "" || e.Payload.Event.Payload.Block == "" {
		return ""
	}
	switch e.Payload.Event.Type {
	case "block_closed":
		return rexPane(e.Payload.Session, e.Payload.Event.Payload.Block)
	case "block_event":
		switch e.Payload.Event.Payload.Name {
		case "program_status_changed", "program_status_removed", "child_exited":
			return rexPane(e.Payload.Session, e.Payload.Event.Payload.Block)
		}
	}
	return ""
}

// Changes runs one long-lived `rex events` and names the pane of each status change, program exit and
// block close it reports. A line that is not one of those, or does not parse, is skipped. process_changed
// is skipped too: a worker's Claude ends as child_exited, and a tool starting under Claude would only cost
// reads that find Claude still running.
func (r *Rex) Changes(ctx context.Context, changed func(pane string)) error {
	bin, err := r.find()
	if err != nil {
		return err
	}
	argv := []string{"--autostart=false", "events", "--all", "--json"}
	if r.socket != "" {
		argv = append([]string{"-S", r.socket}, argv...)
	}
	cmd := r.env.Command(ctx, bin, argv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// A rex wrapped in a script leaves a child holding stdout after the kill, which WaitDelay cuts off.
	cmd.WaitDelay = time.Second
	pipeOut, pipeIn := io.Pipe()
	cmd.Stdout = pipeIn
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("while running rex events: %w", err)
	}
	waited := make(chan error, 1)
	go func() {
		waited <- cmd.Wait()
		_ = pipeIn.Close()
	}()
	var seen atomic.Bool
	lines := make(chan error, 1)
	go func() { lines <- r.relay(pipeOut, changed, &seen) }()
	probe, stopProbe := context.WithCancel(ctx)
	defer stopProbe()
	live := make(chan struct{})
	go func() {
		defer close(live)
		r.awaitLive(probe, &seen)
	}()
	var waitErr error
	select {
	case <-live:
		if ctx.Err() == nil {
			// A listing that fails is the next resync's to find, because its full read fails the same way.
			if panes, err := r.workerPanes(ctx); err == nil {
				for _, pane := range panes {
					changed(pane)
				}
			}
		}
		waitErr = <-waited
	case waitErr = <-waited:
	}
	stopProbe()
	return r.ended(ctx, waitErr, <-lines, &stderr)
}

// ended maps the end of the rex events process to the port's return.
func (r *Rex) ended(ctx context.Context, waitErr, readErr error, stderr *bytes.Buffer) error {
	if ctx.Err() != nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) && exit.ExitCode() == exitNotRunning {
		return ErrRexNotRunning
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("while reading rex events: %w", readErr)
	}
	if text := string(bytes.TrimSpace(stderr.Bytes())); waitErr != nil && text != "" {
		return fmt.Errorf("rex events ended: %s", text)
	}
	return ErrRexEventsEnded
}

// relay names the pane of each event line until the stream ends. seen turns true on the first line of
// any kind, which is how awaitLive knows the subscription works.
func (r *Rex) relay(in io.Reader, changed func(pane string), seen *atomic.Bool) error {
	reader := bufio.NewReaderSize(in, 64*1024)
	var line []byte
	var skipping bool
	for {
		part, more, err := reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.EOF
			}
			return err
		}
		seen.Store(true)
		// A line past the cap is dropped to its end, because stopping to read would stall rex.
		if !skipping && len(line)+len(part) > eventLineMax {
			skipping = true
		}
		if !skipping {
			line = append(line, part...)
		}
		if more {
			continue
		}
		var event rexEvent
		if !skipping && json.Unmarshal(line, &event) == nil {
			if pane := event.pane(); pane != "" {
				changed(pane)
			}
		}
		line, skipping = line[:0], false
	}
}

// awaitLive returns once the subscription has shown it works, or after liveWait. rex events prints
// nothing on subscribing, but with --all it prints client_joined for each client that connects, so a call
// of ours that gets a line back proves events reach this listener.
func (r *Rex) awaitLive(ctx context.Context, seen *atomic.Bool) {
	deadline := time.After(liveWait)
	for !seen.Load() {
		_ = r.api(ctx, "", "server.status", nil, nil)
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			return
		case <-time.After(liveProbeEvery):
		}
	}
}

// workerPanes lists the panes of every worker block.
func (r *Rex) workerPanes(ctx context.Context) ([]string, error) {
	sessions, err := r.sessions(ctx)
	if err != nil {
		return nil, err
	}
	var panes []string
	for _, session := range sessions {
		for _, window := range session.Windows {
			blocks, _ := window.workers()
			for _, b := range blocks {
				panes = append(panes, rexPane(session.ID, b.ID))
			}
		}
	}
	return panes, nil
}
