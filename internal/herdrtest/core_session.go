package herdrtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/herdr"
)

// coreSocketPrefix starts the name of every scratch core's socket and session, so a
// maintainer's own core is never mistaken for one. macOS allows 103 bytes for a socket
// path, and a test temp dir's path is longer, so sockets live directly under /tmp.
const coreSocketPrefix = "dd"

var coreCount atomic.Int32

// CoreSession is a herdr-core of its own for the drift check, with its own session name,
// scratch XDG directories and a socket under /tmp. It is the only core the drift check
// touches: the maintainer's core is never read, stopped or restarted.
type CoreSession struct {
	// Socket is the path the scratch core listens on.
	Socket string
	Env    environment.Environment
	hc     string
}

// StartCoreSession starts a scratch core and stops it when t ends, pass or fail. It first
// removes the sockets a crashed run left behind. A missing hc fails the test.
func StartCoreSession(t testing.TB) *CoreSession {
	t.Helper()
	hc, err := exec.LookPath("hc")
	require.NoError(t, err)
	require.NoError(t, removeStaleCores())

	name := fmt.Sprintf("%s%d-%d", coreSocketPrefix, os.Getpid(), coreCount.Add(1))
	scratch := t.TempDir()
	env := environment.Environment{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HC_") {
			env = append(env, kv)
		}
	}
	socket := "/tmp/" + name + ".sock"
	for _, dir := range []string{"config", "data", "state", "cache", "run"} {
		require.NoError(t, os.MkdirAll(filepath.Join(scratch, dir), 0o700))
	}
	env = env.With("HC_SESSION", name).With("HC_SOCKET", socket).
		With("XDG_CONFIG_HOME", filepath.Join(scratch, "config")).With("XDG_DATA_HOME", filepath.Join(scratch, "data")).
		With("XDG_STATE_HOME", filepath.Join(scratch, "state")).With("XDG_CACHE_HOME", filepath.Join(scratch, "cache")).
		With("XDG_RUNTIME_DIR", filepath.Join(scratch, "run"))
	s := &CoreSession{Socket: socket, Env: env, hc: hc}
	t.Cleanup(func() {
		_ = s.command("server", "stop").Run()
		_ = os.Remove(socket)
	})

	out, err := s.command("server", "start").CombinedOutput()
	if err != nil {
		t.Log(string(out))
	}
	require.NoError(t, err)
	// The test acts only through a core it proved to be its own.
	info, err := os.Stat(socket)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSocket)
	return s
}

func (s *CoreSession) command(args ...string) *exec.Cmd {
	cmd := exec.Command(s.hc, args...)
	cmd.Env = append([]string{}, s.Env...)
	return cmd
}

// Run runs an hc command against the scratch core and returns its output.
func (s *CoreSession) Run(t testing.TB, args ...string) string {
	t.Helper()
	out, err := s.command(args...).CombinedOutput()
	if err != nil {
		t.Log(string(out))
	}
	require.NoError(t, err)
	return string(out)
}

// Type types text into the terminal at pane and presses Enter, as a person does. Production
// never types a nudge, so only a test that has to put a worker in a state does.
func (s *CoreSession) Type(t testing.TB, pane, text string) {
	t.Helper()
	s.Run(t, "send", pane, text, "--force")
}

// Restart stops the scratch core and starts it again, which ends every terminal in it with
// reason server_stop. It never touches any other core.
func (s *CoreSession) Restart(t testing.TB) {
	t.Helper()
	s.Run(t, "server", "stop")
	s.Run(t, "server", "start")
}

// Core is the real app adapter pointed at the scratch core. Its app command is true, so no
// test names or closes a space in the maintainer's app window.
func (s *CoreSession) Core() *herdr.Core {
	return herdr.NewCore(s.hc).WithEnv(s.Env).WithApp("/usr/bin/true")
}

// CoreLeftovers lists the sockets of scratch cores that process pid started and left behind.
func CoreLeftovers(pid int) ([]string, error) {
	matches, err := filepath.Glob(fmt.Sprintf("/tmp/%s%d-*.sock", coreSocketPrefix, pid))
	return matches, err
}

func removeStaleCores() error {
	matches, err := filepath.Glob("/tmp/" + coreSocketPrefix + "*.sock")
	if err != nil {
		return err
	}
	for _, m := range matches {
		base := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), coreSocketPrefix), ".sock")
		pid, _, _ := strings.Cut(base, "-")
		n, err := strconv.Atoi(pid)
		if err != nil {
			continue
		}
		// EPERM means the process exists and belongs to another user.
		if err := syscall.Kill(n, 0); err == nil || err == syscall.EPERM {
			continue
		}
		_ = os.Remove(m)
	}
	return nil
}
