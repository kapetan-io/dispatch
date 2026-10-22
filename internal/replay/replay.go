package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/httpservice"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/world"
)

// ErrNotReplayable means history holds no record of the world the cycles read, as in a
// segment written before dispatch recorded it.
var ErrNotReplayable = errors.New("history cannot be replayed")

// Options picks the stretch of history to run again.
type Options struct {
	// Dir is the history directory.
	Dir string
	// From and To bound the cycles replayed by the time they ran. A zero time is no bound.
	From, To time.Time
	// Rules replace the rules each segment recorded. Replay then compares only the actions
	// the cycles want, and stops at the first one that differs from the live run.
	Rules *rules.Rules
	// Env expands the ~ in the paths each segment's config recorded.
	Env environment.Environment
}

// Replayed is how a replay came out.
type Replayed struct {
	// Cycles counts the cycles run again and Matched those that came out as recorded.
	Cycles, Matched int
	// Difference describes the first place replay and the live run part, or is empty.
	Difference string
	// Skipped says why each segment that cannot be replayed was left out.
	Skipped []string
}

// Replay runs the cycles in a stretch of history again, over the world and the ledger
// they ran over, and compares each with what the live run decided. A cycle the live run
// found identical to the one before wrote no line, so only the cycles that wrote one are
// run, and each is compared with the last cycle line written.
func Replay(o Options) (Replayed, error) {
	paths, err := history.Segments(o.Dir)
	if err != nil {
		return Replayed{}, err
	}
	if len(paths) == 0 {
		return Replayed{}, history.ErrNoHistory
	}
	first := 0
	if !o.From.IsZero() {
		for i, p := range paths {
			if start, ok := history.SegmentStart(p); ok && !start.After(o.From) {
				first = i
			}
		}
	}
	var res Replayed
	var skipped error
	replayable := false
	for _, p := range paths[first:] {
		if start, ok := history.SegmentStart(p); ok && !o.To.IsZero() && start.After(o.To) {
			break
		}
		seg, err := history.Read(p)
		if errors.Is(err, history.ErrIncomplete) {
			continue
		}
		if err != nil {
			return res, err
		}
		done, hadCycles, err := replaySegment(seg, o, &res)
		if errors.Is(err, ErrNotReplayable) {
			// A segment from before history recorded the world must not hide the newer ones.
			res.Skipped = append(res.Skipped, err.Error())
			skipped = err
			continue
		}
		// An empty segment, such as the one a reset starts, says nothing about the world, so it can't vouch
		// for the older ones it sits beside.
		replayable = replayable || hadCycles
		if err != nil || done || res.Difference != "" {
			return res, err
		}
	}
	if !replayable && skipped != nil {
		return res, skipped
	}
	return res, nil
}

// cycleRecord is the lines one cycle wrote, in the order it wrote them.
type cycleRecord struct {
	now   int64
	read  *history.Reading
	world *history.World
	cycle *history.Cycle
	// rank is the rank of the newest line added, so a line that doesn't rank above it starts the next cycle.
	rank int
}

// Lines of one cycle come in this order, so a line that doesn't rank above the last starts the next cycle.
const (
	rankRead = iota + 1
	rankWorld
	rankCycle
)

// segmentReplay is the state replay carries through one segment: the config and rules once loaded,
// the memory and ledger as they evolve, and the newest read and cycle line.
type segmentReplay struct {
	seg   history.Segment
	env   environment.Environment
	cfg   config.Config
	rules *rules.Rules
	// ready is true once cfg and rules are loaded.
	ready bool
	mem   *world.Memory
	led   *ledger.Ledger
	read  *history.Reading
	// lastCycle is the newest cycle line read, which a cycle that wrote none is compared with.
	lastCycle *history.Cycle
	// kept holds the answers recorded since the last resync, tick or start, which ctx.http.last reads.
	// A segment begins at one of those, so it begins with none.
	kept map[string]rules.Exchange
}

// replaySegment runs the cycles of one segment again. It returns true once a cycle is past To, and
// whether the segment held any cycle, in range or not. It returns ErrNotReplayable when the segment
// holds no record of the world.
func replaySegment(seg history.Segment, o Options, res *Replayed) (stopped, hadCycles bool, err error) {
	st := &segmentReplay{
		seg: seg, mem: seg.Snapshot.World, led: &ledger.Ledger{Entries: slices.Clone(seg.Snapshot.Ledger.Receipts)},
		read: seg.Snapshot.Read, rules: o.Rules, env: o.Env, kept: map[string]rules.Exchange{},
	}
	var pending *cycleRecord
	flush := func(final bool) error {
		if pending == nil || stopped {
			return nil
		}
		g := *pending
		hadCycles = true
		pending = nil
		at := time.UnixMilli(g.now)
		switch {
		case !o.From.IsZero() && at.Before(o.From):
			st.apply(g)
			return nil
		case !o.To.IsZero() && at.After(o.To):
			stopped = true
			return nil
		}
		// A crash can come between the read line and the cycle line, which leaves a cycle nobody decided.
		// A cycle that decided what the one before did writes no cycle line either, so the memory it
		// changed is still compared.
		return st.run(g, o, res, final && !seg.Clean && g.cycle == nil)
	}
	for _, raw := range seg.Lines {
		var head struct {
			Line string `json:"line"`
			Now  int64  `json:"now"`
			At   int64  `json:"at"`
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &head)
		rank, now := 0, head.Now
		switch head.Line {
		case history.LineRead:
			rank = rankRead
		case history.LineWorld:
			rank, now = rankWorld, head.At
		case history.LineCycle:
			rank = rankCycle
		case history.LineReceipt:
			if err := flush(false); err != nil || res.Difference != "" {
				return stopped, hadCycles, err
			}
			var r history.Receipt
			if err := json.Unmarshal(raw, &r); err != nil {
				return stopped, hadCycles, err
			}
			st.led.Upsert(r.Receipt)
			continue
		default:
			if err := flush(false); err != nil || res.Difference != "" {
				return stopped, hadCycles, err
			}
			if head.Line == history.LineEvent && history.ClearsKept(head.Type) {
				st.kept = map[string]rules.Exchange{}
			}
			continue
		}
		if pending != nil && (pending.now != now || rank <= pending.rank) {
			if err := flush(false); err != nil || res.Difference != "" {
				return stopped, hadCycles, err
			}
		}
		if pending == nil {
			pending = &cycleRecord{now: now}
		}
		pending.rank = rank
		var err error
		switch rank {
		case rankRead:
			pending.read = new(history.Reading)
			err = json.Unmarshal(raw, pending.read)
		case rankWorld:
			pending.world = new(history.World)
			err = json.Unmarshal(raw, pending.world)
		case rankCycle:
			pending.cycle = new(history.Cycle)
			err = json.Unmarshal(raw, pending.cycle)
		}
		if err != nil {
			return stopped, hadCycles, err
		}
	}
	if err := flush(true); err != nil || res.Difference != "" {
		return stopped, hadCycles, err
	}
	return stopped, hadCycles, nil
}

// apply takes a cycle that ran before the replay's start as it was recorded.
func (st *segmentReplay) apply(g cycleRecord) {
	if g.read != nil {
		st.read = g.read
	}
	if g.world != nil {
		g.world.ApplyTo(st.mem)
	}
	if g.cycle != nil {
		st.lastCycle = g.cycle
	}
	// A cycle that wrote no line repeated the last one's gets, and live it kept their answers again.
	if st.lastCycle != nil {
		for _, ex := range st.lastCycle.HTTP {
			if ex.Unknown == "" {
				st.kept[httpservice.Key(ex.Service, ex.Path)] = ex
			}
		}
	}
}

// run does one cycle again and compares it with the recorded one.
func (st *segmentReplay) run(g cycleRecord, o Options, res *Replayed, torn bool) error {
	if g.read != nil {
		st.read = g.read
	}
	if st.read == nil || st.read.World == nil {
		return fmt.Errorf("%s holds no record of the world its cycles read: %w", filepath.Base(st.seg.Path), ErrNotReplayable)
	}
	if !st.ready {
		if err := st.load(); err != nil {
			return err
		}
	}
	wd := *st.read.World
	wd.At = time.UnixMilli(g.now)
	state := rules.FromWorld(wd, st.cfg, st.led, "")
	state.Kept = st.kept
	want := st.lastCycle
	if g.cycle != nil {
		want, st.lastCycle = g.cycle, g.cycle
	}
	var answers *recordedServices
	if want != nil {
		answers = &recordedServices{recorded: want.HTTP}
		state.HTTP = answers
	}
	for _, subject := range st.read.Reserved {
		state.Reserve(subject)
	}
	next := state.Carry(st.mem, st.seg.Header.Started)
	line, changed := history.Diff(st.mem, next)
	line.At = g.now
	st.mem = next
	got := history.CycleLine(g.now, "", st.rules.Cycle(state, nil))
	res.Cycles++
	at := time.UnixMilli(g.now).Format(time.TimeOnly)
	// A torn segment may end between the read line and the world line, so it only vouches for a world line it has.
	if o.Rules == nil && (!torn || g.world != nil) {
		if msg := compareMemory(line, changed, g.world); msg != "" {
			res.Difference = fmt.Sprintf("difference at %s\n  %s", at, msg)
			return nil
		}
	}
	if want != nil && !torn {
		if recorded, replayed, differs := compareCycles(*want, got, o.Rules != nil); differs {
			res.Difference = fmt.Sprintf("difference at %s\n  recorded: %s\n  replayed: %s", at, recorded, replayed)
			return nil
		}
		if recorded, replayed, differs := answers.differences(*want); differs {
			res.Difference = fmt.Sprintf("difference at %s\n  recorded: %s\n  replayed: %s", at, recorded, replayed)
			return nil
		}
	}
	res.Matched++
	return nil
}

// load takes the config the segment ran with, and its rules unless replay was given others.
func (st *segmentReplay) load() error {
	h := st.seg.Header
	cfg, err := config.Parse(h.Config, st.env)
	if err != nil {
		return fmt.Errorf("while reading the config recorded in %s: %w", filepath.Base(st.seg.Path), err)
	}
	st.cfg, st.ready = cfg, true
	if st.rules != nil {
		return nil
	}
	if h.Rules == "" {
		return fmt.Errorf("%s was written before history recorded the rules: %w", filepath.Base(st.seg.Path), ErrNotReplayable)
	}
	loaded, err := rules.Compile("", h.Rules, rules.Timeouts{})
	if err != nil {
		return fmt.Errorf("while loading the rules recorded in %s: %w", filepath.Base(st.seg.Path), err)
	}
	st.rules = loaded
	return nil
}

// compareMemory says how the memory a replayed cycle found differs from the world line the live run wrote.
func compareMemory(got history.World, changed bool, recorded *history.World) string {
	switch {
	case !changed && recorded == nil:
		return ""
	case !changed:
		return "memory: the live run recorded a change that replay did not find"
	case recorded == nil:
		return "memory: replay found a change that the live run did not record"
	}
	replayed, _ := json.Marshal(got)
	live, _ := json.Marshal(*recorded)
	if string(replayed) != string(live) {
		return fmt.Sprintf("memory: recorded %s, replayed %s", live, replayed)
	}
	return ""
}

// compareCycles returns the first line only the recorded cycle has, the first line only the replayed
// one has, and whether the two differ. A rule call that timed out live isn't compared, because replay doesn't spend its budget.
func compareCycles(recorded, replayed history.Cycle, actionsOnly bool) (string, string, bool) {
	skip := map[string]bool{}
	for _, t := range recorded.Throws {
		if t.Timeout {
			skip[t.Rule+"/"+t.Subject] = true
		}
	}
	a, b := cycleLines(recorded, skip, actionsOnly), cycleLines(replayed, skip, actionsOnly)
	onlyA := slices.DeleteFunc(slices.Clone(a), func(s string) bool { return slices.Contains(b, s) })
	onlyB := slices.DeleteFunc(slices.Clone(b), func(s string) bool { return slices.Contains(a, s) })
	if len(onlyA) == 0 && len(onlyB) == 0 {
		return "", "", false
	}
	return firstOr(onlyA, "nothing like it"), firstOr(onlyB, "nothing like it"), true
}

func firstOr(lines []string, none string) string {
	if len(lines) == 0 {
		return none
	}
	return lines[0]
}

// cycleLines lists what a cycle decided, one line each. Replay compares them and prints a difference, so
// their wording need not match trace or plan.
func cycleLines(c history.Cycle, skip map[string]bool, actionsOnly bool) []string {
	var out []string
	for _, e := range c.Wants {
		if !skip[e.Rule+"/"+e.Subject] {
			out = append(out, wantsText(e, actionsOnly))
		}
	}
	if actionsOnly {
		return out
	}
	for _, e := range c.Waiting {
		if !skip[e.Rule+"/"+e.Subject] {
			out = append(out, fmt.Sprintf("%s waits for %s: %s", e.Subject, e.Kind, e.Reason)+byRule(e))
		}
	}
	for _, e := range c.Held {
		if !skip[e.Rule+"/"+e.Subject] {
			out = append(out, heldText(e))
		}
	}
	for _, e := range c.Covered {
		if !skip[e.Rule+"/"+e.Subject] {
			out = append(out, fmt.Sprintf("%s has %s covered once %s: %s", e.Subject, e.Kind, e.Once.String(), e.Reason)+byRule(e))
		}
	}
	for _, t := range c.Throws {
		if !skip[t.Rule+"/"+t.Subject] {
			out = append(out, fmt.Sprintf("%s threw for %s: %s", t.Rule, t.Subject, t.Error))
		}
	}
	keys := make([]string, 0, len(c.Reads))
	for key := range c.Reads {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if !skip[key] {
			out = append(out, fmt.Sprintf("%s read %s", key, strings.Join(c.Reads[key], " ")))
		}
	}
	return out
}

func byRule(e history.Entry) string { return " (" + e.Rule + ")" }

// wantsText describes a wanted action. Rules compared for their actions alone may be named differently.
func wantsText(e history.Entry, actionsOnly bool) string {
	text := fmt.Sprintf("wants %s %s once %s", e.Kind, e.Subject, e.Once.String())
	for _, extra := range []struct {
		name string
		with map[string]any
	}{{"args", e.Args}, {"options", e.Options}} {
		if len(extra.with) > 0 {
			b, _ := json.Marshal(extra.with)
			text += fmt.Sprintf(" %s %s", extra.name, b)
		}
	}
	if actionsOnly {
		return text
	}
	return text + byRule(e)
}

func heldText(e history.Entry) string {
	if e.Dropped {
		return fmt.Sprintf("%s dropped an event for %s: %s", e.Rule, e.Subject, e.Reason)
	}
	return fmt.Sprintf("%s is held for %s: %s", e.Rule, e.Subject, e.Reason)
}

// recordedServices answers a replayed cycle's gets from the exchanges the live cycle recorded, in the
// order it made them. It never reaches a service, and a get the live cycle didn't make gets no answer.
type recordedServices struct {
	recorded []rules.Exchange
	used     []bool
	// extra lists the gets replay made that the live cycle did not.
	extra []string
}

func (r *recordedServices) Get(_ context.Context, service, path string) (httpservice.Response, error) {
	if r.used == nil {
		r.used = make([]bool, len(r.recorded))
	}
	for i, ex := range r.recorded {
		if !r.used[i] && ex.Service == service && ex.Path == path {
			r.used[i] = true
			if ex.Unknown != "" {
				return httpservice.Response{}, &httpservice.Unavailable{Reason: ex.Unknown}
			}
			return httpservice.Response{Status: ex.Status, Body: ex.Body}, nil
		}
	}
	r.extra = append(r.extra, "GET "+service+" "+path)
	return httpservice.Response{}, &httpservice.Unavailable{Reason: fmt.Sprintf("no recorded answer for GET %s %s", service, path)}
}

func (r *recordedServices) Post(context.Context, string, string, []byte) (httpservice.Response, error) {
	return httpservice.Response{}, &httpservice.Unavailable{Reason: "replay sends no POST"}
}

// differences returns the first get only the recorded cycle made, the first only replay made, and whether
// they differ. It compares which gets were made, not what they returned. A cycle with a rule call that timed out live isn't compared, because replay doesn't spend its budget.
func (r *recordedServices) differences(live history.Cycle) (string, string, bool) {
	for _, t := range live.Throws {
		if t.Timeout {
			return "", "", false
		}
	}
	var missing []string
	for i, ex := range r.recorded {
		if r.used == nil || !r.used[i] {
			missing = append(missing, "GET "+httpservice.Key(ex.Service, ex.Path))
		}
	}
	if len(missing) == 0 && len(r.extra) == 0 {
		return "", "", false
	}
	return firstOr(missing, "nothing like it"), firstOr(r.extra, "nothing like it"), true
}
