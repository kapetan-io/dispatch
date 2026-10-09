package claimeval

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

// Bar thresholds, fixed in the suite's README before attempt 1.
const (
	perCaseMin  = 0.8 // 4 of 5 runs
	pooledMin   = 0.9
	trustCaught = 0.4 // the known-bad arm must catch fewer planted claims than this
)

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
	for _, rc := range res.Cases {
		c, ok := byName[rc.Name]
		if !ok {
			return fmt.Errorf("result has case %q that cases.json lacks", rc.Name)
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
	return printScores(scores, *known, opts)
}

func printScores(scores []caseScore, known bool, opts Options) error {
	w := opts.Stdout
	_, _ = fmt.Fprintf(w, "%-22s %-8s %-8s %-8s %-8s %-8s %s\n", "case", "kind", "caught", "clean", "routed", "repro", "held out")
	var planted, controls, plantedClean, routed, repro tally
	failures := []string{}
	check := func(label string, t tally) {
		if t.total > 0 && t.rate() < perCaseMin {
			failures = append(failures, fmt.Sprintf("%s %s is under 4 of 5", label, t))
		}
	}
	for _, s := range scores {
		caught, reproS := "-", "-"
		if s.Case.Kind == "planted" {
			caught = s.Caught.String()
			planted.pass += s.Caught.pass
			planted.total += s.Caught.total
			plantedClean.pass += s.Clean.pass
			plantedClean.total += s.Clean.total
			check(s.Case.Name+" caught", s.Caught)
		} else {
			controls.pass += s.Clean.pass
			controls.total += s.Clean.total
		}
		if s.Repro.total > 0 {
			reproS = s.Repro.String()
			repro.pass += s.Repro.pass
			repro.total += s.Repro.total
			check(s.Case.Name+" repro", s.Repro)
		}
		routed.pass += s.Routed.pass
		routed.total += s.Routed.total
		check(s.Case.Name+" clean", s.Clean)
		check(s.Case.Name+" routed", s.Routed)
		_, _ = fmt.Fprintf(w, "%-22s %-8s %-8s %-8s %-8s %-8s %v\n", s.Case.Name, s.Case.Kind, caught, s.Clean, s.Routed, reproS, s.Case.Heldout)
	}
	_, _ = fmt.Fprintf(w, "\npooled: caught %s, planted clean %s, control clean %s, routed %s, repro %s\n", planted, plantedClean, controls, routed, repro)
	if known {
		if planted.rate() >= trustCaught {
			_, _ = fmt.Fprintf(w, "BROKEN GRADERS: the known-bad arm caught %s of planted claims, so no result counts\n", planted)
			return nil
		}
		_, _ = fmt.Fprintf(w, "graders ok: the known-bad arm caught %s of planted claims\n", planted)
		return nil
	}
	for _, p := range []struct {
		label string
		t     tally
	}{{"caught", planted}, {"planted clean", plantedClean}, {"control clean", controls}, {"routed", routed}, {"repro", repro}} {
		if p.t.rate() < pooledMin {
			failures = append(failures, fmt.Sprintf("pooled %s %s is under 90%%", p.label, p.t))
		}
	}
	heldFail := []string{}
	for _, s := range scores {
		if !s.Case.Heldout {
			continue
		}
		for _, t := range []tally{s.Caught, s.Clean, s.Routed, s.Repro} {
			if t.total > 0 && t.rate() < perCaseMin {
				heldFail = append(heldFail, s.Case.Name)
				break
			}
		}
	}
	if len(failures) == 0 {
		_, _ = fmt.Fprintln(w, "BAR MET")
		return nil
	}
	_, _ = fmt.Fprintln(w, "BAR NOT MET")
	for _, f := range failures {
		_, _ = fmt.Fprintln(w, "  "+f)
	}
	if len(heldFail) > 0 {
		_, _ = fmt.Fprintln(w, "  held-out cases under the bar: "+strings.Join(heldFail, ", "))
	}
	return nil
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

// readEvents turns a trace into the edits and test runs that matter, in order.
// A test edit is not an event of its own: only a source edit or a failing run
// moves the verdict.
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
				case "Write", "Edit":
					if !strings.Contains(in.FilePath, "/ticket/") && !strings.HasSuffix(in.FilePath, "_test.go") {
						events = append(events, event{kind: "source"})
					}
				case "Bash":
					if strings.Contains(in.Command, "gotest") {
						testRuns[b.ID] = len(events)
						events = append(events, event{kind: "run"})
					}
				}
			case l.Type == "user" && b.Type == "tool_result":
				if i, ok := testRuns[b.ToolUseID]; ok && strings.Contains(string(b.Content), "FAIL") {
					events[i].kind = "failing-run"
				}
			}
		}
	}
	return events, sc.Err()
}
