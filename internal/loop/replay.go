package loop

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/world"
)

// ErrNotReplayable means history holds no record of the world the cycles read, as in a
// segment written before dispatch recorded it.
var ErrNotReplayable = errors.New("history cannot be replayed")

// ReplayOptions picks the stretch of history to run again.
type ReplayOptions struct {
	// Dir is the history directory.
	Dir string
	// From and To bound the cycles replayed by the time they ran. A zero time is no bound.
	From, To time.Time
	// Rules replace the rules each segment recorded. Replay then compares only the actions
	// the cycles want, and stops at the first one that differs from the live run.
	Rules *rules.Rules
}

// Replayed is how a replay came out.
type Replayed struct {
	// Cycles counts the cycles run again and Matched those that came out as recorded.
	Cycles, Matched int
	// Difference describes the first place replay and the live run part, or is empty.
	Difference string
}

// Replay runs the cycles in a stretch of history again, over the world and the ledger
// they ran over, and compares each with what the live run decided. A cycle the live run
// found identical to the one before wrote no line, so only the cycles that wrote one are
// run, and each is compared with the last cycle line written.
func Replay(o ReplayOptions) (Replayed, error) {
	paths, err := history.Segments(o.Dir)
	if err != nil {
		return Replayed{}, err
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
		done, err := replaySegment(seg, o, &res)
		if err != nil || done || res.Difference != "" {
			return res, err
		}
	}
	return res, nil
}

// cycleGroup is the lines one cycle wrote, in the order it wrote them.
type cycleGroup struct {
	now   int64
	read  *history.Reading
	world *history.World
	cycle *history.Cycle
	rank  int
}

// Lines of one cycle come in this order, so a line that doesn't rank above the last starts the next cycle.
const (
	rankRead = iota + 1
	rankWorld
	rankCycle
)

type replaying struct {
	seg      history.Segment
	cfg      config.Config
	rules    *rules.Rules
	ready    bool
	mem      *world.Memory
	led      *ledger.Ledger
	read     *history.Reading
	recorded *history.Cycle
}

func replaySegment(seg history.Segment, o ReplayOptions, res *Replayed) (bool, error) {
	st := &replaying{
		seg: seg, mem: seg.Snapshot.World, led: &ledger.Ledger{Entries: slices.Clone(seg.Snapshot.Ledger.Receipts)},
		read: seg.Snapshot.Read, rules: o.Rules,
	}
	var pending *cycleGroup
	stopped := false
	flush := func() error {
		if pending == nil || stopped {
			return nil
		}
		g := *pending
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
		return st.run(g, o, res)
	}
	for _, raw := range seg.Lines {
		var head struct {
			Line string `json:"line"`
			Now  int64  `json:"now"`
			At   int64  `json:"at"`
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
			if err := flush(); err != nil || res.Difference != "" {
				return false, err
			}
			var r history.Receipt
			if err := json.Unmarshal(raw, &r); err != nil {
				return false, err
			}
			st.led.Upsert(r.Receipt)
			continue
		default:
			if err := flush(); err != nil || res.Difference != "" {
				return false, err
			}
			continue
		}
		if pending != nil && (pending.now != now || rank <= pending.rank) {
			if err := flush(); err != nil || res.Difference != "" {
				return false, err
			}
		}
		if pending == nil {
			pending = &cycleGroup{now: now}
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
			return false, err
		}
	}
	if err := flush(); err != nil || res.Difference != "" {
		return false, err
	}
	return stopped, nil
}

// apply takes a cycle that ran before the replay's start as it was recorded.
func (st *replaying) apply(g cycleGroup) {
	if g.read != nil {
		st.read = g.read
	}
	if g.world != nil {
		g.world.ApplyTo(st.mem)
	}
	if g.cycle != nil {
		st.recorded = g.cycle
	}
}

// run does one cycle again and compares it with the recorded one.
func (st *replaying) run(g cycleGroup, o ReplayOptions, res *Replayed) error {
	if g.read != nil {
		st.read = g.read
	}
	if st.read == nil || st.read.World == nil {
		return fmt.Errorf("%s holds no record of the world its cycles read: %w", baseName(st.seg.Path), ErrNotReplayable)
	}
	if !st.ready {
		if err := st.load(); err != nil {
			return err
		}
	}
	wd := *st.read.World
	wd.At = time.UnixMilli(g.now)
	state := rules.FromWorld(wd, st.cfg, st.led, "")
	for _, subject := range st.read.Reserved {
		state.Reserve(subject)
	}
	next := state.Carry(st.mem, st.seg.Header.Started)
	line, changed := history.Diff(st.mem, next)
	line.At = g.now
	st.mem = next
	got := cycleLine(g.now, "", st.rules.Cycle(state, nil))
	want := st.recorded
	if g.cycle != nil {
		want, st.recorded = g.cycle, g.cycle
	}
	res.Cycles++
	at := time.UnixMilli(g.now).Format(time.TimeOnly)
	if o.Rules == nil {
		if msg := compareMemory(line, changed, g.world); msg != "" {
			res.Difference = fmt.Sprintf("difference at %s\n  %s", at, msg)
			return nil
		}
	}
	if want != nil {
		if recorded, replayed, differs := compareCycles(*want, got, o.Rules != nil); differs {
			res.Difference = fmt.Sprintf("difference at %s\n  recorded: %s\n  replayed: %s", at, recorded, replayed)
			return nil
		}
	}
	res.Matched++
	return nil
}

// load takes the config the segment ran with, and its rules unless replay was given others.
func (st *replaying) load() error {
	h := st.seg.Header
	cfg, err := config.Parse(h.Config)
	if err != nil {
		return fmt.Errorf("while reading the config recorded in %s: %w", baseName(st.seg.Path), err)
	}
	st.cfg, st.ready = cfg, true
	if st.rules != nil {
		return nil
	}
	if h.Rules == "" {
		return fmt.Errorf("%s was written before history recorded the rules: %w", baseName(st.seg.Path), ErrNotReplayable)
	}
	loaded, err := rules.Compile("", h.Rules, rules.Timeouts{If: time.Minute, Then: time.Minute})
	if err != nil {
		return fmt.Errorf("while loading the rules recorded in %s: %w", baseName(st.seg.Path), err)
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
	x, _ := json.Marshal(got)
	y, _ := json.Marshal(*recorded)
	if string(x) != string(y) {
		return fmt.Sprintf("memory: recorded %s, replayed %s", y, x)
	}
	return ""
}

// compareCycles returns the first line the recorded cycle has that the replayed one lacks, and the
// other way round. A rule call that timed out live isn't compared, because replay doesn't spend its budget.
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

// cycleLines lists what a cycle decided, one line each, in words the trace uses.
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
			out = append(out, fmt.Sprintf("%s has %s covered once %s: %s", e.Subject, e.Kind, FormatOnce(e.Once), e.Reason)+byRule(e))
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
	text := fmt.Sprintf("wants %s %s once %s", e.Kind, e.Subject, FormatOnce(e.Once))
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
