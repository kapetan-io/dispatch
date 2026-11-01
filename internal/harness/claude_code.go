package harness

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// sessionSettings makes a session deliver messages from other sessions instead of
// holding them for review, whatever the user's own settings say.
const sessionSettings = `{"crossSessionInbound":"accept"}`

// ClaudeCode is the Claude Code harness.
type ClaudeCode struct{}

func (ClaudeCode) AgentKind() string { return "claude" }

func (ClaudeCode) Program(s Start) herdr.Program {
	argv := []string{"--model", s.Model}
	for _, flag := range ClaudeCodeFlags(s.PluginRoot) {
		argv = append(argv, flag.Names[0])
		if flag.Value != "" {
			argv = append(argv, flag.Value)
		}
	}
	return herdr.Program{
		Name:      "Claude",
		AgentKind: "claude",
		Argv:      argv,
		PathFirst: []string{filepath.Join(s.PluginRoot, "scripts", "shims")},
		Startup: herdr.Startup{
			Answers: []herdr.Answer{
				// One-time bypass-permissions consent. Match the option text, never
				// "bypass permissions", which the status line shows forever after.
				{
					Shows: []*regexp.Regexp{regexp.MustCompile(`(?i)yes, i accept`)},
					Tries: []herdr.Try{{{"Down", "Enter"}}, {{"2"}, {"Enter"}}},
				},
				// First-run folder trust. Kickoff uses the first Answer whose Shows all match, so
				// this one comes first: it moves off "No, exit" before Enter, and the next sends
				// Enter alone when the cursor is already on "Yes".
				{
					Shows: []*regexp.Regexp{regexp.MustCompile(`(?i)trust this folder`), regexp.MustCompile(`(?i)❯ *(no|don.t)`)},
					Tries: []herdr.Try{{{"Down"}, {"Enter"}}},
				},
				{
					Shows: []*regexp.Regexp{regexp.MustCompile(`(?i)trust this folder`)},
					Tries: []herdr.Try{{{"Enter"}}},
				},
			},
			Unrun: regexp.MustCompile(`❯ *claude +--`),
			Ready: regexp.MustCompile(`❯`),
		},
	}
}

// Stop reads Claude Code's Stop hook input. BackgroundTasks is a pointer so a
// missing field, which means a Claude Code version that predates it, differs from
// an empty list.
func (ClaudeCode) Stop(input []byte) (sessionlog.Stop, string, bool) {
	var in struct {
		SessionID       string `json:"session_id"`
		CWD             string `json:"cwd"`
		BackgroundTasks *[]struct {
			Status string `json:"status"`
		} `json:"background_tasks"`
	}
	if json.Unmarshal(input, &in) != nil {
		return sessionlog.Stop{}, "", false
	}
	report := sessionlog.Stop{Session: in.SessionID, Reported: in.BackgroundTasks != nil}
	if report.Reported {
		for _, task := range *in.BackgroundTasks {
			if task.Status == "running" {
				report.Running++
			}
		}
	}
	return report, in.CWD, true
}

// Prompt reads Claude Code's UserPromptSubmit and Notification hook input. A
// Notification carries no prompt.
func (ClaudeCode) Prompt(input []byte) (sessionlog.Prompt, string, bool) {
	var in struct {
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
		Prompt    string `json:"prompt"`
	}
	if json.Unmarshal(input, &in) != nil {
		return sessionlog.Prompt{}, "", false
	}
	return sessionlog.Prompt{Session: in.SessionID, Text: in.Prompt}, in.CWD, true
}

// Flag is a flag Claude Code sessions run with.
type Flag struct {
	// Names are the spellings Claude accepts. Names[0] is the one dispatch passes; Has
	// recognizes all of them.
	Names []string
	// Value is empty for a flag that takes none.
	Value string
	// Workers marks a flag the restart shim adds only to an autonomous worker. A launch
	// is always one, so Program passes it regardless.
	Workers bool
}

// Has reports whether args already carry the flag, as `--name value` or `--name=value`.
func (f Flag) Has(args []string) bool {
	for _, arg := range args {
		for _, name := range f.Names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

// ClaudeCodeFlags are the flags dispatch gives a Claude Code session besides its
// model. The restart shim adds the same list, since herdr restarts a worker with a
// bare `claude --resume`. Never add --name: Claude draws the name below an open
// question, and herdr then misses the question.
func ClaudeCodeFlags(pluginRoot string) []Flag {
	var flags []Flag
	if pluginRoot != "" {
		flags = append(flags, Flag{Names: []string{"--plugin-dir"}, Value: pluginRoot})
	}
	return append(flags,
		Flag{Names: []string{"--disallowedTools", "--disallowed-tools"}, Value: "EnterPlanMode,ExitPlanMode", Workers: true},
		Flag{Names: []string{"--settings"}, Value: sessionSettings},
		Flag{Names: []string{"--dangerously-skip-permissions"}, Workers: true},
	)
}
