// Package harnesstest is the fake of a harness, and the scenarios every harness
// adapter must pass.
package harnesstest

import (
	"regexp"

	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
)

// Kind is the config kind and herdr agent kind the fake registers under.
const Kind = "fake"

// Harness is a program that is not Claude Code, so a test can launch something whose
// kind no code path special-cases.
type Harness struct{}

// New returns the fake harness.
func New() *Harness { return &Harness{} }

func (*Harness) AgentKind() string { return Kind }

func (*Harness) Program(s harness.Start) herdr.Program {
	return herdr.Program{
		Name:      "Fake",
		AgentKind: Kind,
		Argv:      []string{"--model", s.Model},
		Startup:   herdr.Startup{Ready: regexp.MustCompile(`>`)},
	}
}
