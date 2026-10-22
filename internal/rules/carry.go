package rules

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/kapetan-io/dispatch/internal/world"
)

// derivedTimes are the facts whose value is a time the facts around them decide,
// so they follow those facts instead of being remembered themselves.
var derivedTimes = map[string]bool{
	"statusSince": true, "previousStatus": true, "seenAt": true, "goneSince": true, "idleSince": true,
	"blockedSince": true, "lastStop": true, "cleanSince": true, "leasedSince": true, "startedAt": true, "started": true,
}

// Carry gives each fact the time dispatch first saw its value, from what mem
// remembers, and returns the memory to keep. A fact whose value equals the
// remembered one keeps that time, so a worker blocked 50 minutes before a
// restart keeps its once key. A fact whose read failed keeps its memory, because a
// failed read deletes nothing. started is when this dispatch began.
func (s *State) Carry(mem *world.Memory, started int64) *world.Memory {
	if mem == nil {
		mem = world.NewMemory()
	}
	mem.Ready()
	at := s.ms()
	next := world.NewMemory()
	for label, p := range mem.Pools {
		next.Pools[label] = p
	}
	keep := func(key string) {
		if old, ok := mem.Facts[key]; ok {
			next.Facts[key] = old
		}
	}
	for _, kind := range Kinds {
		for _, r := range s.kinds[kind.Name] {
			prefix := r.kind + "/" + r.id + "/"
			elem := r.v.Elem()
			for name, i := range fieldIndex[elem.Type()] {
				f := elem.Field(i)
				key := prefix + name
				if f.FieldByName("Unknown").String() != "" {
					keep(key)
					continue
				}
				if derivedTimes[name] {
					continue
				}
				val := f.FieldByName("Value").Interface()
				// A comment or activity is the same one while its own time is, and
				// the time dispatch first saw it is what the fact carries.
				seen, isSeen := val.(*Seen)
				var enc []byte
				switch {
				case isSeen && seen != nil:
					enc, _ = json.Marshal(seen.At)
				case isSeen:
					enc = []byte("null")
				default:
					enc, _ = json.Marshal(val)
				}
				since := at
				if old, ok := mem.Facts[key]; ok && old.Same(enc) {
					since = old.Since
				}
				f.FieldByName("Since").SetInt(since)
				if isSeen && seen != nil {
					seen.SeenAt = since
				}
				next.Facts[key] = world.Remembered{Value: enc, Since: since}
			}
			s.deriveTimes(r, mem, next, started)
		}
	}
	s.carryPreviousStatus(mem, next)
	s.carryWaiting(mem, next)
	s.carryPools(mem, next)
	s.carryChildren(mem, next)
	s.keepWhileUnread(mem, next)
	return next
}

func (s *State) deriveTimes(r *record, mem, next *world.Memory, started int64) {
	at := s.ms()
	set := func(name string, v any, since int64) {
		f, ok := r.fact(name)
		if !ok {
			return
		}
		f.FieldByName("Value").Set(reflect.ValueOf(v))
		f.FieldByName("Since").SetInt(since)
	}
	unknown := func(name string) bool {
		f, ok := r.fact(name)
		return !ok || f.FieldByName("Unknown").String() != ""
	}
	switch rec := r.v.Interface().(type) {
	case *Ticket:
		if rec.Status.Unknown == "" {
			set("statusSince", rec.Status.Since, rec.Status.Since)
		}
	case *Worker:
		if rec.State.Unknown != "" {
			// A failed read deletes nothing, so the worker keeps the presence it had.
			if old, ok := mem.Facts[r.kind+"/"+r.id+"/presence"]; ok {
				next.Facts[r.kind+"/"+r.id+"/presence"] = old
			}
			return
		}
		// Presence is remembered as its own fact, so seenAt and goneSince survive a
		// change from working to idle and a restart.
		presence := "present"
		if rec.State.Value == "none" {
			presence = "absent"
		}
		key := r.kind + "/" + r.id + "/presence"
		enc, _ := json.Marshal(presence)
		since := at
		if old, ok := mem.Facts[key]; ok && old.Same(enc) {
			since = old.Since
		}
		next.Facts[key] = world.Remembered{Value: enc, Since: since}
		set("seenAt", since, since)
		set("goneSince", since, since)
		stateSince := rec.State.Since
		if !unknown("idleSince") && rec.IdleSince.Value != nil {
			v := stateSince
			set("idleSince", &v, stateSince)
			if r.stopFallback {
				set("lastStop", &Stop{At: stateSince}, stateSince)
			}
		}
		if !unknown("blockedSince") && rec.BlockedSince.Value != nil {
			v := stateSince
			set("blockedSince", &v, stateSince)
		}
	case *Tree:
		if rec.Holder.Unknown == "" {
			set("leasedSince", rec.Holder.Since, rec.Holder.Since)
		}
		if rec.Pushed.Unknown == "" && rec.TrackedChanges.Unknown == "" && rec.Untracked.Unknown == "" {
			since := max(rec.Pushed.Since, rec.TrackedChanges.Since, rec.Untracked.Since)
			set("cleanSince", since, since)
		}
	case *Dispatch:
		set("startedAt", started, started)
	}
}

// carryPreviousStatus remembers the status a ticket had before its current one,
// and gives the ticket the answer as previousStatus.
func (s *State) carryPreviousStatus(mem, next *world.Memory) {
	for _, r := range s.kinds["ticket"] {
		t := r.v.Interface().(*Ticket)
		if t.Status.Unknown != "" {
			if prev, ok := mem.PreviousStatus[r.id]; ok {
				next.PreviousStatus[r.id] = prev
			}
			continue
		}
		prev, had := mem.PreviousStatus[r.id]
		old, ok := mem.Facts["ticket/"+r.id+"/status"]
		if ok {
			var was string
			_ = json.Unmarshal(old.Value, &was)
			if was != t.Status.Value {
				prev, had = was, true
			}
		}
		if had {
			next.PreviousStatus[r.id] = prev
			t.PreviousStatus = Known(&prev, t.Status.Since)
		}
	}
}

// carryWaiting remembers when each ticket began waiting on its open blockers.
func (s *State) carryWaiting(mem, next *world.Memory) {
	for id, b := range s.blockers {
		open := slices.ContainsFunc(b.read, func(x Blocker) bool { return !x.Done })
		if !open {
			continue
		}
		since, ok := mem.Waiting[id]
		if !ok {
			since = s.ms()
		}
		next.Waiting[id] = since
		s.waiting[id] = since
	}
	// A ticket whose blockers couldn't be read keeps the wait it had.
	for id, since := range mem.Waiting {
		if _, ok := next.Waiting[id]; ok {
			continue
		}
		if t, ok := s.lookup("ticket", id); ok && t.v.Interface().(*Ticket).Status.Unknown != "" {
			next.Waiting[id] = since
		}
	}
}

// carryPools counts the trees that came back to each pool. A lease seen in one
// read and gone in the next is a return, whoever made it.
func (s *State) carryPools(mem, next *world.Memory) {
	for label, p := range s.pools {
		old := mem.Pools[label]
		if p.unknown != "" {
			next.Pools[label] = old
			p.returns = old.Returns
			s.pools[label] = p
			continue
		}
		leases := map[string]string{}
		for _, tr := range s.kinds["tree"] {
			tree := tr.v.Interface().(*Tree)
			if tree.Holder.Unknown != "" {
				continue
			}
			if repo, ok := s.treeRepo[tr.id]; ok && repo == label && tree.Holder.Value.ID != "" {
				leases[tr.id] = tree.Holder.Value.ID
			}
		}
		returns := old.Returns
		for path, holder := range old.Leases {
			if leases[path] != holder {
				returns++
			}
		}
		next.Pools[label] = world.Pool{Returns: returns, Leases: leases}
		p.returns = returns
		s.pools[label] = p
	}
}

// carryChildren remembers which children each parent listed. A parent whose
// children couldn't be read keeps the list it had, and so does every parent while
// Linear is unread, because no parent exists then to say its children were unread.
func (s *State) carryChildren(mem, next *world.Memory) {
	if s.linearUnread != "" {
		maps.Copy(next.Children, mem.Children)
	}
	for parent, ids := range s.children {
		next.Children[parent] = ids
	}
	for parent := range s.childrenUnread {
		if ids, ok := mem.Children[parent]; ok {
			next.Children[parent] = ids
		}
	}
}

// keepWhileUnread keeps what dispatch remembered about records a failed read left out,
// because no record exists to carry it. While Linear can't be read no ticket exists, so
// every wait is kept. While a pool can't be read its trees don't exist, nor do the
// tickets that hold them, so those are kept. While a parent's children can't be listed
// the ones it last listed are kept. Without this a restart during an outage would forget
// every wait, and the keys built on them would change. Tree facts are kept only for an
// unread pool, so a children-only failure doesn't keep every tree.
func (s *State) keepWhileUnread(mem, next *world.Memory) {
	poolUnread := false
	for _, p := range s.pools {
		poolUnread = poolUnread || p.unknown != ""
	}
	if s.linearUnread == "" && !poolUnread && len(s.childrenUnread) == 0 {
		return
	}
	kept := map[string]bool{}
	if poolUnread {
		for key, old := range mem.Facts {
			var holder Ref
			if strings.HasPrefix(key, "tree/") && strings.HasSuffix(key, "/holder") && json.Unmarshal(old.Value, &holder) == nil {
				kept[holder.ID] = true
			}
		}
	}
	for parent := range s.childrenUnread {
		for _, id := range mem.Children[parent] {
			kept[id] = true
		}
	}
	keep := func(key string) bool {
		if s.linearUnread != "" || (poolUnread && strings.HasPrefix(key, "tree/")) {
			return true
		}
		for id := range kept {
			if strings.HasPrefix(key, "ticket/"+id+"/") || strings.HasPrefix(key, "worker/"+id+"/") || strings.HasPrefix(key, "worker/"+id+"@") {
				return true
			}
		}
		return false
	}
	for key, old := range mem.Facts {
		if _, ok := next.Facts[key]; !ok && keep(key) {
			next.Facts[key] = old
		}
	}
	for id, v := range mem.PreviousStatus {
		if _, ok := next.PreviousStatus[id]; !ok && (s.linearUnread != "" || kept[id]) {
			next.PreviousStatus[id] = v
		}
	}
	for id, v := range mem.Waiting {
		if _, ok := next.Waiting[id]; !ok && (s.linearUnread != "" || kept[id]) {
			next.Waiting[id] = v
		}
	}
}
