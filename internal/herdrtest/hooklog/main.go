// Command hooklog is a test stub, so it has no Run(ctx, args, opts). It stands in for dispatch on a real Claude's PATH, so a claude-tagged test sees what
// the plugin's hooks run and what Claude Code hands them. It appends one JSON line to the file named
// by HOOKLOG with the subcommand and the hook input read from stdin. ticket-hint also prints a note
// to stdout, which Claude Code adds to the session as SessionStart output.
package main

import (
	"encoding/json"
	"io"
	"os"
)

// sessionNote is what ticket-hint prints, so a test can ask the session to repeat it.
const sessionNote = "Session note: the secret word is PINEAPPLE7."

func main() {
	hook := firstArg()
	input, _ := io.ReadAll(os.Stdin)
	line, err := json.Marshal(map[string]any{"hook": hook, "input": json.RawMessage(rawOrNull(input))})
	if err != nil {
		os.Exit(1)
	}
	f, err := os.OpenFile(os.Getenv("HOOKLOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(1)
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
	if hook == "ticket-hint" {
		_, _ = os.Stdout.WriteString(sessionNote + "\n")
	}
}

func firstArg() string {
	if len(os.Args) > 1 {
		return os.Args[1]
	}
	return ""
}

func rawOrNull(b []byte) []byte {
	if json.Valid(b) {
		return b
	}
	return []byte("null")
}
