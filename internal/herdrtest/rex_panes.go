package herdrtest

import (
	"context"
	"slices"
	"sync"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

// RexPanes is an in-process stand-in for the panes of a Rex server: it lists the panes a test
// gave it, keeps what is typed into each, and starts a program in a pane a line is typed into.
type RexPanes struct {
	mu       sync.Mutex
	instance string
	down     bool
	panes    []herdr.RexPane
	typed    map[string][]string
	stuck    map[string]bool
}

// NewRexPanes is a server whose process has the instance given and that holds no pane.
func NewRexPanes(instance string) *RexPanes {
	return &RexPanes{instance: instance, typed: map[string][]string{}, stuck: map[string]bool{}}
}

// Add puts a pane on the server.
func (r *RexPanes) Add(pane herdr.RexPane) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.panes = append(r.panes, pane)
}

// Restart gives the server a new instance and brings every pane back at its prompt, as a Rex restart does.
func (r *RexPanes) Restart(instance string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instance = instance
	for i := range r.panes {
		r.panes[i].AtPrompt = true
	}
}

// Down makes every call fail with herdr.ErrRexNotRunning, as a stopped server does.
func (r *RexPanes) Down() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.down = true
}

// Stuck makes the pane stay at its prompt when a line is typed, as a shell that lost the line does.
func (r *RexPanes) Stuck(pane string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stuck[pane] = true
}

// Typed is every line typed into the pane, in order.
func (r *RexPanes) Typed(pane string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.typed[pane])
}

func (r *RexPanes) Instance(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.down {
		return "", herdr.ErrRexNotRunning
	}
	return r.instance, nil
}

func (r *RexPanes) Panes(context.Context) ([]herdr.RexPane, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.down {
		return nil, herdr.ErrRexNotRunning
	}
	return slices.Clone(r.panes), nil
}

func (r *RexPanes) TypeLine(_ context.Context, pane, line string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.down {
		return herdr.ErrRexNotRunning
	}
	for i := range r.panes {
		if r.panes[i].ID != pane {
			continue
		}
		r.typed[pane] = append(r.typed[pane], line)
		r.panes[i].AtPrompt = r.stuck[pane]
		return nil
	}
	return &herdr.Refusal{Code: "not_found", Message: "block " + pane + " not found", Command: "rex:block.method"}
}
