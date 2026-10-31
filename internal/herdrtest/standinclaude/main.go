// Command standinclaude plays Claude for the drift check, so herdr starts
// something it takes for Claude without a paid session. It reports its state to
// herdr, shows the dialogs Claude shows on a first run, then waits for a
// kickoff at an input box. It leaves standin-claude.json in its working
// directory, recording the arguments and environment it was started with. A kickoff that arrives makes it report working, and
// a dialog answered the wrong way makes it exit, as Claude does when "No, exit"
// is chosen.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const down = "\x1b[B"

func main() {
	record()
	report("idle")
	// Claude draws on a cleared screen, which also hides the shell command that
	// started it.
	fmt.Print("\x1b[2J\x1b[H")
	in := bufio.NewReader(os.Stdin)

	fmt.Println("Bypass Permissions mode")
	fmt.Println("  1. No, exit")
	fmt.Println("  2. Yes, I accept")
	if !chose(in, func(line string) bool { return strings.Contains(line, "2") || line == down }) {
		os.Exit(1)
	}

	fmt.Print("\x1b[2J\x1b[H")
	fmt.Println("Do you trust this folder?")
	fmt.Println("  ❯ No, exit")
	fmt.Println("    Yes, proceed")
	if !chose(in, func(line string) bool { return line == down }) {
		os.Exit(1)
	}

	fmt.Print("\x1b[2J\x1b[H")
	fmt.Print("❯ ")
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return
		}
		if strings.TrimSpace(line) != "" {
			report("working")
			break
		}
	}
	time.Sleep(time.Hour)
}

// chose reads lines until one is an answer, and reports whether it was the
// right one. An empty line is Enter on the default choice, which is "No, exit".
func chose(in *bufio.Reader, right func(line string) bool) bool {
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return false
		}
		if line = strings.TrimRight(line, "\r\n"); line == "" {
			return false
		}
		if right(line) {
			return true
		}
	}
}

// record writes how herdr started this program to standin-claude.json in its
// working directory, so a test can see the arguments and the environment the
// worker got.
func record() {
	b, _ := json.Marshal(map[string]any{
		"args":              os.Args[1:],
		"plugin_root":       os.Getenv("DISPATCH_PLUGIN_ROOT"),
		"autonomous_worker": os.Getenv("AUTONOMOUS_WORKER"),
		"dispatch_bin":      os.Getenv("DISPATCH_BIN"),
	})
	_ = os.WriteFile("standin-claude.json", b, 0o644)
}

func report(state string) {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	_ = exec.Command(bin, "pane", "report-agent", "--source", "standin", "--agent", "claude", "--state", state, os.Getenv("HERDR_PANE_ID")).Run()
}
