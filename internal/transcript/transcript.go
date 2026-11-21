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
	// Files is how many transcript files the read read to the end.
	Files int
	// PRCreators is, for each PR URL a successful `gh pr create` printed, the agents that made
	// that call. It is read from every record on disk, whatever the window.
	PRCreators map[string]map[Agent]bool
	// BranchSessions is, for each git branch a record names other than main, master and HEAD, the
	// sessions that ran on it, read from every record on disk.
	BranchSessions map[string]map[string]bool
	// Oldest is the time of the oldest record on disk, zero when there is none. Claude Code
	// deletes old transcripts, so nothing before it can be found.
	Oldest time.Time
	// Unreadable is each transcript or directory the read could not open or finish, with why.
	// Claude Code deletes old transcripts, so one can vanish between the listing and the read. The
	// records read before an error are kept.
	Unreadable []string
}

// Read reads every transcript under dir and keeps the records within window. A session with a
// line holding any of the skip markers, at any time, is left out whole. A file or directory it
// cannot read is listed in Unreadable, not returned as an error.
func Read(dir string, window Window, skipMarkers ...string) Sessions {
	s := Sessions{
		Calls:          map[string]ToolCall{},
		Results:        map[string]ToolResult{},
		AgentCalls:     map[Agent][]string{},
		Reports:        map[Agent]Report{},
		Descriptions:   map[Agent]string{},
		PRCreators:     map[string]map[Agent]bool{},
		BranchSessions: map[string]map[string]bool{},
	}
	paths, unreadable := transcriptFiles(dir)
	s.Unreadable = unreadable
	skipped := map[string]bool{}
	// prCreates is the agent that made each `gh pr create` call, by tool_use_id. Like Calls, the
	// first file in path order that holds a call owns it, so a subagent file that repeats its
	// parent's records never claims the parent's PR.
	prCreates := map[string]Agent{}
	for _, path := range paths {
		who := identify(dir, path)
		hit, err := s.readFile(path, who, window, skipMarkers, prCreates)
		if hit {
			skipped[who.Session] = true
		}
		if err != nil {
			s.Unreadable = append(s.Unreadable, err.Error())
			continue
		}
		s.Files++
	}
	s.drop(skipped)
	return s
}

// transcriptFiles lists the .jsonl files under dir in path order, leaving out hidden files and
// directories, and what it could not list. A missing dir holds no transcripts.
func transcriptFiles(dir string) (paths, unreadable []string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if !(errors.Is(err, fs.ErrNotExist) && path == dir) {
				unreadable = append(unreadable, err.Error())
			}
			if d != nil && d.IsDir() && path != dir {
				return filepath.SkipDir
			}
			return nil
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
	return paths, unreadable
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
	timestampWord  = []byte(`"timestamp"`)
	timestampField = []byte(`"timestamp":"`)
	userWord       = []byte(`"user"`)
	assistantWord  = []byte(`"assistant"`)
)

// readFile reads one transcript and reports whether a line holds a skip marker.
func (s *Sessions) readFile(path string, who Agent, window Window, skipMarkers []string, prCreates map[string]Agent) (bool, error) {
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
			s.readSessionLinks(line, who, prCreates)
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

var (
	anyTimestamp = regexp.MustCompile(`"timestamp"\s*:\s*"([^"]+)"`)
	gitBranch    = regexp.MustCompile(`"gitBranch"\s*:\s*"([^"]+)"`)
	// prURL takes every digit, so .../pull/12 never matches .../pull/123.
	prURL      = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/\d+`)
	ghPRCreate = []byte("gh pr create")
	pullPath   = []byte("/pull/")
)

// readSessionLinks reads what proves which session made a PR from any record, whatever its time:
// the oldest record's time, the branch the record names, and the PR URLs a successful `gh pr
// create` printed.
func (s *Sessions) readSessionLinks(line []byte, who Agent, prCreates map[string]Agent) {
	if !bytes.Contains(line, timestampWord) {
		return
	}
	if m := anyTimestamp.FindSubmatch(line); m != nil {
		if at, err := time.Parse(time.RFC3339Nano, string(m[1])); err == nil && (s.Oldest.IsZero() || at.Before(s.Oldest)) {
			s.Oldest = at
		}
	}
	if m := gitBranch.FindSubmatch(line); m != nil {
		if branch := string(m[1]); branch != "HEAD" && branch != "main" && branch != "master" {
			if s.BranchSessions[branch] == nil {
				s.BranchSessions[branch] = map[string]bool{}
			}
			s.BranchSessions[branch][who.Session] = true
		}
	}
	// Only a create call, or a result naming a PR after some create call was seen, can prove one.
	if !bytes.Contains(line, ghPRCreate) && (len(prCreates) == 0 || !bytes.Contains(line, pullPath)) {
		return
	}
	var rec record
	if json.Unmarshal(line, &rec) != nil || (rec.Type != "user" && rec.Type != "assistant") {
		return
	}
	var blocks []block
	_ = json.Unmarshal(rec.Message.Content, &blocks)
	for _, b := range blocks {
		switch {
		case b.Type == "tool_use":
			var in struct {
				Command string `json:"command"`
			}
			if _, seen := prCreates[b.ID]; !seen && json.Unmarshal(b.Input, &in) == nil && strings.Contains(in.Command, "gh pr create") {
				prCreates[b.ID] = who
			}
		case b.Type == "tool_result" && string(bytes.TrimSpace(b.IsError)) != "true":
			maker, ok := prCreates[b.ToolUseID]
			if !ok {
				continue
			}
			for _, url := range prURL.FindAllString(resultText(b.Content), -1) {
				if s.PRCreators[url] == nil {
					s.PRCreators[url] = map[Agent]bool{}
				}
				s.PRCreators[url][maker] = true
			}
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

// readLine keeps one record when it is a user or assistant record within the window. Most lines
// on disk predate the window or are other records, so the bytes are checked before a decode. A
// line without both a timestamp and a record type's name is skipped, and so is one whose single
// compact timestamp field is before the window. A line with more such fields may hold a nested
// record's time first, so it is decoded and judged by the record's own time.
func (s *Sessions) readLine(line []byte, who Agent, window Window) {
	if !bytes.Contains(line, timestampWord) || (!bytes.Contains(line, userWord) && !bytes.Contains(line, assistantWord)) {
		return
	}
	if bytes.Count(line, timestampField) == 1 {
		i := bytes.Index(line, timestampField)
		if at, ok := leadingTime(line[i+len(timestampField):]); ok && at.Before(window.From) {
			return
		}
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
			DenialKind: denialKind(rec.ToolDenialKind),
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

// denialKind is toolDenialKind when it is a string, and empty for any other value.
func denialKind(raw json.RawMessage) string {
	var kind string
	_ = json.Unmarshal(raw, &kind)
	return kind
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
		}
	}
	for who := range s.Reports {
		if skipped[who.Session] {
			delete(s.Reports, who)
		}
	}
	for url, agents := range s.PRCreators {
		for who := range agents {
			if skipped[who.Session] {
				delete(agents, who)
			}
		}
		if len(agents) == 0 {
			delete(s.PRCreators, url)
		}
	}
	for branch, sessions := range s.BranchSessions {
		for session := range sessions {
			if skipped[session] {
				delete(sessions, session)
			}
		}
		if len(sessions) == 0 {
			delete(s.BranchSessions, branch)
		}
	}
}
