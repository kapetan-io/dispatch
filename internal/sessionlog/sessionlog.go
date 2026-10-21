// Package sessionlog keeps the file of what one worker session did, one line
// for each command it took and each turn it ended. The plugin's hooks append to
// it and only the world reads it, so a rule sees all of the session and not just
// the latest turn.
package sessionlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kapetan-io/dispatch/internal/treekey"
)

// Turn is one reply the session finished, ending in a Stop.
type Turn struct {
	// At is when the turn ended, in milliseconds since the epoch.
	At int64 `json:"at"`
	// Running is true when the Stop reported at least one running background task.
	Running bool `json:"running"`
}

// Sender is who typed a command into the session.
type Sender string

const (
	ByPerson   Sender = "person"
	ByDispatch Sender = "dispatch"
)

// ArgsLimit is the most bytes of a command's arguments the log keeps.
const ArgsLimit = 1024

// Command is a slash command the session took.
type Command struct {
	// At is when Claude took the message, in milliseconds since the epoch.
	At   int64  `json:"at"`
	Name string `json:"name"`
	// Args is the rest of the message, trimmed and cut at ArgsLimit.
	Args string `json:"args"`
	// Cut is true when Args was longer than ArgsLimit.
	Cut bool   `json:"cut"`
	By  Sender `json:"by"`
}

// Log holds a session's commands and turns, oldest first. Neither is ever nil, so they encode as [] and not null.
type Log struct {
	Commands []Command `json:"commands"`
	Turns    []Turn    `json:"turns"`
}

// line is one event in the file.
type line struct {
	Command *Command `json:"command,omitempty"`
	Turn    *Turn    `json:"turn,omitempty"`
}

// ParseCommand reads a message as a slash command. A command is a / at the first byte, a name of
// letters, digits and : _ . - starting with a letter or digit, and then white space or the end.
// Its arguments are the rest of the message, trimmed and cut at ArgsLimit without splitting a rune.
func ParseCommand(prompt string) (Command, bool) {
	rest, ok := strings.CutPrefix(prompt, "/")
	if !ok {
		return Command{}, false
	}
	end := strings.IndexFunc(rest, func(r rune) bool { return !isNameRune(r) })
	if end < 0 {
		end = len(rest)
	}
	name := rest[:end]
	if name == "" || !isNameStart(rune(name[0])) {
		return Command{}, false
	}
	if end < len(rest) {
		if r, _ := utf8.DecodeRuneInString(rest[end:]); !unicode.IsSpace(r) {
			return Command{}, false
		}
	}
	args := strings.TrimSpace(rest[end:])
	cmd := Command{Name: name, Args: args}
	if len(args) > ArgsLimit {
		cut := ArgsLimit
		for cut > 0 && !utf8.RuneStart(args[cut]) {
			cut--
		}
		cmd.Args, cmd.Cut = args[:cut], true
	}
	return cmd, true
}

func isNameStart(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func isNameRune(r rune) bool {
	return isNameStart(r) || strings.ContainsRune(":_.-", r)
}

// Dir is where a state directory keeps its session logs.
func Dir(stateDir string) string {
	return filepath.Join(stateDir, "sessions")
}

func treeDir(stateDir, tree string) string {
	return filepath.Join(Dir(stateDir), treekey.Of(tree))
}

func file(stateDir, tree, session string) string {
	return filepath.Join(treeDir(stateDir, tree), treekey.Hash(session)+".jsonl")
}

// AppendTurn adds a turn to the log of session in the tree.
func AppendTurn(stateDir, tree, session string, t Turn) error {
	return appendLine(stateDir, tree, session, line{Turn: &t}, "the turn")
}

// AppendCommand adds a command to the log of session in the tree.
func AppendCommand(stateDir, tree, session string, c Command) error {
	return appendLine(stateDir, tree, session, line{Command: &c}, "the command")
}

func appendLine(stateDir, tree, session string, l line, what string) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	name := file(stateDir, tree, session)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return fmt.Errorf("while recording %s: %w", what, err)
	}
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("while recording %s: %w", what, err)
	}
	defer func() { _ = f.Close() }()
	// A writer killed mid-line leaves no newline. Starting a fresh line keeps this event
	// parseable but leaves that half line as a hole, so Read reports the log unreadable from then on.
	b = append(b, '\n')
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err == nil && last[0] != '\n' {
			b = append([]byte{'\n'}, b...)
		}
	}
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("while recording %s: %w", what, err)
	}
	return nil
}

// Read returns the log of session in the tree. A missing file is an empty log.
// A line that can't be decoded is an error, because a log with a hole in it
// would pass for a shorter one. The exception is a last line with no newline,
// which is a write still in progress.
func Read(stateDir, tree, session string) (Log, error) {
	log := Log{Commands: []Command{}, Turns: []Turn{}}
	b, err := os.ReadFile(file(stateDir, tree, session))
	if errors.Is(err, fs.ErrNotExist) {
		return log, nil
	}
	if err != nil {
		return Log{}, fmt.Errorf("while reading the session log: %w", err)
	}
	// Cutting at the last newline drops a final line still being written.
	complete := b[:bytes.LastIndexByte(b, '\n')+1]
	var n int
	for raw := range bytes.Lines(complete) {
		n++
		var l line
		if err := json.Unmarshal(raw, &l); err != nil {
			return Log{}, fmt.Errorf("while reading the session log: line %d: %w", n, err)
		}
		if l.Command != nil {
			log.Commands = append(log.Commands, *l.Command)
		}
		if l.Turn != nil {
			log.Turns = append(log.Turns, *l.Turn)
		}
	}
	return log, nil
}

// Remove deletes the logs of the tree, so the next ticket leased that tree
// does not inherit this one's sessions.
func Remove(stateDir, tree string) {
	_ = os.RemoveAll(treeDir(stateDir, tree))
}
