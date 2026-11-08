package notify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/kapetan-io/dispatch/internal/harness"
)

// Follower is a session that follows a ticket: the kind of harness it runs and where
// that harness delivers its messages.
type Follower struct {
	Kind    string `json:"kind"`
	Address string `json:"address"`
	// Process is the harness's name for the process the session runs in, empty when it has none.
	Process string `json:"process,omitempty"`
}

// Followers holds which sessions follow which ticket. It lives in memory only,
// because a session's inbox dies with the session and a list read back after a restart
// would be mostly dead inboxes.
type Followers struct {
	mu       sync.Mutex
	byTicket map[string][]Follower
}

// Follow records that a session follows a ticket, so Tell sends that ticket's
// notifications to the session through its harness. A session is its kind and address, so
// following again from a restarted process keeps one entry and updates its process.
func (f *Followers) Follow(ticket string, who Follower) error {
	if ticket == "" {
		return errors.New("a follower needs a ticket")
	}
	if who.Kind == "" || who.Address == "" {
		return errors.New("a follower needs a harness kind and an address")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byTicket == nil {
		f.byTicket = map[string][]Follower{}
	}
	for i, o := range f.byTicket[ticket] {
		if o.Kind == who.Kind && o.Address == who.Address {
			f.byTicket[ticket][i].Process = who.Process
			return nil
		}
	}
	f.byTicket[ticket] = append(f.byTicket[ticket], who)
	return nil
}

// Move gives each session of kind running in process the new address, for every ticket it
// follows, because the harness started a new conversation in the same process. A session
// already at the address stays one entry.
func (f *Followers) Move(kind, process, address string) {
	if process == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for ticket, followers := range f.byTicket {
		var moved []Follower
		for _, who := range followers {
			if who.Kind == kind && who.Process == process {
				who.Address = address
			}
			if !slices.ContainsFunc(moved, func(o Follower) bool { return sameSession(o, who) }) {
				moved = append(moved, who)
			}
		}
		f.byTicket[ticket] = moved
	}
}

// Unfollow stops a session following a ticket. A session that was not
// following it is not an error.
func (f *Followers) Unfollow(ticket string, who Follower) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byTicket == nil {
		return
	}
	f.byTicket[ticket] = slices.DeleteFunc(f.byTicket[ticket], func(o Follower) bool { return sameSession(o, who) })
}

func sameSession(a, b Follower) bool { return a.Kind == b.Kind && a.Address == b.Address }

// Of lists the sessions following a ticket.
func (f *Followers) Of(ticket string) []Follower {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.byTicket[ticket])
}

// Sessions lists each session following at least one ticket, once.
func (f *Followers) Sessions() []Follower {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Follower
	for _, followers := range f.byTicket {
		for _, who := range followers {
			if !slices.ContainsFunc(out, func(o Follower) bool { return sameSession(o, who) }) {
				out = append(out, who)
			}
		}
	}
	slices.SortFunc(out, func(a, b Follower) int { return strings.Compare(a.Kind+" "+a.Address, b.Kind+" "+b.Address) })
	return out
}

// drop forgets sessions that have exited, for every ticket they followed.
func (f *Followers) drop(gone []Follower) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ticket, followers := range f.byTicket {
		f.byTicket[ticket] = slices.DeleteFunc(followers, func(o Follower) bool {
			return slices.ContainsFunc(gone, func(g Follower) bool { return sameSession(g, o) })
		})
	}
}

// deliver sends n to each of followers at once through that session's harness, so a hung
// session costs only its own wait, and returns how many sessions took the message. A
// session that has exited is dropped from every ticket it followed. Any other failure keeps
// the follower, because the session may be only briefly busy.
func (p Post) deliver(ctx context.Context, followers []Follower, n Notification) int {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		gone    []Follower
		reached int
	)
	for _, who := range followers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, ok := p.Harnesses[who.Kind]
			if !ok {
				mu.Lock()
				defer mu.Unlock()
				p.warn("while messaging session %s: no harness adapter for kind %q", who.Address, who.Kind)
				return
			}
			err := h.Deliver(ctx, who.Address, message(n))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				reached++
			case errors.Is(err, harness.ErrGone):
				gone = append(gone, who)
				p.warn("dropped session %s, which is not open", who.Address)
			default:
				p.warn("while messaging session %s: %v", who.Address, err)
			}
		}()
	}
	wg.Wait()
	p.Followers.drop(gone)
	return reached
}

// warn reports a follower failure without failing the action.
func (p Post) warn(format string, args ...any) {
	if p.Stderr != nil {
		_, _ = fmt.Fprintf(p.Stderr, "dispatch: "+format+"\n", args...)
	}
}

// message is the text a following session's model reads. The ticket URL is added
// only when the text lacks it, so a PR notification carries both links.
func message(n Notification) string {
	s := "dispatch event"
	if n.Reason != "" {
		s += " (" + n.Reason + ")"
	}
	s += ": " + n.Text
	if u := n.Ticket.URL; u != "" && !strings.Contains(n.Text, u) {
		s += " Ticket: " + u
	}
	return s
}
