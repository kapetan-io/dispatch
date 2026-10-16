// Package dispatch is the dispatcher. `dispatch run` starts a reconcile loop in
// the foreground, in a herdr window the user watches. Events wake it, and each
// cycle runs the TypeScript rules over what Linear, herdr, treehouse and GitHub
// say right now and over what dispatch has already done. A resync every few
// minutes re-reads everything as a backstop, so a failure or a missed event is
// retried.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/delivery"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/launcher"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// RuleTimeout bounds how long one call of a rule's if and then may run.
type RuleTimeout struct{ If, Then time.Duration }

// Options points the dispatcher at its config, its state and the tools it
// drives. Zero values take the defaults.
type Options struct {
	// ConfigPath defaults to ~/.config/dispatch/config.toml.
	ConfigPath string
	// StateDir holds the lock, the loop's socket and the Stop records. Defaults to
	// ~/.local/state/dispatch. Keep it short, because macOS caps a socket path at
	// about 104 bytes.
	StateDir string
	// Linear opens the Linear workspace for an API key. Defaults to Linear's
	// GraphQL API.
	Linear linear.Connect
	// LinearKey overrides LINEAR_API_KEY, the config's api_key_command and its keychain entry.
	LinearKey string
	// KeyCommandTimeout cuts off an api_key_command that never returns, such as a
	// password manager waiting on an unlock prompt. Defaults to 1 minute.
	KeyCommandTimeout time.Duration
	// Herdr is the workspace manager that hosts the workers. Defaults to the installed herdr.
	Herdr herdr.Herdr
	// Treehouse is the pool of worktrees. Defaults to the installed treehouse.
	Treehouse treehouse.Pool
	// PullRequests is the GitHub repo's pull requests. Defaults to the installed
	// gh, which cuts each call off after 30 seconds so a stalled network can't
	// hold up the loop.
	PullRequests pullrequest.Repository
	// Alerts shows notifications. Defaults to macOS notifications through osascript.
	Alerts delivery.Alerts
	// Launcher starts the workers. Defaults to leasing a tree from Treehouse and
	// starting the worker in Herdr.
	Launcher launcher.Launcher
	// Dir is where `dispatch launch` looks for the target repo. Defaults to the
	// working directory.
	Dir string
	// LaunchPace is the pause between launcher polls of herdr. Defaults to 1 second.
	LaunchPace time.Duration
	// RulesDir overrides [dispatch].rules, so tests run the repo's rules/ directory.
	RulesDir string
	// RuleTimeout cuts off a rule call on the wall clock. Zero means 1 second for
	// an if and 30 seconds for a then.
	RuleTimeout RuleTimeout
	// LaunchTimeout is how long a launch may take before it is given up on, 25 seconds by default,
	// and ActionTimeout how long every other action may take, 1 minute by default.
	LaunchTimeout, ActionTimeout time.Duration
	// Clock owns every timer the loop sets, the resync and history rotation, and
	// stamps ingest times. Defaults to the wall clock.
	Clock  Clock
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

const usage = `usage: dispatch run
       dispatch tick [ticket] [--timeout DURATION]
       dispatch plan
       dispatch trace <ticket>
       dispatch replay [--from TIME] [--to TIME] [--rules DIR]
       dispatch state reset [--quiet]
       dispatch tell <ticket> "<text>"
       dispatch rules init|check|test [dir]
       dispatch ticket-hint
       dispatch repo-settings
       dispatch linear-viewer
       dispatch launch <ticket> <branch> [--base REF] [--model NAME] [--kickoff TEXT] [--title TEXT]
       dispatch claude-shim [CLAUDE ARGS]
       dispatch record-stop
       dispatch watch <ticket>
       dispatch unwatch <ticket>

  run          start the reconcile loop and run until interrupted (Ctrl-C). Holds the state
               directory's lock, so a second run fails
  tick         ask the running loop to re-read every source now and wait until it has settled,
               then print one line per ticket. Exits 3 with "dispatch: not running" when no loop runs
  --timeout    how long tick waits before it reports what is still pending; defaults to 2m
  plan         print what the latest cycle acts on, waits for, holds and has covered, read from history
  trace        print why dispatch acted on the ticket, read from history: each cycle that changed what a
               rule wanted for it with what the rule read, each hold, wait, throw and dropped event,
               and each receipt. Exits 1 when kept history has no record of the ticket
  replay       run the cycles in a stretch of history again over the world and ledger they ran over,
               and print "N cycles, N match" and exit 0, or print the first difference and exit 1.
               TIME is RFC 3339, "2006-01-02 15:04" or "15:04" today. With --rules, run the rules in
               DIR over the same history, print the first action that differs from the live run and
               exit 0. Exits 2 for history written before it recorded the world
  state reset  start a new history segment with an empty ledger, keeping what the world remembers.
               With --quiet, record the messages the first cycle wants as sent, so none is repeated.
               Exits 1 while dispatch run holds the lock
  tell         type text into the ticket's worker through the running loop; fails when the worker is
               blocked or absent
  rules        init writes the shipped TypeScript rules, check lints them and prints what each wants
               now, and test runs their *.test.ts scenarios
  ticket-hint  print a reminder to use the dispatch:ticket skill when run in a repo listed under
               [repos] in the config, or a worktree of one; prints nothing elsewhere.
               The plugin's SessionStart hook runs it
  repo-settings  print the [repos.<label>] table of the configured repo containing the working
               directory, every setting at its effective value, in a form the config accepts. Fails
               elsewhere
  linear-viewer  print the id and name of the Linear user who owns dispatch's API key, as one JSON
               object, found the way run finds the key
  launch       lease a tree, open a herdr workspace and start a worker typed --kickoff, which
               defaults to "/dispatch:build <ticket>". Run it from inside the repo. Prints one
               JSON result and exits 0 only when a worker is running
  claude-shim  run the real claude with the plugin and worker flags added. The claude shim in
               scripts/shims runs it
  record-stop  read a Stop hook's JSON on stdin and record when the worker in this tree last ended a
               turn and whether it still has background tasks running; does nothing outside a
               worktree of a repo listed under [repos]. The plugin's Stop hook runs it
  watch        make this Claude Code session follow the ticket, so each notice dispatch sends about
               it is also sent to this session as a message. A session can follow several
               tickets, and a ticket can have several followers. Fails outside a Claude Code
               session or when the socket path is not absolute
  unwatch      stop this session following the ticket`

// Run executes one dispatch command. main is a thin wrapper around it.
func Run(ctx context.Context, args []string, opts Options) error {
	opts = withDefaults(opts)
	if len(args) == 0 {
		_, _ = fmt.Fprintln(opts.Stderr, usage)
		return errors.New("no command given")
	}
	switch args[0] {
	case "tick":
		return tickCommand(ctx, opts, args[1:])
	case "plan":
		return planCommand(opts)
	case "trace":
		if len(args) != 2 {
			_, _ = fmt.Fprintln(opts.Stderr, usage)
			return errors.New("trace takes a ticket")
		}
		return traceCommand(opts, args[1])
	case "replay":
		return replayCommand(opts, args[1:])
	case "state":
		return stateCommand(ctx, opts, args[1:])
	case "rules":
		return rulesCommand(ctx, opts, args[1:])
	case "tell":
		if len(args) != 3 {
			_, _ = fmt.Fprintln(opts.Stderr, usage)
			return errors.New(`tell takes a ticket and the text to type`)
		}
		return tellCommand(ctx, opts, args[1], args[2])
	case "ticket-hint":
		return ticketHint(ctx, opts)
	case "repo-settings":
		return repoSettings(ctx, opts)
	case "linear-viewer":
		return linearViewer(ctx, opts)
	case "launch":
		return launchWorker(ctx, opts, args[1:])
	case "claude-shim":
		return claudeShim(args[1:])
	case "record-stop":
		return recordStop(ctx, opts)
	case "watch", "unwatch":
		if len(args) != 2 {
			_, _ = fmt.Fprintln(opts.Stderr, usage)
			return fmt.Errorf("%s takes a ticket", args[0])
		}
		return follow(opts, args[0] == "watch", strings.ToUpper(args[1]))
	case "run":
		if len(args) != 1 {
			_, _ = fmt.Fprintln(opts.Stderr, usage)
			return errors.New("run takes no arguments. The resync interval is [dispatch].resync in the config")
		}
		return runCommand(ctx, opts)
	default:
		_, _ = fmt.Fprintln(opts.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func withDefaults(o Options) Options {
	if o.ConfigPath == "" {
		o.ConfigPath = config.ExpandHome("~/.config/dispatch/config.toml")
	}
	if o.StateDir == "" {
		o.StateDir = config.ExpandHome("~/.local/state/dispatch")
	}
	if o.Linear == nil {
		o.Linear = func(key string) linear.Workspace { return linear.NewClient(linear.DefaultURL, key) }
	}
	if o.Treehouse == nil {
		o.Treehouse = treehouse.NewCLI("treehouse")
	}
	if o.PullRequests == nil {
		o.PullRequests = pullrequest.NewCLI("gh")
	}
	if o.Alerts == nil {
		o.Alerts = delivery.NewNotificationCenter("osascript")
	}
	if o.Clock == nil {
		o.Clock = loop.WallClock{}
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Herdr == nil {
		// DISPATCH_LAUNCH_TRACE=<file> records every screen the kickoff step reads.
		o.Herdr = herdr.NewCLI("herdr").WithPace(o.LaunchPace).WithTrace(os.Getenv("DISPATCH_LAUNCH_TRACE"))
	}
	return o
}

// keyLookup finds the Linear API key. A key printed by api_key_command is kept
// for the life of the process, so a password manager that prompts to unlock
// asks once per `dispatch run` instead of on every read.
type keyLookup struct {
	fromCommand string
}

// find tries, in order, Options.LinearKey, LINEAR_API_KEY, the config's
// api_key_command, and the keychain entry named in the config.
func (k *keyLookup) find(ctx context.Context, opts Options, cfg config.Config) (string, error) {
	if opts.LinearKey != "" {
		return opts.LinearKey, nil
	}
	if env := os.Getenv("LINEAR_API_KEY"); env != "" {
		return env, nil
	}
	if cmd := cfg.Plugins.Linear.APIKeyCommand; cmd != "" {
		if k.fromCommand == "" {
			key, err := keyFromCommand(ctx, opts, cmd)
			if err != nil {
				return "", err
			}
			k.fromCommand = key
		}
		return k.fromCommand, nil
	}
	out, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", cfg.Plugins.Linear.APIKeyKeychain, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("no Linear API key: LINEAR_API_KEY is unset and the keychain has no %q entry", cfg.Plugins.Linear.APIKeyKeychain)
	}
	return strings.TrimSpace(string(out)), nil
}

// keyFromCommand runs command in a shell. Its stderr goes to the user so an
// unlock prompt is visible, and its stdout is never echoed into an error.
func keyFromCommand(ctx context.Context, opts Options, command string) (string, error) {
	timeout := opts.KeyCommandTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stderr = opts.Stderr
	// A process the command started can hold stdout open after sh is killed.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("no Linear API key: api_key_command %q timed out after %s", command, timeout)
	}
	if err != nil {
		return "", fmt.Errorf("no Linear API key: api_key_command %q failed: %w", command, err)
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", fmt.Errorf("no Linear API key: api_key_command %q printed nothing (exit status 0)", command)
	}
	return key, nil
}

// linearViewer prints the owner of the API key, so skills can assign tickets
// without handling the key.
func linearViewer(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	key, err := (&keyLookup{}).find(ctx, opts, cfg)
	if err != nil {
		return err
	}
	viewer, err := opts.Linear(key).Viewer(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(opts.Stdout).Encode(viewer)
}

// follow makes the calling Claude Code session start or stop following a ticket.
func follow(opts Options, watch bool, ticket string) error {
	if !ticketID.MatchString(ticket) {
		return fmt.Errorf("%q is not a ticket identifier such as ENG-123", ticket)
	}
	socket := os.Getenv(messagingSocketEnv)
	if socket == "" {
		return fmt.Errorf("%s is not set, so this is not a Claude Code session; run watch from one", messagingSocketEnv)
	}
	if !watch {
		if err := delivery.Unwatch(opts.StateDir, ticket, socket); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(opts.Stdout, "unwatching %s\n", ticket)
		return nil
	}
	if err := delivery.Watch(opts.StateDir, ticket, socket); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(opts.Stdout, "watching %s\n", ticket)
	return nil
}

// ticketID is the shape of a Linear identifier.
var ticketID = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

// messagingSocketEnv names the variable Claude Code sets to a session's inbox socket.
const messagingSocketEnv = "CLAUDE_CODE_MESSAGING_SOCKET"
