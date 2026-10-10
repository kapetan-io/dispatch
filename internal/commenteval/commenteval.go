// Package commenteval runs and scores the needs-human-comment eval, which
// tests whether the build skill's wording makes a worker open every comment
// that needs the user with a plain-English section and a concrete example.
package commenteval

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Options carries the process edges so tests can run Run in-process.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
	// Ask returns a worker's reply to a prompt. It defaults to `claude -p`.
	Ask func(ctx context.Context, model, prompt string) (string, error)
}

const suffix = "\n\nYou have no tools in this session. Reply with only the markdown body of the ticket comment you post."

// Run is the CLI. It returns the process exit code.
func Run(ctx context.Context, args []string, opts Options) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(opts.Stderr, "usage: commenteval run|report ...")
		return 2
	}
	var err error
	switch args[0] {
	case "run":
		err = runAll(ctx, args[1:], opts)
	case "report":
		err = report(args[1:], opts)
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "commenteval: %v\n", err)
		return 1
	}
	return 0
}

func runAll(ctx context.Context, args []string, opts Options) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	arm := fs.String("arm", "", "file holding the whole build skill")
	scenarios := fs.String("scenarios", "", "directory of scenario files")
	out := fs.String("out", "", "directory for the replies")
	model := fs.String("model", "claude-sonnet-5-5", "worker model")
	count := fs.Int("n", 7, "runs per scenario")
	par := fs.Int("parallel", 6, "runs at once")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *arm == "" || *scenarios == "" || *out == "" {
		return fmt.Errorf("run needs -arm, -scenarios and -out")
	}
	if *par <= 0 {
		return fmt.Errorf("-parallel must be positive")
	}
	skill, err := os.ReadFile(*arm)
	if err != nil {
		return fmt.Errorf("while reading arm: %w", err)
	}
	files, err := filepath.Glob(filepath.Join(*scenarios, "*.md"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no scenarios in %s", *scenarios)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	ask := opts.Ask
	if ask == nil {
		ask = askClaude
	}

	sem := make(chan struct{}, *par)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed []string
	for _, file := range files {
		scenario, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("while reading scenario: %w", err)
		}
		name := strings.TrimSuffix(filepath.Base(file), ".md")
		prompt := "<skill>\n" + string(skill) + "\n</skill>\n\n" + string(scenario) + suffix
		for i := 1; i <= *count; i++ {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				path := filepath.Join(*out, fmt.Sprintf("%s-%d.md", name, i))
				reply, err := ask(ctx, *model, prompt)
				if err == nil {
					err = os.WriteFile(path, []byte(reply), 0o644)
				}
				if err != nil {
					mu.Lock()
					failed = append(failed, fmt.Sprintf("%s-%d: %v", name, i, err))
					mu.Unlock()
				}
			}()
		}
	}
	wg.Wait()
	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf("%d runs failed:\n%s", len(failed), strings.Join(failed, "\n"))
	}
	return nil
}

func askClaude(ctx context.Context, model, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", model, "--tools", "", "--no-session-persistence", "--disable-slash-commands")
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

var heading = regexp.MustCompile(`(?m)^##\s+(.*?)\s*$`)

// Score reports whether a comment opens with an In plain English section,
// shows a code-fenced example before the next heading, and follows with a
// Details section.
func Score(comment string) (opens, example, details bool) {
	comment = strings.TrimRightFunc(comment, unicode.IsSpace)
	heads := heading.FindAllStringSubmatchIndex(comment, -1)
	if len(heads) == 0 || heads[0][0] != 0 || !strings.EqualFold(comment[heads[0][2]:heads[0][3]], "In plain English") {
		return false, false, false
	}
	end := len(comment)
	if len(heads) > 1 {
		end = heads[1][0]
	}
	example = strings.Contains(comment[:end], "```")
	for _, h := range heads[1:] {
		if strings.EqualFold(comment[h[2]:h[3]], "Details") {
			details = true
		}
	}
	return true, example, details
}

func report(args []string, opts Options) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("report needs one or more run directories")
	}
	for _, dir := range fs.Args() {
		files, err := filepath.Glob(filepath.Join(dir, "*.md"))
		if err != nil {
			return err
		}
		type tally struct{ runs, opens, example, details int }
		tallies := map[string]*tally{}
		total := tally{}
		for _, file := range files {
			body, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			base := strings.TrimSuffix(filepath.Base(file), ".md")
			sep := strings.LastIndex(base, "-")
			if sep <= 0 {
				continue
			}
			name := base[:sep]
			if tallies[name] == nil {
				tallies[name] = &tally{}
			}
			opens, example, details := Score(string(body))
			for _, t := range []*tally{tallies[name], &total} {
				t.runs++
				t.opens += b2i(opens)
				t.example += b2i(opens && example)
				t.details += b2i(opens && details)
			}
		}
		names := make([]string, 0, len(tallies))
		for n := range tallies {
			names = append(names, n)
		}
		sort.Strings(names)
		_, _ = fmt.Fprintf(opts.Stdout, "%s\n%-16s %5s %6s %8s %8s\n", dir, "scenario", "runs", "opens", "example", "details")
		for _, n := range names {
			t := tallies[n]
			_, _ = fmt.Fprintf(opts.Stdout, "%-16s %5d %6d %8d %8d\n", n, t.runs, t.opens, t.example, t.details)
		}
		_, _ = fmt.Fprintf(opts.Stdout, "%-16s %5d %6d %8d %8d\n\n", "all", total.runs, total.opens, total.example, total.details)
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
