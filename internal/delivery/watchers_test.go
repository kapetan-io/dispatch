package delivery_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/delivery"
	"github.com/kapetan-io/dispatch/internal/linear"
)

// inbox stands in for a Claude Code session's socket and collects the lines
// written to it.
type inbox struct {
	path  string
	lines chan string
	ln    net.Listener
}

// shortDir keeps socket paths under the 104 byte limit macOS puts on them.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ds")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newInbox(t *testing.T, name string) *inbox {
	t.Helper()
	in := &inbox{path: filepath.Join(shortDir(t), name), lines: make(chan string, 8)}
	ln, err := net.Listen("unix", in.path)
	require.NoError(t, err)
	in.ln = ln
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				sc := bufio.NewScanner(conn)
				for sc.Scan() {
					in.lines <- sc.Text()
				}
			}()
		}
	}()
	return in
}

// content parses the next frame and returns the message text.
func (in *inbox) content(t *testing.T) string {
	t.Helper()
	select {
	case line := <-in.lines:
		var frame struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &frame))
		assert.Equal(t, "user", frame.Type)
		assert.Equal(t, "user", frame.Message.Role)
		return frame.Message.Content
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no message arrived")
		return ""
	}
}

// deadSocket is a socket file whose session has exited, so connecting refuses.
func deadSocket(t *testing.T) string {
	t.Helper()
	path := filepath.Join(shortDir(t), "dead.sock")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	// Closing a UnixListener removes the file, and a leftover file is what a crashed session leaves.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	require.FileExists(t, path)
	return path
}

func okAlert(t *testing.T) string {
	return script(t, "osascript", `exit 0`)
}

var ready = delivery.Event{
	Ticket: linear.Ticket{Identifier: "ENG-1", URL: "https://linear.app/x/issue/ENG-1"},
	Reason: "PR ready for review",
	Text:   "ENG-1 is ready for review: https://github.com/o/r/pull/9",
}

func TestNotifyMessagesEveryWatcher(t *testing.T) {
	state := t.TempDir()
	one, two := newInbox(t, "one.sock"), newInbox(t, "two.sock")
	require.NoError(t, delivery.Watch(state, "ENG-1", one.path))
	require.NoError(t, delivery.Watch(state, "ENG-1", two.path))
	post := delivery.Post{Osascript: okAlert(t), StateDir: state}

	require.NoError(t, post.Notify(context.Background(), ready))

	for _, in := range []*inbox{one, two} {
		got := in.content(t)
		assert.Contains(t, got, "ENG-1")
		assert.Contains(t, got, "PR ready for review")
		assert.Contains(t, got, "https://github.com/o/r/pull/9")
		assert.Contains(t, got, "https://linear.app/x/issue/ENG-1")
	}
}

func TestNotifyShowsTheAlertWithNoWatchers(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	post := delivery.Post{Osascript: script(t, "osascript", `echo shown >> `+log), StateDir: t.TempDir()}

	require.NoError(t, post.Notify(context.Background(), ready))

	assert.FileExists(t, log)
}

func TestNotifyDropsASocketThatRefuses(t *testing.T) {
	state := t.TempDir()
	live := newInbox(t, "live.sock")
	dead := deadSocket(t)
	require.NoError(t, delivery.Watch(state, "ENG-1", dead))
	require.NoError(t, delivery.Watch(state, "ENG-1", live.path))
	var stderr bytes.Buffer
	post := delivery.Post{Osascript: okAlert(t), StateDir: state, Stderr: &stderr}

	require.NoError(t, post.Notify(context.Background(), ready))

	live.content(t)
	assert.Contains(t, stderr.String(), "dropped watcher socket "+dead)
	list, err := delivery.Watchers(state, "ENG-1")
	require.NoError(t, err)
	assert.Equal(t, []string{live.path}, list)
}

func TestNotifyDropsASocketWhoseFileIsGone(t *testing.T) {
	state := t.TempDir()
	missing := filepath.Join(shortDir(t), "gone.sock")
	require.NoError(t, delivery.Watch(state, "ENG-1", missing))
	post := delivery.Post{Osascript: okAlert(t), StateDir: state, Stderr: &bytes.Buffer{}}

	require.NoError(t, post.Notify(context.Background(), ready))

	list, err := delivery.Watchers(state, "ENG-1")
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestNotifyErrorIsTheAlertsAlone(t *testing.T) {
	state := t.TempDir()
	require.NoError(t, delivery.Watch(state, "ENG-1", deadSocket(t)))
	post := delivery.Post{Osascript: okAlert(t), StateDir: state, Stderr: &bytes.Buffer{}}

	require.NoError(t, post.Notify(context.Background(), ready))
}

func TestNotifyMessagesWatchersOnlyOnceTheAlertShowed(t *testing.T) {
	state := t.TempDir()
	live := newInbox(t, "live.sock")
	require.NoError(t, delivery.Watch(state, "ENG-1", live.path))
	post := delivery.Post{Osascript: script(t, "osascript", `echo denied >&2; exit 1`), StateDir: state}

	for range 3 {
		err := post.Notify(context.Background(), ready)
		require.ErrorContains(t, err, "while notifying")
	}

	assert.Empty(t, live.lines)
}

func TestWatchRecordsEachSocketOnce(t *testing.T) {
	state := t.TempDir()

	require.NoError(t, delivery.Watch(state, "ENG-1", "/tmp/a.sock"))
	require.NoError(t, delivery.Watch(state, "ENG-1", "/tmp/b.sock"))
	require.NoError(t, delivery.Watch(state, "ENG-1", "/tmp/a.sock"))

	list, err := delivery.Watchers(state, "ENG-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/a.sock", "/tmp/b.sock"}, list)
}

func TestWatchRefusesARelativePath(t *testing.T) {
	state := t.TempDir()

	err := delivery.Watch(state, "ENG-1", "cc-socks/1.sock")

	require.ErrorContains(t, err, "not absolute")
	list, err := delivery.Watchers(state, "ENG-1")
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestWatchRefusesAPathTheListCannotHold(t *testing.T) {
	for _, path := range []string{"/tmp/a\nb/sock", "/tmp/a/sock "} {
		require.ErrorContains(t, delivery.Watch(t.TempDir(), "ENG-1", path), "whitespace")
	}
}

func TestNotifyOnceMessagesWatchersWhenTheAlertFails(t *testing.T) {
	state := t.TempDir()
	live := newInbox(t, "live.sock")
	require.NoError(t, delivery.Watch(state, "ENG-1", live.path))
	post := delivery.Post{Osascript: script(t, "osascript", `echo denied >&2; exit 1`), StateDir: state}

	err := post.NotifyOnce(context.Background(), ready)

	require.ErrorContains(t, err, "while notifying")
	assert.Contains(t, live.content(t), "ENG-1")
}
