package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// appRex is where the Rex app keeps the rex command, which is on PATH only inside Rex.
const appRex = "/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex"

// exitNotRunning is the exit code of rex when no server answers and --autostart=false forbids starting one.
const exitNotRunning = 4

// exitNotFound is the exit code of rex when a session target does not exist. Unlike a missing
// block, that answer carries no `(status=N code=C)` tail.
const exitNotFound = 3

// ErrRexNotRunning means no Rex server answered. Dispatch never starts one.
var ErrRexNotRunning = errors.New("Rex is not running, and dispatch never starts it: open Rex")

var refusalTail = regexp.MustCompile(`\(status=(\d+) code=([a-z_]+)\)\s*$`)

// find is the rex command, found on PATH first and then in the Rex app.
func (r *Rex) find() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bin != "" {
		return r.bin, nil
	}
	for _, candidate := range []string{"rex", appRex} {
		if path, err := r.env.LookPath(candidate); err == nil {
			r.bin = path
			return path, nil
		}
	}
	return "", fmt.Errorf("no rex command on PATH or at %s", appRex)
}

// api calls one method of the Rex server's API. A non-empty session attaches the connection to
// it first, as session.destroy and the block methods need. Every call passes --autostart=false.
func (r *Rex) api(ctx context.Context, session, method string, args, out any) error {
	bin, err := r.find()
	if err != nil {
		return err
	}
	argv := []string{"--autostart=false"}
	if r.socket != "" {
		argv = append([]string{"-S", r.socket}, argv...)
	}
	argv = append(argv, "api", "call")
	if session != "" {
		argv = append(argv, "-s", session)
	}
	argv = append(argv, method)
	if args != nil {
		body, err := json.Marshal(args)
		if err != nil {
			return err
		}
		argv = append(argv, string(body))
	}
	cmd := r.env.Command(ctx, bin, argv...)
	// A rex wrapped in a script leaves a child holding stdout after the timeout kills the script.
	cmd.WaitDelay = time.Second
	stdout, err := cmd.Output()
	if err != nil {
		return rexFailure(method, err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return fmt.Errorf("while reading rex %s output: %w", method, err)
	}
	return nil
}

// rexFailure maps a failed rex run to the port's errors: ErrRexNotRunning for exit 4, a
// *Refusal when Rex says why it refused, and otherwise what rex printed.
func rexFailure(method string, err error) error {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return fmt.Errorf("while running rex %s: %w", method, err)
	}
	text := strings.TrimSpace(string(bytes.TrimSpace(exit.Stderr)))
	if exit.ExitCode() == exitNotRunning {
		return ErrRexNotRunning
	}
	if exit.ExitCode() == exitNotFound {
		return &Refusal{Code: "not_found", Message: strings.TrimPrefix(text, "Error: "), Command: "rex:" + method}
	}
	if m := refusalTail.FindStringSubmatch(text); m != nil {
		return &Refusal{Code: m[2], Message: strings.TrimPrefix(text, "Error: "), Command: "rex:" + method}
	}
	return fmt.Errorf("while running rex %s: %w: %s", method, err, text)
}

// rexGone reports whether err says the session, window or block is no longer there.
func rexGone(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && refusal.Code == "not_found"
}

// blockCall calls a method of a terminal block.
func (r *Rex) blockCall(ctx context.Context, session, block, method string, args, out any) error {
	return r.api(ctx, session, "block.method", map[string]any{"session_id": session, "block_id": block, "method": method, "args": orEmpty(args)}, out)
}

func orEmpty(args any) any {
	if args == nil {
		return map[string]any{}
	}
	return args
}
