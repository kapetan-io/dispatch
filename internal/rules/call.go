package rules

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/grafana/sobek"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/ledger"
)

// maxOnceItems bounds a once key, which is a topic and a few world values.
const maxOnceItems = 32

// queued is an action a then asked for.
type queued struct {
	id      int
	spec    ActionSpec
	subject *record
	args    map[string]any
	once    ledger.Once
	opts    map[string]any
}

// call is one run of one rule's if or then, in a runtime of its own.
type call struct {
	state *State
	rt    *sobek.Runtime
	phase string
	// knownDepth counts the known() calls the rule is inside.
	knownDepth int
	// sawUnknown is set when an unknown read threw, even if the rule caught
	// the error, so known() can tell that the value was not read.
	sawUnknown bool
	// unknown holds the reason of each unknown read outside known(). The host
	// holds the call when it is not empty, even if the rule caught the error.
	unknown []string
	reads   []string
	queue   []*queued
	objects map[string]*sobek.Object
	// handles maps the object a queue method returned to its place in queue, so a
	// then can only return actions it queued, and reading one runs no rule code.
	handles map[*sobek.Object]int
}

func (c *call) throwUnknown(reason string) {
	c.sawUnknown = true
	obj, err := c.rt.New(c.rt.Get("Unknown"), c.rt.ToValue(reason))
	if err != nil {
		panic(err)
	}
	panic(obj)
}

func (c *call) throwError(format string, args ...any) {
	obj, err := c.rt.New(c.rt.Get("Error"), c.rt.ToValue(fmt.Sprintf(format, args...)))
	if err != nil {
		panic(err)
	}
	panic(obj)
}

// read notes that the rule looked at a value, so a check can show what it decided from.
func (c *call) read(name string, v any) {
	text := fmt.Sprintf("%s=%s", name, short(v))
	if !slices.Contains(c.reads, text) {
		c.reads = append(c.reads, text)
	}
}

func short(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case nil:
		return "none"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// recordObject is a record as a rule sees it: read-only, and throwing on a
// fact that is unknown.
type recordObject struct {
	c *call
	r *record
}

func (c *call) recordValue(r *record) sobek.Value {
	key := r.String()
	if o, ok := c.objects[key]; ok {
		return o
	}
	o := c.rt.NewDynamicObject(&recordObject{c: c, r: r})
	c.objects[key] = o
	return o
}

func (o *recordObject) Get(key string) sobek.Value {
	c, r := o.c, o.r
	if key == "hasLabel" && r.kind == "linear.ticket" {
		return c.rt.ToValue(func(call sobek.FunctionCall) sobek.Value { return c.hasLabel(r, call.Argument(0).String()) })
	}
	f, ok := r.fact(key)
	if !ok {
		return nil
	}
	if reason := f.FieldByName("Unknown").String(); reason != "" {
		if c.knownDepth == 0 {
			c.unknown = append(c.unknown, reason)
		}
		c.read(key, "unknown")
		c.throwUnknown(reason)
	}
	value := f.FieldByName("Value")
	c.read(key, exportable(value))
	return c.toJS(value, tagOf(r, key, "ref"))
}

func tagOf(r *record, key, tag string) string {
	i := fieldIndex[r.v.Elem().Type()][key]
	return r.v.Elem().Type().Field(i).Tag.Get(tag)
}

// hasLabel reads the ticket's labels, so a ticket whose labels were cut short holds.
func (c *call) hasLabel(r *record, name string) sobek.Value {
	f, _ := r.fact("labels")
	if reason := f.FieldByName("Unknown").String(); reason != "" {
		if c.knownDepth == 0 {
			c.unknown = append(c.unknown, reason)
		}
		c.read("labels", "unknown")
		c.throwUnknown(reason)
	}
	labels := f.FieldByName("Value").Interface().([]Label)
	has := slices.ContainsFunc(labels, func(l Label) bool { return l.Name == name })
	c.read("hasLabel("+name+")", has)
	return c.rt.ToValue(has)
}

func (o *recordObject) Set(key string, _ sobek.Value) bool {
	panic(o.c.rt.NewTypeError("%s is read-only: a rule cannot write %q", o.r, key))
}

func (o *recordObject) Has(key string) bool {
	_, ok := o.r.fact(key)
	return ok || (key == "hasLabel" && o.r.kind == "linear.ticket")
}

func (o *recordObject) Delete(key string) bool {
	panic(o.c.rt.NewTypeError("%s is read-only: a rule cannot delete %q", o.r, key))
}

func (o *recordObject) Keys() []string {
	var keys []string
	t := o.r.v.Elem().Type()
	for i := range t.NumField() {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("js"), ","); name != "" {
			keys = append(keys, name)
		}
	}
	return keys
}

// exportable turns a fact's value into something fit for a trace line.
func exportable(v reflect.Value) any {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Type() == reflect.TypeFor[Ref]() {
		return v.FieldByName("ID").String()
	}
	if v.Kind() == reflect.Struct || v.Kind() == reflect.Slice {
		b, _ := json.Marshal(v.Interface())
		return json.RawMessage(b)
	}
	return v.Interface()
}

// toJS converts a fact's value for a rule. ref names the record kind a Ref points to.
func (c *call) toJS(v reflect.Value, ref string) sobek.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return sobek.Undefined()
		}
		return c.toJS(v.Elem(), ref)
	case reflect.String:
		return c.rt.ToValue(v.String())
	case reflect.Bool:
		return c.rt.ToValue(v.Bool())
	case reflect.Int, reflect.Int64:
		return c.rt.ToValue(v.Int())
	case reflect.Float64:
		return c.rt.ToValue(v.Float())
	case reflect.Slice:
		items := make([]any, v.Len())
		for i := range v.Len() {
			items[i] = c.toJS(v.Index(i), ref)
		}
		return c.rt.NewArray(items...)
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[Ref]() {
			target, ok := c.state.lookup(v.FieldByName("Kind").String(), v.FieldByName("ID").String())
			if !ok {
				return sobek.Undefined()
			}
			return c.recordValue(target)
		}
		obj := c.rt.NewObject()
		for i := range v.NumField() {
			sf := v.Type().Field(i)
			name, opts, _ := strings.Cut(sf.Tag.Get("js"), ",")
			if name == "" {
				continue
			}
			if opts == "optional" && v.Field(i).IsZero() {
				continue
			}
			_ = obj.Set(name, c.toJS(v.Field(i), sf.Tag.Get("ref")))
		}
		return obj
	}
	return sobek.Undefined()
}

// known implements known(): true when read() touches no unknown value.
func (c *call) known(call sobek.FunctionCall) sobek.Value {
	fn, ok := sobek.AssertFunction(call.Argument(0))
	if !ok {
		panic(c.rt.NewTypeError("known() takes a function that reads the value"))
	}
	c.knownDepth++
	outer := c.sawUnknown
	c.sawUnknown = false
	defer func() { c.knownDepth--; c.sawUnknown = outer }()
	_, err := fn(sobek.Undefined())
	if err == nil {
		return c.rt.ToValue(!c.sawUnknown)
	}
	if ex, ok := err.(*sobek.Exception); ok {
		if obj, ok := ex.Value().(*sobek.Object); ok && obj.Get("name") != nil && obj.Get("name").String() == "Unknown" {
			return c.rt.ToValue(false)
		}
		panic(ex.Value())
	}
	panic(err)
}

// ctx builds the second argument of a rule's if and then.
func (c *call) ctx() sobek.Value {
	rt := c.rt
	ctx := rt.NewObject()
	now := c.state.ms()
	_ = ctx.Set("now", now)
	_ = ctx.Set("every", func(call sobek.FunctionCall) sobek.Value {
		period, since := call.Argument(0).ToFloat(), call.Argument(1).ToFloat()
		if !(period > 0) {
			c.throwError("every() needs a period above zero")
		}
		return rt.ToValue(int64(math.Floor((float64(now) - since) / period)))
	})
	_ = ctx.Set("config", c.configValue())

	led := rt.NewObject()
	_ = led.Set("receipts", func(call sobek.FunctionCall) sobek.Value {
		subject := c.subjectOf(call.Argument(0))
		var filter ledger.Filter
		if o, ok := call.Argument(1).(*sobek.Object); ok {
			if kinds := o.Get("kind"); kinds != nil && !sobek.IsUndefined(kinds) {
				if arr, ok := kinds.Export().([]any); ok {
					for _, k := range arr {
						filter.Kinds = append(filter.Kinds, fmt.Sprint(k))
					}
				} else {
					filter.Kinds = []string{kinds.String()}
				}
			}
			if topic := o.Get("topic"); topic != nil && !sobek.IsUndefined(topic) {
				filter.Topic = topic.String()
			}
		}
		var out []any
		for _, r := range c.state.Ledger.Receipts(subject, filter) {
			out = append(out, c.receiptValue(r))
		}
		return rt.NewArray(out...)
	})
	_ = led.Set("done", func(call sobek.FunctionCall) sobek.Value {
		once, err := c.parseOnce(call.Argument(1))
		if err != nil {
			c.throwError("%v", err)
		}
		return rt.ToValue(c.state.Ledger.Done(c.subjectOf(call.Argument(0)), once))
	})
	_ = ctx.Set("ledger", led)

	_ = ctx.Set("linear", c.linearAPI())
	_ = ctx.Set("herdr", c.herdrAPI())
	_ = ctx.Set("treehouse", c.treehouseAPI())
	_ = ctx.Set("github", c.pluginAPI("github"))
	_ = ctx.Set("notify", c.pluginAPI("notify"))
	return ctx
}

func (c *call) receiptValue(r ledger.Receipt) sobek.Value {
	o := c.rt.NewObject()
	_ = o.Set("kind", r.Kind)
	_ = o.Set("rule", r.Rule)
	_ = o.Set("detail", r.Detail)
	_ = o.Set("started", r.Started)
	if r.Outcome != "" {
		_ = o.Set("outcome", string(r.Outcome))
	}
	if r.Ended != 0 {
		_ = o.Set("ended", r.Ended)
	}
	if r.Reset {
		_ = o.Set("reset", true)
	}
	items := make([]any, len(r.Once))
	for i, item := range r.Once {
		items[i] = c.rt.ToValue(item)
	}
	_ = o.Set("once", c.rt.NewArray(items...))
	return o
}

// recordArg reads a record out of an argument a rule passed.
func (c *call) recordArg(v sobek.Value, want ...string) *record {
	if o, ok := v.(*sobek.Object); ok {
		if ro, ok := o.Export().(*recordObject); ok && (len(want) == 0 || slices.Contains(want, ro.r.kind)) {
			return ro.r
		}
	}
	if len(want) == 0 {
		c.throwError("expected a record")
	}
	c.throwError("expected a %s record", strings.Join(want, " or "))
	return nil
}

func (c *call) subjectOf(v sobek.Value) string {
	return c.recordArg(v).subject
}

func (c *call) parseOnce(v sobek.Value) (ledger.Once, error) {
	if v == nil {
		return nil, fmt.Errorf("once is missing. Every action needs once: [topic, ...values]")
	}
	if obj, isObj := v.(*sobek.Object); isObj && obj.ClassName() == "Array" && obj.Get("length").ToInteger() > maxOnceItems {
		return nil, fmt.Errorf("once has more than %d items. Items are the topic and the few world values that must change", maxOnceItems)
	}
	arr, ok := v.Export().([]any)
	if !ok || len(arr) == 0 {
		return nil, fmt.Errorf("once must be a list that starts with a topic, such as [\"comment\", t.lastHumanComment.at]")
	}
	topic, ok := arr[0].(string)
	if !ok || topic == "" {
		return nil, fmt.Errorf("the first item of once is its topic, and must be a non-empty string")
	}
	once := make(ledger.Once, len(arr))
	for i, item := range arr {
		switch item := item.(type) {
		case string, bool, nil:
			once[i] = item
		case int64:
			once[i] = item
		case float64:
			if item == math.Trunc(item) {
				once[i] = int64(item)
			} else {
				once[i] = item
			}
		default:
			return nil, fmt.Errorf("once item %d is a %T. Items are strings, numbers, booleans or undefined", i, item)
		}
	}
	return once, nil
}

// pluginAPI builds ctx.<plugin> with the plugin's action methods.
func (c *call) pluginAPI(plugin string) *sobek.Object {
	o := c.rt.NewObject()
	for _, spec := range Actions {
		if spec.Plugin == plugin {
			_ = o.Set(spec.Method, c.queueFn(spec))
		}
	}
	return o
}

func (c *call) queueFn(spec ActionSpec) func(sobek.FunctionCall) sobek.Value {
	return func(call sobek.FunctionCall) sobek.Value {
		if c.phase != phaseThen {
			c.throwError("%s can only be called in then. An if reads and never queues", spec.Method)
		}
		subjectKinds := map[string][]string{
			"Ticket": {"linear.ticket"}, "Worker": {"herdr.worker"}, "PR": {"github.pr"}, "Tree": {"treehouse.tree"},
		}[spec.Subject.TS]
		subject := c.recordArg(call.Argument(0), subjectKinds...)
		q := &queued{id: len(c.queue), spec: spec, subject: subject, args: map[string]any{}, opts: map[string]any{}}
		for i, p := range spec.Params {
			v := call.Argument(i + 1)
			if sobek.IsUndefined(v) || sobek.IsNull(v) {
				c.throwError("%s needs %s", spec.Method, p.Name)
			}
			q.args[p.Name] = v.Export()
		}
		opts, ok := call.Argument(len(spec.Params) + 1).(*sobek.Object)
		if !ok {
			c.throwError("%s needs an options object with a once key, as the last argument", spec.Method)
		}
		once, err := c.parseOnce(opts.Get("once"))
		if err != nil {
			c.throwError("%s: %v", spec.Method, err)
		}
		q.once = once
		for _, key := range opts.Keys() {
			if key != "once" {
				q.opts[key] = opts.Get(key).Export()
			}
		}
		if spec.Kind == kindLinearMove {
			status, _ := q.args["status"].(string)
			if _, ok := StatusName(status, c.state.Config.Plugins.Linear.Statuses); !ok {
				c.throwError("queueMove: %q is not a status key. Use todo, in_progress, in_review, needs_human, close_parent or blocker_done", status)
			}
		}
		c.queue = append(c.queue, q)
		handle := c.rt.NewObject()
		_ = handle.Set("queued", true)
		c.handles[handle] = q.id
		return handle
	}
}

func (c *call) linearAPI() *sobek.Object {
	o := c.pluginAPI("linear")
	rt, s := c.rt, c.state
	ticket := func(v sobek.Value) *record { return c.recordArg(v, "linear.ticket") }
	_ = o.Set("parent", func(call sobek.FunctionCall) sobek.Value {
		t := ticket(call.Argument(0))
		parent, ok := s.parents[t.id]
		if !ok {
			return sobek.Undefined()
		}
		if p, ok := s.lookup("linear.ticket", parent); ok {
			return c.recordValue(p)
		}
		return sobek.Undefined()
	})
	_ = o.Set("children", func(call sobek.FunctionCall) sobek.Value {
		t := ticket(call.Argument(0))
		hasChildren := t.v.Interface().(*Ticket).HasChildren.Value
		if reason, unread := s.childrenUnread[t.id]; unread {
			c.unknownRead(reason)
		}
		ids, ok := s.children[t.id]
		if hasChildren && !ok {
			c.unknownRead("the children of " + t.id + " are only read for an In Progress parent that isn't a design ticket")
		}
		out := make([]any, 0, len(ids))
		for _, id := range ids {
			if child, ok := s.lookup("linear.ticket", id); ok {
				out = append(out, c.recordValue(child))
			}
		}
		return rt.NewArray(out...)
	})
	blockerList := func(list []Blocker) sobek.Value {
		out := make([]any, len(list))
		for i, b := range list {
			out[i] = c.toJS(reflect.ValueOf(b), "")
		}
		return rt.NewArray(out...)
	}
	_ = o.Set("blockers", func(call sobek.FunctionCall) sobek.Value {
		t := ticket(call.Argument(0))
		b := s.blockers[t.id]
		if b.truncated {
			c.unknownRead(t.id + " has more relations than dispatch reads, so a blocker may be missing")
		}
		c.read("blockers", len(b.read))
		return blockerList(b.read)
	})
	_ = o.Set("blockersRead", func(call sobek.FunctionCall) sobek.Value {
		return blockerList(s.blockers[ticket(call.Argument(0)).id].read)
	})
	// Until history keeps when a stretch of waiting began, a reading has only its own
	// time, so this is the status time unless a scenario tracked the stretch.
	_ = o.Set("waitingSince", func(call sobek.FunctionCall) sobek.Value {
		t := ticket(call.Argument(0))
		if since, ok := s.waiting[t.id]; ok {
			return rt.ToValue(since)
		}
		return rt.ToValue(t.v.Interface().(*Ticket).StatusSince.Value)
	})
	return o
}

// unknownRead throws Unknown for a read the host couldn't make.
func (c *call) unknownRead(reason string) {
	if c.knownDepth == 0 {
		c.unknown = append(c.unknown, reason)
	}
	c.throwUnknown(reason)
}

func (c *call) herdrAPI() *sobek.Object {
	o := c.pluginAPI("herdr")
	s := c.state
	_ = o.Set("worker", func(call sobek.FunctionCall) sobek.Value {
		t := c.recordArg(call.Argument(0), "linear.ticket")
		w, ok := s.workers[t.id]
		if !ok {
			c.throwError("%s has no worker record", t.id)
		}
		return c.recordValue(w)
	})
	_ = o.Set("workerIn", func(call sobek.FunctionCall) sobek.Value {
		tree := c.recordArg(call.Argument(0), "treehouse.tree")
		w, ok := s.workersByTree[tree.id]
		if !ok {
			c.throwError("%s has no worker record", tree.id)
		}
		return c.recordValue(w)
	})
	return o
}

func (c *call) treehouseAPI() *sobek.Object {
	o := c.pluginAPI("treehouse")
	s := c.state
	_ = o.Set("pool", func(call sobek.FunctionCall) sobek.Value {
		repo := call.Argument(0)
		if sobek.IsUndefined(repo) || sobek.IsNull(repo) {
			c.unknownRead("the ticket has no repo, so there is no pool to ask")
		}
		p, ok := s.pools[repo.String()]
		if !ok {
			c.unknownRead(fmt.Sprintf("there is no pool for repo %q in config.toml", repo.String()))
		}
		if p.unknown != "" {
			c.unknownRead(p.unknown)
		}
		out := c.rt.NewObject()
		_ = out.Set("returns", p.returns)
		return out
	})
	_ = o.Set("leased", func(call sobek.FunctionCall) sobek.Value {
		t := c.recordArg(call.Argument(0), "linear.ticket")
		if reason, ok := s.leasedUnread[t.id]; ok {
			c.unknownRead(reason)
		}
		return c.rt.ToValue(s.leased[t.id])
	})
	return o
}

// configValue exposes the whole config read-only. A key a rule reads from
// [rules] that the file lacks throws, which dispatch rules check reports.
func (c *call) configValue() sobek.Value {
	root := configTree(c.state.Config)
	return c.rt.NewDynamicObject(&configObject{c: c, path: "", value: root})
}

type configObject struct {
	c     *call
	path  string
	value map[string]any
}

func (o *configObject) Get(key string) sobek.Value {
	c := o.c
	if o.path == "" && key == "hasRepo" {
		return c.rt.ToValue(func(call sobek.FunctionCall) sobek.Value {
			label := call.Argument(0)
			if sobek.IsUndefined(label) || sobek.IsNull(label) {
				return c.rt.ToValue(false)
			}
			_, ok := c.state.Config.Repos[label.String()]
			return c.rt.ToValue(ok)
		})
	}
	v, ok := o.value[key]
	if !ok {
		if o.path == "rules" || strings.HasPrefix(o.path, "rules.") {
			c.throwError("config.toml has no [%s] key %q. Add %s = ... under [%s]", o.path, key, key, o.path)
		}
		return nil
	}
	return c.configValueOf(joinPath(o.path, key), v)
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func (c *call) configValueOf(path string, v any) sobek.Value {
	switch v := v.(type) {
	case map[string]any:
		return c.rt.NewDynamicObject(&configObject{c: c, path: path, value: v})
	case []any:
		items := make([]any, len(v))
		for i, item := range v {
			items[i] = c.configValueOf(path, item)
		}
		return c.rt.NewArray(items...)
	case nil:
		return sobek.Undefined()
	}
	return c.rt.ToValue(v)
}

func (o *configObject) Set(key string, _ sobek.Value) bool {
	panic(o.c.rt.NewTypeError("ctx.config is read-only: a rule cannot write %q", key))
}
func (o *configObject) Has(key string) bool { _, ok := o.value[key]; return ok }
func (o *configObject) Delete(key string) bool {
	panic(o.c.rt.NewTypeError("ctx.config is read-only: a rule cannot delete %q", key))
}
func (o *configObject) Keys() []string {
	keys := make([]string, 0, len(o.value))
	for k := range o.value {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// configTree turns the config into nested maps keyed by its toml names.
func configTree(cfg config.Config) map[string]any {
	return toTree(reflect.ValueOf(cfg)).(map[string]any)
}

func toTree(v reflect.Value) any {
	if v.Type() == reflect.TypeFor[config.Duration]() {
		text, _ := v.Interface().(config.Duration).MarshalText()
		return string(text)
	}
	if v.Type() == reflect.TypeFor[config.Rules]() {
		out := map[string]any{}
		for k, val := range v.Interface().(config.Rules) {
			out[k] = val
		}
		return out
	}
	switch v.Kind() {
	case reflect.Struct:
		out := map[string]any{}
		for i := range v.NumField() {
			name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("toml"), ",")
			if name != "" {
				out[name] = toTree(v.Field(i))
			}
		}
		return out
	case reflect.Map:
		out := map[string]any{}
		for _, k := range v.MapKeys() {
			out[k.String()] = toTree(v.MapIndex(k))
		}
		return out
	case reflect.Slice:
		out := make([]any, v.Len())
		for i := range v.Len() {
			out[i] = toTree(v.Index(i))
		}
		return out
	case reflect.Int, reflect.Int64:
		return v.Int()
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return v.Bool()
	}
	return v.Interface()
}

// timeLimit interrupts a runtime when its budget runs out, and returns the stop function.
func timeLimit(rt *sobek.Runtime, d time.Duration) (stop func() bool) {
	t := time.AfterFunc(d, func() { rt.Interrupt("timeout") })
	return t.Stop
}
