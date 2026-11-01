// Package harness is the program that runs in a worker's session, such as
// Claude Code.
package harness

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// Harness is the program that runs in a session. Dispatch reaches a worker's
// program through herdr, so this port says what herdr runs.
//
// Every adapter keeps these:
//   - Its reports carry the session id herdr reports as Agent.Session.
//   - A prompt report's text is exactly as typed, before expansion.
//   - A program that can't say whether background tasks run reports
//     Reported=false, never Running=0.
//   - Every adapter implements Follower and Deliver, since receiving follower
//     messages is required of every harness.
//   - Deliver never answers an open question and is never retried.
//
// Program's Startup must list every screen the program shows before it takes
// input. Kickoff types the kickoff only once Ready matches, so an unlisted
// dialog stalls the launch.
type Harness interface {
	// AgentKind is the herdr agent kind this harness is detected as.
	AgentKind() string
	// Program is how herdr runs this harness for one worker. It touches nothing;
	// the same Start gives the same Program.
	Program(s Start) herdr.Program
	// Stop parses a turn-ended report from the program's own hook or extension
	// input: session id, whether and how many background tasks still run, and the
	// working directory, which the caller maps to a tree. The caller sets Tree and
	// At. ok=false means the input is not a turn end and is dropped without a word.
	Stop(input []byte) (report sessionlog.Stop, dir string, ok bool)
	// Prompt parses a report that the session was typed into or went idle: session
	// id, working directory, and the text exactly as typed, empty when the program
	// went idle. The caller records a command when the text is one and wakes the
	// loop otherwise. ok=false as for Stop.
	Prompt(input []byte) (report sessionlog.Prompt, dir string, ok bool)
	// Follower reads, from the environment of a command running inside a session,
	// where this harness delivers that session's messages. A session of another
	// harness gets an error wrapped as fmt.Errorf("%w: reason", ErrNotThisHarness), where the
	// reason tells the user how to run the command from this harness's session.
	Follower(env environment.Environment) (address string, err error)
	// Deliver puts text into the follower session at address as a message the model
	// reads, waking an idle session. It never lands as the answer to an open
	// question. ErrGone means the session exited, so the follower is dropped.
	Deliver(ctx context.Context, address, text string) error
}

var (
	// ErrNotThisHarness is what Follower returns for a session that belongs to another harness.
	ErrNotThisHarness = errors.New("not a session of this harness")
	// ErrGone is what Deliver returns when the follower session has exited.
	ErrGone = errors.New("the follower session has exited")
)

// Start is what a harness needs to describe one worker's program.
type Start struct {
	Model      string
	PluginRoot string
}

// Registry maps the kind a [harness.<name>] table names, such as "claude-code",
// to its adapter. It is not herdr's agent kind, which Program carries.
type Registry map[string]Harness

// Kinds lists the kinds the registry has an adapter for, in no fixed order.
func (r Registry) Kinds() []string {
	return slices.Collect(maps.Keys(r))
}

// Program describes how herdr runs the harness of kind with model.
func (r Registry) Program(kind, model, pluginRoot string) (herdr.Program, error) {
	h, ok := r[kind]
	if !ok {
		return herdr.Program{}, fmt.Errorf("no harness adapter for kind %q", kind)
	}
	return h.Program(Start{Model: model, PluginRoot: pluginRoot}), nil
}

// ForAgent returns the harness herdr detects as agentKind.
func (r Registry) ForAgent(agentKind string) (Harness, bool) {
	for _, h := range r {
		if h.AgentKind() == agentKind {
			return h, true
		}
	}
	return nil, false
}

// AgentKinds lists the herdr agent kinds of the registered harnesses, in no fixed order.
func (r Registry) AgentKinds() []string {
	var kinds []string
	for _, h := range r {
		kinds = append(kinds, h.AgentKind())
	}
	return kinds
}

// Follower asks each adapter whether the calling session is one of its own and
// returns the first that claims it, with the address it delivers to. A session no
// adapter recognizes gets each adapter's reason, so the user learns which harnesses
// dispatch drives.
func (r Registry) Follower(env environment.Environment) (string, string, error) {
	kinds := r.Kinds()
	slices.Sort(kinds)
	var reasons []string
	for _, kind := range kinds {
		address, err := r[kind].Follower(env)
		if err == nil {
			return kind, address, nil
		}
		if !errors.Is(err, ErrNotThisHarness) {
			return "", "", err
		}
		reasons = append(reasons, strings.TrimPrefix(err.Error(), ErrNotThisHarness.Error()+": "))
	}
	if len(reasons) == 1 {
		return "", "", errors.New(reasons[0])
	}
	return "", "", fmt.Errorf("this is not a session of a harness dispatch drives: %s", strings.Join(reasons, "; "))
}
