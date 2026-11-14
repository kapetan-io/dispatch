package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// sessionSettings makes a session deliver messages from other sessions instead of
// holding them for review, whatever the user's own settings say.
const sessionSettings = `{"crossSessionInbound":"accept"}`

// ClaudeCode is the Claude Code harness.
type ClaudeCode struct {
	// Sessions is the directory of Claude Code's session list, one <pid>.json per running
	// session. Deliver fails when it is empty, so a test never reads the developer's own list.
	Sessions string
}

// SessionsDir is where Claude Code keeps its session list for a session run with env.
func SessionsDir(env environment.Environment) string {
	if dir := env.Get("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	return env.ExpandHome("~/.claude/sessions")
}

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
		// Not in ClaudeCodeFlags, so the restart shim never reuses an old id.
		Session:     s.Session,
		SessionFlag: "--session-id",
		ResumeFlag:  "--resume",
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
			// Claude Code 2.1.316 and later leave the launch command on screen above the banner.
			Started: regexp.MustCompile(`Claude Code v\d`),
			Ready:   regexp.MustCompile(`❯`),
		},
	}
}

// Stop reads Claude Code's Stop hook input. BackgroundTasks is a pointer so a
// missing field, which means a Claude Code version that predates it, differs from
// an empty list.
func (ClaudeCode) Stop(input []byte) (sessionlog.Stop, string, bool) {
	var in struct {
		SessionID            string `json:"session_id"`
		CWD                  string `json:"cwd"`
		TranscriptPath       string `json:"transcript_path"`
		LastAssistantMessage string `json:"last_assistant_message"`
		BackgroundTasks      *[]struct {
			Status string `json:"status"`
		} `json:"background_tasks"`
	}
	if json.Unmarshal(input, &in) != nil {
		return sessionlog.Stop{}, "", false
	}
	report := sessionlog.Stop{
		Session: in.SessionID, Reported: in.BackgroundTasks != nil,
		Message: in.LastAssistantMessage, Transcript: in.TranscriptPath,
	}
	if report.Reported {
		for _, task := range *in.BackgroundTasks {
			if task.Status == "running" {
				report.Running++
			}
		}
	}
	return report, in.CWD, true
}

// TookMessageAfter reads the transcript at the path the Stop hook reported. Only user and assistant
// entries carry the conversation: after a Stop Claude appends only stop_hook_summary, last-prompt
// and cost-state entries, so a user entry stamped after the turn ended is a message the session took.
func (ClaudeCode) TookMessageAfter(transcript string, at int64) (bool, error) {
	if transcript == "" {
		return false, errors.New("the Stop report named no transcript")
	}
	f, err := os.Open(transcript)
	if err != nil {
		return false, fmt.Errorf("while reading the transcript: %w", err)
	}
	defer func() { _ = f.Close() }()
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		raw, err := reader.ReadBytes('\n')
		if bytes.Contains(raw, []byte(`"user"`)) {
			var entry struct {
				Type      string `json:"type"`
				Timestamp string `json:"timestamp"`
			}
			// A last line with no newline is a write in progress.
			if json.Unmarshal(raw, &entry) == nil && entry.Type == "user" {
				when, perr := time.Parse(time.RFC3339Nano, entry.Timestamp)
				if perr != nil {
					return false, fmt.Errorf("while reading the transcript: a user entry has no readable time: %w", perr)
				}
				if when.UnixMilli() > at {
					return true, nil
				}
			}
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("while reading the transcript: %w", err)
		}
	}
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

// sessionEnv names the variable Claude Code sets to the id of the session a command runs in.
const sessionEnv = "CLAUDE_CODE_SESSION_ID"

const sendTimeout = 5 * time.Second

// Follower reads the id of the session the command runs in.
func (ClaudeCode) Follower(env environment.Environment) (string, error) {
	session := env.Get(sessionEnv)
	if session == "" {
		return "", fmt.Errorf("%w: %s is not set, so this is not a Claude Code session; run watch from one",
			ErrNotThisHarness, sessionEnv)
	}
	return session, nil
}

// socketOf finds the messaging socket of the running session with this id in Claude Code's
// session list. A restart or resume keeps the id and moves the socket, and a crash can leave a
// dead process's file behind, so the newest entry wins. doubt is true when a list file could not
// be read or parsed, because Claude rewrites its file as the session's status changes and one
// caught half written may be the session's own.
func (c ClaudeCode) socketOf(session string) (socket string, doubt bool, err error) {
	if c.Sessions == "" {
		return "", false, errors.New("the Claude Code session list directory is not set")
	}
	if session == "" {
		return "", false, errors.New("no session id to deliver to")
	}
	// A directory that can't be listed says nothing about the session, so it is not ErrGone.
	entries, err := os.ReadDir(c.Sessions)
	if err != nil {
		return "", false, fmt.Errorf("while reading the Claude Code session list: %w", err)
	}
	newest := float64(-1)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(c.Sessions, e.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			doubt = true
			continue
		}
		var entry struct {
			SessionID string  `json:"sessionId"`
			Socket    string  `json:"messagingSocketPath"`
			StartedAt float64 `json:"startedAt"`
		}
		if json.Unmarshal(raw, &entry) != nil {
			doubt = true
			continue
		}
		if entry.SessionID != session || entry.Socket == "" {
			continue
		}
		if entry.StartedAt > newest {
			socket, newest = entry.Socket, entry.StartedAt
		}
	}
	if socket == "" && doubt {
		return "", true, fmt.Errorf("no Claude Code session %s was found, and a session list file could not be read", session)
	}
	if socket == "" {
		return "", false, fmt.Errorf("%w: no Claude Code session %s is open", ErrGone, session)
	}
	return socket, doubt, nil
}

// Deliver writes one message into a Claude Code session's inbox socket, found in the session
// list by the session's id. The frame is undocumented, taken from the binary's own debug hint
// and a captured SendMessage (anthropics/claude-code#93720), and observed working on Claude
// Code 2.1.319. A Claude Code release that changes it, or the session list, breaks here and
// nowhere else. The socket never replies. A session with no list entry, or whose socket
// refuses the connection or is gone, has exited. Claude drops a frame cut off before its
// newline, so a failed write left nothing in the session.
func (c ClaudeCode) Deliver(ctx context.Context, session, text string) error {
	address, doubt, err := c.socketOf(session)
	if err != nil {
		return err
	}
	line, err := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]string{"role": "user", "content": text},
	})
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: sendTimeout}
	conn, err := dialer.DialContext(ctx, "unix", address)
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist) {
		if doubt {
			// The entry that held the live socket may be the unreadable one.
			return fmt.Errorf("the chosen socket is dead and a session list file could not be read: %w", err)
		}
		return fmt.Errorf("%w: %w", ErrGone, err)
	}
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetWriteDeadline(time.Now().Add(sendTimeout))
	_, err = conn.Write(append(line, '\n'))
	return err
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
// model and its --session-id. The restart shim adds the same list, since herdr restarts a worker with a
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
