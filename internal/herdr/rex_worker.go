package herdr

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/kapetan-io/dispatch/internal/treekey"
)

// heldBlock is a worker block of the tree StartWorker found.
type heldBlock struct {
	session string
	block   string
}

func (r *Rex) StartWorker(ctx context.Context, w Worker) (Started, error) {
	repo, _, ok := splitRexWorkspaceID(w.Workspace)
	if !ok {
		return Started{}, fmt.Errorf("workspace %q is not a Rex workspace id", w.Workspace)
	}
	program := w.Program
	exe, err := findExecutable(r.env, program)
	if err != nil {
		return Started{}, err
	}
	var started Started
	err = r.withStructure(ctx, func() error {
		var status struct {
			Instance string `json:"instance_id"`
		}
		if err := r.api(ctx, "", "server.status", nil, &status); err != nil {
			return err
		}
		if status.Instance == "" {
			return errors.New("rex server.status named no instance id, which the worker label needs")
		}
		instance := status.Instance
		if len(instance) > instanceLen {
			instance = instance[:instanceLen]
		}
		sessions, err := r.sessions(ctx)
		if err != nil {
			return err
		}
		stale, previous, err := r.clearTree(ctx, sessions, w.Dir, instance)
		if err != nil {
			return err
		}
		argv, session := program.StartArgv(), program.Session
		if resume, ok := program.ResumeArgv(previous); ok && !w.FreshLease {
			argv, session = resume, previous
		}
		label := workerLabel{Session: session, Instance: instance, Tree: w.Dir}
		if err := checkLabel(label); err != nil {
			return err
		}
		command := r.command(w, exe, argv)
		// The restored block's label is the only copy of the conversation to resume, so it
		// closes only once the new worker exists. A close that fails then leaves a shell for
		// Return to remove, and the launch has still started its worker.
		started, err = r.createWorker(ctx, sessions, repo, w, label, command)
		if err != nil {
			return err
		}
		for _, b := range stale {
			if _, err := r.closeBlock(ctx, b.session, b.block); err != nil {
				r.record("stale-close-failed", err.Error())
			}
		}
		return nil
	})
	return started, err
}

// clearTree checks the tree's worker blocks in every session. It fails with ErrSessionRunning
// when a Claude runs there, and with *TerminalWithoutAgent when a block of this Rex server
// runs none, because that block may be a racing launch still loading. It returns the blocks
// StartWorker closes before it starts a fresh worker: an exited program's block, and a block a
// Rex restart brought back as a shell. It also returns the harness session the restored block
// was running, which the caller resumes unless this launch took the lease fresh. An exited
// program's block never counts, because a Claude that exited was closed by the user or ended.
func (r *Rex) clearTree(ctx context.Context, sessions []rexSession, tree, instance string) ([]heldBlock, string, error) {
	var stale []heldBlock
	var previous string
	var loading *TerminalWithoutAgent
	for _, s := range sessions {
		for _, w := range s.Windows {
			blocks, labels := w.workers()
			for i, b := range blocks {
				if treekey.Of(labels[i].Tree) != treekey.Of(tree) {
					continue
				}
				proc, err := r.process(ctx, s.ID, b.ID)
				if rexGone(err) {
					continue
				}
				if err != nil {
					return nil, "", err
				}
				held := heldBlock{session: s.ID, block: b.ID}
				if _, running := r.kind(proc); running {
					return nil, "", ErrSessionRunning
				}
				switch {
				case proc.LastExit != nil:
					stale = append(stale, held)
				case labels[i].Instance != instance:
					stale = append(stale, held)
					if previous == "" {
						previous = labels[i].Session
					}
				case loading == nil:
					loading = &TerminalWithoutAgent{Address: rexPane(s.ID, b.ID)}
				}
			}
		}
	}
	if loading != nil {
		return nil, "", loading
	}
	return stale, previous, nil
}

// createWorker puts the worker's block in a new window of the repo session, creating the session
// when no session has the repo's label. The new window takes no focus.
func (r *Rex) createWorker(ctx context.Context, sessions []rexSession, repo string, w Worker, label workerLabel, command []string) (Started, error) {
	options := map[string]any{"shell": "none", "login": false, "cwd": w.Dir, "command": command}
	if len(w.Program.PathFirst) > 0 {
		options["path_prepend"] = w.Program.PathFirst
	}
	layout := map[string]any{"block": map[string]any{"flavor": terminalFlavor, "label": label.String(), "options": options}}
	windowLabel := clipLabel(w.Label)
	name := "dw-" + treekey.Digest("dispatch-terminal", w.Dir, 24)
	if existing, ok := repoSession(sessions, repo); ok {
		var made struct {
			Blocks []string `json:"block_ids"`
		}
		err := r.api(ctx, existing.ID, "session.new_window", map[string]any{"session_id": existing.ID, "window_label": windowLabel, "focus": false, "layout": layout}, &made)
		if err != nil {
			return Started{}, err
		}
		if len(made.Blocks) == 0 {
			return Started{}, errors.New("rex session.new_window created no block")
		}
		return Started{Pane: rexPane(existing.ID, made.Blocks[0]), Agent: name}, nil
	}
	var made struct {
		Session string `json:"session_id"`
		Windows []struct {
			Blocks []string `json:"block_ids"`
		} `json:"initial_windows"`
	}
	err := r.api(ctx, "", "session.create", map[string]any{"label": repo, "initial_windows": []any{map[string]any{"window_label": windowLabel, "layout": layout}}}, &made)
	if err != nil {
		return Started{}, err
	}
	if len(made.Windows) == 0 || len(made.Windows[0].Blocks) == 0 {
		return Started{}, errors.New("rex session.create created no block")
	}
	return Started{Pane: rexPane(made.Session, made.Windows[0].Blocks[0]), Agent: name}, nil
}

// command is the block's command. Rex has no env option, so env sets dispatch's stamps.
func (r *Rex) command(w Worker, exe string, argv []string) []string {
	stamps := map[string]string{"AUTONOMOUS_WORKER": "1", "DISPATCH_PLUGIN_ROOT": w.PluginRoot}
	if r.dispatchBin != "" {
		stamps["DISPATCH_BIN"] = r.dispatchBin
	}
	maps.Copy(stamps, w.Program.Env)
	command := []string{"/usr/bin/env"}
	for _, key := range slices.Sorted(maps.Keys(stamps)) {
		command = append(command, key+"="+stamps[key])
	}
	command = append(command, exe)
	return append(command, argv...)
}

// submit types text into the worker's box and presses Enter only when the status read before
// and after typing says idle, done or working. A worker showing a question is refused before
// anything is typed, and a status that moved clears the box with Ctrl-U and returns
// status_mismatch, because Escape at a question would cancel it. It returns when the worker last
// reported before the text went in. A non-empty want is the status the caller read before, and
// any other read is a status_mismatch, as herdr answers when the status moved under a send.
func (r *Rex) submit(ctx context.Context, pane, text, want string) (uint64, error) {
	session, block, _ := splitRexPane(pane)
	agent, at, found, err := r.observe(ctx, pane)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, &Refusal{Code: "agent_not_found", Message: "no worker session in block " + pane, Command: "rex:prompt"}
	}
	switch agent.Status {
	case "unknown":
		return 0, ErrStatusUnknown
	case "blocked":
		return 0, &Refusal{Code: "agent_blocked", Message: "block " + pane + " is blocked and requires interactive input", Command: "rex:prompt"}
	}
	if want != "" && agent.Status != want {
		return 0, &Refusal{Code: "status_mismatch", Message: "the status of block " + pane + " is not " + want, Command: "rex:prompt"}
	}
	if err := r.blockCall(ctx, session, block, "write", map[string]any{"data": text}, nil); err != nil {
		r.clear(ctx, session, block, text)
		return 0, err
	}
	after, _, found, err := r.observe(ctx, pane)
	if err != nil || !found || !slices.Contains([]string{"idle", "done", "working"}, after.Status) || (want != "" && after.Status != want) {
		r.clear(ctx, session, block, text)
		return 0, &Refusal{Code: "status_mismatch", Message: "the status of block " + pane + " moved while the text was typed", Command: "rex:prompt"}
	}
	if err := r.blockCall(ctx, session, block, "write", map[string]any{"data": "\r"}, nil); err != nil {
		r.clear(ctx, session, block, text)
		return 0, &Refusal{Code: "status_mismatch", Message: "Enter could not be sent to block " + pane + ": " + err.Error(), Command: "rex:prompt"}
	}
	return at, nil
}

// clear removes typed text from the box with Ctrl-U, twice per typed line plus once. It runs on a
// fresh short context, so typed text is not left behind when the call's own context has ended.
func (r *Rex) clear(ctx context.Context, session, block, text string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clearWait)
	defer cancel()
	presses := 2*(strings.Count(text, "\n")+1) + 1
	if err := r.blockCall(ctx, session, block, "write", map[string]any{"data": strings.Repeat("\x15", presses)}, nil); err != nil {
		r.record("clear-failed", err.Error())
	}
}

func (r *Rex) Kickoff(ctx context.Context, pane, text string, startup Startup) error {
	session, block, ok := splitRexPane(pane)
	if !ok {
		return ErrKickoffUnconfirmed
	}
	return r.workLog.kickoff(ctx, rexKickoff{r: r, pane: pane, session: session, block: block}, text, startup)
}

// rexKickoff gives the kickoff driver Rex's four operations on one block. Its send never answers
// started, because pressing Enter does not show that a turn began.
type rexKickoff struct {
	r              *Rex
	pane           string
	session, block string
}

func (k rexKickoff) read(ctx context.Context) (observed, error) {
	proc, err := k.r.process(ctx, k.session, k.block)
	if rexGone(err) {
		return observed{}, nil
	}
	if err != nil {
		return observed{}, err
	}
	if proc.LastExit != nil {
		return observed{Exited: true, Exit: proc.LastExit.text()}, nil
	}
	o := observed{Alive: true, Status: "unknown"}
	if _, o.Agent = k.r.kind(proc); !o.Agent {
		return o, nil
	}
	got, err := k.r.programStatus(ctx, k.session, k.block)
	if rexGone(err) {
		return observed{}, nil
	}
	if err != nil {
		return observed{}, err
	}
	o.Status, o.Turn = got.Status, got.At
	return o, nil
}

// screen is the visible screen plus history lines above it. With none it is the visible screen alone,
// so a startup dialog already answered and scrolled off is never answered again.
func (k rexKickoff) screen(ctx context.Context, history int) string {
	var size struct {
		Rows int `json:"rows"`
	}
	if err := k.r.blockCall(ctx, k.session, k.block, "size", nil, &size); err != nil || size.Rows <= 0 {
		return ""
	}
	var read struct {
		Content string `json:"content"`
	}
	if err := k.r.blockCall(ctx, k.session, k.block, "format", map[string]any{"format": "text", "trim": true}, &read); err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(read.Content, "\n"), "\n")
	if keep := size.Rows + history; len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}
	return oneLine(strings.Join(lines, "\n"))
}

func (k rexKickoff) press(ctx context.Context, keys []string) {
	var data strings.Builder
	for _, key := range keys {
		data.WriteString(keyBytes(key))
	}
	_ = k.r.blockCall(ctx, k.session, k.block, "write", map[string]any{"data": data.String()}, nil)
}

func (k rexKickoff) send(ctx context.Context, text, status string) (sent, error) {
	at, err := k.r.submit(ctx, k.pane, text, status)
	var refusal *Refusal
	if errors.As(err, &refusal) && refusal.Code == "status_mismatch" {
		return sent{Outcome: sendNotStarted}, nil
	}
	if err != nil {
		return sent{}, err
	}
	return sent{Outcome: sendUnknown, Turn: at}, nil
}

// keyBytes is what a terminal receives for a key a startup Try names. A name it doesn't know is typed as it is.
func keyBytes(key string) string {
	switch strings.ToLower(key) {
	case "enter":
		return "\r"
	case "escape", "esc":
		return "\x1b"
	case "tab":
		return "\t"
	case "space":
		return " "
	case "up":
		return "\x1b[A"
	case "down":
		return "\x1b[B"
	case "right":
		return "\x1b[C"
	case "left":
		return "\x1b[D"
	}
	return key
}
