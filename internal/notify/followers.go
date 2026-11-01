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
}

// Followers holds which sessions follow which ticket. It lives in memory only,
// because a session's inbox dies with the session and a list read back after a restart
// would be mostly dead inboxes.
type Followers struct {
	mu       sync.Mutex
	byTicket map[string][]Follower
}

// Follow records that a session follows a ticket, so Notify sends that ticket's
// notifications to the session through its harness.
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
	if !slices.Contains(f.byTicket[ticket], who) {
		f.byTicket[ticket] = append(f.byTicket[ticket], who)
	}
	return nil
}

// Unfollow stops a session following a ticket. A session that was not
// following it is not an error.
func (f *Followers) Unfollow(ticket string, who Follower) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byTicket == nil {
		return
	}
	f.byTicket[ticket] = slices.DeleteFunc(f.byTicket[ticket], func(o Follower) bool { return o == who })
}

// Of lists the sessions following a ticket.
func (f *Followers) Of(ticket string) []Follower {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.byTicket[ticket])
}

// drop forgets sessions that have exited, for every ticket they followed.
func (f *Followers) drop(gone []Follower) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ticket, followers := range f.byTicket {
		f.byTicket[ticket] = slices.DeleteFunc(followers, func(o Follower) bool { return slices.Contains(gone, o) })
	}
}

// tellFollowers sends n to every session following its ticket, through that session's
// harness. A session that has exited is dropped from every ticket it followed. Any
// other failure keeps the follower, because the session may be only briefly busy.
func (p Post) tellFollowers(ctx context.Context, n Notification) {
	if p.Followers == nil {
		return
	}
	var gone []Follower
	for _, who := range p.Followers.Of(n.Ticket.Identifier) {
		h, ok := p.Harnesses[who.Kind]
		if !ok {
			p.warn("while messaging watcher %s: no harness adapter for kind %q", who.Address, who.Kind)
			continue
		}
		err := h.Deliver(ctx, who.Address, message(n))
		switch {
		case err == nil:
		case errors.Is(err, harness.ErrGone):
			gone = append(gone, who)
			p.warn("dropped watcher socket %s, which no longer accepts connections or is gone", who.Address)
		default:
			p.warn("while messaging watcher %s: %v", who.Address, err)
		}
	}
	p.Followers.drop(gone)
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
