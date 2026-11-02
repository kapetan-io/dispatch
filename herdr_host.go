package dispatch

import (
	"context"
	"io"
	"os"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
)

// herdrFor is the herdr that hosts the workers of host: Options.Herdr when a test or caller
// set one, and otherwise the real adapter for that host. progress, when given, receives the
// adapter's launch progress.
func herdrFor(opts Options, host string, progress io.Writer) herdr.Herdr {
	if opts.Herdr != nil {
		return opts.Herdr
	}
	// DISPATCH_LAUNCH_TRACE=<file> records every screen the kickoff step reads.
	trace := opts.Env.Get("DISPATCH_LAUNCH_TRACE")
	exe, _ := os.Executable()
	kinds := opts.Harnesses.AgentKinds()
	if host == config.HostApp {
		return herdr.NewCore("hc").WithKinds(kinds...).WithEnv(opts.Env).WithPace(opts.LaunchPace).
			WithTrace(trace).WithDispatchBin(exe).WithProgress(progress)
	}
	return herdr.NewCLI("herdr").WithKinds(kinds...).WithEnv(opts.Env).WithPace(opts.LaunchPace).
		WithTrace(trace).WithDispatchBin(exe).WithProgress(progress)
}

// withHerdr is opts with the herdr of the host cfg names, for the commands that drive or read workers.
func withHerdr(opts Options, cfg config.Config, progress io.Writer) Options {
	opts.Herdr = herdrFor(opts, cfg.Plugins.Herdr.Host, progress)
	return opts
}

// reportingHarness finds the harness of the session whose hook is running, from the session's
// own environment (HC_TERMINAL_ID, then HERDR_PANE_ID) and not from the config. The Herdr
// app's terminal comes first, because a herdr TUI started from an app terminal can hand its
// panes both variables. A session outside herdr has neither variable and is dropped before
// any herdr call, because the hook runs in every session on the machine.
func reportingHarness(ctx context.Context, opts Options) (harness.Harness, bool) {
	hosts := []struct{ variable, host string }{{"HC_TERMINAL_ID", config.HostApp}, {"HERDR_PANE_ID", config.HostTUI}}
	for _, h := range hosts {
		place := opts.Env.Get(h.variable)
		if place == "" {
			continue
		}
		agent, found, err := herdrFor(opts, h.host, nil).AgentAt(ctx, place)
		if err != nil || !found {
			continue
		}
		return opts.Harnesses.ForAgent(agent.Kind)
	}
	return nil, false
}
