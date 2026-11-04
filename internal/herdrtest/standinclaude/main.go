// Command standinclaude plays Claude for the drift check, so herdr starts
// something it takes for Claude without a paid session. It reports its state to
// herdr, shows the dialogs Claude shows on a first run, then waits for a
// kickoff at an input box. It leaves standin-claude.json in its working
// directory, recording the arguments and environment it was started with. A kickoff that arrives makes it report working, and
// a dialog answered the wrong way makes it exit, as Claude does when "No, exit"
// is chosen.
//
// Run by herdr-core (HC_TERMINAL_ID set) or in a Rex block (REX_BLOCK set), it reports through the OSC 7501 escape sequence Claude prints
// instead of a herdr command, says nothing at the dialogs (as Claude does), and reports idle at its input
// box. After the kickoff it reports working, and a later line of "!idle", "!done" or "!blocked" makes it
// report that state, so a test can move the worker without a paid session.
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
	app := osc()
	if !app {
		report("idle")
	}
	// Dialogs draw on a cleared screen, which hides the shell command that started Claude.
	fmt.Print("\x1b[2J\x1b[H")
	in := bufio.NewReader(os.Stdin)

	// STANDIN_NO_DIALOGS=1 goes straight to the input box, for a test that is not about the dialogs.
	if os.Getenv("STANDIN_NO_DIALOGS") != "1" {
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
	}

	fmt.Print("\x1b[2J\x1b[H")
	// Like Claude Code 2.1.316, leave a command line the harness's Unrun pattern matches above the banner.
	fmt.Println("❯ claude --stand-in")
	fmt.Println(" ▐▛███▛█   Claude Code v2.1.316")
	fmt.Print("❯ ")
	if app {
		report("idle")
	}
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
	if !app {
		time.Sleep(time.Hour)
	}
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return
		}
		if state, ok := strings.CutPrefix(strings.TrimSpace(line), "!"); ok {
			report(state)
		} else {
			report("working")
		}
	}
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
		"hc_terminal_id":    os.Getenv("HC_TERMINAL_ID"),
	})
	_ = os.WriteFile("standin-claude.json", b, 0o644)
}

// osc reports whether the program runs where status travels by OSC 7501: herdr-core and Rex.
// A Rex worker has no HC_TERMINAL_ID, so REX_BLOCK stands in for it there.
func osc() bool { return os.Getenv("HC_TERMINAL_ID") != "" || os.Getenv("REX_BLOCK") != "" }

func report(state string) {
	if osc() {
		fmt.Printf("\x1b]7501;state=%s\a", state)
		return
	}
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	_ = exec.Command(bin, "pane", "report-agent", "--source", "standin", "--agent", "claude", "--state", state, os.Getenv("HERDR_PANE_ID")).Run()
}
