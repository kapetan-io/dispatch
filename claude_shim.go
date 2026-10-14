package dispatch

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

// checkFlag lets the shim script ask whether the dispatch on PATH is new enough
// to have claude-shim, since a git pull updates the script before go install
// updates the binary.
const checkFlag = "--dispatch-check"

// claudeShim runs the real claude with the plugin and worker flags added. herdr
// restarts a worker with a bare `claude --resume` and does not replay argv, so
// the shim in scripts/shims re-adds them. Print-mode runs pass through
// untouched. It never adds --name, because Claude draws the name below an open
// question and herdr then misses the question.
func claudeShim(args []string) error {
	if len(args) == 1 && args[0] == checkFlag {
		return nil
	}
	target, err := realClaude(filepath.SplitList(os.Getenv("PATH")))
	if err != nil {
		return err
	}
	return syscall.Exec(target, append([]string{target}, shimArgs(args, os.Getenv)...), os.Environ())
}

func shimArgs(args []string, getenv func(string) string) []string {
	if slices.Contains(args, "-p") || slices.Contains(args, "--print") {
		return args
	}
	out := slices.Clone(args)
	if !slices.Contains(args, "--plugin-dir") && getenv("DISPATCH_PLUGIN_ROOT") != "" {
		out = append(out, "--plugin-dir", getenv("DISPATCH_PLUGIN_ROOT"))
	}
	if getenv("AUTONOMOUS_WORKER") == "1" {
		if !slices.Contains(args, "--disallowedTools") && !slices.Contains(args, "--disallowed-tools") {
			out = append(out, "--disallowedTools", "EnterPlanMode,ExitPlanMode")
		}
		if !slices.Contains(args, "--settings") {
			out = append(out, "--settings", herdr.WorkerSettings)
		}
		if !slices.Contains(args, "--dangerously-skip-permissions") {
			out = append(out, "--dangerously-skip-permissions")
		}
	}
	return out
}

// realClaude is the first claude on path outside a scripts/shims directory. Two
// shims that each skip only their own directory exec each other forever when
// PATH holds both.
func realClaude(path []string) (string, error) {
	for _, dir := range path {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, "claude")
		info, err := os.Stat(cand)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		// A symlink to a shim directory hides the directory's name.
		if resolved, err := filepath.EvalSymlinks(cand); err == nil && isShimDir(filepath.Dir(resolved)) {
			continue
		}
		return cand, nil
	}
	return "", errors.New("no real claude found on PATH")
}

func isShimDir(dir string) bool {
	clean := filepath.ToSlash(filepath.Clean(dir))
	return clean == "scripts/shims" || strings.HasSuffix(clean, "/scripts/shims")
}
