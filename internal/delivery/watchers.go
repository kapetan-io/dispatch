package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const sendTimeout = 5 * time.Second

// Followers holds which Claude Code sessions follow which ticket. It lives in memory only,
// because a session's inbox socket dies with the session and a list read back after a restart
// would be mostly dead sockets.
type Followers struct {
	mu      sync.Mutex
	sockets map[string][]string
}

// Follow records that a Claude Code session follows a ticket, so Notify sends
// that ticket's events to the session's inbox socket. Only an absolute path is
// taken, because the sender may run from any directory.
func (f *Followers) Follow(ticket, socket string) error {
	if !filepath.IsAbs(socket) {
		return fmt.Errorf("socket path %q is not absolute", socket)
	}
	if ticket == "" {
		return errors.New("a follower needs a ticket")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sockets == nil {
		f.sockets = map[string][]string{}
	}
	if !slices.Contains(f.sockets[ticket], socket) {
		f.sockets[ticket] = append(f.sockets[ticket], socket)
	}
	return nil
}

// Unfollow stops a session following a ticket. A session that was not
// following it is not an error.
func (f *Followers) Unfollow(ticket, socket string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sockets == nil {
		return
	}
	f.sockets[ticket] = slices.DeleteFunc(f.sockets[ticket], func(s string) bool { return s == socket })
}

// Of lists the sockets of the sessions following a ticket.
func (f *Followers) Of(ticket string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sockets[ticket])
}

// drop forgets the sockets of sessions that have exited, for every ticket they followed.
func (f *Followers) drop(gone []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ticket, list := range f.sockets {
		f.sockets[ticket] = slices.DeleteFunc(list, func(s string) bool { return slices.Contains(gone, s) })
	}
}

// tellWatchers sends ev to every session following its ticket. A socket that
// refuses the connection or whose file is gone belongs to a session that has
// exited, so every ticket it followed is dropped. Any other failure keeps the
// follower, because the session may be only briefly busy.
func (p Post) tellWatchers(ctx context.Context, ev Event) {
	if p.Followers == nil {
		return
	}
	var gone []string
	for _, socket := range p.Followers.Of(ev.Ticket.Identifier) {
		err := send(ctx, socket, message(ev))
		switch {
		case err == nil:
		case errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist):
			gone = append(gone, socket)
			p.warn("dropped watcher socket %s, which no longer accepts connections or is gone", socket)
		default:
			p.warn("while messaging watcher %s: %v", socket, err)
		}
	}
	p.Followers.drop(gone)
}

// warn reports a watcher failure without failing the action.
func (p Post) warn(format string, args ...any) {
	if p.Stderr != nil {
		_, _ = fmt.Fprintf(p.Stderr, "dispatch: "+format+"\n", args...)
	}
}

// message is the text a watching session's model reads. The ticket URL is added
// only when the text lacks it, so a PR event carries both links.
func message(ev Event) string {
	s := "dispatch event"
	if ev.Reason != "" {
		s += " (" + ev.Reason + ")"
	}
	s += ": " + ev.Text
	if u := ev.Ticket.URL; u != "" && !strings.Contains(ev.Text, u) {
		s += " Ticket: " + u
	}
	return s
}

// send writes one message into a Claude Code session's inbox socket. The
// frame is undocumented, taken from the binary's own debug hint and a captured
// SendMessage (anthropics/claude-code#93720), and observed working on Claude
// Code 2.1.296. A Claude Code release that
// changes it breaks here and nowhere else. The socket never replies.
func send(ctx context.Context, socket, text string) error {
	line, err := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]string{"role": "user", "content": text},
	})
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: sendTimeout}
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetWriteDeadline(time.Now().Add(sendTimeout))
	_, err = conn.Write(append(line, '\n'))
	return err
}
