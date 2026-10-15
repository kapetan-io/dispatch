package rules

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/grafana/sobek"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/ledger"
)

// Summary is the outcome of running every scenario under a directory.
type Summary struct {
	Lines  []string
	Passed int
	Failed int
}

// scenarioStart is the clock every scenario starts at.
var scenarioStart = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// RunScenarios runs each *.test.ts file under dir in the sobek runtime a live
// run uses. A failed scenario is reported, and the rest still run.
func RunScenarios(dir string, timeouts Timeouts) (Summary, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.test.ts"))
	if err != nil {
		return Summary{}, err
	}
	if len(files) == 0 {
		return Summary{}, fmt.Errorf("no *.test.ts files in %s", dir)
	}
	slices.Sort(files)
	var sum Summary
	for _, file := range files {
		if err := runScenarioFile(dir, file, timeouts, &sum); err != nil {
			return sum, err
		}
	}
	sum.Lines = append(sum.Lines, fmt.Sprintf("%d scenarios, %d passed, %d failed", sum.Passed+sum.Failed, sum.Passed, sum.Failed))
	return sum, nil
}

func runScenarioFile(dir, file string, timeouts Timeouts, sum *Summary) error {
	code, err := bundle(file, dir, map[string]string{"dispatch/testing": testingModule})
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	prog, err := sobek.Compile(filepath.Base(file), code, false)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	rt := sobek.New()
	rt.SetMaxCallStackSize(maxCallDepth)
	host := &scenarioHost{dir: dir, file: file, rt: rt, timeouts: timeouts, loaded: map[string]*Rules{}, sum: sum}
	if err := host.install(); err != nil {
		return err
	}
	if _, err := rt.RunProgram(prog); err != nil {
		var ex *sobek.Exception
		if errors.As(err, &ex) {
			return fmt.Errorf("%s: %s", filepath.Base(file), ex.Error())
		}
		return fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	return nil
}

type scenarioHost struct {
	dir, file string
	rt        *sobek.Runtime
	timeouts  Timeouts
	loaded    map[string]*Rules
	sum       *Summary
}

func (h *scenarioHost) install() error {
	api := h.rt.NewObject()
	_ = api.Set("rulesFrom", func(call sobek.FunctionCall) sobek.Value {
		path := call.Argument(0).String()
		abs := filepath.Join(filepath.Dir(h.file), path)
		if filepath.Ext(abs) == "" {
			abs += ".ts"
		}
		if _, ok := h.loaded[abs]; !ok {
			loaded, err := Load(filepath.Dir(abs), filepath.Base(abs), h.timeouts)
			if err != nil {
				panic(h.rt.NewGoError(err))
			}
			h.loaded[abs] = loaded
		}
		handle := h.rt.NewObject()
		_ = handle.Set("path", abs)
		return handle
	})
	_ = api.Set("scenario", func(call sobek.FunctionCall) sobek.Value {
		handle, _ := call.Argument(0).(*sobek.Object)
		name := call.Argument(1).String()
		body, ok := sobek.AssertFunction(call.Argument(2))
		if handle == nil || !ok {
			panic(h.rt.NewTypeError("scenario takes the rules, a name and a function"))
		}
		h.run(h.loaded[handle.Get("path").String()], name, body)
		return sobek.Undefined()
	})
	return h.rt.Set("__testing", api)
}

func (h *scenarioHost) run(rules *Rules, name string, body sobek.Callable) {
	sc := newScenario(h, rules)
	err := func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				switch p := p.(type) {
				case error:
					err = p
				case sobek.Value:
					err = errors.New(p.String())
				default:
					err = fmt.Errorf("%v", p)
				}
			}
		}()
		_, err = body(sobek.Undefined(), sc.object())
		return err
	}()
	label := filepath.Base(h.file) + ": " + name
	if err != nil {
		var ex *sobek.Exception
		msg := err.Error()
		if errors.As(err, &ex) {
			msg = firstLine(ex.Error())
		}
		h.sum.Failed++
		h.sum.Lines = append(h.sum.Lines, "FAIL "+label+"\n     "+strings.ReplaceAll(msg, "\n", "\n     "))
		return
	}
	h.sum.Passed++
	h.sum.Lines = append(h.sum.Lines, "ok   "+label)
}

// scenario is one test: a world that changes step by step, and the cycle that
// runs after each change.
type scenario struct {
	host        *scenarioHost
	rules       *Rules
	cfg         config.Config
	m           *scenarioModel
	led         *ledger.Ledger
	now         time.Time
	pools       map[string]int
	herdrUnread bool
	poolUnread  map[string]bool
	prUnread    map[string]bool
	events      []Event
	// thrown holds the rules expectThrew has acknowledged.
	thrown map[string]bool
	last   Result
	dirty  bool
	ran    bool
}

func newScenario(h *scenarioHost, rules *Rules) *scenario {
	cfg := config.Config{
		Dispatch: config.Dispatch{MaxInProgress: 4, Resync: config.Duration(5 * time.Minute)},
		Plugins: config.Plugins{Linear: config.Linear{
			Teams: []string{"ENG"}, AILabel: "ai", RepoLabelGroup: "repo",
			Statuses: config.Statuses{
				Todo: "Todo", CloseParent: "Done", BlockerDone: "Done", InProgress: "In Progress", InReview: "In Review",
				NeedsHuman: "Needs Human", Finished: []string{"Done", "Canceled", "Duplicate"}, Paused: []string{"Backlog", "Todo"},
			},
		}},
		Repos: config.Repos{"dispatch": {Path: "/work/dispatch", OpenPRAs: config.OpenPRAsOpen}},
		Rules: config.ShippedRules(),
	}
	return &scenario{
		host: h, rules: rules, cfg: cfg, led: &ledger.Ledger{}, now: scenarioStart, dirty: true,
		m:      newModel(),
		thrown: map[string]bool{}, pools: map[string]int{}, poolUnread: map[string]bool{}, prUnread: map[string]bool{},
	}
}

func (s *scenario) fail(format string, args ...any) {
	panic(s.host.rt.NewGoError(fmt.Errorf(format, args...)))
}

func (s *scenario) ms() int64 { return s.now.UnixMilli() }

// tick moves the clock on, so an item seen later is newer than one seen before.
func (s *scenario) tick() {
	s.now = s.now.Add(time.Second)
}

// object builds the s a scenario body gets.
func (s *scenario) object() *sobek.Object {
	rt := s.host.rt
	o := rt.NewObject()
	set := func(name string, fn func(sobek.FunctionCall) sobek.Value) { _ = o.Set(name, fn) }
	fixtures := func(call sobek.FunctionCall) []map[string]any {
		var out []map[string]any
		for _, a := range call.Arguments {
			m, ok := a.Export().(map[string]any)
			if !ok {
				s.fail("expected fixtures made by ticket(), worker(), pool(), comment(), pr() or tree()")
			}
			out = append(out, m)
		}
		return out
	}
	set("config", func(call sobek.FunctionCall) sobek.Value {
		m, _ := call.Argument(0).Export().(map[string]any)
		s.applyConfig(m)
		s.dirty = true
		return sobek.Undefined()
	})
	set("given", func(call sobek.FunctionCall) sobek.Value {
		for _, f := range fixtures(call) {
			s.apply(f, false)
		}
		s.dirty = true
		return sobek.Undefined()
	})
	set("see", func(call sobek.FunctionCall) sobek.Value {
		s.tick()
		for _, f := range fixtures(call) {
			s.apply(f, true)
		}
		s.cycle()
		return sobek.Undefined()
	})
	set("land", func(call sobek.FunctionCall) sobek.Value {
		kind, subject := call.Argument(0).String(), call.Argument(1).String()
		outcome, detail := ledger.Done, ""
		if opts, ok := call.Argument(2).Export().(map[string]any); ok {
			if v, ok := opts["outcome"].(string); ok {
				outcome = ledger.Outcome(v)
			}
			detail, _ = opts["detail"].(string)
		}
		s.land(kind, subject, outcome, detail)
		return sobek.Undefined()
	})
	set("unread", func(call sobek.FunctionCall) sobek.Value {
		s.unread(call.Argument(0).String())
		s.tick()
		s.cycle()
		return sobek.Undefined()
	})
	set("resync", func(call sobek.FunctionCall) sobek.Value { s.tick(); s.cycle(); return sobek.Undefined() })
	set("start", func(call sobek.FunctionCall) sobek.Value { s.cycle(); return sobek.Undefined() })
	set("restart", func(call sobek.FunctionCall) sobek.Value {
		// The world and the ledger come back as they were. Reading them back
		// through history arrives with the reconcile loop.
		s.events = nil
		s.tick()
		s.cycle()
		return sobek.Undefined()
	})
	set("advance", func(call sobek.FunctionCall) sobek.Value {
		s.now = s.now.Add(time.Duration(call.Argument(0).ToFloat()) * time.Millisecond)
		s.cycle()
		return sobek.Undefined()
	})
	set("expect", func(call sobek.FunctionCall) sobek.Value {
		s.ensureCycle()
		s.noStrayThrows()
		s.expect(call.Argument(0).Export())
		return sobek.Undefined()
	})
	set("expectHeld", func(call sobek.FunctionCall) sobek.Value {
		s.ensureCycle()
		s.noStrayThrows()
		rule, subject := call.Argument(0).String(), call.Argument(1).String()
		reason := ""
		if v := call.Argument(2); !sobek.IsUndefined(v) {
			reason = v.String()
		}
		for _, h := range s.last.Held {
			if h.Rule == rule && h.Subject == subject && strings.Contains(h.Reason, reason) {
				return sobek.Undefined()
			}
		}
		s.fail("expected %s to be held for %s (%s), but the cycle held %s", rule, subject, reason, describeHeld(s.last))
		return sobek.Undefined()
	})
	set("expectWaiting", func(call sobek.FunctionCall) sobek.Value {
		s.ensureCycle()
		s.noStrayThrows()
		rule, subject := call.Argument(0).String(), call.Argument(1).String()
		for _, w := range s.last.Waiting {
			if w.Rule == rule && w.Subject == subject {
				return sobek.Undefined()
			}
		}
		s.fail("expected %s to wait for %s, but the cycle waits on %d actions", rule, subject, len(s.last.Waiting))
		return sobek.Undefined()
	})
	set("expectThrew", func(call sobek.FunctionCall) sobek.Value {
		s.ensureCycle()
		rule := call.Argument(0).String()
		for _, t := range s.last.Throws {
			if t.Rule == rule {
				s.thrown[rule] = true
				return sobek.Undefined()
			}
		}
		s.fail("expected %s to throw, but it did not", rule)
		return sobek.Undefined()
	})
	return o
}

func describeHeld(r Result) string {
	if len(r.Held) == 0 {
		return "nothing"
	}
	parts := make([]string, len(r.Held))
	for i, h := range r.Held {
		parts[i] = fmt.Sprintf("%s for %s (%s)", h.Rule, h.Subject, h.Reason)
	}
	return strings.Join(parts, ", ")
}

func (s *scenario) ensureCycle() {
	if s.dirty {
		s.cycle()
	}
}

// noStrayThrows fails the scenario when a rule threw or ran out of time, so a
// broken rule can't pass a scenario that expects it to want nothing.
func (s *scenario) noStrayThrows() {
	for _, t := range s.last.Throws {
		if !s.thrown[t.Rule] {
			s.fail("%s threw in its %s for %s: %s. Use expectThrew(%q) if that is what the scenario is about", t.Rule, t.Phase, t.Subject, t.Error, t.Rule)
		}
	}
}

// cycle runs the rules over the world as it is, with the events seen since the last cycle.
func (s *scenario) cycle() {
	state := s.state()
	s.last = s.rules.Cycle(state, s.events)
	s.events, s.dirty, s.ran = nil, false, true
}

func (s *scenario) state() *State {
	s.trackWaiting()
	w := s.world()
	if s.herdrUnread {
		w.HerdrErr = errors.New("scenario marked it unread")
	}
	for repo := range s.poolUnread {
		w.UnreadRepos[repo] = true
		w.PoolErrs[repo] = errors.New("scenario marked it unread")
	}
	for id := range s.prUnread {
		w.PRReadFailed[id] = true
	}
	state := FromWorld(w, s.cfg, s.led, "you")
	for repo, returns := range s.pools {
		if p, ok := state.pools[repo]; ok && p.unknown == "" {
			state.pools[repo] = pool{returns: returns}
		}
	}
	s.stamped(state)
	return state
}

// land writes the receipt of a wanted action, as if the executor had run it.
func (s *scenario) land(kind, subject string, outcome ledger.Outcome, detail string) {
	s.ensureCycle()
	for _, w := range append(slices.Clone(s.last.Wants), waitingWants(s.last)...) {
		if w.Kind == kind && w.Subject == subject {
			s.tick()
			rc := ledger.Receipt{Kind: w.Receipt, Subject: w.Subject, Once: w.Once, Rule: w.Rule, Outcome: outcome, Detail: detail, Started: s.ms(), Ended: s.ms()}
			if w.Kind == kindLinearEscalate {
				rc.Detail, _ = w.Args["why"].(string)
				rc.Steps = []ledger.Step{{Name: "move", Outcome: outcome}, {Name: "comment", Outcome: outcome}}
			}
			s.led.Add(rc)
			s.cycle()
			return
		}
	}
	s.fail("land(%q, %q): the cycle wants no such action. It wants %s", kind, subject, describeWants(s.last))
}

func waitingWants(r Result) []Want {
	out := make([]Want, len(r.Waiting))
	for i, w := range r.Waiting {
		out[i] = w.Want
	}
	return out
}

func describeWants(r Result) string {
	if len(r.Wants) == 0 {
		return "nothing"
	}
	parts := make([]string, len(r.Wants))
	for i, w := range r.Wants {
		parts[i] = wantLine(w)
	}
	return strings.Join(parts, ", ")
}

func wantLine(w Want) string {
	line := fmt.Sprintf("[%s %s %s]", w.Rule, w.Kind, w.Subject)
	if status, ok := w.Args["status"].(string); ok {
		line = fmt.Sprintf("[%s %s %s %s]", w.Rule, w.Kind, w.Subject, status)
	}
	return line
}

// expect compares the actions the last cycle wants with the list, ignoring
// order. A [rule, kind, subject] entry matches a move to any status.
func (s *scenario) expect(v any) {
	list, _ := v.([]any)
	var want []string
	for _, item := range list {
		parts, _ := item.([]any)
		strs := make([]string, len(parts))
		for i, p := range parts {
			strs[i] = fmt.Sprint(p)
		}
		if len(strs) < 3 {
			s.fail("expect takes [rule, kind, subject] lists, got %v", item)
		}
		want = append(want, "["+strings.Join(strs, " ")+"]")
	}
	got := make([]string, len(s.last.Wants))
	for i, w := range s.last.Wants {
		got[i] = wantLine(w)
		if _, ok := w.Args["status"].(string); ok && !slices.Contains(want, got[i]) {
			if short := fmt.Sprintf("[%s %s %s]", w.Rule, w.Kind, w.Subject); slices.Contains(want, short) {
				got[i] = short
			}
		}
	}
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(want, got) {
		s.fail("expected the cycle to want %s but it wants %s%s", listOrNothing(want), listOrNothing(got), s.explainWaiting())
	}
}

func listOrNothing(l []string) string {
	if len(l) == 0 {
		return "nothing"
	}
	return strings.Join(l, ", ")
}

func (s *scenario) explainWaiting() string {
	var parts []string
	for _, w := range s.last.Waiting {
		parts = append(parts, fmt.Sprintf("%s waits (%s)", wantLine(w.Want), w.Reason))
	}
	for _, h := range s.last.Held {
		parts = append(parts, fmt.Sprintf("%s held for %s (%s)", h.Rule, h.Subject, h.Reason))
	}
	for _, t := range s.last.Throws {
		parts = append(parts, fmt.Sprintf("%s threw for %s: %s", t.Rule, t.Subject, t.Error))
	}
	if len(parts) == 0 {
		return ""
	}
	return ". Also: " + strings.Join(parts, "; ")
}

func (s *scenario) unread(source string) {
	switch {
	case source == "herdr":
		s.herdrUnread = true
	case source == "treehouse":
		for repo := range s.cfg.Repos {
			s.poolUnread[repo] = true
		}
	case strings.HasPrefix(source, "treehouse:"):
		s.poolUnread[strings.TrimPrefix(source, "treehouse:")] = true
	case strings.HasPrefix(source, "github:"):
		s.prUnread[strings.TrimPrefix(source, "github:")] = true
	default:
		s.fail("unread(%q): the sources are herdr, treehouse, treehouse:<repo> and github:<ticket>", source)
	}
	s.dirty = true
}

// applyConfig sets what a scenario's config differs in.
func (s *scenario) applyConfig(m map[string]any) {
	for key, v := range m {
		switch key {
		case "maxInProgress":
			s.cfg.Dispatch.MaxInProgress = int(toInt(v))
		case "rules":
			table, _ := v.(map[string]any)
			for k, val := range table {
				if f, ok := val.(float64); ok && f == float64(int64(f)) {
					val = int64(f)
				}
				if i, ok := val.(int64); ok {
					s.cfg.Rules[k] = i
				} else {
					s.cfg.Rules[k] = val
				}
			}
		case "repos":
			table, _ := v.(map[string]any)
			for label, settings := range table {
				repo := config.Repo{Path: "/work/" + label, OpenPRAs: config.OpenPRAsOpen}
				if fields, ok := settings.(map[string]any); ok {
					if b, ok := fields["autoMerge"].(bool); ok {
						repo.AutoMerge = b
					}
					if b, ok := fields["openPrAs"].(string); ok {
						repo.OpenPRAs = b
					}
				}
				s.cfg.Repos[label] = repo
			}
		default:
			s.fail("config(%q): a scenario can set maxInProgress, rules and repos", key)
		}
	}
}

func toInt(v any) int64 {
	switch v := v.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}
