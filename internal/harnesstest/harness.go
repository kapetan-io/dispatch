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

// Kind is the config kind and multiplexer agent kind the fake registers under.
const Kind = "fake"

// Harness is a program that is not Claude Code, so a test can launch something whose
// kind no code path special-cases.
type Harness struct {
	mu          sync.Mutex
	inboxes     map[string]*inbox
	transcripts map[string][]int64
}

type inbox struct {
	exited    bool
	delivered []string
}

// InboxEnv names the variable that carries a fake session's id.
const InboxEnv = "FAKE_HARNESS_INBOX"

// New returns the fake harness.
func New() *Harness { return &Harness{inboxes: map[string]*inbox{}, transcripts: map[string][]int64{}} }

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
		Said  string `json:"said"`
		Notes string `json:"notes"`
	}
	if json.Unmarshal(input, &in) != nil || in.SID == "" {
		return sessionlog.Stop{}, "", false
	}
	report := sessionlog.Stop{Session: in.SID, Reported: in.Tasks != nil, Message: in.Said, Transcript: in.Notes}
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

// TookMessageAfter answers from the times of the messages the fake's transcript named by ref
// holds. A ref it was never given is a transcript it cannot read.
func (h *Harness) TookMessageAfter(ref string, at int64) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	taken, ok := h.transcripts[ref]
	if !ok {
		return false, fmt.Errorf("the fake has no transcript %q", ref)
	}
	for _, when := range taken {
		if when > at {
			return true, nil
		}
	}
	return false, nil
}

// Take records that the session whose transcript is ref took a message at at, in milliseconds
// since the epoch, creating the transcript on first use.
func (h *Harness) Take(ref string, at int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.transcripts[ref] = append(h.transcripts[ref], at)
}

// PromptInput builds the fake's typed-text input for Prompt.
func PromptInput(session, dir, text string) []byte {
	b, _ := json.Marshal(map[string]any{"sid": session, "where": dir, "typed": text})
	return b
}

// StopSaying builds the fake's turn-end input for a turn that ended with said and a transcript.
func StopSaying(session, dir, said, transcript string) []byte {
	b, _ := json.Marshal(map[string]any{"sid": session, "where": dir, "said": said, "notes": transcript})
	return b
}
