package rules

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/grafana/sobek"

	"github.com/kapetan-io/dispatch/internal/ledger"
)

// Event is something that happened, for an on rule to match.
type Event struct {
	Type    string
	Subject string
	// Payload is the *Comment or *CheckRun the event carries.
	Payload any
}

// Want is an action a rule asked for.
type Want struct {
	Rule string
	// Receipt names the action, as its receipts and rule scenarios do.
	Receipt string
	Subject string
	Once    ledger.Once
	// Args holds the action's positional arguments by name, such as text or status.
	Args map[string]any
	// Options holds the options a rule gave besides once.
	Options map[string]any
	// Record is the record the rule ran over.
	Record string
	Shadow bool
	// After is the key of the step this one waits for, when the rule returned an ordered list.
	After string
	// Capped is true when the action takes a worker slot.
	Capped bool
	// Target holds what the cycle read about the record the action is for, such
	// as a worker's workspace, so the action acts on what the rule saw and not on
	// whatever is there when it starts.
	Target map[string]string
	// Created is when the record's ticket was created, which orders admission.
	Created string
	seq     int
	// then numbers the then call that queued the want within its cycle, so a later step finds the step
	// before it in its own then and not in another record's.
	then int
}

// Key is the want's once key as the ledger keeps it.
func (w Want) Key() string { return ledger.Key(w.Receipt, w.Subject, w.Once) }

// Waiting is a wanted action that waits for a worker slot or for an earlier step.
type Waiting struct {
	Want
	Reason string
}

// Held is a rule that couldn't decide for a record, because it read an unknown value.
type Held struct {
	Rule, Record, Subject, Reason string
	Reads                         []string
	// Dropped is true for an on rule, whose event is gone for good.
	Dropped bool
}

// Exempted is a rule an exemption that names rules stopped for a record, or one want an exemption that
// names actions took away, one entry per want.
type Exempted struct {
	Exemption, Rule, Subject string
	// Receipt and Once are the action taken away and its once key. Both are empty when the exemption names rules.
	Receipt string
	Once    ledger.Once
}

// Throw is a rule call that threw or ran out of time. It wants nothing.
type Throw struct {
	Rule, Record, Subject, Error string
	Timeout                      bool
	Phase                        string
}

// Covered is a wanted action the ledger already has a receipt for.
type Covered struct {
	Want
	Receipt ledger.Receipt
}

// Result is what one cycle decided.
type Result struct {
	Wants   []Want
	Waiting []Waiting
	Held    []Held
	Throws  []Throw
	Covered []Covered
	// Exempted lists the rules an exemption stopped and the wants one took away, so a record they want nothing for says why.
	Exempted []Exempted
	// Shadow lists what rules marked shadow would have wanted.
	Shadow []Want
	// Reads maps "rule/subject" to what the rule read, for the records that
	// wanted something, were held or threw.
	Reads map[string][]string
	// HTTP lists the requests a then waited on and what each got, which a replay can read instead of calling the service.
	HTTP []Exchange
	// Searches lists the local ticket searches a then ran and what each found, which a replay can read instead of the database.
	Searches []Search
}

// WantsFor returns the actions wanted by rule.
func (r Result) WantsFor(rule string) []Want {
	var out []Want
	for _, w := range r.Wants {
		if w.Rule == rule {
			out = append(out, w)
		}
	}
	return out
}

// Cycle runs every rule over every record and every event, given the state.
// Each call gets a runtime of its own, so a rule's failure stays with that
// rule: it wants nothing for that record in this cycle and every other rule
// goes ahead.
func (r *Rules) Cycle(state *State, events []Event) Result {
	res := Result{Reads: map[string][]string{}}
	var fresh []Want
	calls := 0
	queue := func(wants []Want) {
		calls++
		for i := range wants {
			wants[i].then = calls
		}
		fresh = append(fresh, wants...)
	}
	exemptions := map[string]exemption{}
	for _, def := range r.defs {
		if def.isEvent() {
			for i := range events {
				if events[i].Type == def.event {
					queue(r.evaluate(&res, state, def, nil, &events[i]))
				}
			}
			continue
		}
		for _, rec := range state.kinds[def.each] {
			if rec.hidden || r.exempts(&res, state, exemptions, def, rec) {
				continue
			}
			queue(r.evaluate(&res, state, def, rec, nil))
		}
	}
	fresh = r.exemptWants(&res, state, exemptions, fresh)
	res.admit(state, r.settle(&res, state, fresh))
	return res
}

// exemptWants takes away each want of an action an exemption names, when that exemption's if is true
// of the want's ticket, and every later step of the same then that waits on a want it took away, so
// none of them is reported as waiting. A hidden ticket counts too, so an if that can't read its facts
// holds the want. An exemption that reads an unknown holds the wants and one that throws takes them
// away, as an exemption that names rules does for its rules.
func (r *Rules) exemptWants(res *Result, state *State, decided map[string]exemption, wants []Want) []Want {
	gone := map[int]exemption{}
	// goneKey maps a then call and the ledger key of a want taken away to its decision, for the steps that wait on it.
	goneKey := map[string]exemption{}
	stepKey := func(then int, key string) string { return strconv.Itoa(then) + "/" + key }
	for i, w := range wants {
		rec, ok := state.lookup("ticket", w.Subject)
		if !ok {
			continue
		}
		names := func(ex *ruleDef) bool {
			return slices.ContainsFunc(ex.actions, func(m actionMatch) bool { return m.matches(w) })
		}
		if d, ok := r.decisive(res, state, decided, rec, names); ok {
			gone[i], goneKey[stepKey(w.then, w.Key())] = d, d
		}
	}
	// collect lists wants in the order they were queued, but After follows the order then returned
	// them, so the step a want waits on can come after it here. A step whose step before it landed
	// waits on nothing, so it stays.
	for changed := len(gone) > 0; changed; {
		changed = false
		for i, w := range wants {
			if _, ok := gone[i]; ok || w.After == "" || landed(state.Ledger, w.After) {
				continue
			}
			if d, ok := goneKey[stepKey(w.then, w.After)]; ok {
				gone[i], goneKey[stepKey(w.then, w.Key())], changed = d, d, true
			}
		}
	}
	held := map[string]bool{}
	var out []Want
	for i, w := range wants {
		d, ok := gone[i]
		if !ok {
			out = append(out, w)
			continue
		}
		switch d.state {
		case exemptTrue:
			res.Exempted = append(res.Exempted, Exempted{Exemption: d.by, Rule: w.Rule, Subject: w.Subject, Receipt: w.Receipt, Once: w.Once})
		case exemptHeld:
			if key := w.Rule + "/" + w.Subject + "/" + d.by; !held[key] {
				held[key] = true
				dropped := slices.ContainsFunc(r.defs, func(def *ruleDef) bool { return def.name == w.Rule && def.isEvent() })
				res.Held = append(res.Held, Held{Rule: w.Rule, Record: w.Record, Subject: w.Subject, Reason: d.heldReason(), Reads: d.reads, Dropped: dropped})
			}
		case exemptThrew:
			// decide reported the throw, and the want goes, as a rule goes when an exemption naming it throws.
		}
	}
	return out
}

// exemption is what one exemption decided for one record.
type exemption struct {
	// by is the exemption's name.
	by string
	// state is exemptTrue, exemptFalse, exemptHeld (its if read an unknown) or exemptThrew.
	state  string
	reason string
	reads  []string
}

func (d exemption) heldReason() string { return "exemption " + d.by + " read an unknown: " + d.reason }

const (
	exemptTrue  = "true"
	exemptFalse = "false"
	exemptHeld  = "held"
	exemptThrew = "threw"
)

// exempts reports whether an exemption stops def for rec, and records why. An exemption that reads an
// unknown holds the rule and one that throws stops it, so a guard that fails never lets the rule through.
func (r *Rules) exempts(res *Result, state *State, decided map[string]exemption, def *ruleDef, rec *record) bool {
	names := func(ex *ruleDef) bool { return ex.each == def.each && slices.Contains(ex.exempts, def.name) }
	d, ok := r.decisive(res, state, decided, rec, names)
	if !ok {
		return false
	}
	switch d.state {
	case exemptTrue:
		res.Exempted = append(res.Exempted, Exempted{Exemption: d.by, Rule: def.name, Subject: rec.subject})
	case exemptHeld:
		res.Held = append(res.Held, Held{Rule: def.name, Record: rec.String(), Subject: rec.subject, Reason: d.heldReason(), Reads: d.reads})
	}
	return true
}

// decisive runs the exemptions covers picks over rec and returns the one that decides: the first that
// is true or threw, else the first that read an unknown. ok is false when each one is false or none covers.
// Each exemption runs once per record in a cycle, and decisions are keyed by record id because two
// records of a kind can share a subject, as two trees leased to one ticket do.
func (r *Rules) decisive(res *Result, state *State, decided map[string]exemption, rec *record, covers func(*ruleDef) bool) (exemption, bool) {
	var held *exemption
	for _, ex := range r.exemptions {
		if !covers(ex) {
			continue
		}
		key := ex.name + "/" + rec.kind + "/" + rec.id
		d, ok := decided[key]
		if !ok {
			d = r.decide(res, state, ex, rec)
			decided[key] = d
		}
		switch d.state {
		case exemptTrue, exemptThrew:
			return d, true
		case exemptHeld:
			if held == nil {
				held = &d
			}
		}
	}
	if held != nil {
		return *held, true
	}
	return exemption{}, false
}

// decide runs an exemption's if over rec.
func (r *Rules) decide(res *Result, state *State, ex *ruleDef, rec *record) exemption {
	verdict, c := r.run(state, ex, phaseIf, rec, nil)
	key := ex.name + "/" + rec.subject
	switch {
	case len(c.unknown) > 0:
		res.Reads[key] = c.reads
		return exemption{by: ex.name, state: exemptHeld, reason: strings.Join(unique(c.unknown), "; "), reads: c.reads}
	case verdict.err != nil:
		res.Throws = append(res.Throws, Throw{Rule: ex.name, Record: rec.String(), Subject: rec.subject, Error: verdict.err.Error(), Timeout: verdict.timeout, Phase: phaseIf})
		res.Reads[key] = c.reads
		return exemption{by: ex.name, state: exemptThrew}
	case verdict.ok:
		res.Reads[key] = c.reads
		return exemption{by: ex.name, state: exemptTrue}
	}
	return exemption{by: ex.name, state: exemptFalse}
}

// settle drops the wants the ledger covers and a second rule's duplicates, and
// sets aside shadow rules and later steps whose earlier step hasn't landed.
func (r *Rules) settle(res *Result, state *State, wants []Want) []Want {
	seen := map[string]bool{}
	var out []Want
	for _, w := range wants {
		key := w.Key()
		if w.Shadow {
			res.Shadow = append(res.Shadow, w)
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if rc, ok := covering(state.Ledger, w); ok {
			res.Covered = append(res.Covered, Covered{Want: w, Receipt: rc})
			continue
		}
		if w.After != "" && !landed(state.Ledger, w.After) {
			res.Waiting = append(res.Waiting, Waiting{Want: w, Reason: "after the step before it lands"})
			continue
		}
		out = append(out, w)
	}
	return out
}

// covering finds the receipt that stands in for running w again: one that
// landed, was refused, or was attempted and may have landed.
func covering(l *ledger.Ledger, w Want) (ledger.Receipt, bool) {
	key := w.Key()
	for _, rc := range l.Entries {
		if rc.Key() == key && rc.Outcome != ledger.Failed {
			return rc, true
		}
	}
	return ledger.Receipt{}, false
}

func landed(l *ledger.Ledger, key string) bool {
	for _, rc := range l.Entries {
		if rc.Key() == key && rc.Outcome == ledger.Done {
			return true
		}
	}
	return false
}

// admit holds the wants that take a worker slot to max_in_progress, oldest ticket first.
func (res *Result) admit(state *State, wants []Want) {
	var capped []Want
	for _, w := range wants {
		if w.Capped && w.Options["uncapped"] != true {
			capped = append(capped, w)
			continue
		}
		res.Wants = append(res.Wants, w)
	}
	running := state.running()
	sort.SliceStable(capped, func(i, j int) bool {
		a, b := capped[i], capped[j]
		if (a.Created == "") != (b.Created == "") {
			return a.Created != ""
		}
		if a.Created != b.Created {
			return a.Created < b.Created
		}
		return a.seq < b.seq
	})
	free := max(state.maxInProgress-running, 0)
	for i, w := range capped {
		if i < free {
			res.Wants = append(res.Wants, w)
			continue
		}
		res.Waiting = append(res.Waiting, Waiting{Want: w, Reason: fmt.Sprintf("%d of %d in progress, %s in line", running, state.maxInProgress, ordinal(i-free+1))})
	}
	slices.SortStableFunc(res.Wants, func(a, b Want) int { return a.seq - b.seq })
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// running counts the tickets that hold a worker slot: In Progress tickets with a
// worker, those whose worker is unknown, which may have one, and those a launch or
// a promotion is under way for, whose worker or status isn't visible yet.
func (s *State) running() int {
	n := 0
	counted := map[string]bool{}
	for _, t := range s.kinds["ticket"] {
		if t.hidden || t.v.Interface().(*Ticket).Status.Value != "in_progress" {
			continue
		}
		w, ok := s.workers[t.id]
		if !ok {
			continue
		}
		wk := w.v.Interface().(*Worker)
		if wk.State.Unknown != "" || wk.State.Value != "none" {
			n++
			counted[t.id] = true
		}
	}
	for id := range s.reserved {
		if !counted[id] {
			n++
		}
	}
	return n
}

// Reserve holds a worker slot for a ticket a launch or a promotion is under way
// for, until a read shows the result.
func (s *State) Reserve(ticket string) { s.reserved[ticket] = true }

// evaluate runs one rule over one record or event.
func (r *Rules) evaluate(res *Result, state *State, def *ruleDef, rec *record, ev *Event) []Want {
	subject, recordName := "", ""
	switch {
	case rec != nil:
		subject, recordName = rec.subject, rec.String()
	default:
		subject, recordName = ev.Subject, ev.Type+"/"+ev.Subject
	}
	verdict, c := r.run(state, def, phaseIf, rec, ev)
	key := def.name + "/" + subject
	if out := r.verdict(res, c, def, recordName, subject, verdict, key); out != nil {
		return nil
	}
	if !verdict.ok {
		return nil
	}
	verdict, c = r.run(state, def, phaseThen, rec, ev)
	for _, ex := range c.exchanges {
		ex.Rule, ex.Ticket = def.name, subject
		res.HTTP = append(res.HTTP, ex)
	}
	for _, search := range c.searches {
		search.Rule, search.Subject = def.name, subject
		res.Searches = append(res.Searches, search)
	}
	if out := r.verdict(res, c, def, recordName, subject, verdict, key); out != nil {
		return nil
	}
	if verdict.shape != nil {
		res.Throws = append(res.Throws, Throw{Rule: def.name, Record: recordName, Subject: subject, Error: verdict.shape.Error(), Phase: phaseThen})
		res.Reads[key] = c.reads
		return nil
	}
	wants := r.collect(state, def, c, verdict.ordered)
	res.Reads[key] = c.reads
	return wants
}

// verdict records a held or thrown call and returns non-nil when the call must stop the rule for this record.
func (r *Rules) verdict(res *Result, c *call, def *ruleDef, recordName, subject string, v callResult, key string) error {
	if len(c.unknown) > 0 {
		res.Held = append(res.Held, Held{Rule: def.name, Record: recordName, Subject: subject, Reason: strings.Join(unique(c.unknown), "; "), Reads: c.reads, Dropped: def.isEvent()})
		res.Reads[key] = c.reads
		return errors.New("held")
	}
	if v.err != nil {
		res.Throws = append(res.Throws, Throw{Rule: def.name, Record: recordName, Subject: subject, Error: v.err.Error(), Timeout: v.timeout, Phase: c.phase})
		res.Reads[key] = c.reads
		return v.err
	}
	return nil
}

func unique(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

type callResult struct {
	// ordered lists the actions a then returned, in the order it returned them.
	ordered []int
	// shape is set when a then returned something other than its own actions.
	shape   error
	ok      bool
	value   sobek.Value
	err     error
	timeout bool
}

// run calls the rule's if or then in a fresh runtime.
func (r *Rules) run(state *State, def *ruleDef, phase string, rec *record, ev *Event) (callResult, *call) {
	rt := newRuntime(state.ms())
	c := &call{state: state, rt: rt, phase: phase, objects: map[string]*sobek.Object{}, handles: map[*sobek.Object]int{},
		deadline: r.timeouts.now().Add(r.timeouts.forPhase(phase)), timeouts: r.timeouts, rule: def.name}
	reg := &registry{}
	if err := reg.install(rt, c.known); err != nil {
		return callResult{err: err}, c
	}
	// The top level of a rule file runs on every call, so a loop there is bounded too.
	stop := r.timeouts.limit(rt, "rule "+def.name+" "+phase, phase)
	defer stop()
	if _, err := rt.RunProgram(r.program); err != nil {
		return interpret(c, nil, err), c
	}
	fns, ok := reg.fns[def.name]
	if !ok {
		return callResult{err: fmt.Errorf("rule %q did not register", def.name)}, c
	}
	fn := fns.ifFn
	if phase == phaseThen {
		fn = fns.thenFn
	}
	var arg sobek.Value
	if rec != nil {
		arg = c.recordValue(rec)
	} else {
		arg = c.toJS(reflect.ValueOf(ev.Payload), "")
	}
	var out sobek.Value
	var err, shape error
	var ordered []int
	func() {
		defer func() {
			if p := recover(); p != nil {
				if e, ok := p.(error); ok {
					err = e
					return
				}
				err = fmt.Errorf("%v", p)
			}
		}()
		out, err = fn(sobek.Undefined(), arg, c.ctx())
		// Reading the returned value stays inside the time budget and the guard.
		if err == nil && phase == phaseThen {
			ordered, shape = c.returned(out)
		}
	}()
	res := interpret(c, out, err)
	res.ordered, res.shape = ordered, shape
	return res, c
}

// interpret turns a call's outcome into a verdict. An Unknown error is not a
// failure of the rule, because the unknown read already holds the call.
func interpret(c *call, out sobek.Value, err error) callResult {
	if err == nil {
		res := callResult{value: out}
		if out != nil {
			res.ok = out.ToBoolean()
		}
		return res
	}
	var interrupted *sobek.InterruptedError
	if errors.As(err, &interrupted) {
		return callResult{err: errors.New("took longer than its time budget and was stopped"), timeout: true}
	}
	var overflow *sobek.StackOverflowError
	if errors.As(err, &overflow) {
		return callResult{err: fmt.Errorf("called itself more than %d levels deep and was stopped", maxCallDepth)}
	}
	var ex *sobek.Exception
	if errors.As(err, &ex) {
		if obj, ok := ex.Value().(*sobek.Object); ok {
			if name := obj.Get("name"); name != nil && name.String() == "Unknown" {
				if len(c.unknown) == 0 {
					c.unknown = append(c.unknown, obj.Get("reason").String())
				}
				return callResult{}
			}
		}
		return callResult{err: errors.New(describeThrown(ex))}
	}
	return callResult{err: err}
}

// returned reads what a then returned: nothing, one action it queued, or a list of them.
func (c *call) returned(ret sobek.Value) ([]int, error) {
	notAction := errors.New("then returned something that isn't an action a queue method gave back, or returned one twice")
	if ret == nil || sobek.IsUndefined(ret) || sobek.IsNull(ret) {
		return nil, nil
	}
	if ret.ExportType().Kind() == reflect.Bool {
		return nil, errors.New("then returned a boolean. That looks like a swap with if, because then queues actions and returns nothing or the actions it queued")
	}
	obj, ok := ret.(*sobek.Object)
	if !ok {
		return nil, errors.New("then returned something other than an action or a list of actions")
	}
	items := []sobek.Value{obj}
	if obj.ClassName() == "Array" {
		items = items[:0]
		length := obj.Get("length").ToInteger()
		if length > int64(len(c.queue)) {
			return nil, notAction
		}
		for i := range length {
			items = append(items, obj.Get(strconv.FormatInt(i, 10)))
		}
	}
	var ordered []int
	for _, item := range items {
		ho, ok := item.(*sobek.Object)
		id, queued := c.handles[ho]
		if !ok || !queued || slices.Contains(ordered, id) {
			return nil, notAction
		}
		ordered = append(ordered, id)
	}
	return ordered, nil
}

// collect turns what a then queued into wants.
func (r *Rules) collect(state *State, def *ruleDef, c *call, ordered []int) []Want {
	after := map[int]string{}
	var prev *queued
	for _, id := range ordered {
		q := c.queue[id]
		if prev != nil {
			after[q.id] = ledger.Key(prev.spec.Receipt, prev.subjectID(), prev.once)
		}
		prev = q
	}
	var wants []Want
	for _, q := range c.queue {
		w := Want{
			Rule: def.name, Receipt: q.spec.Receipt, Subject: q.subjectID(), Once: q.once, Args: q.args, Options: q.opts,
			Record: q.recordName(), Shadow: def.shadow, After: after[q.id], Capped: q.takesSlot(),
		}
		if q.subject != nil {
			w.Target = state.target(q.subject)
			if t, ok := state.lookup("ticket", q.subject.subject); ok {
				w.Created, w.seq = t.created, t.seq
			}
		}
		wants = append(wants, w)
	}
	return wants
}

// describeThrown names what a rule threw as "Name: message", without the stack.
func describeThrown(ex *sobek.Exception) string {
	obj, ok := ex.Value().(*sobek.Object)
	if !ok {
		return ex.Value().String()
	}
	name, message := obj.Get("name"), obj.Get("message")
	switch {
	case name != nil && message != nil:
		return name.String() + ": " + message.String()
	case message != nil:
		return message.String()
	}
	return firstLine(ex.Error())
}
