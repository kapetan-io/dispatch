package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// SocketName is the loop's socket inside the state directory.
const SocketName = "dispatch.sock"

// maxSocketPath is what macOS allows for a unix socket path.
const maxSocketPath = 100

// SocketPath is where a loop over stateDir listens.
func SocketPath(stateDir string) string { return filepath.Join(stateDir, SocketName) }

type listener struct {
	l    net.Listener
	path string
	done chan struct{}
}

func listen(stateDir string, inbox chan<- any, stopped <-chan struct{}) (*listener, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("while creating state dir: %w", err)
	}
	path := SocketPath(stateDir)
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("the socket path %s is %d bytes, and macOS allows about %d. Use a shorter state directory", path, len(path), maxSocketPath)
	}
	// The lock on the state directory is held, so a socket file here is a dead one.
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("while listening on %s: %w", path, err)
	}
	_ = os.Chmod(path, 0o600)
	ln := &listener{l: l, path: path, done: make(chan struct{})}
	go func() {
		defer close(ln.done)
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go serve(conn, inbox, stopped)
		}
	}()
	return ln, nil
}

func (ln *listener) close() {
	_ = ln.l.Close()
	<-ln.done
	_ = os.Remove(ln.path)
}

func serve(conn net.Conn, inbox chan<- any, stopped <-chan struct{}) {
	defer func() { _ = conn.Close() }()
	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}
	reply := make(chan Response, 1)
	select {
	case inbox <- request{Request: req, reply: reply}:
	case <-stopped:
		_ = json.NewEncoder(conn).Encode(Response{Code: CodeFailed, Error: "dispatch is stopping"})
		return
	}
	// A loop that stops answers no one, so the client hears that instead of waiting.
	select {
	case resp := <-reply:
		_ = json.NewEncoder(conn).Encode(resp)
	case <-stopped:
		_ = json.NewEncoder(conn).Encode(Response{Code: CodeFailed, Error: "dispatch is stopping"})
	}
}

// ErrNotRunning means no loop answers on the state directory's socket.
var ErrNotRunning = errors.New("dispatch: not running")

// Ask sends one request to the loop over stateDir and waits for its answer, or
// until ctx ends. It returns ErrNotRunning when nothing listens.
func Ask(ctx context.Context, stateDir string, req Request) (Response, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", SocketPath(stateDir))
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, ErrNotRunning
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, fmt.Errorf("while reading the loop's answer: %w", err)
	}
	return resp, nil
}

// Wake asks the loop over stateDir to run a cycle and does not wait for it. A hook
// uses it, because a prompt must not wait on a cycle. It returns ErrNotRunning when
// nothing listens.
func Wake(ctx context.Context, stateDir string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", SocketPath(stateDir))
	if err != nil {
		return ErrNotRunning
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	return json.NewEncoder(conn).Encode(Request{Op: OpTick})
}
