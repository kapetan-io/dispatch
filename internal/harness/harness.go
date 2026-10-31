// Package harness is the program that runs in a worker's session, such as
// Claude Code.
package harness

import (
	"fmt"
	"maps"
	"slices"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

// Harness is the program that runs in a session. Dispatch reaches a worker's
// program through herdr, so this port says what herdr runs.
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
}

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
