// Package harnesstest is the fake of a harness, and the scenarios every harness
// adapter must pass.
package harnesstest

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// Kind is the config kind and herdr agent kind the fake registers under.
const Kind = "fake"

// Harness is a program that is not Claude Code, so a test can launch something whose
// kind no code path special-cases.
type Harness struct {
	mu      sync.Mutex
	inboxes map[string]*inbox
}

type inbox struct {
	exited    bool
	delivered []string
}

// InboxEnv names the variable that carries a fake session's id.
const InboxEnv = "FAKE_HARNESS_INBOX"

// New returns the fake harness.
func New() *Harness { return &Harness{inboxes: map[string]*inbox{}} }

// Open starts a fake session with an inbox and returns its id, which Deliver takes.
func (h *Harness) Open() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	address := fmt.Sprintf("fake-session-%d", len(h.inboxes)+1)
	h.inboxes[address] = &inbox{}
	return address
}

// Exit ends the fake session at address, so delivery to it reports the session gone.
func (h *Harness) Exit(address string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if in := h.inboxes[address]; in != nil {
		in.exited = true
	}
}

// Delivered lists what Deliver put into the session at address, in order.
func (h *Harness) Delivered(address string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if in := h.inboxes[address]; in != nil {
		return append([]string(nil), in.delivered...)
	}
	return nil
}

// Follower reads the id of the fake session the command runs in.
func (*Harness) Follower(env environment.Environment) (string, error) {
	address := env.Get(InboxEnv)
	if address == "" {
		return "", fmt.Errorf("%w: %s is not set, so this is not a Fake session; run watch from one",
			harness.ErrNotThisHarness, InboxEnv)
	}
	return address, nil
}

// Process is the fake process the command runs in, which a cleared fake session keeps.
func (*Harness) Process(env environment.Environment) string { return env.Get(ProcessEnv) }

// ProcessEnv names the variable that carries a fake session's process.
const ProcessEnv = "FAKE_HARNESS_PROCESS"

// Cleared reads the fake's clear input, {"sid":"new","where":"/dir","cleared":true}.
func (*Harness) Cleared(input []byte) (string, string, bool) {
	var in struct {
		SID     string `json:"sid"`
		Where   string `json:"where"`
		Cleared bool   `json:"cleared"`
	}
	if json.Unmarshal(input, &in) != nil || in.SID == "" || !in.Cleared {
		return "", "", false
	}
	return in.SID, in.Where, true
}

// Deliver records text in the inbox at address.
func (h *Harness) Deliver(_ context.Context, address, text string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	in := h.inboxes[address]
	if in == nil || in.exited {
		return fmt.Errorf("%w: %s", harness.ErrGone, address)
	}
	in.delivered = append(in.delivered, text)
	return nil
}

func (*Harness) AgentKind() string { return Kind }

func (*Harness) Program(s harness.Start) herdr.Program {
	return herdr.Program{
		Name:      "Fake",
		AgentKind: Kind,
		Argv:      []string{"--model", s.Model},
		Session:   s.Session,
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

// ClearedInput builds the fake's clear input for Cleared.
func ClearedInput(session, dir string) []byte {
	b, _ := json.Marshal(map[string]any{"sid": session, "where": dir, "cleared": true})
	return b
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
