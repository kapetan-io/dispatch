// Command driftdispatch is dispatch with its config file and state directory named by
// DISPATCH_DRIFT_CONFIG and DISPATCH_DRIFT_STATE, so a drift test can put it on a real Claude's PATH
// as the dispatch its plugin hooks run without the hooks reading or writing the maintainer's own.
// The worker's shell may put another dispatch ahead on its PATH, so when the claude shim starts
// Claude through this program, the program's own directory goes first.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/kapetan-io/dispatch"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "claude-shim" {
		if exe, err := os.Executable(); err == nil {
			_ = os.Setenv("PATH", filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
		// A test run from inside a Claude session passes down its child-session marker, which turns
		// the worker's transcript off.
		_ = os.Unsetenv("CLAUDE_CODE_CHILD_SESSION")
		// A scratch herdr-core hands its terminals scratch XDG directories, and Claude's installer
		// would re-point the maintainer's ~/.local/bin/claude into the data one. The runtime and
		// config directories stay scratch, because hc finds its core through them.
		if os.Getenv("DISPATCH_DRIFT_RESTORE_XDG") == "1" {
			for _, name := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
				if value, ok := os.LookupEnv("DISPATCH_DRIFT_REAL_" + name); ok {
					_ = os.Setenv(name, value)
				} else {
					_ = os.Unsetenv(name)
				}
			}
		}
		// The hooks inherit this environment, so it shows which core they reach.
		if trace := os.Getenv("DISPATCH_DRIFT_SHIM_TRACE"); trace != "" {
			var lines string
			for _, name := range []string{"HC_SOCKET", "HC_SESSION", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME"} {
				lines += name + "=" + os.Getenv(name) + "\n"
			}
			_ = os.WriteFile(trace, []byte(lines), 0o644)
		}
	}
	opts := dispatch.Options{ConfigPath: os.Getenv("DISPATCH_DRIFT_CONFIG"), StateDir: os.Getenv("DISPATCH_DRIFT_STATE")}
	if err := dispatch.Run(ctx, os.Args[1:], opts); err != nil {
		fmt.Fprintln(os.Stderr, "dispatch:", err)
		os.Exit(dispatch.ExitCode(err))
	}
}
