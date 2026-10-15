package rules

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
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
	Kind string
	// Receipt is the kind its receipts carry.
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
	// Created is when the record's ticket was created, which orders admission.
	Created string
	seq     int
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
	// Shadow lists what rules marked shadow would have wanted.
	Shadow []Want
	// Reads maps "rule/subject" to what the rule read, for the records that
	// wanted something, were held or threw.
	Reads map[string][]string
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
	for _, def := range r.defs {
		if def.isEvent() {
			for i := range events {
				if events[i].Type == def.event {
					fresh = append(fresh, r.evaluate(&res, state, def, nil, &events[i])...)
				}
			}
			continue
		}
		for _, rec := range state.kinds[def.each] {
			if rec.hidden {
				continue
			}
			fresh = append(fresh, r.evaluate(&res, state, def, rec, nil)...)
		}
	}
	res.admit(state, r.settle(&res, state, fresh))
	return res
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

// running counts the In Progress tickets that hold a worker slot: those with a
// worker, and those whose worker is unknown, which may have one.
func (s *State) running() int {
	n := 0
	for _, t := range s.kinds["linear.ticket"] {
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
		}
	}
	return n
}

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
	if out := r.verdict(res, c, def, recordName, subject, verdict, key); out != nil {
		return nil
	}
	wants, err := r.collect(state, def, rec, c, verdict.value)
	if err != nil {
		res.Throws = append(res.Throws, Throw{Rule: def.name, Record: recordName, Subject: subject, Error: err.Error(), Phase: phaseThen})
		res.Reads[key] = c.reads
		return nil
	}
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
	ok      bool
	value   sobek.Value
	err     error
	timeout bool
}

// run calls the rule's if or then in a fresh runtime.
func (r *Rules) run(state *State, def *ruleDef, phase string, rec *record, ev *Event) (callResult, *call) {
	rt := newRuntime(state.ms())
	c := &call{state: state, rt: rt, phase: phase, objects: map[string]*sobek.Object{}}
	reg := &registry{}
	if err := reg.install(rt, c.known); err != nil {
		return callResult{err: err}, c
	}
	if _, err := rt.RunProgram(r.program); err != nil {
		return callResult{err: err}, c
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
	stop := timeLimit(rt, r.timeouts.forPhase(phase))
	var out sobek.Value
	var err error
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
	}()
	stop()
	return interpret(c, out, err), c
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

// collect turns what a then queued into wants.
func (r *Rules) collect(state *State, def *ruleDef, rec *record, c *call, ret sobek.Value) ([]Want, error) {
	var ordered []int
	switch {
	case ret == nil || sobek.IsUndefined(ret) || sobek.IsNull(ret):
	default:
		if _, isBool := ret.Export().(bool); isBool {
			return nil, errors.New("then returned a boolean. That looks like a swap with if, because then queues actions and returns nothing or the actions it queued")
		}
		obj, ok := ret.(*sobek.Object)
		if !ok {
			return nil, errors.New("then returned something other than an action or a list of actions")
		}
		handles := []sobek.Value{obj}
		if arr, ok := obj.Export().([]any); ok {
			handles = handles[:0]
			for i := range arr {
				handles = append(handles, obj.Get(fmt.Sprint(i)))
			}
		}
		for _, h := range handles {
			ho, ok := h.(*sobek.Object)
			if !ok || ho.Get("__id") == nil {
				return nil, errors.New("then returned something that isn't an action a queue method gave back")
			}
			ordered = append(ordered, int(ho.Get("__id").ToInteger()))
		}
	}
	after := map[int]string{}
	var prev *queued
	for _, id := range ordered {
		q := c.queue[id]
		if prev != nil {
			after[q.id] = ledger.Key(prev.spec.Receipt, prev.subject.subject, prev.once)
		}
		prev = q
	}
	var wants []Want
	for _, q := range c.queue {
		w := Want{
			Rule: def.name, Kind: q.spec.Kind, Receipt: q.spec.Receipt, Subject: q.subject.subject, Once: q.once, Args: q.args, Options: q.opts,
			Record: q.subject.String(), Shadow: def.shadow, After: after[q.id], Capped: q.spec.Capped,
		}
		if t, ok := state.lookup("linear.ticket", q.subject.subject); ok {
			w.Created, w.seq = t.created, t.seq
		}
		wants = append(wants, w)
	}
	return wants, nil
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
