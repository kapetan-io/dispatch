package dispatch_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The test clock reads 2026-10-09T12:00:00Z, so the default window is 2026-10-02T12:00 to then.
const (
	inWindow   = "2026-10-08T10:00:00.000Z"
	laterOn    = "2026-10-08T11:30:00.000Z"
	beforeWeek = "2026-10-01T09:00:00.000Z"
)

// transcripts makes a dispatch whose HOME holds Claude Code's transcripts, and no
// CLAUDE_CONFIG_DIR, so the command reads only what the test writes.
func transcripts(t *testing.T) (*Dispatch, string) {
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CONFIG_DIR": ""}})
	return d, filepath.Join(d.Home, ".claude", "projects", "-Users-me-Development-shop")
}

// writeTranscript writes records as a JSONL transcript at path.
func writeTranscript(t *testing.T, path string, records ...map[string]any) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	var b strings.Builder
	for _, r := range records {
		line, err := json.Marshal(r)
		require.NoError(t, err)
		b.Write(line)
		b.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
}

func toolCall(at, cwd, id, name string, input map[string]any) map[string]any {
	return map[string]any{
		"type": "assistant", "timestamp": at, "cwd": cwd,
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}},
	}
}

func bash(at, cwd, id, command string) map[string]any {
	return toolCall(at, cwd, id, "Bash", map[string]any{"command": command})
}

func toolResult(at, id, text string, isError bool) map[string]any {
	return map[string]any{
		"type": "user", "timestamp": at,
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": isError}}},
	}
}

func assistantText(at, text string) map[string]any {
	return map[string]any{"type": "assistant", "timestamp": at, "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}}
}

type failuresJSON struct {
	ToolFailures []struct {
		Signature, Repos, Example string
		FirstSeen                 string `json:"first_seen"`
		LastSeen                  string `json:"last_seen"`
		Count, Sessions           int
	} `json:"tool_failures"`
	Dropped struct {
		Signatures int `json:"signatures"`
	} `json:"tool_failures_dropped_singletons"`
	PermissionDenials []struct {
		Signature string
		Count     int
	} `json:"permission_denials"`
	SubagentDenials []struct {
		Session, Agent, Description, Kind, Denied string
		NextActionOnSameFile                      string `json:"next_action_on_same_file"`
		ReportNamesFile                           string `json:"report_names_file"`
		ReportDenialWords                         string `json:"report_denial_words"`
		ReportSource                              string `json:"report_source"`
	} `json:"subagent_denials"`
	RepeatedEdits []struct {
		Session, Agent, Repo, File string
		Edits                      int
	} `json:"repeated_edits"`
	SkippedSessions []string `json:"skipped_sessions"`
	From, To        string
}

func collectJSON(t *testing.T, d *Dispatch, args ...string) failuresJSON {
	t.Helper()
	res := d.Run(append([]string{"collect-agent-failures", "--json"}, args...)...)
	require.NoError(t, res.Err, res.Stderr)
	var out failuresJSON
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &out))
	return out
}

func TestCollectAgentFailuresGroupsFailedCallsBySignature(t *testing.T) {
	d, project := transcripts(t)
	cwd := filepath.Join(d.Home, "Development", "shop")
	// Two sessions hit the same test failure in different packages, which normalizes to one
	// signature, and one hits a failure nobody else did.
	writeTranscript(t, filepath.Join(project, "aaaaaaaa-1111.jsonl"),
		bash(inWindow, cwd, "toolu_1", "cd /src && go test ./internal/cart/..."),
		toolResult(inWindow, "toolu_1", "Exit code 1\n--- FAIL: TestCart (0.01s)\nFAIL\tgithub.com/x/shop/internal/cart 0.123s", true),
		bash(inWindow, cwd, "toolu_2", "ls /nope"),
		toolResult(inWindow, "toolu_2", "Exit code 2\nls: /nope: No such file or directory", true),
		// Error-looking text on a call that succeeded is not a failure.
		bash(inWindow, cwd, "toolu_3", "grep -r panic ."),
		toolResult(inWindow, "toolu_3", "Exit code 1\nFAIL somewhere", false),
	)
	writeTranscript(t, filepath.Join(project, "bbbbbbbb-2222.jsonl"),
		bash(laterOn, cwd, "toolu_4", "go test ./internal/orders/"),
		toolResult(laterOn, "toolu_4", "Exit code 1\n--- FAIL: TestOrders (0.20s)\nFAIL\tgithub.com/x/shop/internal/orders 1.5s", true),
	)

	out := collectJSON(t, d)

	require.Len(t, out.ToolFailures, 1)
	f := out.ToolFailures[0]
	assert.Equal(t, "Bash | go test | Exit code 1: FAIL github.com<path> <n>s", f.Signature)
	assert.Equal(t, 2, f.Count)
	assert.Equal(t, 2, f.Sessions)
	assert.Equal(t, "shop", f.Repos)
	assert.Equal(t, "2026-10-08T10:00", f.FirstSeen)
	assert.Equal(t, "2026-10-08T11:30", f.LastSeen)
	assert.True(t, strings.HasPrefix(f.Example, "cd /src && go test ./internal/cart/...\n=> Exit code 1"))
	assert.Equal(t, 1, out.Dropped.Signatures)
	assert.Empty(t, out.PermissionDenials)

	res := d.Run("collect-agent-failures")
	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "# Agent failures from 2026-10-02T12:00:00Z to 2026-10-09T12:00:00Z\n")
	assert.Contains(t, res.Stdout, "## 1. Tool failures (1)\n")
	assert.Contains(t, res.Stdout, "| Bash \\| go test \\| Exit code 1: FAIL github.com<path> <n>s | 2 | 2 | shop |")
	assert.Contains(t, res.Stdout, "Dropped 1 signatures seen once.\n")
	assert.Contains(t, res.Stdout, "## 1b. Permission denials (0)\n\n_none_\n")
}

func TestCollectAgentFailuresCountsACallOnce(t *testing.T) {
	d, project := transcripts(t)
	session := filepath.Join(project, "cccccccc-3333")
	call := bash(inWindow, "/tmp/x", "toolu_9", "make lint")
	failed := toolResult(inWindow, "toolu_9", "Exit code 2\nmake: *** [lint] Error 2", true)
	// A subagent file repeats the records of another, and each call still counts once.
	writeTranscript(t, filepath.Join(session, "subagents", "agent-a1.jsonl"), call, failed)
	writeTranscript(t, filepath.Join(session, "subagents", "agent-a2.jsonl"), call, failed,
		bash(laterOn, "/tmp/x", "toolu_10", "make lint"),
		toolResult(laterOn, "toolu_10", "Exit code 2\nmake: *** [lint] Error 2", true))

	out := collectJSON(t, d)

	require.Len(t, out.ToolFailures, 1)
	assert.Equal(t, "Bash | make lint | Exit code 2: make: ** [lint] Error <n>", out.ToolFailures[0].Signature)
	assert.Equal(t, 2, out.ToolFailures[0].Count)
	assert.Equal(t, "(temp dir)", out.ToolFailures[0].Repos)
}

func TestCollectAgentFailuresReportsSubagentDenials(t *testing.T) {
	d, project := transcripts(t)
	require.NoError(t, os.MkdirAll(filepath.Join(d.Home, ".claude"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Home, ".claude", "settings.json"),
		[]byte(`{"permissions":{"deny":["Bash(git push:*)","Read(./.env)"]}}`), 0o644))
	cwd := filepath.Join(d.Home, ".treehouse", "shop-1a2b", "3", "shop")
	session := filepath.Join(project, "dddddddd-4444")
	denied := "Permission to use Bash with command git push origin main has been denied."
	// One subagent hits the deny rule and says nothing of it; another is refused by the user and
	// hands back a report that names the file.
	writeTranscript(t, filepath.Join(session, "subagents", "agent-quiet.jsonl"),
		bash(inWindow, cwd, "toolu_20", "git add cart.go && git push origin main"),
		toolResult(inWindow, "toolu_20", denied, true),
		bash(laterOn, cwd, "toolu_21", "git diff cart.go"),
		toolResult(laterOn, "toolu_21", "diff --git", false),
		assistantText(laterOn, "Done. Everything is committed."),
	)
	require.NoError(t, os.WriteFile(filepath.Join(session, "subagents", "agent-quiet.meta.json"), []byte(`{"description":"Commit the cart fix"}`), 0o644))
	writeTranscript(t, filepath.Join(session, "subagents", "agent-loud.jsonl"),
		toolCall(inWindow, cwd, "toolu_30", "Edit", map[string]any{"file_path": cwd + "/orders.go"}),
		toolResult(inWindow, "toolu_30", "The user doesn't want to proceed with this tool use. The tool use was rejected.", true),
		toolCall(laterOn, cwd, "toolu_31", "SubagentHandback", map[string]any{"message": "I could not edit orders.go."}),
	)

	out := collectJSON(t, d)

	require.Len(t, out.SubagentDenials, 2)
	quiet := out.SubagentDenials[0]
	assert.Equal(t, "dddddddd", quiet.Session)
	assert.Equal(t, "agent-quiet", quiet.Agent)
	assert.Equal(t, "Commit the cart fix", quiet.Description)
	assert.Equal(t, "rule", quiet.Kind)
	assert.Equal(t, "git push origin main", quiet.Denied)
	assert.Equal(t, "no", quiet.ReportNamesFile)
	assert.Equal(t, "no", quiet.ReportDenialWords)
	assert.Equal(t, "last text", quiet.ReportSource)
	loud := out.SubagentDenials[1]
	assert.Equal(t, "agent-loud", loud.Agent)
	assert.Equal(t, "user-rejected", loud.Kind)
	assert.Equal(t, "orders.go", loud.ReportNamesFile)
	assert.Equal(t, "SubagentHandback", loud.ReportSource)

	require.Len(t, out.PermissionDenials, 2)
	assert.Equal(t, "rule | Bash | git push", out.PermissionDenials[0].Signature)
	assert.Equal(t, "user-rejected | Edit", out.PermissionDenials[1].Signature)
	// Denials stay out of the failures table and its singleton count.
	assert.Empty(t, out.ToolFailures)
	assert.Equal(t, 0, out.Dropped.Signatures)
}

func TestCollectAgentFailuresReportsWhatASubagentDidNext(t *testing.T) {
	d, project := transcripts(t)
	session := filepath.Join(project, "eeeeeeee-5555")
	writeTranscript(t, filepath.Join(session, "subagents", "agent-retry.jsonl"),
		toolCall(inWindow, "/tmp/x", "toolu_40", "Read", map[string]any{"file_path": "/tmp/x/.env.local"}),
		toolResult(inWindow, "toolu_40", "Permission for this action was denied by the Claude Code auto mode classifier. Reason: [secrets] reads a credential file", true),
		bash(laterOn, "/tmp/x", "toolu_41", "ls"),
		toolResult(laterOn, "toolu_41", "a b", false),
		bash(laterOn, "/tmp/x", "toolu_42", "cat /tmp/x/.env.local"),
		toolResult(laterOn, "toolu_42", "Exit code 1\ncat: /tmp/x/.env.local: Operation not permitted", true),
		assistantText(laterOn, "The read was blocked."),
	)

	out := collectJSON(t, d)

	require.Len(t, out.SubagentDenials, 1)
	row := out.SubagentDenials[0]
	assert.Equal(t, "classifier: secrets", row.Kind)
	assert.Equal(t, "file_path=/tmp/x/.env.local", row.Denied)
	assert.Equal(t, "[error] cat /tmp/x/.env.local", row.NextActionOnSameFile)
	assert.Equal(t, "blocked", row.ReportDenialWords)
}

func TestCollectAgentFailuresReportsRepeatedEdits(t *testing.T) {
	d, project := transcripts(t)
	cwd := filepath.Join(d.Home, "Development", "shop")
	var records []map[string]any
	for i, id := range []string{"e1", "e2", "e3", "e4", "e5"} {
		at := []string{inWindow, inWindow, inWindow, laterOn, laterOn}[i]
		records = append(records, toolCall(at, cwd, id, "Edit", map[string]any{"file_path": cwd + "/cart.go"}), toolResult(at, id, "ok", false))
	}
	// Four edits of another file, and a failed fifth, stay under the bar.
	for _, id := range []string{"f1", "f2", "f3", "f4"} {
		records = append(records, toolCall(inWindow, cwd, id, "Write", map[string]any{"file_path": cwd + "/orders.go"}), toolResult(inWindow, id, "ok", false))
	}
	records = append(records, toolCall(inWindow, cwd, "f5", "Edit", map[string]any{"file_path": cwd + "/orders.go"}),
		toolResult(inWindow, "f5", "<tool_use_error>String to replace not found in file.</tool_use_error>", true))
	writeTranscript(t, filepath.Join(project, "ffffffff-6666.jsonl"), records...)

	out := collectJSON(t, d)

	require.Len(t, out.RepeatedEdits, 1)
	e := out.RepeatedEdits[0]
	assert.Equal(t, "ffffffff", e.Session)
	assert.Equal(t, "main", e.Agent)
	assert.Equal(t, "shop", e.Repo)
	assert.Equal(t, cwd+"/cart.go", e.File)
	assert.Equal(t, 5, e.Edits)

	res := d.Run("collect-agent-failures")
	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "## 2b. Files edited 5+ times by one agent or session (1)\n")
}

func TestCollectAgentFailuresReadsOnlyTheWindow(t *testing.T) {
	d, project := transcripts(t)
	fail := func(at, id string) []map[string]any {
		return []map[string]any{bash(at, "/tmp/x", id, "make build"), toolResult(at, id, "Exit code 2\nmake: *** No rule to make target 'build'", true)}
	}
	var records []map[string]any
	records = append(records, fail(beforeWeek, "old1")...)
	records = append(records, fail(beforeWeek, "old2")...)
	records = append(records, fail(inWindow, "new1")...)
	records = append(records, fail(laterOn, "new2")...)
	writeTranscript(t, filepath.Join(project, "99999999-7777.jsonl"), records...)

	for _, test := range []struct {
		name  string
		args  []string
		count int
	}{
		{name: "the last 7 days by default", count: 2},
		{name: "from a date", args: []string{"--from", "2026-09-30"}, count: 4},
		{name: "to an RFC 3339 time", args: []string{"--from", "2026-09-30", "--to", "2026-10-08T11:00:00Z"}, count: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := collectJSON(t, d, test.args...)
			require.Len(t, out.ToolFailures, 1)
			assert.Equal(t, test.count, out.ToolFailures[0].Count)
		})
	}
}

func TestCollectAgentFailuresSkipsThePatternMiner(t *testing.T) {
	d, project := transcripts(t)
	writeTranscript(t, filepath.Join(project, "11111111-miner.jsonl"),
		// Written in two halves so this file does not hold the marker itself.
		assistantText(beforeWeek, "DAILY-"+"PATTERN-MINING run"),
		bash(inWindow, "/tmp/x", "m1", "make build"), toolResult(inWindow, "m1", "Exit code 2\nmake: *** stop", true),
		bash(inWindow, "/tmp/x", "m2", "make build"), toolResult(inWindow, "m2", "Exit code 2\nmake: *** stop", true),
	)

	out := collectJSON(t, d)

	assert.Empty(t, out.ToolFailures)
	assert.Equal(t, []string{"11111111"}, out.SkippedSessions)
}

func TestCollectAgentFailuresReadsClaudeConfigDir(t *testing.T) {
	config := t.TempDir()
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CONFIG_DIR": config}})
	project := filepath.Join(config, "projects", "-tmp-x")
	writeTranscript(t, filepath.Join(project, "22222222-8888.jsonl"),
		bash(inWindow, "/tmp/x", "c1", "make build"), toolResult(inWindow, "c1", "Exit code 2\nmake: *** stop", true),
		bash(inWindow, "/tmp/x", "c2", "make build"), toolResult(inWindow, "c2", "Exit code 2\nmake: *** stop", true),
	)

	out := collectJSON(t, d)

	require.Len(t, out.ToolFailures, 1)
	assert.Equal(t, 2, out.ToolFailures[0].Count)
}

func TestCollectAgentFailuresWithNoTranscripts(t *testing.T) {
	d, _ := transcripts(t)

	res := d.Run("collect-agent-failures")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "Read 0 transcript files.")
	assert.Contains(t, res.Stdout, "## 1. Tool failures (0)\n\n_none_\n")
}

func TestCollectAgentFailuresRejectsBadArguments(t *testing.T) {
	d, _ := transcripts(t)
	for _, test := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "a time that is not one", args: []string{"--from", "last week"}, wantErr: `--from "last week" is not a time`},
		{name: "a window that ends before it starts", args: []string{"--from", "2026-10-09", "--to", "2026-10-01"}, wantErr: "is not before --to"},
		{name: "an argument", args: []string{"ENG-1"}, wantErr: "collect-agent-failures takes flags only"},
	} {
		t.Run(test.name, func(t *testing.T) {
			res := d.Run(append([]string{"collect-agent-failures"}, test.args...)...)
			require.ErrorContains(t, res.Err, test.wantErr)
		})
	}
}

func TestCollectAgentFailuresNamesWhatTheCallRan(t *testing.T) {
	// Each signature here is what datamine.py's tool_failure_tables makes of the same call and
	// result, so the port keeps the reference's grouping.
	const uuidLine = "Request req_011CXyz9 failed for https://api.example.com/v1/x?id=3 session 0F8FAD5B-D9CB-469F-A165-70867728950E commit 3f2a9c1d7e agent toolu_01ABC and a0123456789abcdef at 0x7FFE ===== retry 3.2.1 of 10 in main.go"
	for _, test := range []struct {
		name, tool, command, result, signature string
	}{
		{
			name: "past cd and export", tool: "Bash",
			command: "cd /src && export GOFLAGS=-mod=mod && go vet ./...",
			result:  "Exit code 1\ngo: cannot find main module",
			// A line that starts with the program's name is its diagnostic.
			signature: "Bash | go vet | Exit code 1: go: cannot find main module",
		},
		{
			// The apostrophe sits in a comment, so it opens no quote.
			name: "past a comment line", tool: "Bash",
			command:   "# don't skip the cache\ngo test ./...",
			result:    "Exit code 1\nFAIL\tgithub.com/x/shop 0.5s",
			signature: "Bash | go test | Exit code 1: FAIL github.com<path> <n>s",
		},
		{
			name: "past a comment after a command", tool: "Bash",
			command:   "go build ./... # it's slow\ngo vet ./...",
			result:    "Exit code 1\ngo: cannot find main module",
			signature: "Bash | go vet | Exit code 1: go: cannot find main module",
		},
		{
			name: "git -C a directory", tool: "Bash",
			command:   "git -C /Users/me/Development/shop push origin HEAD",
			result:    "Exit code 1\nerror: failed to push some refs to 'github.com:x/shop.git'",
			signature: "Bash | git push | Exit code 1: error: failed to push some refs to 'github.com:x<path>'",
		},
		{
			name: "gh with two subcommands", tool: "Bash",
			command:   "gh pr view 12 --json state",
			result:    "Exit code 1\nGraphQL: Could not resolve to a PullRequest with the number of 12.",
			signature: "Bash | gh pr view | Exit code 1",
		},
		{
			// datamine.py's program() turns { into a space before it reads ${DISPATCH_BIN:-dispatch},
			// so the program reads as "$".
			name: "dispatch through DISPATCH_BIN", tool: "Bash",
			command:   `${DISPATCH_BIN:-dispatch} ask ENG-12 "Which limit?"`,
			result:    "Exit code 1\ndispatch: no ticket ENG-12",
			signature: "Bash | $ | Exit code 1",
		},
		{
			name: "past an assignment and env", tool: "Bash",
			command:   "GOFLAGS=-count=1 env go test ./...",
			result:    "Exit code 1\nFAIL\tgithub.com/x/shop 0.5s",
			signature: "Bash | go test | Exit code 1: FAIL github.com<path> <n>s",
		},
		{
			// The shell reports the exit status of the last list element, not of its pipeline's tail.
			name: "the last list element on an exit code", tool: "Bash",
			command:   "make build && go test ./... 2>&1 | tail -20",
			result:    "Exit code 1\nFAIL\tgithub.com/x/shop 0.5s",
			signature: "Bash | go test | Exit code 1: FAIL github.com<path> <n>s",
		},
		{
			name: "the first command on any other error", tool: "Bash",
			command:   "make build && go test ./... 2>&1 | tail -20",
			result:    "<tool_use_error>Error: Command timed out after 2m 0s</tool_use_error>",
			signature: "Bash | make build | Error: Command timed out after <n>m <n>s",
		},
		{
			name: "not a command in a heredoc body", tool: "Bash",
			command:   "git commit -F - <<'EOF'\nFix the cart\n\nmake test\nEOF",
			result:    "Exit code 1\nerror: gpg failed to sign the data",
			signature: "Bash | git commit | Exit code 1: error: gpg failed to sign the data",
		},
		{
			name: "setup alone when a heredoc holds the rest", tool: "Bash",
			command:   "source <<EOF\ngo vet ./...\nEOF",
			result:    "Error: source failed",
			signature: "Bash | source | Error: source failed",
		},
		{
			name: "the shell for an eval diagnostic", tool: "Bash",
			command:   "frobnicate --all",
			result:    "Exit code 127\n(eval):1: command not found: frobnicate",
			signature: "Bash | (shell) | Exit code 127: (eval):<n>: command not found: frobnicate",
		},
		{
			name: "the shell for a zsh diagnostic", tool: "Bash",
			command:   "cd /x && nosuch",
			result:    "Exit code 127\nzsh: command not found: nosuch",
			signature: "Bash | (shell) | Exit code 127: zsh: command not found: nosuch",
		},
		{
			// An a-prefixed agent id is also a hex word with a digit and a letter, so the sha rule,
			// which runs first, takes it.
			name: "placeholders for ids, urls, shas and numbers", tool: "Bash",
			command:   "echo hi",
			result:    uuidLine,
			signature: "Bash | echo | Request <id> failed for <url> session <uuid> commit <sha> agent <id> and <sha> at <hex> == retry <n> of <n> in <file>",
		},
		{
			name: "no program for another tool", tool: "WebFetch",
			result:    uuidLine,
			signature: "WebFetch | Request <id> failed for <url> session <uuid> commit <sha> agent <id> and <sha> at <hex> == retry <n> of <n> in <file>",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, project := transcripts(t)
			input := map[string]any{"command": test.command}
			if test.tool != "Bash" {
				input = map[string]any{"url": "https://example.com"}
			}
			writeTranscript(t, filepath.Join(project, "12345678-9999.jsonl"),
				toolCall(inWindow, "/tmp/x", "s1", test.tool, input), toolResult(inWindow, "s1", test.result, true),
				toolCall(laterOn, "/tmp/x", "s2", test.tool, input), toolResult(laterOn, "s2", test.result, true),
			)

			out := collectJSON(t, d)

			require.Len(t, out.ToolFailures, 1)
			assert.Equal(t, test.signature, out.ToolFailures[0].Signature)
			assert.Equal(t, 2, out.ToolFailures[0].Count)
		})
	}
}

func TestCollectAgentFailuresTrimsALongExample(t *testing.T) {
	// The example is "<subject>\n=> <result>", whose first 26 characters here are
	// "make build\n=> Exit code 2\n".
	for _, test := range []struct {
		name, result, example string
	}{
		{
			name:    "longer than 700 characters keeps the first 400 and the last 300",
			result:  "Exit code 2\n" + strings.Repeat("é", 500) + strings.Repeat("z", 500),
			example: "make build\n=> Exit code 2\n" + strings.Repeat("é", 374) + " … " + strings.Repeat("z", 300),
		},
		{
			name:    "700 characters stays whole",
			result:  "Exit code 2\n" + strings.Repeat("y", 674),
			example: "make build\n=> Exit code 2\n" + strings.Repeat("y", 674),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, project := transcripts(t)
			writeTranscript(t, filepath.Join(project, "12345678-9999.jsonl"),
				bash(inWindow, "/tmp/x", "t1", "make build"), toolResult(inWindow, "t1", test.result, true),
				bash(laterOn, "/tmp/x", "t2", "make build"), toolResult(laterOn, "t2", test.result, true),
			)

			out := collectJSON(t, d)

			require.Len(t, out.ToolFailures, 1)
			assert.Equal(t, test.example, out.ToolFailures[0].Example)
		})
	}
}

func TestCollectAgentFailuresSeparatesDenialKinds(t *testing.T) {
	d, project := transcripts(t)
	safety := "Permission for this command was denied by a built-in Claude Code safety check: rm -rf"
	hook := toolResult(inWindow, "d3", "Blocked by hook", true)
	hook["toolDenialKind"] = "hook"
	notTrue := func(at, id string) map[string]any {
		r := toolResult(at, id, "Exit code 2\nmake: *** lint", true)
		r["message"].(map[string]any)["content"].([]any)[0].(map[string]any)["is_error"] = "true"
		return r
	}
	writeTranscript(t, filepath.Join(project, "abcd1234-0001.jsonl"),
		bash(inWindow, "/tmp/x", "d1", "cd /tmp/x && rm -rf build"), toolResult(inWindow, "d1", safety, true),
		bash(laterOn, "/tmp/x", "d2", "cd /tmp/x && rm -rf build"), toolResult(laterOn, "d2", safety, true),
		toolCall(inWindow, "/tmp/x", "d3", "Read", map[string]any{"file_path": "/tmp/x/secret.txt"}), hook,
		toolCall(inWindow, "/tmp/x", "d4", "WebFetch", map[string]any{"url": "https://example.com"}),
		toolResult(inWindow, "d4", "The server-side auto mode classifier gave no verdict for this action.", true),
		// is_error as the string "true" is not a failure.
		bash(inWindow, "/tmp/x", "s1", "make lint"), notTrue(inWindow, "s1"),
		bash(inWindow, "/tmp/x", "s2", "make lint"), notTrue(inWindow, "s2"),
		// The first result read for a call is the one that counts.
		bash(inWindow, "/tmp/x", "r1", "make vet"), toolResult(inWindow, "r1", "ok", false),
		toolResult(laterOn, "r1", "Exit code 2\nmake: *** vet", true),
		bash(inWindow, "/tmp/x", "r2", "make vet"), toolResult(inWindow, "r2", "ok", false),
		toolResult(laterOn, "r2", "Exit code 2\nmake: *** vet", true),
	)

	out := collectJSON(t, d)

	assert.Empty(t, out.ToolFailures)
	assert.Equal(t, 0, out.Dropped.Signatures)
	// A denial seen once stays in its table.
	require.Len(t, out.PermissionDenials, 3)
	assert.Equal(t, "safety-check | Bash | rm", out.PermissionDenials[0].Signature)
	assert.Equal(t, 2, out.PermissionDenials[0].Count)
	assert.Equal(t, "classifier-no-verdict | WebFetch", out.PermissionDenials[1].Signature)
	assert.Equal(t, "toolDenialKind=hook | Read", out.PermissionDenials[2].Signature)
}

func TestCollectAgentFailuresPrefersTheHandbackReport(t *testing.T) {
	d, project := transcripts(t)
	require.NoError(t, os.MkdirAll(filepath.Join(d.Home, ".claude"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Home, ".claude", "settings.json"),
		[]byte(`{"permissions":{"deny":["Bash(git push:*)"]}}`), 0o644))
	// The subagent's last text comes after its handback, and the handback is still its report.
	writeTranscript(t, filepath.Join(project, "abcd1234-0001", "subagents", "agent-both.jsonl"),
		bash(inWindow, "/tmp/x", "h1", "git push origin main"),
		toolResult(inWindow, "h1", "Permission to use Bash with command git push origin main has been denied.", true),
		toolCall(laterOn, "/tmp/x", "h2", "SubagentHandback", map[string]any{"message": "Pushed nothing: the push was denied."}),
		toolResult(laterOn, "h2", "ok", false),
		assistantText(laterOn, "All done."),
	)

	out := collectJSON(t, d)

	require.Len(t, out.SubagentDenials, 1)
	row := out.SubagentDenials[0]
	assert.Equal(t, "SubagentHandback", row.ReportSource)
	assert.Equal(t, "denied", row.ReportDenialWords)
	assert.Equal(t, "no", row.ReportNamesFile)
	assert.Equal(t, "git push origin main", row.Denied)
	assert.Equal(t, "", row.NextActionOnSameFile)
}

func TestCollectAgentFailuresCountsEditsPerAgent(t *testing.T) {
	d, project := transcripts(t)
	edit := func(at, id, tool, key, file string) []map[string]any {
		return []map[string]any{toolCall(at, "/tmp/x", id, tool, map[string]any{key: file}), toolResult(at, id, "ok", false)}
	}
	var main, sub []map[string]any
	// Three edits of cart.go by the session and three by its subagent make no row.
	for _, id := range []string{"m1", "m2", "m3"} {
		main = append(main, edit(inWindow, id, "Edit", "file_path", "/tmp/x/cart.go")...)
		sub = append(sub, edit(inWindow, "n"+id, "Edit", "file_path", "/tmp/x/cart.go")...)
	}
	// MultiEdit and Edit of one file add up.
	for _, id := range []string{"o1", "o2", "o3"} {
		main = append(main, edit(inWindow, id, "MultiEdit", "file_path", "/tmp/x/orders.go")...)
	}
	for _, id := range []string{"p1", "p2"} {
		main = append(main, edit(laterOn, id, "Edit", "file_path", "/tmp/x/orders.go")...)
	}
	for _, id := range []string{"b1", "b2", "b3", "b4", "b5"} {
		sub = append(sub, edit(inWindow, id, "NotebookEdit", "notebook_path", "/tmp/x/plot.ipynb")...)
	}
	writeTranscript(t, filepath.Join(project, "eeee5555-0002.jsonl"), main...)
	writeTranscript(t, filepath.Join(project, "eeee5555-0002", "subagents", "agent-ed.jsonl"), sub...)

	out := collectJSON(t, d)

	require.Len(t, out.RepeatedEdits, 2)
	assert.Equal(t, "main", out.RepeatedEdits[0].Agent)
	assert.Equal(t, "/tmp/x/orders.go", out.RepeatedEdits[0].File)
	assert.Equal(t, 5, out.RepeatedEdits[0].Edits)
	assert.Equal(t, "agent-ed", out.RepeatedEdits[1].Agent)
	assert.Equal(t, "/tmp/x/plot.ipynb", out.RepeatedEdits[1].File)
	assert.Equal(t, 5, out.RepeatedEdits[1].Edits)
}

func TestCollectAgentFailuresPrintsTheSameRowsAsJSONAndMarkdown(t *testing.T) {
	d, project := transcripts(t)
	session := filepath.Join(project, "abcd1234-0001")
	// A row of each table, with a | and a newline in its cells.
	records := []map[string]any{
		bash(inWindow, "/tmp/x", "f1", "go test ./... | tail -5"), toolResult(inWindow, "f1", "Exit code 1\nFAIL\tx 0.1s", true),
		bash(laterOn, "/tmp/x", "f2", "go test ./... | tail -5"), toolResult(laterOn, "f2", "Exit code 1\nFAIL\tx 0.1s", true),
	}
	for _, id := range []string{"e1", "e2", "e3", "e4", "e5"} {
		records = append(records, toolCall(inWindow, "/tmp/x", id, "Edit", map[string]any{"file_path": "/tmp/x/cart.go"}), toolResult(inWindow, id, "ok", false))
	}
	writeTranscript(t, session+".jsonl", records...)
	writeTranscript(t, filepath.Join(session, "subagents", "agent-a1.jsonl"),
		toolCall(inWindow, "/tmp/x", "g1", "Read", map[string]any{"file_path": "/tmp/x/.env"}),
		toolResult(inWindow, "g1", "The user doesn't want to proceed with this tool use.", true),
		assistantText(laterOn, "Finished | nothing to report."),
	)

	res := d.Run("collect-agent-failures", "--json")
	require.NoError(t, res.Err, res.Stderr)
	var tables map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &tables))
	md := d.Run("collect-agent-failures")
	require.NoError(t, md.Err, md.Stderr)

	escape := strings.NewReplacer("|", `\|`, "\n", "⏎ ")
	failureCols := []string{"signature", "count", "sessions", "repos", "first_seen", "last_seen", "example"}
	for _, table := range []struct {
		key, title string
		cols       []string
	}{
		{key: "tool_failures", title: "1. Tool failures", cols: failureCols},
		{key: "permission_denials", title: "1b. Permission denials", cols: failureCols},
		{key: "subagent_denials", title: "2a. Subagent permission denials", cols: []string{"session", "agent", "description", "time",
			"kind", "denied", "next_action_on_same_file", "report_names_file", "report_denial_words", "report_source"}},
		{key: "repeated_edits", title: "2b. Files edited 5+ times by one agent or session", cols: []string{"session", "agent", "repo", "file", "edits", "first", "last"}},
	} {
		t.Run(table.key, func(t *testing.T) {
			var rows []map[string]any
			require.NoError(t, json.Unmarshal(tables[table.key], &rows))
			require.Len(t, rows, 1)
			var want strings.Builder
			fmt.Fprintf(&want, "## %s (1)\n\n| %s |\n|%s\n", table.title, strings.Join(table.cols, " | "), strings.Repeat("---|", len(table.cols)))
			var cells []string
			for _, c := range table.cols {
				cells = append(cells, escape.Replace(fmt.Sprint(rows[0][c])))
			}
			want.WriteString("| " + strings.Join(cells, " | ") + " |\n")
			assert.Contains(t, md.Stdout, want.String())
		})
	}
}

func TestCollectAgentFailuresKeepsASessionThatReadDispatchUsage(t *testing.T) {
	d, project := transcripts(t)
	usage := d.Run("no-such-command")
	require.Error(t, usage.Err)
	require.Contains(t, usage.Stderr, "collect-agent-failures")
	// A session that ran dispatch wrongly got its usage text back, and its failures still count.
	writeTranscript(t, filepath.Join(project, "33333333-9999.jsonl"),
		bash(inWindow, "/tmp/x", "u1", "dispatch"), toolResult(inWindow, "u1", "Exit code 1\n"+usage.Stderr, true),
		bash(inWindow, "/tmp/x", "u2", "dispatch"), toolResult(inWindow, "u2", "Exit code 1\n"+usage.Stderr, true),
	)

	out := collectJSON(t, d)

	assert.Empty(t, out.SkippedSessions)
	require.Len(t, out.ToolFailures, 1)
	assert.Equal(t, 2, out.ToolFailures[0].Count)
}

func TestCollectAgentFailuresSkipsAnUnreadableTranscript(t *testing.T) {
	d, project := transcripts(t)
	failTwice := []map[string]any{
		bash(inWindow, "/tmp/x", "r1", "make build"), toolResult(inWindow, "r1", "Exit code 2\nmake: *** stop", true),
		bash(inWindow, "/tmp/x", "r2", "make build"), toolResult(inWindow, "r2", "Exit code 2\nmake: *** stop", true),
	}
	writeTranscript(t, filepath.Join(project, "44444444-aaaa.jsonl"), failTwice...)
	locked := filepath.Join(project, "55555555-bbbb.jsonl")
	writeTranscript(t, locked, failTwice...)
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	res := d.Run("collect-agent-failures", "--json")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stderr, "dispatch: skipped open "+locked)
	var out failuresJSON
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &out))
	require.Len(t, out.ToolFailures, 1)
	assert.Equal(t, 2, out.ToolFailures[0].Count)
}

func TestCollectAgentFailuresPrintsEmptyTablesAsEmptyLists(t *testing.T) {
	d, _ := transcripts(t)

	res := d.Run("collect-agent-failures", "--json")

	require.NoError(t, res.Err)
	var out map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &out))
	for _, table := range []string{"tool_failures", "permission_denials", "subagent_denials", "repeated_edits", "skipped_sessions"} {
		assert.Equal(t, "[]", string(out[table]), table)
	}
}

func TestCollectAgentFailuresBreaksErrorLinesAtCarriageReturns(t *testing.T) {
	d, project := transcripts(t)
	// A progress meter ends its updates with a bare \r, and the diagnostic after it still names
	// the failure.
	writeTranscript(t, filepath.Join(project, "66666666-cccc.jsonl"),
		bash(inWindow, "/tmp/x", "p1", "git clone https://example.com/a.git"),
		toolResult(inWindow, "p1", "Exit code 128\nCloning into 'a'...\rfatal: repository not found", true),
		bash(inWindow, "/tmp/x", "p2", "git clone https://example.com/b.git"),
		toolResult(inWindow, "p2", "Exit code 128\nCloning into 'b'...\rfatal: repository not found", true),
	)

	out := collectJSON(t, d)

	require.Len(t, out.ToolFailures, 1)
	assert.Equal(t, "Bash | git clone | Exit code 128: fatal: repository not found", out.ToolFailures[0].Signature)
}

func TestCollectAgentFailuresIgnoresABlankDenyRule(t *testing.T) {
	d, project := transcripts(t)
	require.NoError(t, os.MkdirAll(filepath.Join(d.Home, ".claude"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Home, ".claude", "settings.json"),
		[]byte(`{"permissions":{"deny":["Bash( :*)"]}}`), 0o644))
	writeTranscript(t, filepath.Join(project, "77777777-dddd", "subagents", "agent-x.jsonl"),
		bash(inWindow, "/tmp/x", "b1", "rm a.go"),
		toolResult(inWindow, "b1", "Permission for this command was denied by a built-in Claude Code safety check", true),
	)

	out := collectJSON(t, d)

	require.Len(t, out.SubagentDenials, 1)
	assert.Equal(t, "safety-check", out.SubagentDenials[0].Kind)
}

func TestCollectAgentFailuresReadsTheRecordsOwnTime(t *testing.T) {
	d, project := transcripts(t)
	// The call's input holds an older timestamp, which json.Marshal writes ahead of the record's
	// own because "message" sorts before "timestamp". The record's own time is in the window.
	nested := func(id string) map[string]any {
		return toolCall(inWindow, "/tmp/x", id, "Bash", map[string]any{"command": "make build", "timestamp": beforeWeek})
	}
	writeTranscript(t, filepath.Join(project, "88888888-eeee.jsonl"),
		nested("n1"), toolResult(inWindow, "n1", "Exit code 2\nmake: *** stop", true),
		nested("n2"), toolResult(inWindow, "n2", "Exit code 2\nmake: *** stop", true),
	)

	out := collectJSON(t, d)

	require.Len(t, out.ToolFailures, 1)
	assert.Equal(t, 2, out.ToolFailures[0].Count)
}

func TestCollectAgentFailuresReadsJSONWrittenWithSpaces(t *testing.T) {
	d, project := transcripts(t)
	require.NoError(t, os.MkdirAll(project, 0o755))
	// Claude Code writes compact JSON, but a record with spaces after its colons is the same
	// record.
	var b strings.Builder
	for _, r := range []map[string]any{
		bash(inWindow, "/tmp/x", "s1", "make build"), toolResult(inWindow, "s1", "Exit code 2\nmake: *** stop", true),
		bash(laterOn, "/tmp/x", "s2", "make build"), toolResult(laterOn, "s2", "Exit code 2\nmake: *** stop", true),
	} {
		line, err := json.MarshalIndent(r, "", "")
		require.NoError(t, err)
		b.WriteString(strings.ReplaceAll(string(line), "\n", "") + "\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(project, "99999999-ffff.jsonl"), []byte(b.String()), 0o644))

	out := collectJSON(t, d)

	require.Len(t, out.ToolFailures, 1)
	assert.Equal(t, 2, out.ToolFailures[0].Count)
}

func TestCollectAgentFailuresReadsARecordWithSomeFieldsSpaced(t *testing.T) {
	d, project := transcripts(t)
	require.NoError(t, os.MkdirAll(project, 0o755))
	// The timestamp is compact and the type is spaced, so neither byte pattern speaks for the
	// whole line.
	var b strings.Builder
	for _, r := range []map[string]any{
		bash(inWindow, "/tmp/x", "s1", "make build"), toolResult(inWindow, "s1", "Exit code 2\nmake: *** stop", true),
		bash(laterOn, "/tmp/x", "s2", "make build"), toolResult(laterOn, "s2", "Exit code 2\nmake: *** stop", true),
	} {
		line, err := json.Marshal(r)
		require.NoError(t, err)
		b.WriteString(strings.NewReplacer(`"type":"user"`, `"type": "user"`, `"type":"assistant"`, `"type": "assistant"`).Replace(string(line)) + "\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(project, "99999999-eeee.jsonl"), []byte(b.String()), 0o644))

	out := collectJSON(t, d)

	require.Len(t, out.ToolFailures, 1)
	assert.Equal(t, 2, out.ToolFailures[0].Count)
}
