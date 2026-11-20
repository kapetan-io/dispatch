package dispatch_test

import (
	"encoding/json"
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
		Failures   int `json:"failures"`
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
	assert.Equal(t, 1, out.Dropped.Failures)
	assert.Empty(t, out.PermissionDenials)

	res := d.Run("collect-agent-failures")
	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "# Agent failures from 2026-10-02T12:00:00Z to 2026-10-09T12:00:00Z\n")
	assert.Contains(t, res.Stdout, "## 1. Tool failures (1)\n")
	assert.Contains(t, res.Stdout, "| Bash \\| go test \\| Exit code 1: FAIL github.com<path> <n>s | 2 | 2 | shop |")
	assert.Contains(t, res.Stdout, "Dropped 1 signatures seen once, holding 1 failures.\n")
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
	assert.Equal(t, 0, out.Dropped.Failures)
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
		assistantText(beforeWeek, "DAILY-PATTERN-MINING run"),
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
