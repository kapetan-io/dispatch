package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	watchersFile = "watchers"
	sendTimeout  = 5 * time.Second
)

// Watch records that a Claude Code session follows a ticket, so Notify sends
// that ticket's events to the session's inbox socket. Only an absolute path is
// taken, because the sender may run from any directory.
func Watch(stateDir, ticket, socket string) error {
	if !filepath.IsAbs(socket) {
		return fmt.Errorf("socket path %q is not absolute", socket)
	}
	// Each line holds a ticket, one space and a socket path, and trims the line.
	if socket != strings.TrimSpace(socket) || strings.Contains(socket, "\n") {
		return fmt.Errorf("socket path %q has a newline or surrounding whitespace", socket)
	}
	if ticket == "" || strings.ContainsAny(ticket, " \t\r\n") {
		return fmt.Errorf("ticket %q is not a ticket identifier", ticket)
	}
	line := ticket + " " + socket
	return updateWatchers(stateDir, func(list []string) []string {
		if slices.Contains(list, line) {
			return list
		}
		return append(list, line)
	})
}

// Unwatch stops a session following a ticket. A session that was not
// following it is not an error.
func Unwatch(stateDir, ticket, socket string) error {
	return updateWatchers(stateDir, func(list []string) []string {
		return slices.DeleteFunc(list, func(line string) bool { return line == ticket+" "+socket })
	})
}

// Watchers lists the sockets of the sessions following a ticket.
func Watchers(stateDir, ticket string) ([]string, error) {
	lines, err := readWatchers(stateDir)
	if err != nil {
		return nil, err
	}
	var sockets []string
	for _, line := range lines {
		if id, socket, ok := strings.Cut(line, " "); ok && id == ticket {
			sockets = append(sockets, socket)
		}
	}
	return sockets, nil
}

func readWatchers(stateDir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, watchersFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("while reading watchers: %w", err)
	}
	var list []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			list = append(list, line)
		}
	}
	return list, nil
}

// Preview lists what Notify would send for ev, one line per follower. An
// unreadable list previews nothing, and the live tick reports it.
func (p Post) Preview(ev Event) []string {
	list, err := Watchers(p.StateDir, ev.Ticket.Identifier)
	if err != nil {
		return nil
	}
	var lines []string
	for _, socket := range list {
		lines = append(lines, fmt.Sprintf("would message %s: %s", socket, message(ev)))
	}
	return lines
}

// tellWatchers sends ev to every session following its ticket. A socket that
// refuses the connection or whose file is gone belongs to a session that has
// exited, so every ticket it followed is dropped. Any other failure keeps the
// follower, because the session may be only briefly busy.
func (p Post) tellWatchers(ctx context.Context, ev Event) {
	list, err := Watchers(p.StateDir, ev.Ticket.Identifier)
	if err != nil {
		p.warn("%v", err)
		return
	}
	var gone []string
	for _, socket := range list {
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
	if len(gone) == 0 {
		return
	}
	err = updateWatchers(p.StateDir, func(list []string) []string {
		return slices.DeleteFunc(list, func(line string) bool {
			_, socket, _ := strings.Cut(line, " ")
			return slices.Contains(gone, socket)
		})
	})
	if err != nil {
		p.warn("%v", err)
	}
}

// warn reports a watcher failure without failing the tick.
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

// updateWatchers rewrites the list under a lock, because a tick dropping a
// dead socket can overlap a session watching a ticket.
func updateWatchers(stateDir string, change func([]string) []string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("while creating %s: %w", stateDir, err)
	}
	lock, err := os.OpenFile(filepath.Join(stateDir, watchersFile+".lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("while locking watchers: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("while locking watchers: %w", err)
	}
	list, err := readWatchers(stateDir)
	if err != nil {
		return err
	}
	list = change(list)
	path := filepath.Join(stateDir, watchersFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(list, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("while writing watchers: %w", err)
	}
	return os.Rename(tmp, path)
}
