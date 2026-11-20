// Package transcript reads Claude Code's transcripts: the JSONL files Claude Code writes under
// ~/.claude/projects, one per session, with each subagent's in its own file under the session.
//
// A transcript is Claude's own file. Dispatch reads it and never writes it.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/environment"
)

// Dir is the directory Claude Code writes transcripts to: $CLAUDE_CONFIG_DIR/projects, or
// ~/.claude/projects when CLAUDE_CONFIG_DIR is unset.
func Dir(env environment.Environment) string {
	if dir := env.Get("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	return env.ExpandHome("~/.claude/projects")
}

// Agent names who made a tool call: a session, and a subagent in it. Agent is empty for the
// session's main thread.
type Agent struct {
	Session string
	Agent   string
}

// ToolCall is one tool_use block an assistant record holds.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
	Agent
	// Cwd is the directory the session ran in when it made the call.
	Cwd string
	// At is the record's timestamp as Claude Code wrote it, an RFC 3339 time in UTC.
	At string
}

// ToolResult is the tool_result block that answers a tool call.
type ToolResult struct {
	// IsError is true only when the block's is_error is true. Claude Code marks a failed or
	// denied call this way, and error-looking text in a result without it is a success.
	IsError bool
	Text    string
	At      string
	// DenialKind is the record's toolDenialKind, which Claude Code sets on some denials.
	DenialKind string
}

// Report is the last thing a subagent said: the message of its SubagentHandback call, through
// which a subagent hands its final report back, and the text of its last assistant text block.
type Report struct {
	Handback string
	Text     string
}

// Window is the stretch of time a read covers. A record counts when From <= its time <= To.
type Window struct {
	From, To time.Time
}

// Sessions is what a read found in the transcripts within its window.
type Sessions struct {
	// Calls is every tool call, once per tool_use_id: subagent files repeat records, and the
	// first file in path order that holds a call owns it.
	Calls map[string]ToolCall
	// CallOrder is the tool_use_ids of Calls in the order they were read.
	CallOrder []string
	// Results is the first result read for each tool_use_id.
	Results map[string]ToolResult
	// ResultOrder is the tool_use_ids of Results in the order they were read.
	ResultOrder []string
	// AgentCalls is each agent's tool_use_ids in the order the agent made them.
	AgentCalls map[Agent][]string
	// Reports is each agent's last words, keyed like AgentCalls.
	Reports map[Agent]Report
	// Descriptions is each subagent's description, from the .meta.json beside its transcript.
	Descriptions map[Agent]string
	// Skipped is the sessions left out because a line of theirs holds a skip marker, sorted.
	Skipped []string
	// Files is how many transcript files the read opened.
	Files int
}

// Read reads every transcript under dir and keeps the records within window. A session with a
// line holding any of the skip markers, at any time, is left out whole.
func Read(dir string, window Window, skipMarkers ...string) (Sessions, error) {
	s := Sessions{
		Calls:        map[string]ToolCall{},
		Results:      map[string]ToolResult{},
		AgentCalls:   map[Agent][]string{},
		Reports:      map[Agent]Report{},
		Descriptions: map[Agent]string{},
	}
	paths, err := transcriptFiles(dir)
	if err != nil {
		return s, err
	}
	skipped := map[string]bool{}
	for _, path := range paths {
		who := identify(dir, path)
		s.Files++
		hit, err := s.readFile(path, who, window, skipMarkers)
		if err != nil {
			return s, err
		}
		if hit {
			skipped[who.Session] = true
		}
	}
	s.drop(skipped)
	return s, nil
}

// transcriptFiles lists the .jsonl files under dir in path order, leaving out hidden files and
// directories. A missing dir holds no transcripts.
func transcriptFiles(dir string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == dir {
				return filepath.SkipAll
			}
			return err
		}
		if path != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

var agentFile = regexp.MustCompile(`agent-(\w+)\.jsonl$`)

// identify names the agent a transcript file belongs to. A session's own file is
// <project>/<session>.jsonl, and its subagents' are under <project>/<session>/.
func identify(dir, path string) Agent {
	rel, _ := filepath.Rel(dir, path)
	parts := strings.Split(rel, string(filepath.Separator))
	var who Agent
	switch {
	case len(parts) == 2:
		who.Session = strings.TrimSuffix(parts[1], ".jsonl")
	case len(parts) > 2:
		who.Session = parts[1]
	}
	if m := agentFile.FindStringSubmatch(path); m != nil {
		who.Agent = m[1]
	}
	return who
}

var (
	timestampField = []byte(`"timestamp":"`)
	userType       = []byte(`"type":"user"`)
	assistantType  = []byte(`"type":"assistant"`)
)

// readFile reads one transcript and reports whether a line holds a skip marker.
func (s *Sessions) readFile(path string, who Agent, window Window, skipMarkers []string) (bool, error) {
	if who.Agent != "" {
		s.readDescription(path, who)
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	hit := false
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if !hit && holdsAny(line, skipMarkers) {
				hit = true
			}
			s.readLine(line, who, window)
		}
		if errors.Is(err, io.EOF) {
			return hit, nil
		}
		if err != nil {
			return hit, err
		}
	}
}

func holdsAny(line []byte, markers []string) bool {
	for _, m := range markers {
		if bytes.Contains(line, []byte(m)) {
			return true
		}
	}
	return false
}

func (s *Sessions) readDescription(path string, who Agent) {
	data, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json")
	if err != nil {
		return
	}
	var meta struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(data, &meta) == nil {
		s.Descriptions[who] = meta.Description
	}
}

type record struct {
	Type           string          `json:"type"`
	Timestamp      string          `json:"timestamp"`
	Cwd            string          `json:"cwd"`
	ToolDenialKind json.RawMessage `json:"toolDenialKind"`
	Message        struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   json.RawMessage `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// readLine keeps one record when it is a user or assistant record within the window. It skips a
// line whose first timestamp falls before the window before decoding it, since most of a week's
// transcripts on disk are older than the window.
func (s *Sessions) readLine(line []byte, who Agent, window Window) {
	i := bytes.Index(line, timestampField)
	if i < 0 {
		return
	}
	if at, ok := leadingTime(line[i+len(timestampField):]); !ok || at.Before(window.From) {
		return
	}
	if !bytes.Contains(line, userType) && !bytes.Contains(line, assistantType) {
		return
	}
	var rec record
	if json.Unmarshal(line, &rec) != nil || (rec.Type != "user" && rec.Type != "assistant") {
		return
	}
	at, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
	if err != nil || at.Before(window.From) || at.After(window.To) {
		return
	}
	var blocks []block
	_ = json.Unmarshal(rec.Message.Content, &blocks)
	if rec.Type == "assistant" {
		s.readAssistant(rec, blocks, who)
		return
	}
	s.readUser(rec, blocks)
}

func leadingTime(rest []byte) (time.Time, bool) {
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, string(rest[:end]))
	return at, err == nil
}

func (s *Sessions) readAssistant(rec record, blocks []block, who Agent) {
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				r := s.Reports[who]
				r.Text = b.Text
				s.Reports[who] = r
			}
		case "tool_use":
			if b.Name == "SubagentHandback" {
				r := s.Reports[who]
				r.Handback = handbackMessage(b.Input)
				s.Reports[who] = r
			}
			if _, seen := s.Calls[b.ID]; seen {
				continue
			}
			s.Calls[b.ID] = ToolCall{ID: b.ID, Name: b.Name, Input: b.Input, Agent: who, Cwd: rec.Cwd, At: rec.Timestamp}
			s.CallOrder = append(s.CallOrder, b.ID)
			s.AgentCalls[who] = append(s.AgentCalls[who], b.ID)
		}
	}
}

func handbackMessage(input json.RawMessage) string {
	var in map[string]json.RawMessage
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	return String(in["message"])
}

func (s *Sessions) readUser(rec record, blocks []block) {
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		if _, seen := s.Results[b.ToolUseID]; seen {
			continue
		}
		s.Results[b.ToolUseID] = ToolResult{
			IsError:    string(bytes.TrimSpace(b.IsError)) == "true",
			Text:       resultText(b.Content),
			At:         rec.Timestamp,
			DenialKind: String(rec.ToolDenialKind),
		}
		s.ResultOrder = append(s.ResultOrder, b.ToolUseID)
	}
}

// resultText is a tool result's text: the content itself when it is a string, or its text
// blocks joined by newlines.
func resultText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []block
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// String is a JSON value as text: a string's contents, the JSON itself for any other value,
// and empty for null or nothing.
func String(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

func (s *Sessions) drop(skipped map[string]bool) {
	for session := range skipped {
		s.Skipped = append(s.Skipped, session)
	}
	sort.Strings(s.Skipped)
	if len(skipped) == 0 {
		return
	}
	order := s.CallOrder[:0]
	for _, id := range s.CallOrder {
		if skipped[s.Calls[id].Session] {
			delete(s.Calls, id)
			continue
		}
		order = append(order, id)
	}
	s.CallOrder = order
	for who := range s.AgentCalls {
		if skipped[who.Session] {
			delete(s.AgentCalls, who)
			delete(s.Reports, who)
		}
	}
	for who := range s.Reports {
		if skipped[who.Session] {
			delete(s.Reports, who)
		}
	}
}
