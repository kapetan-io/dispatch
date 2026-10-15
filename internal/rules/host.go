package rules

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/grafana/sobek"
)

// Timeouts bound one rule call on the wall clock. The zero value means 1
// second for an if and 30 seconds for a then.
type Timeouts struct {
	If, Then time.Duration
}

func (t Timeouts) forPhase(phase string) time.Duration {
	if phase == phaseIf {
		if t.If > 0 {
			return t.If
		}
		return time.Second
	}
	if t.Then > 0 {
		return t.Then
	}
	return 30 * time.Second
}

const (
	phaseIf   = "if"
	phaseThen = "then"
	// maxCallDepth stops runaway recursion in a rule.
	maxCallDepth = 256
)

// ruleDef is one rule as the rules registered it.
type ruleDef struct {
	name string
	// each is the record kind of a whenever rule, event the event type of an on rule.
	each, event string
	shadow      bool
	// file is where the rule was defined, for messages.
	order int
}

func (d *ruleDef) isEvent() bool { return d.event != "" }

// Rules is a loaded set of rules: the program they compile to and what each registered.
type Rules struct {
	Dir      string
	program  *sobek.Program
	defs     []*ruleDef
	timeouts Timeouts
}

// Defs lists the rules in the order they were registered.
func (r *Rules) Defs() []RuleInfo {
	out := make([]RuleInfo, len(r.defs))
	for i, d := range r.defs {
		out[i] = RuleInfo{Name: d.name, Each: d.each, Event: d.event, Shadow: d.shadow}
	}
	return out
}

// RuleInfo describes one loaded rule.
type RuleInfo struct {
	Name   string
	Each   string
	Event  string
	Shadow bool
}

// Kind is "whenever" or "on".
func (r RuleInfo) Kind() string {
	if r.Event != "" {
		return "on"
	}
	return "whenever"
}

// Target is the record kind or event type the rule runs on.
func (r RuleInfo) Target() string {
	if r.Event != "" {
		return r.Event
	}
	return r.Each
}

// LoadError is a rule file that can't be loaded. It stops the start.
type LoadError struct {
	Problems []string
}

func (e *LoadError) Error() string {
	return "rules failed to load:\n  " + strings.Join(e.Problems, "\n  ")
}

// Load bundles the rules under dir, starting at entry (such as "dispatch.ts"),
// and checks them. A rule that fails to load stops the start.
func Load(dir, entry string, timeouts Timeouts) (*Rules, error) {
	code, err := bundle(filepath.Join(dir, entry), dir, nil)
	if err != nil {
		return nil, err
	}
	return compile(dir, code, timeouts)
}

func compile(dir, code string, timeouts Timeouts) (*Rules, error) {
	program, err := sobek.Compile("rules.js", code, false)
	if err != nil {
		return nil, &LoadError{Problems: []string{err.Error()}}
	}
	r := &Rules{Dir: dir, program: program, timeouts: timeouts}
	problems := lintSource(code)
	rt := newRuntime(0)
	reg := &registry{}
	if err := reg.install(rt, nil); err != nil {
		return nil, err
	}
	stop := timeLimit(rt, timeouts.forPhase(phaseThen))
	_, err = rt.RunProgram(program)
	stop()
	if err != nil {
		problems = append(problems, "while loading: "+firstLine(err.Error()))
	}
	problems = append(problems, reg.problems...)
	if len(problems) > 0 {
		return nil, &LoadError{Problems: unique(problems)}
	}
	r.defs = reg.defs
	return r, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// bundle compiles a TypeScript entry file and everything it imports into one
// script. The "dispatch" module is the host's API, and virtual holds any other
// virtual modules by name.
func bundle(entry, resolveDir string, virtual map[string]string) (string, error) {
	modules := map[string]string{"dispatch": dispatchModule}
	for name, src := range virtual {
		modules[name] = src
	}
	entry, _ = filepath.Abs(entry)
	resolveDir, _ = filepath.Abs(resolveDir)
	res := api.Build(api.BuildOptions{
		EntryPoints:   []string{entry},
		AbsWorkingDir: resolveDir,
		Bundle:        true,
		Format:        api.FormatIIFE,
		Target:        api.ES2017,
		Write:         false,
		LogLevel:      api.LogLevelSilent,
		Plugins: []api.Plugin{{Name: "dispatch", Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: `^dispatch(/.*)?$`}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
				if _, ok := modules[a.Path]; !ok {
					return api.OnResolveResult{}, fmt.Errorf("there is no module %q", a.Path)
				}
				return api.OnResolveResult{Path: a.Path, Namespace: "dispatch"}, nil
			})
			b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "dispatch"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
				src := modules[a.Path]
				return api.OnLoadResult{Contents: &src, Loader: api.LoaderJS}, nil
			})
		}}},
	})
	if len(res.Errors) > 0 {
		var problems []string
		for _, e := range res.Errors {
			where := ""
			if e.Location != nil {
				where = fmt.Sprintf("%s:%d:%d: ", e.Location.File, e.Location.Line, e.Location.Column)
			}
			problems = append(problems, where+e.Text)
		}
		return "", &LoadError{Problems: problems}
	}
	return string(res.OutputFiles[0].Contents), nil
}

// dispatchModule is what `import ... from "dispatch"` gives a rule.
const dispatchModule = `
const d = globalThis.__dispatch;
export const whenever = d.whenever;
export const on = d.on;
export const known = d.known;
export const minutes = d.minutes;
export const hours = d.hours;
`

// prelude pins the sources of nondeterminism and removes what a rule must not reach.
const prelude = `
(function () {
  const g = globalThis;
  class Unknown extends Error {
    constructor(reason) { super(reason); this.name = "Unknown"; this.reason = reason; }
  }
  g.Unknown = Unknown;
  const NOW = g.__now;
  const RealDate = g.Date;
  function PinnedDate(...args) {
    if (!new.target) return new RealDate(NOW).toString();
    return args.length === 0 ? new RealDate(NOW) : new RealDate(...args);
  }
  PinnedDate.prototype = RealDate.prototype;
  Object.defineProperty(RealDate.prototype, "constructor", { value: PinnedDate, writable: true, configurable: true });
  PinnedDate.now = () => NOW;
  PinnedDate.parse = RealDate.parse;
  PinnedDate.UTC = RealDate.UTC;
  for (const name of Object.getOwnPropertyNames(RealDate.prototype)) {
    if (/^(get|set)(?!UTC)/.test(name) && name !== "getTime" && name !== "setTime" || /^to(Locale|String|DateString|TimeString)/.test(name) && name !== "toISOString") {
      try { delete RealDate.prototype[name]; } catch (e) {}
    }
  }
  g.Date = PinnedDate;
  Math.random = function () { throw new Error("Math.random is not available in a rule, because a cycle must be a function of the world, the ledger and ctx.now"); };
  const FunctionProto = Object.getPrototypeOf(function () {});
  try { FunctionProto.constructor = undefined; } catch (e) {}
  g.eval = undefined;
  g.Function = undefined;
  g.Promise = undefined;
  delete g.__now;
})();
`

var preludeProgram = func() *sobek.Program {
	p, err := sobek.Compile("prelude.js", prelude, false)
	if err != nil {
		panic(err)
	}
	return p
}()

func newRuntime(nowMillis int64) *sobek.Runtime {
	rt := sobek.New()
	rt.SetMaxCallStackSize(maxCallDepth)
	_ = rt.Set("__now", nowMillis)
	if _, err := rt.RunProgram(preludeProgram); err != nil {
		panic(err)
	}
	return rt
}

// registry collects the rules a program registers.
type registry struct {
	defs     []*ruleDef
	problems []string
	// fns holds each rule's callbacks by name, for a call runtime.
	fns map[string]ruleFns
}

type ruleFns struct {
	ifFn, thenFn sobek.Callable
}

// install puts the dispatch API into rt. known is the implementation of
// known() for a call, or nil while registering.
func (reg *registry) install(rt *sobek.Runtime, known func(sobek.FunctionCall) sobek.Value) error {
	reg.fns = map[string]ruleFns{}
	api := rt.NewObject()
	register := func(kind string) func(sobek.FunctionCall) sobek.Value {
		return func(call sobek.FunctionCall) sobek.Value {
			reg.register(rt, kind, call)
			return sobek.Undefined()
		}
	}
	_ = api.Set("whenever", register("whenever"))
	_ = api.Set("on", register("on"))
	if known == nil {
		known = func(call sobek.FunctionCall) sobek.Value { return sobek.Undefined() }
	}
	_ = api.Set("known", known)
	_ = api.Set("minutes", func(call sobek.FunctionCall) sobek.Value {
		return rt.ToValue(call.Argument(0).ToFloat() * float64(time.Minute/time.Millisecond))
	})
	_ = api.Set("hours", func(call sobek.FunctionCall) sobek.Value {
		return rt.ToValue(call.Argument(0).ToFloat() * float64(time.Hour/time.Millisecond))
	})
	return rt.Set("__dispatch", api)
}

func (reg *registry) problem(format string, args ...any) {
	reg.problems = append(reg.problems, fmt.Sprintf(format, args...))
}

func (reg *registry) register(rt *sobek.Runtime, kind string, call sobek.FunctionCall) {
	obj, ok := call.Argument(0).(*sobek.Object)
	if !ok {
		reg.problem("%s() takes one object with a name, an if and a then", kind)
		return
	}
	str := func(key string) string {
		v := obj.Get(key)
		if v == nil || sobek.IsUndefined(v) || sobek.IsNull(v) {
			return ""
		}
		return v.String()
	}
	name := str("name")
	def := &ruleDef{name: name, each: str("each"), event: str("event"), order: len(reg.defs)}
	if v := obj.Get("shadow"); v != nil && !sobek.IsUndefined(v) {
		def.shadow = v.ToBoolean()
	}
	label := fmt.Sprintf("rule %q", name)
	if name == "" {
		label = fmt.Sprintf("a %s rule with no name", kind)
		reg.problem("%s", label)
	}
	switch {
	case kind == "whenever" && def.event != "":
		reg.problem("%s: whenever takes each, not event. Use on for an event", label)
	case kind == "on" && def.each != "":
		reg.problem("%s: on takes event, not each. Use whenever for a record kind", label)
	case kind == "whenever" && def.each == "":
		reg.problem("%s: whenever needs each, one of %s", label, strings.Join(kindNames(), ", "))
	case kind == "on" && def.event == "":
		reg.problem("%s: on needs event, one of %s", label, strings.Join(eventNames(), ", "))
	case kind == "whenever":
		if _, ok := kindOf(def.each); !ok {
			reg.problem("%s: there is no record kind %q. The kinds are %s", label, def.each, strings.Join(kindNames(), ", "))
		}
	case kind == "on":
		if !slices.Contains(eventNames(), def.event) {
			reg.problem("%s: there is no event %q. The events are %s", label, def.event, strings.Join(eventNames(), ", "))
		}
	}
	fns := ruleFns{}
	for _, phase := range []string{phaseIf, phaseThen} {
		fn, ok := sobek.AssertFunction(obj.Get(phase))
		if !ok {
			reg.problem("%s: %s must be a function", label, phase)
			continue
		}
		if phase == phaseIf {
			fns.ifFn = fn
		} else {
			fns.thenFn = fn
		}
	}
	if name != "" {
		if slices.ContainsFunc(reg.defs, func(d *ruleDef) bool { return d.name == name }) {
			reg.problem("rule %q is defined twice. Rule names must be unique", name)
			return
		}
		reg.fns[name] = fns
	}
	reg.defs = append(reg.defs, def)
}

func eventNames() []string {
	names := make([]string, len(Events))
	for i, e := range Events {
		names[i] = e.Name
	}
	return names
}
