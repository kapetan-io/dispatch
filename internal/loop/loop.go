// Package loop is the reconcile loop. One goroutine owns the world and the
// ledger and handles one event at a time. Sources read the tools off the
// goroutine and the executor acts off it, so a slow launch never delays a cycle.
// Every event wakes the loop, and a cycle runs every rule over everything again.
package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/delivery"
	"github.com/kapetan-io/dispatch/internal/executor"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/world"
)

// Options is what the loop runs over.
type Options struct {
	Config     config.Config
	ConfigText string
	// StateDir holds the loop's socket.
	StateDir string
	Rules    *rules.Rules
	Clock    Clock
	// Sources are the tools one read goes through. The loop sets Tolerant and Resilient.
	Sources  world.Sources
	Executor *executor.Executor
	Herdr    herdr.Herdr
	Alerts   delivery.Alerts
	// Usage reports the Linear calls made so far.
	Usage func() linear.Stats
	// Unauthorized is called when Linear rejects the key, so the next read fetches it again.
	Unauthorized func()
	// Viewer names the Linear user whose tickets are tracked.
	Viewer string
	Out    io.Writer
	Err    io.Writer
}

// Loop is the running reconcile loop.
type Loop struct {
	o     Options
	clock Clock
	scope world.Scope
	hist  *history.Writer
	// segStart is when the segment being written began.
	segStart time.Time

	wd     world.World
	mem    *world.Memory
	led    *ledger.Ledger
	resync int
	// started is when this dispatch began, in milliseconds.
	started    int64
	lastIngest int64

	state     *rules.State
	result    rules.Result
	lastCycle *history.Cycle
	cycleDue  bool

	inbox   chan any
	reading bool
	pending world.Part
	// afterRead runs once the read in flight lands.
	afterRead []func()

	flights   map[string]*flight
	launching map[string]bool
	idle      uint64
	wasBusy   bool
	waiters   []*waiter
	threw     map[string]bool

	renaming map[string]string
	renames  int

	// lastResync is when the previous resync line printed.
	lastResync time.Time
	listener   *listener
	// actions counts the actions started since the last resync line.
	actions     int
	linearCalls int
	tells       int
	// log lists the actions that ended, in order, so a tick reports only its own.
	log []logged
}

type logged struct {
	seq int
	rc  ledger.Receipt
}

// flight is an action in flight.
type flight struct {
	want  rules.Want
	rc    ledger.Receipt
	reply chan Response
}

type readDone struct {
	parts world.Part
	w     world.World
	err   error
}

type actionDone struct {
	f   *flight
	res executor.Result
}

type renameDone struct{}

type request struct {
	Request
	reply chan Response
}

// New returns a loop over o.
func New(o Options) *Loop {
	if o.Clock == nil {
		o.Clock = WallClock{}
	}
	o.Sources.Tolerant, o.Sources.Resilient = true, true
	if o.Sources.Stderr == nil {
		o.Sources.Stderr = o.Err
	}
	return &Loop{
		o: o, clock: o.Clock, scope: world.ScopeOf(o.Config),
		inbox: make(chan any, 256), flights: map[string]*flight{}, launching: map[string]bool{}, threw: map[string]bool{},
		renaming: map[string]string{},
	}
}

// messageKinds are the receipts whose action may have landed when its outcome
// is lost, so they count as sent and never run again.
var messageKinds = []string{"nudge", "notify", "comment"}

// ingest is the time the loop stamps an event with. It never goes backward.
func (l *Loop) ingest() int64 {
	t := l.clock.Now().UnixMilli()
	if t < l.lastIngest {
		t = l.lastIngest
	}
	l.lastIngest = t
	return t
}

func (l *Loop) stamp() time.Time { return time.UnixMilli(l.ingest()) }

// Run starts the loop and returns when ctx ends, after the actions in flight finish,
// or when it cannot go on. A failure to start or to write history is returned.
func (l *Loop) Run(ctx context.Context) (err error) {
	l.started = l.ingest()
	if err := l.start(ctx); err != nil {
		return err
	}
	ln, err := listen(l.o.StateDir, l.inbox)
	if err != nil {
		_ = l.hist.Close(false)
		return err
	}
	l.listener = ln
	defer func() {
		ln.close()
		if cerr := l.hist.Close(err == nil); cerr != nil && err == nil {
			err = cerr
		}
	}()
	timer := l.clock.NewTimer(time.Duration(l.o.Config.Dispatch.Resync))
	defer func() { timer.Stop() }()

	l.settle()
	for {
		select {
		case <-ctx.Done():
			return l.drain()
		case m := <-l.inbox:
			err = l.handle(m)
		case <-timer.C():
			timer = l.clock.NewTimer(time.Duration(l.o.Config.Dispatch.Resync))
			err = l.onResync("dispatch.resync")
		}
		for err == nil && len(l.inbox) > 0 {
			err = l.handle(<-l.inbox)
		}
		if err == nil {
			err = l.settle()
		}
		if err != nil {
			l.fatal(err)
			return err
		}
	}
}

func (l *Loop) fatal(err error) {
	_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: stopping: %v\n", l.clock.Now().Format(time.TimeOnly), err)
	if l.o.Alerts != nil {
		_ = l.o.Alerts.Show(context.Background(), "dispatch stopped: "+err.Error())
	}
}

// drain waits for the actions in flight, up to their deadlines, then stops.
func (l *Loop) drain() error {
	deadline := time.After(executor.DefaultDeadline + 5*time.Second)
	for len(l.flights) > 0 || l.reading {
		select {
		case m := <-l.inbox:
			switch m := m.(type) {
			case actionDone:
				if err := l.onAction(m); err != nil {
					return err
				}
			case readDone:
				l.reading = false
			case request:
				m.reply <- Response{Code: CodeFailed, Error: "dispatch is stopping"}
			}
		case <-deadline:
			return nil
		}
	}
	return nil
}

// start loads history, reads every source and runs the first cycle.
func (l *Loop) start(ctx context.Context) error {
	at := l.stamp()
	l.mem, l.led = world.NewMemory(), &ledger.Ledger{}
	if seg, ok, err := history.Latest(l.o.Config.History.Dir); err != nil {
		return fmt.Errorf("while reading history: %w", err)
	} else if ok {
		st, err := seg.Load()
		if err != nil {
			return fmt.Errorf("while loading %s: %w", seg.Path, err)
		}
		l.mem, l.led, l.resync = st.Memory.Ready(), st.Ledger, st.Resync
		l.closeCrashed(at)
	}
	l.resync++
	hist, err := history.Start(l.o.Config.History.Dir, at, history.Header{Why: "start", Config: l.o.ConfigText, At: at.UnixMilli()},
		history.Snapshot{At: at.UnixMilli(), World: l.mem, Ledger: history.LedgerState{Receipts: l.led.Entries}, Resync: l.resync})
	if err != nil {
		return err
	}
	l.hist, l.segStart = hist, at
	if err := l.pruneHistory(at); err != nil {
		return err
	}
	if err := l.write(history.Event{Line: history.LineEvent, Ingest: at.UnixMilli(), Source: "dispatch", Type: "dispatch.start"}); err != nil {
		return err
	}
	wd, err := world.Refresh(ctx, l.o.Sources, l.scope, world.World{}, world.Everything, at)
	if err != nil {
		_ = l.hist.Close(false)
		return err
	}
	l.wd = wd
	l.cycleDue = true
	if err := l.cycle("start"); err != nil {
		return err
	}
	l.cycleDue = false
	_, _ = fmt.Fprintf(l.o.Out, "%s start: %s, %s, ledger %s from %s\n", at.Format(time.TimeOnly), count(len(l.wd.Tickets), "tracked ticket"),
		count(len(l.o.Rules.Defs()), "rule"), count(len(l.led.Entries), "receipt"), baseName(l.hist.Path()))
	l.resumeRest()
	return nil
}

// closeCrashed settles the attempts a crash left without an outcome. A message may
// have landed, so it counts as sent and is never repeated. A state change is
// decided again from a fresh read, so it becomes Failed.
func (l *Loop) closeCrashed(at time.Time) {
	for i := range l.led.Entries {
		rc := &l.led.Entries[i]
		if rc.Outcome != "" || slices.Contains(messageKinds, rc.Kind) {
			continue
		}
		rc.Outcome, rc.Detail, rc.Ended = ledger.Failed, "crashed", at.UnixMilli()
		for j := range rc.Steps {
			if rc.Steps[j].Outcome == "" {
				rc.Steps[j].Outcome = ledger.Failed
			}
		}
	}
}

func (l *Loop) pruneHistory(at time.Time) error {
	return history.Prune(l.o.Config.History.Dir, at, time.Duration(l.o.Config.History.Keep), l.hist.Path())
}

// write appends a line, and a failure stops the loop, because carrying on would
// break restart unseen.
func (l *Loop) write(line any) error {
	if err := l.hist.Append(line); err != nil {
		return err
	}
	return nil
}

func (l *Loop) handle(m any) error {
	switch m := m.(type) {
	case readDone:
		return l.onRead(m)
	case actionDone:
		return l.onAction(m)
	case renameDone:
		l.renames--
		return nil
	case request:
		return l.onRequest(m)
	}
	return nil
}

// onResync re-reads every source. A resync is the only clock rules wait on.
func (l *Loop) onResync(typ string) error {
	at := l.ingest()
	l.resync++
	if err := l.write(history.Event{Line: history.LineEvent, Ingest: at, Source: "dispatch", Type: typ}); err != nil {
		return err
	}
	l.wd.Nudged = nil
	l.readAgain(world.Everything)
	if typ == "dispatch.resync" {
		l.afterRead = append(l.afterRead, l.printResync)
	}
	return l.rotate()
}

func (l *Loop) printResync() {
	now := l.clock.Now()
	line := fmt.Sprintf("%s resync: %s, %s since %s", now.Format(time.TimeOnly), count(len(l.wd.Tickets), "ticket"), count(l.actions, "action"), l.lastResync.Format(time.TimeOnly))
	if l.lastResync.IsZero() {
		line = fmt.Sprintf("%s resync: %s, %s", now.Format(time.TimeOnly), count(len(l.wd.Tickets), "ticket"), count(l.actions, "action"))
	}
	if l.o.Usage != nil {
		s := l.o.Usage()
		line += fmt.Sprintf(", Linear %s", count(s.Calls-l.linearCalls, "call"))
		if s.RequestsLimit > 0 {
			line += fmt.Sprintf(" (%d of %d left this hour)", s.RequestsRemaining, s.RequestsLimit)
		}
		l.linearCalls = s.Calls
	}
	l.actions, l.lastResync = 0, now
	_, _ = fmt.Fprintln(l.o.Out, line)
}

// rotate starts a new segment once the current one is old enough. The snapshot
// drops receipts of tickets that are neither tracked nor holding a tree.
func (l *Loop) rotate() error {
	every := time.Duration(l.o.Config.History.Rotate)
	if every <= 0 || l.clock.Now().Sub(l.segStart) < every {
		return nil
	}
	at := l.stamp()
	live := map[string]bool{}
	for _, t := range l.wd.Tickets {
		live[t.Identifier] = true
	}
	for id := range l.wd.Leased {
		live[id] = true
	}
	for _, tree := range l.wd.Trees {
		live[tree.Path] = true
	}
	if l.wd.LinearErr == nil {
		l.led.Keep(func(subject string) bool { return live[subject] || l.flightFor(subject) })
	}
	next, err := history.Start(l.o.Config.History.Dir, at, history.Header{Why: "rotate", Config: l.o.ConfigText, At: at.UnixMilli()},
		history.Snapshot{At: at.UnixMilli(), World: l.mem, Ledger: history.LedgerState{Receipts: l.led.Entries}, Resync: l.resync})
	if err != nil {
		return err
	}
	old := l.hist
	l.hist, l.segStart = next, at
	if err := old.Close(true); err != nil {
		return err
	}
	l.lastCycle = nil
	return l.pruneHistory(at)
}

func (l *Loop) flightFor(subject string) bool {
	for _, f := range l.flights {
		if f.want.Subject == subject {
			return true
		}
	}
	return false
}

// readAgain asks for the sources in parts to be read, off the loop goroutine.
func (l *Loop) readAgain(parts world.Part) {
	l.pending |= parts
	l.cycleDue = true
}

func (l *Loop) startRead() {
	if l.reading || l.pending == 0 {
		return
	}
	parts, prev := l.pending, l.wd
	l.pending, l.reading = 0, true
	at := l.stamp()
	go func() {
		w, err := world.Refresh(context.Background(), l.o.Sources, l.scope, prev, parts, at)
		l.inbox <- readDone{parts: parts, w: w, err: err}
	}()
}

func (l *Loop) onRead(m readDone) error {
	l.reading = false
	if m.err != nil {
		_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: while reading: %v\n", l.clock.Now().Format(time.TimeOnly), m.err)
		return nil
	}
	nudged := l.wd.Nudged
	l.wd = m.w
	// A read of herdr that came back after a nudge is the next resync's job to
	// believe, so the nudge overlay stays until then.
	if m.parts&world.FromLocal != 0 && nudged != nil && m.parts != world.Everything {
		l.wd.Nudged = nudged
	}
	if m.parts&world.FromLocal != 0 {
		l.renaming = map[string]string{}
	}
	if m.w.LinearErr != nil && errors.Is(m.w.LinearErr, linear.ErrUnauthorized) && l.o.Unauthorized != nil {
		l.o.Unauthorized()
	}
	for _, f := range l.afterRead {
		f()
	}
	l.afterRead = nil
	l.cycleDue = true
	return nil
}

// settle starts the reads asked for, runs a cycle once every read has landed, and
// reports idleness.
func (l *Loop) settle() error {
	l.startRead()
	if l.cycleDue && !l.reading && l.pending == 0 {
		l.cycleDue = false
		if err := l.cycle("event"); err != nil {
			return err
		}
	}
	busy := l.busy()
	if l.wasBusy && !busy {
		l.idle++
		l.release()
	}
	l.wasBusy = busy
	return nil
}

// busy is true while anything is queued, pending or in flight.
func (l *Loop) busy() bool {
	return len(l.inbox) > 0 || l.reading || l.pending != 0 || l.cycleDue || len(l.flights) > 0 || l.renames > 0
}

// cycle runs every rule over the world and the ledger, writes what changed, and
// starts the actions wanted.
func (l *Loop) cycle(why string) error {
	now := l.ingest()
	l.wd.At = time.UnixMilli(now)
	state := rules.FromWorld(l.wd, l.o.Config, l.led, l.o.Viewer)
	for _, f := range l.flights {
		if f.want.Capped {
			state.Reserve(f.want.Subject)
		}
	}
	next := state.Carry(l.mem, l.started)
	if line, changed := history.Diff(l.mem, next); changed {
		if err := l.write(line); err != nil {
			return err
		}
	}
	l.mem = next
	res := l.o.Rules.Cycle(state, nil)
	l.state, l.result = state, res
	entry := cycleLine(now, why, res)
	if l.lastCycle == nil || !sameCycle(*l.lastCycle, entry) {
		if err := l.write(entry); err != nil {
			return err
		}
		l.lastCycle = &entry
	}
	l.alertThrows(res)
	return l.act(res)
}

func (l *Loop) alertThrows(res rules.Result) {
	for _, t := range res.Throws {
		if l.threw[t.Rule] {
			continue
		}
		l.threw[t.Rule] = true
		how := "threw"
		if t.Timeout {
			how = "ran out of time"
		}
		text := fmt.Sprintf("rule %s %s in its %s for %s: %s", t.Rule, how, t.Phase, t.Subject, t.Error)
		_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: %s\n", l.clock.Now().Format(time.TimeOnly), text)
		if l.o.Alerts != nil {
			_ = l.o.Alerts.Show(context.Background(), "dispatch: "+text)
		}
	}
}

// act starts each wanted action the ledger doesn't cover and nothing is already doing.
func (l *Loop) act(res rules.Result) error {
	for _, w := range res.Wants {
		key := w.Key()
		if _, busy := l.flights[key]; busy {
			continue
		}
		if l.failedThisResync(key) {
			continue
		}
		if w.Kind == "herdr.launch" && l.launching[w.Subject] {
			continue
		}
		if why := l.o.Executor.WhyNotLaunch(w, l.wd); why != "" {
			_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: not launching %s: %s\n", l.clock.Now().Format(time.TimeOnly), w.Subject, why)
			continue
		}
		rest := l.restOf(w, res)
		if err := l.begin(w, rest, nil); err != nil {
			return err
		}
	}
	l.renameWorkspaces()
	return nil
}

// failedThisResync is true when the action already failed in the resync that is
// running, so a failure retries at the next one and can't spin.
func (l *Loop) failedThisResync(key string) bool {
	for _, rc := range l.led.Entries {
		if rc.Outcome == ledger.Failed && rc.Resync == l.resync && rc.Key() == key {
			return true
		}
	}
	return false
}

// restOf lists the steps that wait on w, in order, for a rule that returned an ordered list.
func (l *Loop) restOf(w rules.Want, res rules.Result) []rules.Want {
	var rest []rules.Want
	key := w.Key()
	for {
		i := slices.IndexFunc(res.Waiting, func(x rules.Waiting) bool { return x.After == key })
		if i < 0 {
			return rest
		}
		next := res.Waiting[i].Want
		rest = append(rest, next)
		key = next.Key()
	}
}

// begin writes the attempted receipt and only then starts the action, so a crash
// between the two leaves a record that it was tried.
func (l *Loop) begin(w rules.Want, rest []rules.Want, reply chan Response) error {
	if w.Kind == "linear.comment" {
		w.Target = maps.Clone(w.Target)
		if w.Target == nil {
			w.Target = map[string]string{}
		}
		w.Target["linearID"] = l.linearID(w.Subject)
	}
	at := l.ingest()
	rc := ledger.Receipt{Kind: w.Receipt, Subject: w.Subject, Once: w.Once, Rule: w.Rule, Started: at, Resync: l.resync}
	if len(rest) > 0 {
		b, _ := json.Marshal(rest)
		rc.Rest = b
	}
	if err := l.write(history.Receipt{Line: history.LineReceipt, Receipt: rc}); err != nil {
		return err
	}
	if err := l.hist.Sync(); err != nil {
		return err
	}
	l.led.Upsert(rc)
	f := &flight{want: w, rc: rc, reply: reply}
	l.flights[w.Key()] = f
	if w.Kind == "herdr.launch" {
		l.launching[w.Subject] = true
	}
	l.actions++
	_, _ = fmt.Fprintf(l.o.Out, "%s %s %s (%s)\n", l.clock.Now().Format(time.TimeOnly), w.Receipt, w.Subject, w.Rule)
	wd := l.wd
	go func() {
		res := l.o.Executor.Run(context.Background(), w, wd)
		l.inbox <- actionDone{f: f, res: res}
	}()
	return nil
}

func (l *Loop) onAction(m actionDone) error {
	f, res := m.f, m.res
	delete(l.flights, f.want.Key())
	if f.want.Kind == "herdr.launch" {
		delete(l.launching, f.want.Subject)
	}
	rc := f.rc
	rc.Outcome, rc.Detail, rc.Ended, rc.Steps = res.Outcome, res.Detail, l.ingest(), res.Steps
	if res.TicketID != "" {
		rc.TicketID = res.TicketID
	}
	if err := l.write(history.Receipt{Line: history.LineReceipt, Receipt: rc}); err != nil {
		return err
	}
	l.led.Upsert(rc)
	l.log = append(l.log, logged{seq: len(l.log) + 1, rc: rc})
	note := res.Detail
	if res.Note != "" {
		note = res.Note
	}
	switch {
	case res.Outcome == ledger.Done:
		_, _ = fmt.Fprintf(l.o.Out, "%s %s %s %s\n", l.clock.Now().Format(time.TimeOnly), f.want.Receipt, f.want.Subject, orDefault(note, "done"))
	default:
		_, _ = fmt.Fprintf(l.o.Out, "%s %s %s %s: %s\n", l.clock.Now().Format(time.TimeOnly), f.want.Receipt, f.want.Subject, res.Outcome, note)
	}
	if res.Nudged != "" {
		nudged := map[string]bool{res.Nudged: true}
		for k := range l.wd.Nudged {
			nudged[k] = true
		}
		l.wd.Nudged = nudged
	}
	if res.Reread != 0 {
		l.readAgain(res.Reread)
	}
	l.cycleDue = true
	if f.reply != nil {
		f.reply <- tellResponse(f.want, rc)
	}
	if res.Outcome == ledger.Done && len(rc.Rest) > 0 {
		return l.runNext(rc)
	}
	return nil
}

// runNext starts the step after one that is Done, from the receipt, so the rest
// of an ordered list runs even after the first step made its rule's condition false.
func (l *Loop) runNext(rc ledger.Receipt) error {
	var rest []rules.Want
	if err := json.Unmarshal(rc.Rest, &rest); err != nil || len(rest) == 0 {
		return nil
	}
	next := rest[0]
	if l.hasReceipt(next.Key()) {
		return nil
	}
	return l.begin(next, rest[1:], nil)
}

func (l *Loop) hasReceipt(key string) bool {
	for _, rc := range l.led.Entries {
		if rc.Key() == key {
			return true
		}
	}
	return false
}

// resumeRest starts the steps a crash left unrun after a step that is Done.
func (l *Loop) resumeRest() {
	for _, rc := range slices.Clone(l.led.Entries) {
		if rc.Outcome == ledger.Done && len(rc.Rest) > 0 {
			_ = l.runNext(rc)
		}
	}
}

// linearID is Linear's id for a ticket an escalation moved, from its receipt, so a lost
// comment can be finished after the ticket left the tracked set.
func (l *Loop) linearID(subject string) string {
	for i := len(l.led.Entries) - 1; i >= 0; i-- {
		if rc := l.led.Entries[i]; rc.Subject == subject && rc.TicketID != "" {
			return rc.TicketID
		}
	}
	return ""
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func baseName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
