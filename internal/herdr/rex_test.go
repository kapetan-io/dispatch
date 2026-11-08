package herdr_test

// Package exception: this package wraps the rex tool, so every test here is an adapter test of it.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
)

const (
	envRexState = "REX_STANDIN_STATE"
	rexInstance = "0123456789abcdef"
	// staleInstance is the first 8 characters of the server a restored block was made by.
	staleInstance = "deadbeef"
	rexSession    = "6f0c1e2a-0000-4000-8000-0000000000a1"
)

// The stand-in rex keeps what a Rex server holds in a JSON file, so each rex process the adapter
// starts sees the one before it, and a test edits the same state.
type standInRex struct {
	Down     bool
	Next     int
	Fail     map[string]string
	Sessions []standInSession
}

type standInSession struct {
	ID, Label string
	Windows   []standInWindow
	// Detached holds blocks in no window, which session.list_blocks lists and session.view does not.
	Detached []standInBlock
}

type standInWindow struct {
	ID, Label string
	Blocks    []standInBlock
}

type standInBlock struct {
	ID, Label string
	// Argv0 is the foreground program, "" when the block has none.
	Argv0 string
	// Args is what followed the program in the block's command.
	Args     []string
	LastExit bool
	Records  []standInRecord
	Writes   []string
	// AfterWrite is the root state the block reports once anything is written to it.
	AfterWrite string
}

type standInRecord struct{ ID, State, UpdatedAt string }

func (s *standInRex) id(prefix string) string {
	s.Next++
	return fmt.Sprintf("%s%d", prefix, s.Next)
}

// block adds a block in a new window of the session labeled session, creating the session, and
// returns the session and block ids.
func (s *standInRex) block(session, window string, b standInBlock) (sessionID, blockID string) {
	i := 0
	for ; i < len(s.Sessions) && s.Sessions[i].Label != session; i++ {
	}
	if i == len(s.Sessions) {
		s.Sessions = append(s.Sessions, standInSession{ID: s.id("s"), Label: session})
	}
	b.ID = s.id("b")
	s.Sessions[i].Windows = append(s.Sessions[i].Windows, standInWindow{ID: s.id("w"), Label: window, Blocks: []standInBlock{b}})
	return s.Sessions[i].ID, b.ID
}

func (s *standInRex) find(session, block string) *standInBlock {
	for i := range s.Sessions {
		for j := range s.Sessions[i].Windows {
			for k := range s.Sessions[i].Windows[j].Blocks {
				if b := &s.Sessions[i].Windows[j].Blocks[k]; s.Sessions[i].ID == session && b.ID == block {
					return b
				}
			}
		}
	}
	return nil
}

func withStandInState(path string, fn func(*standInRex) error) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	var state standInRex
	if raw, _ := io.ReadAll(file); len(raw) > 0 {
		if err := json.Unmarshal(raw, &state); err != nil {
			return err
		}
	}
	fnErr := fn(&state)
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.WriteAt(raw, 0); err != nil {
		return err
	}
	return fnErr
}

type standInExit struct {
	code   int
	stderr string
}

func (e standInExit) Error() string { return e.stderr }

func standInRefusal(status int, code, message string) error {
	return standInExit{1, fmt.Sprintf("Error: %s (status=%d code=%s)", message, status, code)}
}

// standInSessionGone is what Rex prints for a session that does not exist: exit 3 and no status tail.
func standInSessionGone(session string) error {
	return standInExit{3, fmt.Sprintf("Error: session target %q not found", session)}
}

// runRexStandIn answers `rex [-S sock] --autostart=false api call [-s session] METHOD [json]`.
func runRexStandIn(path string, args []string) int {
	if slices.Contains(args, "events") {
		return runRexEvents(path)
	}
	var stdout bytes.Buffer
	err := withStandInState(path, func(state *standInRex) error { return state.serve(args, &stdout) })
	var exit standInExit
	if errors.As(err, &exit) {
		fmt.Fprintln(os.Stderr, exit.stderr)
		return exit.code
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	_, _ = os.Stdout.Write(stdout.Bytes())
	// Rex tells an events listener started with --all about every other client's call.
	appendEvent(path, `{"type":"client_joined"}`)
	return 0
}

// appendEvent adds a line to what a running `rex events` prints next.
func appendEvent(path, line string) {
	file, err := os.OpenFile(path+".events", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintln(file, line)
}

// standInExitEvent makes a running `rex events` exit with code, as the server going away does.
func standInExitEvent(code int) string { return fmt.Sprintf(`{"type":"__exit","code":%d}`, code) }

// runRexEvents answers `rex events --all --json`: it prints the lines tests append after it started,
// and prints nothing on subscribing, as Rex does.
func runRexEvents(path string) int {
	var down bool
	_ = withStandInState(path, func(s *standInRex) error { down = s.Down; return nil })
	if down {
		fmt.Fprintln(os.Stderr, "Error: no Rex server is running")
		return 4
	}
	file, err := os.OpenFile(path+".events", os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer file.Close()
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return 2
	}
	reader, partial := bufio.NewReader(file), ""
	for {
		line, err := reader.ReadString('\n')
		partial += line
		if err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		var exit struct {
			Type string
			Code int
		}
		if json.Unmarshal([]byte(partial), &exit) == nil && exit.Type == "__exit" {
			return exit.Code
		}
		_, _ = os.Stdout.WriteString(partial)
		partial = ""
	}
}

func (s *standInRex) serve(args []string, out io.Writer) error {
	if s.Down {
		return standInExit{4, "Error: no Rex server is running"}
	}
	var session, method, body string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-S" || args[i] == "-s":
			i++
			if args[i-1] == "-s" {
				session = args[i]
			}
		case strings.HasPrefix(args[i], "--"), args[i] == "api", args[i] == "call":
		case method == "":
			method = args[i]
		default:
			body = args[i]
		}
	}
	if stderr, ok := s.Fail[method]; ok {
		return standInExit{1, stderr}
	}
	reply, err := s.call(session, method, []byte(body))
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(reply)
}

type standInLayout struct {
	Block struct {
		Label   string `json:"label"`
		Options struct {
			Command []string `json:"command"`
		} `json:"options"`
	} `json:"block"`
}

func (l standInLayout) newBlock() standInBlock {
	b := standInBlock{Label: l.Block.Label}
	// The command is env, then K=V stamps, then the program.
	for i, arg := range l.Block.Options.Command[1:] {
		if !strings.Contains(arg, "=") {
			b.Argv0 = arg
			b.Args = l.Block.Options.Command[i+2:]
			break
		}
	}
	return b
}

func (s *standInRex) call(session, method string, raw []byte) (any, error) {
	switch method {
	case "server.status":
		return map[string]any{"instance_id": rexInstance}, nil
	case "session.list":
		sessions := []map[string]string{}
		for _, ss := range s.Sessions {
			sessions = append(sessions, map[string]string{"session_id": ss.ID, "label": ss.Label})
		}
		return map[string]any{"sessions": sessions}, nil
	case "session.view":
		for _, ss := range s.Sessions {
			if ss.ID != session {
				continue
			}
			windows := []map[string]any{}
			for _, w := range ss.Windows {
				blocks := []map[string]string{}
				for _, b := range w.Blocks {
					blocks = append(blocks, map[string]string{"block_id": b.ID, "label": b.Label})
				}
				windows = append(windows, map[string]any{"window_id": w.ID, "label": w.Label, "layers": []any{map[string]any{"blocks": blocks}}})
			}
			return map[string]any{"session_id": ss.ID, "label": ss.Label, "windows": windows}, nil
		}
		return nil, standInSessionGone(session)
	case "session.create":
		var args struct {
			Label   string `json:"label"`
			Windows []struct {
				Label  string        `json:"window_label"`
				Layout standInLayout `json:"layout"`
			} `json:"initial_windows"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		var created []map[string]any
		var id string
		for _, w := range args.Windows {
			var block string
			id, block = s.block(args.Label, w.Label, w.Layout.newBlock())
			created = append(created, map[string]any{"block_ids": []string{block}})
		}
		return map[string]any{"session_id": id, "initial_windows": created}, nil
	case "session.new_window":
		var args struct {
			Session string        `json:"session_id"`
			Label   string        `json:"window_label"`
			Layout  standInLayout `json:"layout"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		for i := range s.Sessions {
			if s.Sessions[i].ID != args.Session {
				continue
			}
			b := s.id("b")
			block := args.Layout.newBlock()
			block.ID = b
			s.Sessions[i].Windows = append(s.Sessions[i].Windows, standInWindow{ID: s.id("w"), Label: args.Label, Blocks: []standInBlock{block}})
			return map[string]any{"block_ids": []string{b}}, nil
		}
		return nil, standInSessionGone(args.Session)
	case "session.set_window_label":
		var args struct {
			Session, Window, Label string
			SessionID              string `json:"session_id"`
			WindowID               string `json:"window_id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		var labelled struct{ Label string }
		_ = json.Unmarshal(raw, &labelled)
		for i := range s.Sessions {
			for j := range s.Sessions[i].Windows {
				if s.Sessions[i].ID == args.SessionID && s.Sessions[i].Windows[j].ID == args.WindowID {
					s.Sessions[i].Windows[j].Label = labelled.Label
					return map[string]any{}, nil
				}
			}
		}
		return nil, standInRefusal(404, "not_found", "window "+args.WindowID+" not found")
	case "session.set_block_label":
		var args struct {
			SessionID string `json:"session_id"`
			BlockID   string `json:"block_id"`
			Label     string `json:"label"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		for i := range s.Sessions {
			for j := range s.Sessions[i].Windows {
				for k := range s.Sessions[i].Windows[j].Blocks {
					if s.Sessions[i].ID == args.SessionID && s.Sessions[i].Windows[j].Blocks[k].ID == args.BlockID {
						s.Sessions[i].Windows[j].Blocks[k].Label = args.Label
						return map[string]any{}, nil
					}
				}
			}
		}
		return nil, standInRefusal(404, "not_found", "block "+args.BlockID+" not found")
	case "block.close":
		var args struct {
			Session string `json:"session_id"`
			Block   string `json:"block_id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		for i := range s.Sessions {
			for j := range s.Sessions[i].Windows {
				w := &s.Sessions[i].Windows[j]
				for k := range w.Blocks {
					if s.Sessions[i].ID == args.Session && w.Blocks[k].ID == args.Block {
						w.Blocks = append(w.Blocks[:k], w.Blocks[k+1:]...)
						if len(w.Blocks) == 0 {
							s.Sessions[i].Windows = append(s.Sessions[i].Windows[:j], s.Sessions[i].Windows[j+1:]...)
						}
						return map[string]any{}, nil
					}
				}
			}
		}
		return nil, standInRefusal(404, "not_found", "block "+args.Block+" not found")
	case "session.list_blocks":
		var args struct {
			Session string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		for _, ss := range s.Sessions {
			if ss.ID != args.Session {
				continue
			}
			blocks := []map[string]string{}
			for _, w := range ss.Windows {
				for _, b := range w.Blocks {
					blocks = append(blocks, map[string]string{"block_id": b.ID})
				}
			}
			for _, b := range ss.Detached {
				blocks = append(blocks, map[string]string{"block_id": b.ID})
			}
			return map[string]any{"blocks": blocks}, nil
		}
		return nil, standInSessionGone(args.Session)
	case "session.destroy":
		var args struct {
			Session string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		for i := range s.Sessions {
			if s.Sessions[i].ID == args.Session {
				s.Sessions = append(s.Sessions[:i], s.Sessions[i+1:]...)
				return map[string]any{}, nil
			}
		}
		return nil, standInSessionGone(args.Session)
	case "block.method":
		return s.blockMethod(raw)
	}
	return nil, standInRefusal(400, "unknown_method", "unknown method "+method)
}

func (s *standInRex) blockMethod(raw []byte) (any, error) {
	var args struct {
		Session string `json:"session_id"`
		Block   string `json:"block_id"`
		Method  string `json:"method"`
		Args    struct {
			Data string `json:"data"`
		} `json:"args"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	b := s.find(args.Session, args.Block)
	if b == nil {
		return nil, standInRefusal(404, "not_found", "block "+args.Block+" not found")
	}
	switch args.Method {
	case "process":
		reply := map[string]any{"last_exit": nil, "foreground": nil}
		if b.LastExit {
			reply["last_exit"] = map[string]any{}
		}
		if b.Argv0 != "" {
			reply["foreground"] = map[string]string{"argv0": b.Argv0}
		}
		return reply, nil
	case "program_status":
		records := []map[string]string{}
		for _, r := range b.Records {
			records = append(records, map[string]string{"id": r.ID, "state": r.State, "updated_at": r.UpdatedAt})
		}
		return map[string]any{"records": records}, nil
	case "write":
		b.Writes = append(b.Writes, args.Args.Data)
		if b.AfterWrite != "" {
			b.Records = []standInRecord{{State: b.AfterWrite, UpdatedAt: time.Now().Format(time.RFC3339Nano)}}
		}
		return map[string]any{}, nil
	}
	return nil, standInRefusal(400, "unknown_method", "unknown block method "+args.Method)
}

// standInRig is the Rex adapter over the stand-in rex.
type standInRig struct {
	rex     *herdr.Rex
	state   string
	program herdr.Program
}

func newStandInRig(t *testing.T) *standInRig {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	bin := t.TempDir()
	require.NoError(t, os.Symlink(exe, filepath.Join(bin, "rex")))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755))
	state := filepath.Join(t.TempDir(), "rex.json")
	// The race detector otherwise holds each stand-in process for a second at exit.
	env := environment.Environment{"PATH=" + bin, envRexState + "=" + state, "GORACE=atexit_sleep_ms=0"}
	return &standInRig{
		state:   state,
		rex:     herdr.NewRex(t.TempDir()).WithEnv(env).WithKinds("claude").WithDispatchBin("/bin/dispatch").WithPace(time.Millisecond),
		program: harness.ClaudeCode{}.Program(harness.Start{Model: "sonnet", PluginRoot: "/plugin", Session: rexSession}),
	}
}

// edit changes what the stand-in server holds.
func (r *standInRig) edit(t *testing.T, fn func(*standInRex)) {
	t.Helper()
	require.NoError(t, withStandInState(r.state, func(s *standInRex) error { fn(s); return nil }))
}

func (r *standInRig) held(t *testing.T) (state standInRex) {
	t.Helper()
	r.edit(t, func(s *standInRex) { state = *s })
	return state
}

// workers is every block with a dispatch worker label.
func (r *standInRig) workers(t *testing.T) (blocks []standInBlock) {
	t.Helper()
	for _, s := range r.held(t).Sessions {
		for _, w := range s.Windows {
			for _, b := range w.Blocks {
				if strings.HasPrefix(b.Label, "dispatch ") {
					blocks = append(blocks, b)
				}
			}
		}
	}
	return blocks
}

func (r *standInRig) worker(repo, tree string) herdr.Worker {
	return herdr.Worker{Workspace: repo + "|" + tree, Dir: tree, Label: "ENG-1 title", Name: "eng-1", PluginRoot: "/plugin", Program: r.program}
}

func (r *standInRig) setStatus(t *testing.T, pane string, records ...standInRecord) {
	t.Helper()
	session, block, _ := strings.Cut(pane, "/")
	r.edit(t, func(s *standInRex) { s.find(session, block).Records = records })
}

func (r *standInRig) writes(t *testing.T, pane string) []string {
	t.Helper()
	session, block, _ := strings.Cut(pane, "/")
	var writes []string
	r.edit(t, func(s *standInRex) { writes = s.find(session, block).Writes })
	return writes
}

func idle(state string) standInRecord {
	return standInRecord{State: state, UpdatedAt: time.Now().Format(time.RFC3339Nano)}
}

func shortTree(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rx")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return resolved
}

func workerLabelOf(instance, tree string) string {
	return "dispatch " + rexSession + " " + instance + " " + tree
}

func TestRexStartWorkerCreatesTheRepoSessionWithOneWindowAndBlock(t *testing.T) {
	r := newStandInRig(t)
	tree := shortTree(t)

	started, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))

	require.NoError(t, err)
	held := r.held(t)
	require.Len(t, held.Sessions, 1)
	assert.Equal(t, "repo", held.Sessions[0].Label)
	require.Len(t, held.Sessions[0].Windows, 1)
	assert.Equal(t, "ENG-1 title", held.Sessions[0].Windows[0].Label)
	assert.Equal(t, workerLabelOf("01234567", tree), held.Sessions[0].Windows[0].Blocks[0].Label)
	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []herdr.Workspace{{ID: "repo|" + tree, Label: "ENG-1", CheckoutPath: tree}}, snap.Workspaces)
	require.Len(t, snap.Panes, 1)
	assert.Equal(t, started.Pane, snap.Panes[0].ID)
	assert.Equal(t, []herdr.Agent{{Kind: "claude", Status: "unknown", PaneID: started.Pane, TabID: snap.Tabs[0].ID, WorkspaceID: "repo|" + tree, Session: rexSession}}, snap.Agents)
}

func TestRexStartWorkerPutsTheSecondTreeInTheSameRepoSession(t *testing.T) {
	r := newStandInRig(t)
	first, second := shortTree(t), shortTree(t)

	_, err := r.rex.StartWorker(context.Background(), r.worker("repo", first))
	require.NoError(t, err)
	_, err = r.rex.StartWorker(context.Background(), r.worker("repo", second))

	require.NoError(t, err)
	held := r.held(t)
	require.Len(t, held.Sessions, 1)
	assert.Len(t, held.Sessions[0].Windows, 2)
}

func TestRexLeavesOneBlockWhenTwoStartsRace(t *testing.T) {
	for _, test := range []struct {
		name string
		link bool
	}{
		{"SameSpelling", false},
		{"SymlinkSpelling", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newStandInRig(t)
			tree := shortTree(t)
			second := tree
			if test.link {
				second = tree + "-link"
				require.NoError(t, os.Symlink(tree, second))
				t.Cleanup(func() { _ = os.Remove(second) })
			}
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for i, dir := range []string{tree, second} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, errs[i] = r.rex.StartWorker(context.Background(), r.worker("repo", dir))
				}()
			}
			wg.Wait()

			assert.Len(t, r.workers(t), 1)
			succeeded := 0
			for _, err := range errs {
				if err == nil {
					succeeded++
				}
			}
			assert.Equal(t, 1, succeeded)
			for _, err := range errs {
				var loading *herdr.TerminalWithoutAgent
				if err != nil {
					assert.True(t, errors.Is(err, herdr.ErrSessionRunning) || errors.As(err, &loading))
				}
			}
		})
	}
}

func TestRexStartWorkerStartsFreshOverABlockThatCannotHoldTheTree(t *testing.T) {
	for _, test := range []struct {
		name  string
		block func(tree string) standInBlock
	}{
		// A Rex restart brings a worker back as a shell under the old server's instance id.
		{"RestoredByARexRestart", func(tree string) standInBlock {
			return standInBlock{Label: workerLabelOf(staleInstance, tree), Argv0: "zsh"}
		}},
		{"ProgramExited", func(tree string) standInBlock {
			return standInBlock{Label: workerLabelOf("01234567", tree), Argv0: "claude", LastExit: true}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newStandInRig(t)
			tree := shortTree(t)
			var old string
			r.edit(t, func(s *standInRex) {
				session, block := s.block("repo", "ENG-1 old", test.block(tree))
				old = session + "/" + block
			})

			started, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))

			require.NoError(t, err)
			assert.NotEqual(t, old, started.Pane)
			workers := r.workers(t)
			require.Len(t, workers, 1)
			assert.Equal(t, workerLabelOf("01234567", tree), workers[0].Label)
			assert.Len(t, r.held(t).Sessions, 1)
		})
	}
}

func TestRexStartWorkerResumesTheConversationOfABlockARexRestartBroughtBack(t *testing.T) {
	r := newStandInRig(t)
	tree := shortTree(t)
	const earlier = "11111111-2222-4333-8444-555555555555"
	r.edit(t, func(s *standInRex) {
		s.block("repo", "ENG-1 old", standInBlock{Label: "dispatch " + earlier + " " + staleInstance + " " + tree, Argv0: "zsh"})
	})

	_, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))

	require.NoError(t, err)
	workers := r.workers(t)
	require.Len(t, workers, 1)
	assert.Equal(t, "dispatch "+earlier+" 01234567 "+tree, workers[0].Label)
	assert.Equal(t, []string{"--resume", earlier}, workers[0].Args[len(workers[0].Args)-2:])
	assert.NotContains(t, workers[0].Args, "--session-id")
}

func TestRexStartWorkerKeepsTheConversationWhenTheNewBlockFailsToStart(t *testing.T) {
	r := newStandInRig(t)
	tree := shortTree(t)
	const earlier = "11111111-2222-4333-8444-555555555555"
	r.edit(t, func(s *standInRex) {
		s.block("repo", "ENG-1 old", standInBlock{Label: "dispatch " + earlier + " " + staleInstance + " " + tree, Argv0: "zsh"})
		s.Fail = map[string]string{"session.new_window": "Error: busy (status=409 code=busy)"}
	})

	_, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))
	require.Error(t, err)
	r.edit(t, func(s *standInRex) { s.Fail = nil })
	_, err = r.rex.StartWorker(context.Background(), r.worker("repo", tree))

	require.NoError(t, err)
	workers := r.workers(t)
	require.Len(t, workers, 1)
	assert.Equal(t, "dispatch "+earlier+" 01234567 "+tree, workers[0].Label)
	assert.Equal(t, []string{"--resume", earlier}, workers[0].Args[len(workers[0].Args)-2:])
}

func TestRexStartWorkerStartsAConversationInsteadOfResumingOne(t *testing.T) {
	for _, test := range []struct {
		name     string
		instance string
		exited   bool
		fresh    bool
	}{
		{"FreshLease", staleInstance, false, true},
		{"ClaudeExited", "01234567", true, false},
		{"ClaudeExitedBeforeARexRestart", staleInstance, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newStandInRig(t)
			tree := shortTree(t)
			const earlier = "11111111-2222-4333-8444-555555555555"
			r.edit(t, func(s *standInRex) {
				s.block("repo", "ENG-1 old", standInBlock{Label: "dispatch " + earlier + " " + test.instance + " " + tree, Argv0: map[bool]string{true: "claude", false: "zsh"}[test.exited], LastExit: test.exited})
			})
			w := r.worker("repo", tree)
			w.FreshLease = test.fresh

			_, err := r.rex.StartWorker(context.Background(), w)

			require.NoError(t, err)
			workers := r.workers(t)
			require.Len(t, workers, 1)
			assert.Equal(t, workerLabelOf("01234567", tree), workers[0].Label)
			assert.Equal(t, []string{"--session-id", rexSession}, workers[0].Args[len(workers[0].Args)-2:])
			assert.NotContains(t, workers[0].Args, "--resume")
		})
	}
}

func TestRexStartWorkerRefusesATreeThatAClaudeOrALoadingBlockHolds(t *testing.T) {
	for _, test := range []struct {
		name  string
		argv0 string
		check func(t *testing.T, err error)
	}{
		{"ClaudeRuns", "/bin/claude", func(t *testing.T, err error) { require.ErrorIs(t, err, herdr.ErrSessionRunning) }},
		{"LoadingBlockOfThisServer", "", func(t *testing.T, err error) {
			var loading *herdr.TerminalWithoutAgent
			require.ErrorAs(t, err, &loading)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newStandInRig(t)
			tree := shortTree(t)
			r.edit(t, func(s *standInRex) {
				s.block("repo", "ENG-1 title", standInBlock{Label: workerLabelOf("01234567", tree), Argv0: test.argv0})
			})

			_, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))

			test.check(t, err)
			assert.Len(t, r.workers(t), 1)
		})
	}
}

func TestRexStartWorkerHoldsTreesUpToWhatTheWorkerLabelFits(t *testing.T) {
	for _, test := range []struct {
		name    string
		length  int
		wantErr string
	}{
		{"AtTheLimit", 73, ""},
		{"OverTheLimit", 74, "the Rex worker label holds trees of at most 73"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newStandInRig(t)

			_, err := r.rex.StartWorker(context.Background(), r.worker("repo", "/"+strings.Repeat("a", test.length-1)))

			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
			assert.Empty(t, r.held(t).Sessions)
		})
	}
}

func TestRexCloseWorkspaceClosesRestoredAndLoadingBlocksFoundByRead(t *testing.T) {
	for _, test := range []struct {
		name     string
		instance string
		argv0    string
	}{
		{"Restored", staleInstance, "zsh"},
		{"Loading", "01234567", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newStandInRig(t)
			tree := shortTree(t)
			r.edit(t, func(s *standInRex) {
				s.block("repo", "ENG-1 title", standInBlock{Label: workerLabelOf(test.instance, tree), Argv0: test.argv0})
				s.block("repo", "mine", standInBlock{Label: "my shell", Argv0: "zsh"})
			})
			snap, err := r.rex.Read(context.Background())
			require.NoError(t, err)
			require.Len(t, snap.Workspaces, 1)

			require.NoError(t, r.rex.CloseWorkspace(context.Background(), snap.Workspaces[0].ID))

			assert.Empty(t, r.workers(t))
			held := r.held(t)
			require.Len(t, held.Sessions, 1)
			require.Len(t, held.Sessions[0].Windows, 1)
			assert.Equal(t, "my shell", held.Sessions[0].Windows[0].Blocks[0].Label)
		})
	}
}

func TestRexCloseWorkspaceLeavesTheRepoSessionWhenItsLastWorkerCloses(t *testing.T) {
	r := newStandInRig(t)
	tree := shortTree(t)
	_, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))
	require.NoError(t, err)

	require.NoError(t, r.rex.CloseWorkspace(context.Background(), "repo|"+tree))

	held := r.held(t)
	require.Len(t, held.Sessions, 1)
	assert.Equal(t, "repo", held.Sessions[0].Label)
	assert.Empty(t, held.Sessions[0].Windows)
	require.NoError(t, r.rex.CloseWorkspace(context.Background(), "repo|"+tree))
}

func TestRexCloseWorkspaceDeletesTheRepoSessionItEmptiedWhenAsked(t *testing.T) {
	r := newStandInRig(t)
	r.rex.WithDeleteEmptySessions(true)
	tree := shortTree(t)
	_, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))
	require.NoError(t, err)

	require.NoError(t, r.rex.CloseWorkspace(context.Background(), "repo|"+tree))

	assert.Empty(t, r.held(t).Sessions)
	require.NoError(t, r.rex.CloseWorkspace(context.Background(), "repo|"+tree))
}

func TestRexCloseTabDeletesTheRepoSessionItEmptiedWhenAsked(t *testing.T) {
	r := newStandInRig(t)
	r.rex.WithDeleteEmptySessions(true)
	_, err := r.rex.StartWorker(context.Background(), r.worker("repo", shortTree(t)))
	require.NoError(t, err)
	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Tabs, 1)

	require.NoError(t, r.rex.CloseTab(context.Background(), snap.Tabs[0].ID))

	assert.Empty(t, r.held(t).Sessions)
}

func TestRexCloseWorkspaceKeepsTheRepoSessionWhenSomethingElseIsInIt(t *testing.T) {
	for _, test := range []struct {
		name  string
		extra func(s *standInRex)
	}{
		{"UsersTab", func(s *standInRex) { s.block("repo", "mine", standInBlock{Label: "my shell", Argv0: "zsh"}) }},
		{"AnotherTreesWorker", func(s *standInRex) {
			s.block("repo", "ENG-2 other", standInBlock{Label: workerLabelOf("01234567", "/other"), Argv0: "claude"})
		}},
		{"DetachedBlock", func(s *standInRex) { s.Sessions[0].Detached = []standInBlock{{ID: "detached"}} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newStandInRig(t)
			r.rex.WithDeleteEmptySessions(true)
			tree := shortTree(t)
			_, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))
			require.NoError(t, err)
			r.edit(t, test.extra)

			require.NoError(t, r.rex.CloseWorkspace(context.Background(), "repo|"+tree))

			held := r.held(t)
			require.Len(t, held.Sessions, 1)
			assert.Equal(t, "repo", held.Sessions[0].Label)
		})
	}
}

func TestRexCloseWorkspaceNeverDeletesASessionTheUserEmptied(t *testing.T) {
	r := newStandInRig(t)
	r.rex.WithDeleteEmptySessions(true)
	r.edit(t, func(s *standInRex) { s.Sessions = []standInSession{{ID: "s1", Label: "repo"}} })

	require.NoError(t, r.rex.CloseWorkspace(context.Background(), "repo|"+shortTree(t)))

	assert.Len(t, r.held(t).Sessions, 1)
}

func TestRexTreatsASessionDestroyedUnderItAsGone(t *testing.T) {
	r := newStandInRig(t)
	_, err := r.rex.StartWorker(context.Background(), r.worker("repo", shortTree(t)))
	require.NoError(t, err)
	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Tabs, 1)
	r.edit(t, func(s *standInRex) { s.Sessions = nil })

	require.NoError(t, r.rex.CloseTab(context.Background(), snap.Tabs[0].ID))
	require.NoError(t, r.rex.RenameTab(context.Background(), snap.Tabs[0].ID, "ENG-1 new title"))
}

func TestRexRenamesKeepTheEmojiAndTheTitleApart(t *testing.T) {
	r := newStandInRig(t)
	tree := shortTree(t)
	_, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))
	require.NoError(t, err)

	require.NoError(t, r.rex.RenameWorkspace(context.Background(), "repo|"+tree, "🟢 ENG-1"))

	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "🟢 ENG-1", snap.Workspaces[0].Label)
	assert.Equal(t, "ENG-1 title", snap.Tabs[0].Label)
	assert.Equal(t, "🟢 ENG-1 title", r.held(t).Sessions[0].Windows[0].Label)

	require.NoError(t, r.rex.RenameTab(context.Background(), snap.Tabs[0].ID, "ENG-1 new title"))

	snap, err = r.rex.Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "🟢 ENG-1", snap.Workspaces[0].Label)
	assert.Equal(t, "ENG-1 new title", snap.Tabs[0].Label)

	require.NoError(t, r.rex.RenameWorkspace(context.Background(), "repo|"+tree, "🔴 ENG-1"))

	snap, err = r.rex.Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "🔴 ENG-1", snap.Workspaces[0].Label)
	assert.Equal(t, "ENG-1 new title", snap.Tabs[0].Label)
}

func TestRexReadsAStoppedServerAsNotRunning(t *testing.T) {
	r := newStandInRig(t)
	tree := shortTree(t)
	r.edit(t, func(s *standInRex) { s.Down = true })

	_, readErr := r.rex.Read(context.Background())
	_, startErr := r.rex.StartWorker(context.Background(), r.worker("repo", tree))

	require.ErrorIs(t, readErr, herdr.ErrRexNotRunning)
	require.ErrorIs(t, startErr, herdr.ErrRexNotRunning)
	assert.EqualError(t, readErr, "Rex is not running, and dispatch never starts it: open Rex")
}

func TestRexTurnsATailedStatusAndCodeIntoARefusal(t *testing.T) {
	r := newStandInRig(t)
	tree := shortTree(t)
	_, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))
	require.NoError(t, err)
	r.edit(t, func(s *standInRex) {
		s.Fail = map[string]string{"block.close": "Error: block is busy (status=409 code=busy)"}
	})

	err = r.rex.CloseWorkspace(context.Background(), "repo|"+tree)

	var refusal *herdr.Refusal
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, "busy", refusal.Code)
	assert.Equal(t, "block is busy (status=409 code=busy)", refusal.Message)
}

func TestRexReportsAFailureWithoutAStatusTailAsWhatRexPrinted(t *testing.T) {
	r := newStandInRig(t)
	r.edit(t, func(s *standInRex) { s.Fail = map[string]string{"session.list": "Error: socket closed"} })

	_, err := r.rex.Read(context.Background())

	require.ErrorContains(t, err, "Error: socket closed")
	var refusal *herdr.Refusal
	assert.False(t, errors.As(err, &refusal))
}

func TestRexKickoffPressesEnterOnTheTypedKickoffOfAnIdleWorker(t *testing.T) {
	r := newStandInRig(t)
	started, err := r.rex.StartWorker(context.Background(), r.worker("repo", shortTree(t)))
	require.NoError(t, err)
	r.setStatus(t, started.Pane, idle("idle"))

	_ = r.rex.Kickoff(context.Background(), started.Pane, "/dispatch:build ENG-1", r.program.Startup)

	writes := r.writes(t, started.Pane)
	require.NotEmpty(t, writes)
	assert.Equal(t, "/dispatch:build ENG-1", writes[0])
	assert.Contains(t, writes, "\r")
}

func TestRexKickoffClearsTheBoxAndPressesNoEnterWhenTheStatusMovesWhileTyping(t *testing.T) {
	r := newStandInRig(t)
	started, err := r.rex.StartWorker(context.Background(), r.worker("repo", shortTree(t)))
	require.NoError(t, err)
	r.setStatus(t, started.Pane, idle("idle"))
	session, block, _ := strings.Cut(started.Pane, "/")
	r.edit(t, func(s *standInRex) { s.find(session, block).AfterWrite = "blocked" })

	_ = r.rex.Kickoff(context.Background(), started.Pane, "/dispatch:build ENG-1", r.program.Startup)

	writes := r.writes(t, started.Pane)
	require.NotEmpty(t, writes)
	assert.Equal(t, "/dispatch:build ENG-1", writes[0])
	assert.Contains(t, writes, "\x15\x15\x15")
	assert.NotContains(t, writes, "\r")
}

func TestRexFollowSessionRelabelsTheWorkersBlockWithTheNewSession(t *testing.T) {
	r := newStandInRig(t)
	tree := shortTree(t)
	started, err := r.rex.StartWorker(context.Background(), r.worker("repo", tree))
	require.NoError(t, err)

	require.NoError(t, r.rex.FollowSession(context.Background(), started.Pane, "cleared-session"))

	agent, found, err := r.rex.AgentAt(context.Background(), started.Pane)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "cleared-session", agent.Session)
	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Agents, 1)
	assert.Equal(t, "cleared-session", snap.Agents[0].Session)
}

func TestRexFollowSessionLeavesAPaneThatIsNoWorkerAlone(t *testing.T) {
	r := newStandInRig(t)

	assert.NoError(t, r.rex.FollowSession(context.Background(), "nowhere", "cleared-session"))
	assert.NoError(t, r.rex.FollowSession(context.Background(), "gone/b9", "cleared-session"))
}
