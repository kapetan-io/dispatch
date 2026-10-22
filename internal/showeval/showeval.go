// Package showeval replays one real miss against rule 5 of the converse
// skill. A session proposed a code change in prose, the user misread it, and
// only a second ask for examples got the code shown. The replay counts, per
// arm, the replies that hold a fenced code block and their average prose words.
package showeval

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Options carries the process edges so tests can run Run in-process.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
	// Ask returns the reply to a prompt under a system prompt file. It defaults to `claude -p`.
	Ask func(ctx context.Context, model, systemFile, prompt string) (string, error)
}

// Tally is what one arm's replies add up to.
type Tally struct {
	Runs      int
	WithCode  int
	ProseSum  int
	ProseMean int
}

// Count returns how many fenced code blocks a reply holds and how many words sit outside them.
func Count(reply string) (blocks, prose int) {
	inside := false
	for _, line := range strings.Split(reply, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if !inside {
				blocks++
			}
			inside = !inside
			continue
		}
		if !inside {
			prose += len(strings.Fields(line))
		}
	}
	return blocks, prose
}

// Run is the CLI. It returns the process exit code.
func Run(ctx context.Context, args []string, opts Options) int {
	if err := run(ctx, args, opts); err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "showeval: %v\n", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string, opts Options) error {
	fs := flag.NewFlagSet("showeval", flag.ContinueOnError)
	dir := fs.String("dir", "evals/show-examples", "eval directory")
	skillPath := fs.String("skill", "skills/converse/SKILL.md", "converse skill under test")
	model := fs.String("model", "claude-sonnet-5-5", "model that answers")
	count := fs.Int("n", 5, "runs per arm")
	out := fs.String("out", "", "directory to keep every reply in")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *count <= 0 {
		return fmt.Errorf("-n must be positive")
	}
	skill, err := os.ReadFile(*skillPath)
	if err != nil {
		return fmt.Errorf("while reading skill: %w", err)
	}
	read := func(name string) (string, error) {
		data, err := os.ReadFile(filepath.Join(*dir, name))
		return string(data), err
	}
	scene, err := read("context.md")
	if err != nil {
		return err
	}
	prompt, err := read("prompt.txt")
	if err != nil {
		return err
	}
	baseline, err := read("arms/current.md")
	if err != nil {
		return err
	}
	current, err := replaceRule(string(skill), baseline)
	if err != nil {
		return err
	}

	ask := opts.Ask
	if ask == nil {
		ask = askClaude
	}
	tmp, err := os.MkdirTemp("", "showeval")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	arms := []struct{ name, skill string }{{"current", current}, {"skill", string(skill)}}
	tallies := make([]Tally, len(arms))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed []string
	for idx, arm := range arms {
		file := filepath.Join(tmp, arm.name+".md")
		body := "# Loaded skill: dispatch:converse\n\n" + arm.skill + "\n\n" + scene
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			return err
		}
		for i := 1; i <= *count; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				reply, err := ask(ctx, *model, file, prompt)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					failed = append(failed, fmt.Sprintf("%s-%d: %v", arm.name, i, err))
					return
				}
				if *out != "" {
					if err := os.MkdirAll(*out, 0o755); err == nil {
						_ = os.WriteFile(filepath.Join(*out, fmt.Sprintf("%s-%d.md", arm.name, i)), []byte(reply), 0o644)
					}
				}
				blocks, prose := Count(reply)
				tallies[idx].Runs++
				tallies[idx].ProseSum += prose
				if blocks > 0 {
					tallies[idx].WithCode++
				}
			}()
		}
	}
	wg.Wait()
	if len(failed) > 0 {
		return fmt.Errorf("%d replies failed: %s", len(failed), strings.Join(failed, "; "))
	}

	_, _ = fmt.Fprintf(opts.Stdout, "%-8s %-22s %s\n", "arm", "replies with code", "avg prose words")
	for idx, arm := range arms {
		t := tallies[idx]
		_, _ = fmt.Fprintf(opts.Stdout, "%-8s %-22s %d\n", arm.name, fmt.Sprintf("%d of %d", t.WithCode, t.Runs), t.ProseSum/t.Runs)
	}
	return nil
}

// replaceRule swaps the skill's rule 5 line for the baseline wording.
func replaceRule(skill, rule string) (string, error) {
	lines := strings.Split(skill, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "5. ") {
			lines[i] = strings.TrimRight(rule, "\n")
			return strings.Join(lines, "\n"), nil
		}
	}
	return "", fmt.Errorf("skill has no line starting with %q", "5. ")
}

func askClaude(ctx context.Context, model, systemFile, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", model, "--tools", "", "--setting-sources", "",
		"--strict-mcp-config", "--no-session-persistence", "--append-system-prompt-file", systemFile)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
