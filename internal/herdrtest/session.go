package herdrtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

// sessionPrefix starts the name of every throwaway session, so a maintainer's
// own sessions are never mistaken for one.
const sessionPrefix = "dispatch-drift-"

// Session is a throwaway headless herdr session for the drift check. It is the
// only herdr the drift check touches, so the maintainer's default session is
// never read or changed.
type Session struct {
	// Name is the session's name, always starting with dispatch-drift-.
	Name string
	bin  string
}

// StartSession starts a headless herdr session of its own and deletes it when
// t ends, pass or fail. It first deletes the sessions a crashed run left
// behind, which are the ones named for a process that no longer runs. A missing
// herdr fails the test.
func StartSession(t testing.TB) *Session {
	t.Helper()
	bin, err := exec.LookPath("herdr")
	require.NoError(t, err)
	require.NoError(t, removeStale(bin))

	s := &Session{Name: fmt.Sprintf("%s%d-%d", sessionPrefix, os.Getpid(), time.Now().UnixNano()), bin: bin}
	server := exec.Command(bin, "--session", s.Name, "server")
	require.NoError(t, server.Start())
	t.Cleanup(func() {
		_ = exec.Command(bin, "session", "stop", s.Name).Run()
		_ = exec.Command(bin, "session", "delete", s.Name).Run()
		_ = server.Process.Kill()
		_ = server.Wait()
	})

	require.Eventually(t, func() bool { return s.Run("workspace", "list") == nil }, 20*time.Second, 100*time.Millisecond)
	return s
}

// CLI is the real herdr adapter pointed at the session.
func (s *Session) CLI() *herdr.CLI { return herdr.NewCLI(s.bin, "--session", s.Name) }

// Output runs a herdr command in the session and returns its output.
func (s *Session) Output(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, s.bin, append([]string{"--session", s.Name}, args...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("while running herdr %v in %s: %w", args, s.Name, err)
	}
	return out, nil
}

// Run is Output for a command whose output does not matter.
func (s *Session) Run(args ...string) error {
	_, err := s.Output(context.Background(), args...)
	return err
}

// Leftovers lists the throwaway sessions that herdr still lists for process
// pid. A herdr that can't list its sessions is an error, not an empty answer.
func Leftovers(pid int) ([]string, error) {
	bin, err := exec.LookPath("herdr")
	if err != nil {
		return nil, err
	}
	names, err := sessions(bin)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, name := range names {
		if strings.HasPrefix(name, fmt.Sprintf("%s%d-", sessionPrefix, pid)) {
			found = append(found, name)
		}
	}
	return found, nil
}

func removeStale(bin string) error {
	names, err := sessions(bin)
	if err != nil {
		return err
	}
	for _, name := range names {
		pid, _, _ := strings.Cut(strings.TrimPrefix(name, sessionPrefix), "-")
		// EPERM means the process exists and belongs to another user.
		if n, err := strconv.Atoi(pid); err == nil {
			if err := syscall.Kill(n, 0); err == nil || errors.Is(err, syscall.EPERM) {
				continue
			}
		}
		_ = exec.Command(bin, "session", "stop", name).Run()
		_ = exec.Command(bin, "session", "delete", name).Run()
	}
	return nil
}

// sessions lists the throwaway sessions herdr knows of. A failed listing is an
// error, because an empty answer would let a leftover session pass unseen.
func sessions(bin string) ([]string, error) {
	out, err := exec.Command(bin, "session", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("while listing herdr sessions: %w", err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && strings.HasPrefix(fields[0], sessionPrefix) {
			names = append(names, fields[0])
		}
	}
	return names, nil
}
