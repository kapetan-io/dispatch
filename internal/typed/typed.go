// Package typed remembers the messages dispatch typed into workers, so the running dispatch can
// tell a command it sent from one a person typed. It lives in the loop's memory, because only
// the loop hears the hooks. A launch in another process reports its notes to the loop over the socket.
package typed

import (
	"slices"
	"strings"
	"sync"

	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/treekey"
)

// perTree caps what a tree remembers, so text that never reaches Claude cannot pile up.
const perTree = 64

// Log holds the messages dispatch typed into each worker tree that Claude has not yet taken.
// The zero value is ready to use, and a nil Log remembers nothing.
type Log struct {
	mu   sync.Mutex
	sent map[string][]string
}

// Note records that text is about to be typed into the Claude in tree. Text that is not a command is
// not kept, because the hook never reports it.
func (l *Log) Note(tree, text string) {
	if _, ok := sessionlog.ParseCommand(text); !ok || l == nil {
		return
	}
	key, text := treekey.Of(tree), strings.TrimSpace(text)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sent == nil {
		l.sent = map[string][]string{}
	}
	l.sent[key] = append(l.sent[key], text)
	if over := len(l.sent[key]) - perTree; over > 0 {
		l.sent[key] = l.sent[key][over:]
	}
}

// Withdraw forgets the latest Note of text, because the text was refused and never typed.
func (l *Log) Withdraw(tree, text string) {
	if l == nil {
		return
	}
	key, text := treekey.Of(tree), strings.TrimSpace(text)
	l.mu.Lock()
	defer l.mu.Unlock()
	sent := l.sent[key]
	for i := len(sent) - 1; i >= 0; i-- {
		if sent[i] == text {
			l.sent[key] = slices.Delete(sent, i, i+1)
			return
		}
	}
}

// Take reports whether dispatch typed text into tree, and forgets the oldest such Note, so
// one send answers one message.
func (l *Log) Take(tree, text string) bool {
	if l == nil {
		return false
	}
	key, text := treekey.Of(tree), strings.TrimSpace(text)
	l.mu.Lock()
	defer l.mu.Unlock()
	sent := l.sent[key]
	for i, s := range sent {
		if s == text {
			l.sent[key] = slices.Delete(sent, i, i+1)
			return true
		}
	}
	return false
}

// Clear forgets everything dispatch typed into tree, because its Claude is gone and the
// next one in that tree cannot be answering these.
func (l *Log) Clear(tree string) {
	if l == nil {
		return
	}
	key := treekey.Of(tree)
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sent, key)
}
