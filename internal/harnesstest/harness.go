// Package harnesstest is the fake of a harness, and the scenarios every harness
// adapter must pass.
package harnesstest

import (
	"encoding/json"
	"regexp"

	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
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

// Stop reads the fake's turn-end input, {"sid":"s","where":"/dir","tasks":2}. It is not
// Claude Code's form, so a report only parses through the fake when the root picked the fake.
func (*Harness) Stop(input []byte) (sessionlog.Stop, string, bool) {
	var in struct {
		SID   string `json:"sid"`
		Where string `json:"where"`
		Tasks *int   `json:"tasks"`
	}
	if json.Unmarshal(input, &in) != nil || in.SID == "" {
		return sessionlog.Stop{}, "", false
	}
	report := sessionlog.Stop{Session: in.SID, Reported: in.Tasks != nil}
	if in.Tasks != nil {
		report.Running = *in.Tasks
	}
	return report, in.Where, true
}

// Prompt reads the fake's typed-text input, {"sid":"s","where":"/dir","typed":"text"}.
func (*Harness) Prompt(input []byte) (sessionlog.Prompt, string, bool) {
	var in struct {
		SID   string `json:"sid"`
		Where string `json:"where"`
		Typed string `json:"typed"`
	}
	if json.Unmarshal(input, &in) != nil || in.SID == "" {
		return sessionlog.Prompt{}, "", false
	}
	return sessionlog.Prompt{Session: in.SID, Text: in.Typed}, in.Where, true
}

// StopInput builds the fake's turn-end input for Stop.
func StopInput(session, dir string, tasks *int) []byte {
	in := map[string]any{"sid": session, "where": dir}
	if tasks != nil {
		in["tasks"] = *tasks
	}
	b, _ := json.Marshal(in)
	return b
}

// PromptInput builds the fake's typed-text input for Prompt.
func PromptInput(session, dir, text string) []byte {
	b, _ := json.Marshal(map[string]any{"sid": session, "where": dir, "typed": text})
	return b
}
