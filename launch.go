package dispatch

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/kapetan-io/dispatch/internal/launcher"
	"github.com/kapetan-io/dispatch/internal/workers"
)

// pluginRoot is where the plugin's scripts/shims live: DISPATCH_PLUGIN_ROOT when the
// worker's environment names it, and otherwise the config.
func pluginRoot(opts Options) (string, error) {
	if root := opts.Env.Get("DISPATCH_PLUGIN_ROOT"); root != "" {
		return root, nil
	}
	cfg, err := loadConfig(opts)
	if err != nil {
		return "", fmt.Errorf("no plugin root: set DISPATCH_PLUGIN_ROOT or plugin_root in the config: %w", err)
	}
	if cfg.Plugins.PluginRoot() == "" {
		return "", errors.New("no plugin root: set DISPATCH_PLUGIN_ROOT or plugin_root in the config")
	}
	return cfg.Plugins.PluginRoot(), nil
}

// launcherFor is the launcher a launch action goes through, which is Options.Launcher
// when a test or caller set one. It writes the worker row in rows before each step. typed is told
// the kickoff, so the prompt hook can tell it from a person's message.
func launcherFor(opts Options, root string, rows *workers.Store, typed launcher.Typing) launcher.Launcher {
	if opts.Launcher != nil {
		return opts.Launcher
	}
	lo := launcher.Options{
		PluginRoot:  root,
		Multiplexer: opts.Multiplexer,
		Treehouse:   opts.Treehouse,
		Pace:        opts.LaunchPace,
		Workers:     rows,
		Typed:       typed,
	}
	lo.Clock, lo.Env = opts.Clock, opts.Env
	// DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT=<seconds> bounds the wait for the multiplexer
	// server. Zero fails at once, so it becomes the smallest nonzero wait.
	if secs, err := strconv.Atoi(opts.Env.Get("DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT")); err == nil {
		lo.PreflightTimeout = max(time.Duration(secs)*time.Second, time.Nanosecond)
	}
	return launcher.New(lo)
}
