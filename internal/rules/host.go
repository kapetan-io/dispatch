package rules

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/grafana/sobek"

	"github.com/kapetan-io/dispatch/internal/clock"
)

// Timeouts bound one rule call on Clock, which defaults to the wall clock. The zero value
// means 1 second for an if and 30 seconds for a then.
type Timeouts struct {
	If, Then time.Duration
	Clock    clock.Clock
}

func (t Timeouts) clock() clock.Clock {
	if t.Clock == nil {
		return clock.Wall{}
	}
	return t.Clock
}

func (t Timeouts) now() time.Time { return t.clock().Now() }

// limit interrupts rt once the clock passes the budget of phase, and returns the stop function.
// what names the deadline, see clock.Clock.NewDeadline.
func (t Timeouts) limit(rt *sobek.Runtime, what, phase string) (stop func() bool) {
	return clock.AfterFunc(t.clock(), what, t.forPhase(phase), func() { rt.Interrupt("timeout") })
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
	// exempts names the rules an exemption switches off.
	exempts []string
}

func (d *ruleDef) isEvent() bool { return d.event != "" }

// Rules is a loaded set of rules: the program they compile to and what each registered.
type Rules struct {
	Dir        string
	source     string
	program    *sobek.Program
	defs       []*ruleDef
	exemptions []*ruleDef
	timeouts   Timeouts
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

// Exemptions lists the exempt declarations in the order they were registered.
func (r *Rules) Exemptions() []ExemptionInfo {
	out := make([]ExemptionInfo, len(r.exemptions))
	for i, d := range r.exemptions {
		out[i] = ExemptionInfo{Name: d.name, Each: d.each, Rules: d.exempts}
	}
	return out
}

// ExemptionInfo describes one loaded exemption.
type ExemptionInfo struct {
	Name  string
	Each  string
	Rules []string
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
	return LoadEntries(dir, []string{filepath.Join(dir, entry)}, timeouts)
}

// LoadEntries is Load for several entry files, bundled as if one file imported each in order.
func LoadEntries(dir string, entries []string, timeouts Timeouts) (*Rules, error) {
	code, err := bundle(entries, dir, nil)
	if err != nil {
		return nil, err
	}
	return compile(dir, code, timeouts)
}

// Source is the bundled script the rules run as, which history keeps so a replay
// runs the rules the live run did even after the files change.
func (r *Rules) Source() string { return r.source }

// Compile loads rules from the script Source returned, with dir as the directory
// they came from.
func Compile(dir, source string, timeouts Timeouts) (*Rules, error) {
	return compile(dir, source, timeouts)
}

func compile(dir, code string, timeouts Timeouts) (*Rules, error) {
	program, err := sobek.Compile("rules.js", code, false)
	if err != nil {
		return nil, &LoadError{Problems: []string{err.Error()}}
	}
	r := &Rules{Dir: dir, source: code, program: program, timeouts: timeouts}
	problems := lintSource(code)
	rt := newRuntime(0)
	reg := &registry{}
	if err := reg.install(rt, nil); err != nil {
		return nil, err
	}
	stop := timeouts.limit(rt, "rules load", phaseThen)
	_, err = rt.RunProgram(program)
	stop()
	if err != nil {
		problems = append(problems, "while loading: "+firstLine(err.Error()))
	}
	if err == nil {
		reg.checkExemptions()
	}
	problems = append(problems, reg.problems...)
	if len(problems) > 0 {
		return nil, &LoadError{Problems: unique(problems)}
	}
	r.defs = reg.defs
	r.exemptions = reg.exemptions
	return r, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// bundle compiles a TypeScript entry file and everything it imports into one
// script. The "dispatch" module is the host's API, and virtual holds any other
// virtual modules by name.
func bundle(entries []string, resolveDir string, virtual map[string]string) (string, error) {
	modules := map[string]string{"dispatch": dispatchModule}
	for name, src := range virtual {
		modules[name] = src
	}
	resolveDir, _ = filepath.Abs(resolveDir)
	opts := api.BuildOptions{
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
	}
	if len(entries) == 1 {
		abs, _ := filepath.Abs(entries[0])
		opts.EntryPoints = []string{abs}
	} else {
		var imports strings.Builder
		for _, entry := range entries {
			abs, _ := filepath.Abs(entry)
			fmt.Fprintf(&imports, "import %q;\n", abs)
		}
		opts.Stdin = &api.StdinOptions{Contents: imports.String(), ResolveDir: resolveDir, Sourcefile: "entries.ts", Loader: api.LoaderTS}
	}
	res := api.Build(opts)
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
export const exempt = d.exempt;
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
	defs       []*ruleDef
	exemptions []*ruleDef
	problems   []string
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
	_ = api.Set("exempt", register("exempt"))
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

// taken reports whether a rule or an exemption already has name.
func (reg *registry) taken(name string) bool {
	byName := func(d *ruleDef) bool { return d.name == name }
	return slices.ContainsFunc(reg.defs, byName) || slices.ContainsFunc(reg.exemptions, byName)
}

// registerExemption records an exempt declaration, which has an if and the names of the rules it covers.
func (reg *registry) registerExemption(obj *sobek.Object, str func(string) string) {
	name := str("name")
	def := &ruleDef{name: name, each: str("each"), event: str("event"), order: len(reg.exemptions)}
	label := fmt.Sprintf("exemption %q", name)
	if name == "" {
		label = "an exemption with no name"
		reg.problem("%s", label)
	}
	switch {
	case def.event != "":
		reg.problem("%s: exempt takes each, not event, because it covers whenever rules only. Use each", label)
	case def.each == "":
		reg.problem("%s: exempt needs each, one of %s", label, strings.Join(kindNames(), ", "))
	default:
		if _, ok := kindOf(def.each); !ok {
			reg.problem("%s: there is no record kind %q. The kinds are %s", label, def.each, strings.Join(kindNames(), ", "))
		}
	}
	if v := obj.Get("then"); v != nil && !sobek.IsUndefined(v) {
		reg.problem("%s: exempt takes no then, because it only takes away what rules want", label)
	}
	var listed any
	if v := obj.Get("rules"); v != nil {
		listed = v.Export()
	}
	switch names := listed.(type) {
	case []any:
		for _, n := range names {
			if s, ok := n.(string); ok {
				def.exempts = append(def.exempts, s)
				continue
			}
			reg.problem("%s: rules must be strings, the names of whenever rules", label)
		}
	default:
		reg.problem("%s: rules must be a list of rule names", label)
	}
	if len(def.exempts) == 0 {
		reg.problem("%s: rules is empty. List the whenever rules it covers", label)
	}
	fn, ok := sobek.AssertFunction(obj.Get(phaseIf))
	if !ok {
		reg.problem("%s: %s must be a function", label, phaseIf)
	}
	if name != "" {
		if reg.taken(name) {
			reg.problem("%s reuses a name already taken. Rule and exemption names must be unique", label)
			return
		}
		reg.fns[name] = ruleFns{ifFn: fn}
	}
	reg.exemptions = append(reg.exemptions, def)
}

// checkExemptions reports each exemption that names a rule it can't cover, once every file has registered.
func (reg *registry) checkExemptions() {
	for _, ex := range reg.exemptions {
		for _, name := range ex.exempts {
			i := slices.IndexFunc(reg.defs, func(d *ruleDef) bool { return d.name == name })
			switch {
			case i < 0:
				reg.problem("exemption %q names rule %q, which no file defines", ex.name, name)
			case reg.defs[i].isEvent():
				reg.problem("exemption %q names rule %q, an on rule. exempt covers whenever rules only", ex.name, name)
			case reg.defs[i].each != ex.each:
				reg.problem("exemption %q is each: %q but names rule %q, which is each: %q", ex.name, ex.each, name, reg.defs[i].each)
			}
		}
	}
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
	if kind == "exempt" {
		reg.registerExemption(obj, str)
		return
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
		if reg.taken(name) {
			reg.problem("%s reuses the name of an exemption. Rule and exemption names must be unique", label)
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
