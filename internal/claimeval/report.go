package claimeval

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Bar thresholds, fixed in the suite's README before attempt 1.
const (
	perCaseMin  = 0.8 // 4 of 5 runs
	pooledMin   = 0.9
	trustCaught = 0.4 // the known-bad arm must catch fewer planted claims than this
)

// ErrBarNotMet is returned, and so exits non-zero, when a result misses the bar
// or the known-bad arm shows the graders are broken.
var ErrBarNotMet = errors.New("bar not met")

type result struct {
	Cases []struct {
		Name string `json:"name"`
		Arms struct {
			With []struct {
				TracePath string `json:"tracePath"`
				Graders   []struct {
					Name   string `json:"name"`
					Passed bool   `json:"passed"`
				} `json:"graders"`
			} `json:"with"`
		} `json:"arms"`
	} `json:"cases"`
}

// tally counts passing runs of one measure.
type tally struct{ pass, total int }

func (t *tally) add(ok bool) {
	t.total++
	if ok {
		t.pass++
	}
}

func (t *tally) merge(o tally) {
	t.pass += o.pass
	t.total += o.total
}

func (t tally) rate() float64 {
	if t.total == 0 {
		return 1
	}
	return float64(t.pass) / float64(t.total)
}

func (t tally) String() string { return fmt.Sprintf("%d/%d", t.pass, t.total) }

type caseScore struct {
	Case                  Case
	Caught, Clean, Routed tally
	Repro                 tally
}

func runReport(args []string, opts Options) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	casesPath := fs.String("cases", "cases.json", "cases file")
	known := fs.Bool("known-bad", false, "score the known-bad arm, which must fail")
	runs := fs.Int("runs", 5, "runs every case must have in the result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("report needs one result.json")
	}
	cases, err := loadCases(*casesPath)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var res result
	if err := json.Unmarshal(b, &res); err != nil {
		return fmt.Errorf("while parsing result: %w", err)
	}
	byName := map[string]Case{}
	for _, c := range cases {
		byName[c.Name] = c
	}
	var scores []caseScore
	var incomplete []string
	seen := map[string]bool{}
	for _, rc := range res.Cases {
		c, ok := byName[rc.Name]
		if !ok {
			return fmt.Errorf("result has case %q that cases.json lacks", rc.Name)
		}
		seen[c.Name] = true
		if len(rc.Arms.With) != *runs {
			incomplete = append(incomplete, fmt.Sprintf("%s has %d runs, want %d", c.Name, len(rc.Arms.With), *runs))
		}
		s := caseScore{Case: c}
		for _, run := range rc.Arms.With {
			passed := map[string]bool{}
			for _, g := range run.Graders {
				passed[g.Name] = g.Passed
			}
			if c.Kind == "planted" {
				s.Caught.add(passed["caught"])
			}
			s.Clean.add(passed["no-false-alarm"])
			s.Routed.add(passed["routed"])
			if c.Repro != "" {
				s.Repro.add(reproOK(c.Repro, run.TracePath))
			}
		}
		scores = append(scores, s)
	}
	for _, c := range cases {
		if !seen[c.Name] {
			incomplete = append(incomplete, c.Name+" is missing from the result")
		}
	}
	return printScores(scores, incomplete, *known, opts)
}

// pool holds one subset's tallies for the pooled thresholds.
type pool struct {
	caught, plantedClean, controlClean, routed, repro tally
}

func (p *pool) add(s caseScore) {
	p.routed.merge(s.Routed)
	p.repro.merge(s.Repro)
	if s.Case.Kind == "planted" {
		p.caught.merge(s.Caught)
		p.plantedClean.merge(s.Clean)
	} else {
		p.controlClean.merge(s.Clean)
	}
}

// failures lists the pooled thresholds this subset misses.
func (p pool) failures(prefix string) []string {
	var out []string
	for _, m := range []struct {
		label string
		t     tally
	}{{"caught", p.caught}, {"planted clean", p.plantedClean}, {"control clean", p.controlClean}, {"routed", p.routed}, {"repro", p.repro}} {
		if m.t.rate() < pooledMin {
			out = append(out, fmt.Sprintf("%spooled %s %s is under 90%%", prefix, m.label, m.t))
		}
	}
	return out
}

func printScores(scores []caseScore, incomplete []string, known bool, opts Options) error {
	w := opts.Stdout
	_, _ = fmt.Fprintf(w, "%-22s %-8s %-8s %-8s %-8s %-8s %s\n", "case", "kind", "caught", "clean", "routed", "repro", "held out")
	var all, held pool
	var failures []string
	check := func(label string, t tally) {
		if t.total > 0 && t.rate() < perCaseMin {
			failures = append(failures, fmt.Sprintf("%s %s is under 4 of 5", label, t))
		}
	}
	for _, s := range scores {
		caught, repro := "-", "-"
		if s.Case.Kind == "planted" {
			caught = s.Caught.String()
			check(s.Case.Name+" caught", s.Caught)
		}
		if s.Repro.total > 0 {
			repro = s.Repro.String()
			check(s.Case.Name+" repro", s.Repro)
		}
		check(s.Case.Name+" clean", s.Clean)
		check(s.Case.Name+" routed", s.Routed)
		all.add(s)
		if s.Case.Heldout {
			held.add(s)
		}
		_, _ = fmt.Fprintf(w, "%-22s %-8s %-8s %-8s %-8s %-8s %v\n", s.Case.Name, s.Case.Kind, caught, s.Clean, s.Routed, repro, s.Case.Heldout)
	}
	_, _ = fmt.Fprintf(w, "\npooled: caught %s, planted clean %s, control clean %s, routed %s, repro %s\n",
		all.caught, all.plantedClean, all.controlClean, all.routed, all.repro)
	_, _ = fmt.Fprintf(w, "held out: caught %s, planted clean %s, control clean %s, routed %s, repro %s\n",
		held.caught, held.plantedClean, held.controlClean, held.routed, held.repro)
	if len(incomplete) > 0 {
		_, _ = fmt.Fprintln(w, "INCOMPLETE RESULT, no verdict")
		for _, i := range incomplete {
			_, _ = fmt.Fprintln(w, "  "+i)
		}
		return ErrBarNotMet
	}
	if known {
		if all.caught.rate() >= trustCaught {
			_, _ = fmt.Fprintf(w, "BROKEN GRADERS: the known-bad arm caught %s of planted claims, so no result counts\n", all.caught)
			return ErrBarNotMet
		}
		_, _ = fmt.Fprintf(w, "graders ok: the known-bad arm caught %s of planted claims\n", all.caught)
		return nil
	}
	failures = append(failures, all.failures("")...)
	failures = append(failures, held.failures("held-out ")...)
	if len(failures) == 0 {
		_, _ = fmt.Fprintln(w, "BAR MET")
		return nil
	}
	_, _ = fmt.Fprintln(w, "BAR NOT MET")
	for _, f := range failures {
		_, _ = fmt.Fprintln(w, "  "+f)
	}
	return ErrBarNotMet
}

// reproOK reads a run's trace. A bug that reproduces needs a failing test run
// before the first edit to source. A bug that is gone needs no source edit.
func reproOK(want, tracePath string) bool {
	events, err := readEvents(tracePath)
	if err != nil {
		return false
	}
	firstSource, failingRun := -1, -1
	for i, e := range events {
		switch {
		case e.kind == "source" && firstSource < 0:
			firstSource = i
		case e.kind == "failing-run" && failingRun < 0:
			failingRun = i
		}
	}
	if want == "none" {
		return firstSource < 0
	}
	return failingRun >= 0 && (firstSource < 0 || failingRun < firstSource)
}

type event struct{ kind string }

type traceLine struct {
	Type    string `json:"type"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

var (
	gotestRun = regexp.MustCompile(`(^|[\s;&|(])\./gotest\b`)
	// A failing test prints "--- FAIL: TestName". A build failure prints only
	// "FAIL pkg [build failed]", which is not a reproduction.
	testFailure = regexp.MustCompile(`--- FAIL: \w`)
	heredoc     = regexp.MustCompile(`<<-?\s*['"]?(\w+)['"]?`)
	redirect    = regexp.MustCompile(`>>?\s*([^\s&|;<>]+)`)
	teeTarget   = regexp.MustCompile(`\btee\s+(?:-a\s+)?([^\s&|;<>]+)`)
	inPlace     = regexp.MustCompile(`\b(?:sed|perl)\s+[^;&|\n]*-\w*i\b[^;&|\n]*`)
	pathLike    = regexp.MustCompile(`[\w./~-]*[\w-]+\.(?:go|sh|toml|md|json|yaml|yml|awk)\b|\bbin/[\w-]+|\bscripts/[\w/-]+`)
	scriptWrite = regexp.MustCompile(`open\(\s*['"]([^'"]+)['"]\s*,\s*['"][wa]|Path\(\s*['"]([^'"]+)['"]\s*\)\.write`)
	// open(p, 'w') where p is a name. Only quoted literals assigned to the name
	// are seen, so a path built at runtime is missed.
	varWrite = regexp.MustCompile(`open\(\s*([A-Za-z_]\w*)\s*,\s*['"][wa]`)
	// A quoted string, with no handling of escaped quotes.
	quoted = regexp.MustCompile(`'[^']*'|"[^"]*"`)
)

// splitHeredocs separates a shell command's own text from the bodies of its
// here-documents. A body is data for a command like cat, or a script for
// python, and only a script's body can edit a file.
func splitHeredocs(command string) (head string, bodies []string) {
	var h, body []string
	delim := ""
	for _, line := range strings.Split(command, "\n") {
		if delim != "" {
			if strings.TrimSpace(line) == delim {
				bodies = append(bodies, strings.Join(body, "\n"))
				body, delim = nil, ""
				continue
			}
			body = append(body, line)
			continue
		}
		h = append(h, line)
		if m := heredoc.FindStringSubmatch(line); m != nil {
			delim = m[1]
		}
	}
	if delim != "" {
		bodies = append(bodies, strings.Join(body, "\n"))
	}
	return strings.Join(h, "\n"), bodies
}

func isSourcePath(p string) bool {
	switch {
	case p == "", strings.HasPrefix(p, "$"), strings.HasPrefix(p, "/tmp"), strings.HasPrefix(p, "/dev/"):
		return false
	case strings.HasSuffix(p, "_test.go"), strings.Contains(p, "ticket/"), strings.HasPrefix(p, ".cache"):
		return false
	}
	return true
}

// assignedPath returns the quoted literal most recently assigned to name in the
// script text before an open() call, or "" when there is none. An attribute such
// as self.name = 'x' is not an assignment to name.
func assignedPath(before, name string) string {
	assign := regexp.MustCompile(`(?:^|[^.\w])` + regexp.QuoteMeta(name) + `\s*=\s*['"]([^'"]+)['"]`)
	all := assign.FindAllStringSubmatch(before, -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1][1]
}

// unquotePrograms replaces quoted strings with ” so a ">" inside an awk or grep
// program is not read as a redirect. A quoted string right after ">" is a quoted
// file name, as in cat > "bin/tool", and stays. So does the program of sh -c,
// which is a shell command line of its own. Only redirects use this, because
// tee and sed -i take their file names unquoted in practice.
func unquotePrograms(command string) string {
	var b strings.Builder
	last := 0
	for _, loc := range quoted.FindAllStringIndex(command, -1) {
		b.WriteString(command[last:loc[0]])
		if prefix := strings.TrimRight(command[:loc[0]], " \t"); strings.HasSuffix(prefix, ">") || strings.HasSuffix(prefix, " -c") {
			b.WriteString(command[loc[0]:loc[1]])
		} else {
			b.WriteString("''")
		}
		last = loc[1]
	}
	b.WriteString(command[last:])
	return b.String()
}

// bashWritesSource reports whether a shell command writes a file that is
// neither a test file nor under ticket/, which is how workers edit with sed,
// perl, redirects or an inline script instead of the Edit tool. It looks at the
// targets a command names, not at paths mentioned in text it only prints.
func bashWritesSource(command string) bool {
	head, bodies := splitHeredocs(command)
	var targets []string
	for _, m := range redirect.FindAllStringSubmatch(unquotePrograms(head), -1) {
		targets = append(targets, m[1])
	}
	for _, m := range teeTarget.FindAllStringSubmatch(head, -1) {
		targets = append(targets, m[1])
	}
	for _, stmt := range inPlace.FindAllString(head, -1) {
		targets = append(targets, pathLike.FindAllString(stmt, -1)...)
	}
	for _, text := range append(bodies, head) {
		for _, m := range scriptWrite.FindAllStringSubmatch(text, -1) {
			targets = append(targets, m[1]+m[2])
		}
		for _, m := range varWrite.FindAllStringSubmatchIndex(text, -1) {
			if path := assignedPath(text[:m[0]], text[m[2]:m[3]]); path != "" {
				targets = append(targets, path)
			}
		}
	}
	for _, t := range targets {
		if isSourcePath(t) {
			return true
		}
	}
	return false
}

// readEvents turns a trace into the source edits and test runs that matter, in
// order. A test edit is not an event of its own: only a source edit or a failing
// run moves the verdict. A shell command that both edits and runs the tests
// counts as editing first, so a fix and its test in one step proves nothing.
func readEvents(path string) ([]event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var events []event
	testRuns := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var l traceLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		var blocks []block
		if json.Unmarshal(l.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch {
			case l.Type == "assistant" && b.Type == "tool_use":
				var in struct {
					FilePath string `json:"file_path"`
					Command  string `json:"command"`
				}
				_ = json.Unmarshal(b.Input, &in)
				switch b.Name {
				case "Write", "Edit", "MultiEdit", "NotebookEdit":
					if !strings.Contains(in.FilePath, "/ticket/") && !strings.HasSuffix(in.FilePath, "_test.go") {
						events = append(events, event{kind: "source"})
					}
				case "Bash":
					if bashWritesSource(in.Command) {
						events = append(events, event{kind: "source"})
					}
					if gotestRun.MatchString(in.Command) {
						testRuns[b.ID] = len(events)
						events = append(events, event{kind: "run"})
					}
				}
			case l.Type == "user" && b.Type == "tool_result":
				if i, ok := testRuns[b.ToolUseID]; ok && testFailure.Match(b.Content) {
					events[i].kind = "failing-run"
				}
			}
		}
	}
	return events, sc.Err()
}
