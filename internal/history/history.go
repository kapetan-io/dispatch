// Package history keeps dispatch's state on disk as a sequence of segment files.
// Each segment starts with a header and a snapshot of the world's memory and the
// ledger, then appends a line for every event, world change, cycle and receipt.
// A restart loads the newest segment, so history is the only state dispatch has.
package history

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/httpservice"

	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/world"
)

// Line types, as written in each line's "line" field.
const (
	LineHeader   = "header"
	LineSnapshot = "snapshot"
	LineEvent    = "event"
	LineRead     = "read"
	LineWorld    = "world"
	LineCycle    = "cycle"
	LineReceipt  = "receipt"
	LineStop     = "stop"
)

const (
	segmentPrefix = "events-"
	segmentSuffix = ".jsonl"
	timeLayout    = "20060102T150405Z"
)

// Header is the first line of a segment.
type Header struct {
	Line string `json:"line"`
	// Why is "start", "rotate" or "reset".
	Why string `json:"why"`
	// Config is the text of config.toml this segment ran with.
	Config string `json:"config,omitempty"`
	// Rules is the bundled script the rules ran as, so a replay runs what the live run did.
	Rules string `json:"rules,omitempty"`
	// Started is the run start that Carry stamps new facts with, in milliseconds. A segment that
	// `dispatch state reset` writes has no run, so it holds the reset time.
	Started int64 `json:"started,omitempty"`
	At      int64 `json:"at"`
}

// Snapshot is the second line: everything a restart needs from before the segment began.
type Snapshot struct {
	Line   string        `json:"line"`
	At     int64         `json:"at"`
	World  *world.Memory `json:"world"`
	Ledger LedgerState   `json:"ledger"`
	// Resync counts the resyncs so far, so a receipt can say which one it ran in.
	Resync int `json:"resync"`
	// Read is the last read a cycle ran over, so a segment can be replayed on its own.
	Read *Reading `json:"read,omitempty"`
}

// LedgerState is the receipts a snapshot carries.
type LedgerState struct {
	Receipts []ledger.Receipt `json:"receipts"`
}

// Event is something that told dispatch it should look again.
type Event struct {
	Line    string          `json:"line"`
	Ingest  int64           `json:"ingest"`
	Source  string          `json:"source"`
	Type    string          `json:"type"`
	Subject string          `json:"subject,omitempty"`
	Time    string          `json:"time,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Change is one fact that differs from the one before.
type Change struct {
	Record string          `json:"record"`
	Field  string          `json:"field"`
	Value  json.RawMessage `json:"value"`
	Since  int64           `json:"since"`
}

// World is what a read changed in the world's memory.
type World struct {
	Line string `json:"line"`
	// At is the cycle the change was found in, so a replay can pair it with the read and cycle lines.
	At      int64    `json:"at,omitempty"`
	Changes []Change `json:"changes,omitempty"`
	// Gone lists the facts that are no longer remembered, as "kind/id/field".
	Gone []string `json:"gone,omitempty"`
	// PreviousStatus, Waiting and Pools are written whole when any of them changed.
	PreviousStatus map[string]string     `json:"previousStatus,omitempty"`
	Waiting        map[string]int64      `json:"waiting,omitempty"`
	Pools          map[string]world.Pool `json:"pools,omitempty"`
	Others         bool                  `json:"others,omitempty"`
}

// Reading is the world a cycle ran over, as the cycle saw it, and the tickets that had a
// launch or promotion in flight. It is written before the cycle line, and only when
// it differs from the read before, so a cycle without one ran over the last.
type Reading struct {
	Line string `json:"line"`
	// Now is the cycle's time, which pairs the read with the world and cycle lines of that cycle.
	Now int64 `json:"now"`
	// Reserved are the tickets a worker slot is held for while their launch is in flight.
	Reserved []string     `json:"reserved,omitempty"`
	World    *world.World `json:"world"`
}

// Entry is one action a cycle wanted, set aside or held.
type Entry struct {
	Rule    string      `json:"rule"`
	Subject string      `json:"subject"`
	Kind    string      `json:"kind,omitempty"`
	Once    ledger.Once `json:"once,omitempty"`
	Reason  string      `json:"reason,omitempty"`
	Text    string      `json:"text,omitempty"`
	// Dropped marks a held entry for an on rule, whose event is gone for good.
	Dropped bool `json:"dropped,omitempty"`
	// Args and Options are what a wanted action carries besides its once key, such as a comment's text.
	Args    map[string]any `json:"args,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// Throw is a rule call that threw or ran out of time.
type Throw struct {
	Rule    string `json:"rule"`
	Subject string `json:"subject"`
	Error   string `json:"error"`
	Timeout bool   `json:"timeout,omitempty"`
}

// Cycle is what one run of every rule decided.
type Cycle struct {
	Line    string  `json:"line"`
	Now     int64   `json:"now"`
	Why     string  `json:"why,omitempty"`
	Wants   []Entry `json:"wants,omitempty"`
	Waiting []Entry `json:"waiting,omitempty"`
	Held    []Entry `json:"held,omitempty"`
	Covered []Entry `json:"covered,omitempty"`
	Throws  []Throw `json:"throws,omitempty"`
	// Reads maps "rule/subject" to what the rule read.
	Reads map[string][]string    `json:"reads,omitempty"`
	HTTP  []httpservice.Exchange `json:"http,omitempty"`
}

// Receipt is a receipt line. An attempted action has no outcome.
type Receipt struct {
	Line string `json:"line"`
	ledger.Receipt
}

// Writer appends lines to the segment being written.
type Writer struct {
	f    *os.File
	path string
}

// Path is the file the writer appends to.
func (w *Writer) Path() string { return w.path }

// Start opens a new segment in dir named for at, and writes its header and snapshot.
// A segment is never reopened, and a name already taken moves to the next free second.
func Start(dir string, at time.Time, h Header, snap Snapshot) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("while creating history dir: %w", err)
	}
	t := at.UTC().Truncate(time.Second)
	// A clock that stepped back must not name this segment before an older run's, or the
	// next start would load the older state.
	if existing, _ := Segments(dir); len(existing) > 0 {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(existing[len(existing)-1]), segmentPrefix), segmentSuffix)
		if newest, err := time.Parse(timeLayout, name); err == nil && !t.After(newest) {
			t = newest.Add(time.Second)
		}
	}
	var f *os.File
	var path string
	for {
		path = filepath.Join(dir, segmentPrefix+t.Format(timeLayout)+segmentSuffix)
		var err error
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("while creating %s: %w", path, err)
		}
		t = t.Add(time.Second)
	}
	w := &Writer{f: f, path: path}
	h.Line, snap.Line = LineHeader, LineSnapshot
	if snap.World == nil {
		snap.World = world.NewMemory()
	}
	if snap.Ledger.Receipts == nil {
		snap.Ledger.Receipts = []ledger.Receipt{}
	}
	if err := w.Append(h); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := w.Append(snap); err != nil {
		_ = f.Close()
		return nil, err
	}
	return w, w.f.Sync()
}

// Append writes one line.
func (w *Writer) Append(line any) error {
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	if _, err := w.f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("while writing history %s: %w", w.path, err)
	}
	return nil
}

// Sync makes every line so far durable.
func (w *Writer) Sync() error { return w.f.Sync() }

// Close ends the segment. A clean close writes the stop line, so a later reader
// can tell it from a crash.
func (w *Writer) Close(clean bool) error {
	var err error
	if clean {
		err = w.Append(map[string]any{"line": LineStop, "stop": true})
	}
	_ = w.f.Sync()
	return errors.Join(err, w.f.Close())
}

// Segment is one segment read back.
type Segment struct {
	Path     string
	Header   Header
	Snapshot Snapshot
	// Lines are the lines after the snapshot, in order, each as its raw JSON.
	Lines []json.RawMessage
	// Clean is true when the segment ends with a stop line.
	Clean bool
}

// Segments lists the segment files in dir, oldest first.
func Segments(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), segmentPrefix) && strings.HasSuffix(e.Name(), segmentSuffix) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(out)
	return out, nil
}

// SegmentStart is the time a segment's file name says it began.
func SegmentStart(path string) (time.Time, bool) {
	name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), segmentPrefix), segmentSuffix)
	t, err := time.Parse(timeLayout, name)
	return t, err == nil
}

// Latest reads the newest segment. It returns false when dir holds none.
func Latest(dir string) (Segment, bool, error) {
	paths, err := Segments(dir)
	if err != nil || len(paths) == 0 {
		return Segment{}, false, err
	}
	// A segment is created, then given its header and snapshot. A crash or a full disk in
	// between leaves one with neither, which holds nothing, so the one before it is the state.
	for i := len(paths) - 1; i >= 0; i-- {
		seg, err := Read(paths[i])
		if errors.Is(err, ErrIncomplete) {
			continue
		}
		return seg, err == nil, err
	}
	return Segment{}, false, nil
}

// ErrIncomplete means a segment has no header and snapshot, as after a crash while it was created.
var ErrIncomplete = errors.New("has no header and snapshot")

// Read reads one segment. A last line that doesn't parse is a crash's torn write
// and is dropped, and the read stops there.
func Read(path string) (Segment, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Segment{}, err
	}
	seg := Segment{Path: path}
	n := 0
	// A line holds whole HTTP responses, so it has no length limit.
	for rest := b; len(rest) > 0; {
		line, after, _ := bytes.Cut(rest, []byte("\n"))
		rest = after
		raw := bytes.TrimSpace(line)
		if len(raw) == 0 {
			continue
		}
		if !json.Valid(raw) {
			break
		}
		var head struct {
			Line string `json:"line"`
		}
		_ = json.Unmarshal(raw, &head)
		switch {
		case n == 0:
			if head.Line != LineHeader {
				return seg, fmt.Errorf("%s does not start with a header", path)
			}
			if err := json.Unmarshal(raw, &seg.Header); err != nil {
				return seg, err
			}
		case n == 1:
			if head.Line != LineSnapshot {
				return seg, fmt.Errorf("%s has no snapshot after its header", path)
			}
			if err := json.Unmarshal(raw, &seg.Snapshot); err != nil {
				return seg, err
			}
			if seg.Snapshot.World == nil {
				seg.Snapshot.World = world.NewMemory()
			}
			seg.Snapshot.World.Ready()
		default:
			if head.Line == LineStop {
				seg.Clean = true
				continue
			}
			seg.Lines = append(seg.Lines, slices.Clone(raw))
		}
		n++
	}
	if n < 2 {
		return seg, fmt.Errorf("%s %w", path, ErrIncomplete)
	}
	return seg, nil
}

// State is what a segment leaves behind.
type State struct {
	Memory *world.Memory
	Ledger *ledger.Ledger
	Resync int
	// Cycle is the last cycle written, which stands for every cycle after it that
	// came out the same.
	Cycle *Cycle
}

// Load applies a segment's lines to its snapshot.
func (s Segment) Load() (State, error) {
	st := State{Memory: s.Snapshot.World, Ledger: &ledger.Ledger{Entries: slices.Clone(s.Snapshot.Ledger.Receipts)}, Resync: s.Snapshot.Resync}
	for _, raw := range s.Lines {
		var head struct {
			Line string `json:"line"`
		}
		_ = json.Unmarshal(raw, &head)
		switch head.Line {
		case LineWorld:
			var w World
			if err := json.Unmarshal(raw, &w); err != nil {
				return st, err
			}
			w.ApplyTo(st.Memory)
		case LineReceipt:
			var r Receipt
			if err := json.Unmarshal(raw, &r); err != nil {
				return st, err
			}
			st.Ledger.Upsert(r.Receipt)
			st.Resync = max(st.Resync, r.Resync)
		case LineCycle:
			var c Cycle
			if err := json.Unmarshal(raw, &c); err != nil {
				return st, err
			}
			st.Cycle = &c
		case LineEvent:
			var e Event
			if err := json.Unmarshal(raw, &e); err != nil {
				return st, err
			}
			if e.Type == "dispatch.resync" || e.Type == "dispatch.tick" {
				st.Resync++
			}
		}
	}
	return st, nil
}

// Diff returns the world line that turns old into next, or false when they agree.
func Diff(old, next *world.Memory) (World, bool) {
	w := World{Line: LineWorld}
	for key, now := range next.Facts {
		if was, ok := old.Facts[key]; ok && was.Since == now.Since && bytes.Equal(was.Value, now.Value) {
			continue
		}
		record, field := splitKey(key)
		w.Changes = append(w.Changes, Change{Record: record, Field: field, Value: now.Value, Since: now.Since})
	}
	for key := range old.Facts {
		if _, ok := next.Facts[key]; !ok {
			w.Gone = append(w.Gone, key)
		}
	}
	slices.SortFunc(w.Changes, func(a, b Change) int { return strings.Compare(a.Record+"/"+a.Field, b.Record+"/"+b.Field) })
	slices.Sort(w.Gone)
	others := !mapsEqual(old.PreviousStatus, next.PreviousStatus) || !mapsEqual(old.Waiting, next.Waiting) || !poolsEqual(old.Pools, next.Pools)
	if others {
		w.Others, w.PreviousStatus, w.Waiting, w.Pools = true, next.PreviousStatus, next.Waiting, next.Pools
	}
	return w, len(w.Changes) > 0 || len(w.Gone) > 0 || others
}

// ApplyTo applies the line's changes to m.
func (w World) ApplyTo(m *world.Memory) {
	m.Ready()
	for _, c := range w.Changes {
		m.Facts[c.Record+"/"+c.Field] = world.Remembered{Value: c.Value, Since: c.Since}
	}
	for _, key := range w.Gone {
		delete(m.Facts, key)
	}
	if w.Others {
		m.PreviousStatus, m.Waiting, m.Pools = w.PreviousStatus, w.Waiting, w.Pools
		m.Ready()
	}
}

func splitKey(key string) (record, field string) {
	i := strings.LastIndex(key, "/")
	return key[:i], key[i+1:]
}

func mapsEqual[V comparable](a, b map[string]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func poolsEqual(a, b map[string]world.Pool) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// Prune deletes the segments that began more than keep ago, never the one at current.
func Prune(dir string, now time.Time, keep time.Duration, current string) error {
	paths, err := Segments(dir)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if p == current {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), segmentPrefix), segmentSuffix)
		t, err := time.Parse(timeLayout, name)
		if err != nil {
			continue
		}
		if now.Sub(t) > keep {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}
	return nil
}
