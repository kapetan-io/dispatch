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
	subscribersFile = "supervisors"
	sendTimeout     = 5 * time.Second
)

// Subscribe records a Claude Code session's inbox socket, so every event
// Notify raises is also sent to that session. Only an absolute path is taken,
// because the sender may run from any directory.
func Subscribe(stateDir, socket string) error {
	if !filepath.IsAbs(socket) {
		return fmt.Errorf("socket path %q is not absolute", socket)
	}
	// The list holds one path per line and trims each line.
	if socket != strings.TrimSpace(socket) || strings.Contains(socket, "\n") {
		return fmt.Errorf("socket path %q has a newline or surrounding whitespace", socket)
	}
	return updateSubscribers(stateDir, func(list []string) []string {
		if slices.Contains(list, socket) {
			return list
		}
		return append(list, socket)
	})
}

// Subscribers lists the recorded sockets.
func Subscribers(stateDir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, subscribersFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("while reading supervisors: %w", err)
	}
	var list []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			list = append(list, line)
		}
	}
	return list, nil
}

// Preview lists what Notify would send for ev, one line per subscriber. An
// unreadable list previews nothing, and the live tick reports it.
func (p Post) Preview(ev Event) []string {
	list, err := Subscribers(p.StateDir)
	if err != nil {
		return nil
	}
	var lines []string
	for _, socket := range list {
		lines = append(lines, fmt.Sprintf("would message %s: %s", socket, message(ev)))
	}
	return lines
}

// tellSupervisors sends ev to every subscriber. A socket that refuses the
// connection or whose file is gone belongs to a session that has exited, so it
// is dropped. Any other failure keeps the subscriber, because the session may
// be only briefly busy.
func (p Post) tellSupervisors(ctx context.Context, ev Event) {
	list, err := Subscribers(p.StateDir)
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
			p.warn("dropped supervisor socket %s, which no longer accepts connections or is gone", socket)
		default:
			p.warn("while messaging supervisor %s: %v", socket, err)
		}
	}
	if len(gone) == 0 {
		return
	}
	err = updateSubscribers(p.StateDir, func(list []string) []string {
		return slices.DeleteFunc(list, func(s string) bool { return slices.Contains(gone, s) })
	})
	if err != nil {
		p.warn("%v", err)
	}
}

// warn reports a supervisor failure without failing the tick.
func (p Post) warn(format string, args ...any) {
	if p.Stderr != nil {
		_, _ = fmt.Fprintf(p.Stderr, "dispatch: "+format+"\n", args...)
	}
}

// message is the text the supervisor's model reads. The ticket URL is added
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

// updateSubscribers rewrites the list under a lock, because a tick dropping a
// dead socket can overlap a session subscribing.
func updateSubscribers(stateDir string, change func([]string) []string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("while creating %s: %w", stateDir, err)
	}
	lock, err := os.OpenFile(filepath.Join(stateDir, subscribersFile+".lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("while locking supervisors: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("while locking supervisors: %w", err)
	}
	list, err := Subscribers(stateDir)
	if err != nil {
		return err
	}
	list = change(list)
	path := filepath.Join(stateDir, subscribersFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(list, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("while writing supervisors: %w", err)
	}
	return os.Rename(tmp, path)
}
