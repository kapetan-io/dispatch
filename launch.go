package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/launcher"
)

// launchWorker implements `dispatch launch`. It prints one JSON result on
// stdout, status first, and fails unless a worker is running. Statuses are
// listed in the launcher package.
func launchWorker(ctx context.Context, opts Options, args []string) error {
	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	base := fs.String("base", "", "ref a new branch starts from (default the repo's default branch)")
	model := fs.String("model", "", "Claude model the worker runs (default sonnet)")
	kickoff := fs.String("kickoff", "", "first command typed into the worker (default /dispatch:build <TICKET>)")
	title := fs.String("title", "", "ticket title; the worker's tab is labeled \"<TICKET> <title>\" (default the ticket alone)")
	fs.Bool("no-focus", true, "accepted for compatibility; launches never take focus")

	// flag stops at the first argument that is not a flag, so keep parsing
	// after each positional to allow flags on either side of the ticket and branch.
	var positional []string
	var res launcher.Result
	for {
		if err := fs.Parse(args); err != nil {
			res = launcher.Result{Status: launcher.StatusUsageError, Detail: err.Error()}
			break
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	switch {
	case res.Status != "":
	case len(positional) > 2:
		res = launcher.Result{Status: launcher.StatusUsageError, Detail: "unknown argument: " + positional[2]}
	default:
		req := launcher.Request{RepoDir: opts.Dir, Base: *base, Title: *title, Model: *model, Kickoff: *kickoff}
		if len(positional) > 0 {
			req.Ticket = positional[0]
		}
		if len(positional) > 1 {
			req.Branch = positional[1]
		}
		root, err := pluginRoot(opts)
		if err != nil && opts.Launcher == nil {
			res = launcher.Result{Status: launcher.StatusUsageError, Detail: err.Error()}
		} else {
			// A launch run by hand shows its progress. Launches the loop starts stay quiet.
			if cli, ok := opts.Herdr.(*herdr.CLI); ok {
				cli.WithProgress(opts.Stderr)
			}
			res = launcherFor(opts, root, opts.Stderr).Launch(ctx, req)
		}
	}
	enc := json.NewEncoder(opts.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		return err
	}
	if !res.Succeeded() {
		return errors.New("launch " + res.Status)
	}
	return nil
}

// pluginRoot is where the plugin's scripts/shims live. The bin/dispatch-launch
// wrapper names it, since it knows where it sits, and otherwise it comes from
// the config.
func pluginRoot(opts Options) (string, error) {
	if root := os.Getenv("DISPATCH_PLUGIN_ROOT"); root != "" {
		return root, nil
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("no plugin root: set DISPATCH_PLUGIN_ROOT or plugin_root in the config: %w", err)
	}
	if cfg.Plugins.Herdr.PluginRoot == "" {
		return "", errors.New("no plugin root: set DISPATCH_PLUGIN_ROOT or plugin_root in the config")
	}
	return cfg.Plugins.Herdr.PluginRoot, nil
}

// launcherFor is the launcher a launch goes through, which is Options.Launcher
// when a test or caller set one. Progress goes to the writer given, and nil discards it.
func launcherFor(opts Options, root string, progress io.Writer) launcher.Launcher {
	if opts.Launcher != nil {
		return opts.Launcher
	}
	lo := launcher.Options{
		PluginRoot: root,
		Herdr:      opts.Herdr,
		Treehouse:  opts.Treehouse,
		Pace:       opts.LaunchPace,
		Stderr:     progress,
		StateDir:   opts.StateDir,
	}
	if opts.Clock != nil {
		lo.Now = opts.Clock.Now
	}
	// DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT=<seconds> bounds the wait for the herdr
	// server. Zero fails at once, so it becomes the smallest nonzero wait.
	if secs, err := strconv.Atoi(os.Getenv("DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT")); err == nil {
		lo.PreflightTimeout = max(time.Duration(secs)*time.Second, time.Nanosecond)
	}
	return launcher.New(lo)
}
