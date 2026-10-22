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
// untouched, and so do claude's own subcommands (`claude plugin update`), which
// reject the session flags. Every interactive session accepts cross-session
// messages and loads the plugin from DISPATCH_PLUGIN_ROOT, or from the config's
// plugin_root when that is unset. It never adds --name, because Claude draws the
// name below an open question and herdr then misses the question.
func claudeShim(opts Options, args []string) error {
	if len(args) == 1 && args[0] == checkFlag {
		return nil
	}
	target, err := realClaude(filepath.SplitList(os.Getenv("PATH")))
	if err != nil {
		return err
	}
	worker := os.Getenv("AUTONOMOUS_WORKER") == "1"
	return syscall.Exec(target, append([]string{target}, shimArgs(args, shimPluginRoot(opts), worker)...), os.Environ())
}

// claudeSubcommands are the names `claude --help` lists under Commands.
var claudeSubcommands = []string{
	"agents", "attach", "auth", "auto-mode", "doctor", "gateway", "import", "install", "kill",
	"logs", "mcp", "plugin", "plugins", "purge", "respawn", "rm", "setup-token", "stop",
	"ultrareview", "update", "upgrade",
}

// shimPluginRoot is empty when no checkout is named or the config does not load,
// so a plain claude still starts.
func shimPluginRoot(opts Options) string {
	root, _ := pluginRoot(opts)
	return root
}

// hasSubcommand reports whether claude will run one of its subcommands, which it
// does when the first word that is not an option names one, so
// `claude --model sonnet plugin list` is a subcommand run.
func hasSubcommand(args []string) bool {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return slices.Contains(claudeSubcommands, arg)
		}
	}
	return false
}

// hasFlag matches both `--name value` and `--name=value`.
func hasFlag(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

func shimArgs(args []string, root string, worker bool) []string {
	if slices.Contains(args, "-p") || slices.Contains(args, "--print") || hasSubcommand(args) {
		return args
	}
	out := slices.Clone(args)
	if !hasFlag(args, "--plugin-dir") && root != "" {
		out = append(out, "--plugin-dir", root)
	}
	if worker && !hasFlag(args, "--disallowedTools", "--disallowed-tools") {
		out = append(out, "--disallowedTools", "EnterPlanMode,ExitPlanMode")
	}
	if !hasFlag(args, "--settings") {
		out = append(out, "--settings", herdr.SessionSettings)
	}
	if worker && !slices.Contains(args, "--dangerously-skip-permissions") {
		out = append(out, "--dangerously-skip-permissions")
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
