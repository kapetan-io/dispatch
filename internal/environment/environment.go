// Package environment is the variables a command runs with, so dispatch reads and hands on
// the caller's values instead of the process's.
package environment

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Environment holds NAME=value entries in the form os.Environ returns. When a name appears twice
// the last entry wins, as it does for a child process. The nil Environment is the process's own,
// read when it is used, so a zero value follows the process the way os.Getenv does. An empty one
// that isn't nil has no variables.
type Environment []string

func (e Environment) entries() []string {
	if e == nil {
		return os.Environ()
	}
	return e
}

// Process is the environment of the running process.
func Process() Environment { return os.Environ() }

// Get returns the value of name, or "" when it is unset.
func (e Environment) Get(name string) string {
	all := e.entries()
	for i := len(all) - 1; i >= 0; i-- {
		if key, value, ok := strings.Cut(all[i], "="); ok && key == name {
			return value
		}
	}
	return ""
}

// With returns a copy of e that sets name to value.
func (e Environment) With(name, value string) Environment {
	return append(append(Environment{}, e.entries()...), name+"="+value)
}

// Without returns a copy of e that leaves out every name in names.
func (e Environment) Without(names ...string) Environment {
	kept := Environment{}
	for _, kv := range e.entries() {
		key, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, name := range names {
			drop = drop || key == name
		}
		if !drop {
			kept = append(kept, kv)
		}
	}
	return kept
}

// ExpandHome replaces a leading ~ with the HOME of e. A path stays as it is when e has no HOME.
func (e Environment) ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home := e.Get("HOME")
	if home == "" {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// LookPath finds the executable name on the PATH of e. Like exec.LookPath, it refuses a match
// that is relative to the working directory, such as one found through an empty PATH entry.
func (e Environment) LookPath(name string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		if !executable(name) {
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		}
		return name, nil
	}
	for _, dir := range filepath.SplitList(e.Get("PATH")) {
		if dir == "" {
			dir = "."
		}
		path := filepath.Join(dir, name)
		if !executable(path) {
			continue
		}
		if !filepath.IsAbs(path) {
			return "", &exec.Error{Name: name, Err: exec.ErrDot}
		}
		return path, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// Command is exec.CommandContext with name found on the PATH of e and e as the child's environment.
func (e Environment) Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append([]string{}, e.entries()...)
	path, err := e.LookPath(name)
	if err != nil {
		cmd.Err = err
		return cmd
	}
	cmd.Path, cmd.Err = path, nil
	return cmd
}
