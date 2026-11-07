package herdrtest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/herdr"
)

// RexServer is a Rex server of its own for the drift check, with its own socket, state directory
// and an empty config. It is the only Rex server the drift check touches: the maintainer's is
// never read, stopped or restarted, so every call names this server's socket.
type RexServer struct {
	// Socket is the path the scratch server listens on.
	Socket string
	// StateDir is where the server keeps its sessions across a restart.
	StateDir string
	// Env is the environment of the server, which every block it runs inherits.
	Env environment.Environment

	bin    string
	config string
	cmd    *exec.Cmd
}

// StartRexServer starts a scratch server and stops it when t ends, pass or fail. The server runs
// with the environment given, so a test can start it with the variables a worker must not inherit.
// A missing rex fails the test.
func StartRexServer(t testing.TB, env environment.Environment) *RexServer {
	t.Helper()
	bin, err := rexCommand()
	require.NoError(t, err)
	// A unix socket path is capped near 104 bytes on macOS, which t.TempDir() overruns.
	dir, err := os.MkdirTemp("/tmp", "rx")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	// driftdispatch reads the socket from here, so a dispatch run by a hook in a block reaches this server and not the maintainer's.
	env = env.With("DISPATCH_DRIFT_REX_SOCKET", socket)
	s := &RexServer{Socket: socket, StateDir: filepath.Join(dir, "state"), Env: env, bin: bin, config: filepath.Join(dir, "init.lua")}
	require.NoError(t, os.MkdirAll(s.StateDir, 0o700))
	require.NoError(t, os.WriteFile(s.config, nil, 0o600))
	s.start(t)
	t.Cleanup(s.kill)
	return s
}

func rexCommand() (string, error) {
	if path, err := exec.LookPath("rex"); err == nil {
		return path, nil
	}
	const app = "/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex"
	if _, err := os.Stat(app); err != nil {
		return "", err
	}
	return app, nil
}

func (s *RexServer) start(t testing.TB) {
	t.Helper()
	s.cmd = exec.Command(s.bin, "server", "run", "--listen", "unix://"+s.Socket, "--state-dir", s.StateDir, "--config", s.config)
	s.cmd.Env = append([]string{}, s.Env...)
	require.NoError(t, s.cmd.Start())
	require.Eventually(t, func() bool { return s.Run("api", "call", "server.status") == nil }, 20*time.Second, 50*time.Millisecond)
	// The test acts only through a server it proved to be its own.
	info, err := os.Stat(s.Socket)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSocket)
}

// Restart stops the scratch server and starts it again on the same state directory, which
// brings every block back as a shell with the same id, label and window.
func (s *RexServer) Restart(t testing.TB) {
	t.Helper()
	s.kill()
	_ = os.Remove(s.Socket)
	s.start(t)
}

// Stop stops the scratch server and leaves it stopped.
func (s *RexServer) Stop(t testing.TB) {
	t.Helper()
	s.kill()
	_ = os.Remove(s.Socket)
}

func (s *RexServer) kill() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
	s.cmd = nil
}

// Run runs a rex command against the scratch server, passing its socket and forbidding autostart.
func (s *RexServer) Run(args ...string) error {
	_, err := s.Output(args...)
	return err
}

// Output runs a rex command against the scratch server and returns its output.
func (s *RexServer) Output(args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), s.bin, append([]string{"-S", s.Socket, "--autostart=false"}, args...)...)
	cmd.Env = append([]string{}, s.Env...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// Type types text into the block at pane ("<session>/<block>") and presses Enter, as a person
// does. Production never types a nudge, so only a test that has to put a worker in a state does.
func (s *RexServer) Type(t testing.TB, pane, text string) {
	t.Helper()
	session, block, _ := strings.Cut(pane, "/")
	for _, data := range []string{text, "\r"} {
		body, err := json.Marshal(map[string]any{"session_id": session, "block_id": block, "method": "write", "args": map[string]any{"data": data}})
		require.NoError(t, err)
		require.NoError(t, s.Run("api", "call", "-s", session, "block.method", string(body)))
	}
}

// Rex is the real Rex adapter pointed at the scratch server.
func (s *RexServer) Rex(stateDir string) *herdr.Rex {
	return herdr.NewRex(stateDir).WithSocket(s.Socket).WithEnv(s.Env)
}

// CleanEnv is the process's environment without the Herdr, Rex and Claude variables a scratch
// server must not hand its blocks, so a run does not depend on the terminal that launched it.
func CleanEnv() environment.Environment {
	var kept environment.Environment
	for _, kv := range environment.Process() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, "HC_") && !strings.HasPrefix(name, "HERDR_") && !strings.HasPrefix(name, "REX_") && !strings.HasPrefix(name, "CLAUDE_CODE_") && !slices.Contains([]string{"CLAUDECODE", "CLAUDE_EFFORT", "CLAUDE_PID"}, name) {
			kept = append(kept, kv)
		}
	}
	return kept
}
