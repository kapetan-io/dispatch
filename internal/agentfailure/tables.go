// Package agentfailure reports where agents went wrong in Claude Code transcripts: tool calls
// that failed, permission denials, subagents that hit a denial, and files edited again and
// again. It reports rows and counts and judges none of them.
package agentfailure

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/kapetan-io/dispatch/internal/transcript"
)

const (
	// RepeatedEdits is how many successful edits of one file by one agent make a row of the
	// repeated edits table.
	RepeatedEdits = 5
	// followCalls is how many of a subagent's later calls a denial row looks through for what
	// the subagent did next.
	followCalls = 15
)

// editTools are the tools that change a file.
var editTools = set("Edit", "Write", "MultiEdit", "NotebookEdit")

// Failure is a group of failed tool calls that share a signature.
type Failure struct {
	// Signature is the tool, the program a Bash call ran, and the first error line with its
	// paths, numbers and ids replaced by placeholders. A denial's signature is its kind, the
	// tool and the denied program instead.
	Signature string `json:"signature"`
	Count     int    `json:"count"`
	// Sessions is how many sessions made a call in the group.
	Sessions  int    `json:"sessions"`
	Repos     string `json:"repos"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	// Example is the first call of the group and its result, trimmed.
	Example string `json:"example"`
}

// SubagentDenial is one permission denial a subagent hit.
type SubagentDenial struct {
	Session     string `json:"session"`
	Agent       string `json:"agent"`
	Description string `json:"description"`
	Time        string `json:"time"`
	Kind        string `json:"kind"`
	// Denied is the denied commands of a Bash call, or what the call acted on.
	Denied string `json:"denied"`
	// NextActionOnSameFile is the subagent's next call, within its next 15, that named a file the
	// denied call named and was not itself denied, with whether it failed.
	NextActionOnSameFile string `json:"next_action_on_same_file"`
	// ReportNamesFile is the files the denied call named that the subagent's final report
	// names, or "no".
	ReportNamesFile string `json:"report_names_file"`
	// ReportDenialWords is the words about denial the final report holds, or "no".
	ReportDenialWords string `json:"report_denial_words"`
	// ReportSource is where the final report came from: SubagentHandback, "last text" or "none".
	ReportSource string `json:"report_source"`
}

// Silent reports whether the subagent's final report names neither the denied file nor any
// word about the denial.
func (d SubagentDenial) Silent() bool {
	return d.ReportNamesFile == "no" && d.ReportDenialWords == "no"
}

// RepeatedEdit is a file one agent edited RepeatedEdits or more times.
type RepeatedEdit struct {
	Session string `json:"session"`
	Agent   string `json:"agent"`
	Repo    string `json:"repo"`
	File    string `json:"file"`
	Edits   int    `json:"edits"`
	First   string `json:"first"`
	Last    string `json:"last"`
}

// Dropped counts the failure signatures seen only once, which the failures table leaves out.
type Dropped struct {
	Signatures int `json:"signatures"`
	Failures   int `json:"failures"`
}

// Report is the tables built from one read of the transcripts.
type Report struct {
	// ToolFailures is the failed calls that were not denials, grouped by signature, without the
	// signatures seen once.
	ToolFailures      []Failure        `json:"tool_failures"`
	DroppedSingletons Dropped          `json:"tool_failures_dropped_singletons"`
	PermissionDenials []Failure        `json:"permission_denials"`
	SubagentDenials   []SubagentDenial `json:"subagent_denials"`
	RepeatedEdits     []RepeatedEdit   `json:"repeated_edits"`
	// SkippedSessions is the sessions left out of every table, by their first 8 characters.
	SkippedSessions []string `json:"skipped_sessions"`
	TranscriptFiles int      `json:"transcript_files"`
}

// Collect builds the tables from what a read of the transcripts found. home is the user's home
// directory, against which a session's directory names its repo.
func Collect(s transcript.Sessions, rules DenyRules, home string) Report {
	failures, denied := failureTables(s, rules, home)
	r := Report{PermissionDenials: denied, TranscriptFiles: s.Files}
	for _, f := range failures {
		if f.Count == 1 {
			r.DroppedSingletons.Signatures++
			r.DroppedSingletons.Failures++
			continue
		}
		r.ToolFailures = append(r.ToolFailures, f)
	}
	r.SubagentDenials = subagentDenials(s, rules)
	r.RepeatedEdits = repeatedEdits(s, home)
	for _, session := range s.Skipped {
		r.SkippedSessions = append(r.SkippedSessions, short(session))
	}
	return r
}

type group struct {
	Failure
	sessions, repos map[string]bool
}

func failureTables(s transcript.Sessions, rules DenyRules, home string) (failures, denied []Failure) {
	groups, denials := map[string]*group{}, map[string]*group{}
	for _, id := range s.ResultOrder {
		res := s.Results[id]
		call, ok := s.Calls[id]
		if !ok || !res.IsError {
			continue
		}
		subj := subject(call)
		bash := call.Name == "Bash"
		var parts []string
		bucket := groups
		if kind := denialKind(res); kind != "" {
			head := ""
			if bash {
				if head = strings.Join(rules.matching(subj), ", "); head == "" {
					head = commandHead(subj, false)
				}
			}
			parts, bucket = []string{kind, call.Name, head}, denials
		} else {
			head := ""
			if bash {
				head = commandHead(subj, strings.HasPrefix(res.Text, "Exit code "))
			}
			err := errorSignature(head, res.Text)
			if shellDiagnostic.MatchString(err) {
				head = "(shell)"
			}
			parts = []string{call.Name, head, err}
		}
		sig := joinNonEmpty(parts, " | ")
		g := bucket[sig]
		if g == nil {
			g = &group{
				Failure:  Failure{Signature: sig, FirstSeen: res.At, LastSeen: res.At, Example: trim(subj + "\n=> " + res.Text)},
				sessions: map[string]bool{}, repos: map[string]bool{},
			}
			bucket[sig] = g
		}
		g.Count++
		g.sessions[short(call.Session)] = true
		g.repos[repo(home, call.Cwd)] = true
		g.FirstSeen, g.LastSeen = min(g.FirstSeen, res.At), max(g.LastSeen, res.At)
	}
	return failureRows(groups), failureRows(denials)
}

func failureRows(groups map[string]*group) []Failure {
	var rows []Failure
	for _, g := range groups {
		f := g.Failure
		f.Sessions = len(g.sessions)
		var repos []string
		for r := range g.repos {
			if r != "" {
				repos = append(repos, r)
			}
		}
		sort.Strings(repos)
		f.Repos = strings.Join(repos, ", ")
		f.FirstSeen, f.LastSeen = minute(f.FirstSeen), minute(f.LastSeen)
		rows = append(rows, f)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Signature < rows[j].Signature
	})
	return rows
}

var (
	pathish   = regexp.MustCompile(`[\p{L}\p{N}_.~/@+-]+`)
	extension = regexp.MustCompile(`[\p{L}\p{N}_]\.[A-Za-z][\p{L}\p{N}_]{0,5}$`)
)

// fileNames is the file names, base names with an extension, a call's subject names. For a
// Bash command that a deny rule matches, only the commands the rule's program starts count.
func fileNames(subj string, rules DenyRules) map[string]bool {
	var denied []string
	for _, c := range splitCommands(subj) {
		first := strings.Fields(c.text)
		for _, p := range rules {
			if len(first) > 0 && first[0] == strings.Fields(p)[0] {
				denied = append(denied, c.text)
				break
			}
		}
	}
	text := subj
	if joined := strings.Join(denied, " "); joined != "" {
		text = joined
	}
	names := map[string]bool{}
	for _, t := range pathish.FindAllString(text, -1) {
		t = strings.TrimRight(t, "/")
		base := t[strings.LastIndex(t, "/")+1:]
		if extension.MatchString(base) && len([]rune(base)) > 3 {
			names[base] = true
		}
	}
	return names
}

// denialWords are the words a report that mentions a denial uses.
var denialWords = []string{"blocked", "denied", "not allowed", "permission"}

func subagentDenials(s transcript.Sessions, rules DenyRules) []SubagentDenial {
	var agents []transcript.Agent
	for who := range s.AgentCalls {
		if who.Agent != "" {
			agents = append(agents, who)
		}
	}
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Session != agents[j].Session {
			return agents[i].Session < agents[j].Session
		}
		return agents[i].Agent < agents[j].Agent
	})
	var rows []SubagentDenial
	for _, who := range agents {
		ids := s.AgentCalls[who]
		parts := s.Reports[who]
		report, source := parts.Handback, "SubagentHandback"
		if report == "" {
			report, source = parts.Text, "last text"
		}
		if report == "" {
			source = "none"
		}
		lower := strings.ToLower(report)
		for i, id := range ids {
			res, ok := s.Results[id]
			if !ok || !res.IsError {
				continue
			}
			kind := denialKind(res)
			if kind == "" {
				continue
			}
			call := s.Calls[id]
			subj := subject(call)
			names := fileNames(subj, rules)
			var named, words []string
			for n := range names {
				if strings.Contains(report, n) {
					named = append(named, n)
				}
			}
			sort.Strings(named)
			for _, w := range denialWords {
				if strings.Contains(lower, w) {
					words = append(words, w)
				}
			}
			denied := subj
			if call.Name == "Bash" {
				if cmds := rules.deniedCommands(subj); len(cmds) > 0 {
					denied = strings.Join(cmds, "; ")
				}
			}
			rows = append(rows, SubagentDenial{
				Session:              short(who.Session),
				Agent:                "agent-" + who.Agent,
				Description:          s.Descriptions[who],
				Time:                 minute(call.At),
				Kind:                 kind,
				Denied:               cut(strings.TrimSpace(denied), 200),
				NextActionOnSameFile: nextAction(s, rules, ids[i+1:min(i+1+followCalls, len(ids))], names),
				ReportNamesFile:      orNo(strings.Join(named, ", ")),
				ReportDenialWords:    orNo(strings.Join(words, ", ")),
				ReportSource:         source,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Silent() != b.Silent() {
			return a.Silent()
		}
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		if a.Time != b.Time {
			return a.Time < b.Time
		}
		return a.Agent < b.Agent
	})
	return rows
}

// nextAction is the first of later that was not denied and names a file in names, as
// "[ok] <subject>" or "[error] <subject>".
func nextAction(s transcript.Sessions, rules DenyRules, later []string, names map[string]bool) string {
	for _, id := range later {
		res, ok := s.Results[id]
		if !ok || denialKind(res) != "" {
			continue
		}
		subj := subject(s.Calls[id])
		for n := range fileNames(subj, rules) {
			if names[n] {
				status := "ok"
				if res.IsError {
					status = "error"
				}
				return "[" + status + "] " + cut(strings.TrimSpace(subj), 200)
			}
		}
	}
	return ""
}

func repeatedEdits(s transcript.Sessions, home string) []RepeatedEdit {
	type key struct {
		transcript.Agent
		file string
	}
	counts := map[key]*RepeatedEdit{}
	var order []key
	for _, id := range s.CallOrder {
		call := s.Calls[id]
		if !editTools[call.Name] {
			continue
		}
		res, ok := s.Results[id]
		if !ok || res.IsError {
			continue
		}
		file := editedFile(call)
		k := key{call.Agent, file}
		c := counts[k]
		if c == nil {
			agent := "main"
			if call.Agent.Agent != "" {
				agent = "agent-" + call.Agent.Agent
			}
			c = &RepeatedEdit{Session: short(call.Session), Agent: agent, Repo: repo(home, call.Cwd), File: file, First: call.At, Last: call.At}
			counts[k] = c
			order = append(order, k)
		}
		c.Edits++
		c.First, c.Last = min(c.First, call.At), max(c.Last, call.At)
	}
	var rows []RepeatedEdit
	for _, k := range order {
		if c := counts[k]; c.Edits >= RepeatedEdits {
			c.First, c.Last = minute(c.First), minute(c.Last)
			rows = append(rows, *c)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Edits != b.Edits {
			return a.Edits > b.Edits
		}
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		return a.File < b.File
	})
	return rows
}

func editedFile(call transcript.ToolCall) string {
	var input struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	_ = json.Unmarshal(call.Input, &input)
	if input.FilePath != "" {
		return input.FilePath
	}
	return input.NotebookPath
}

// short is the first 8 characters of a session id, enough to find its transcript.
func short(session string) string { return cut(session, 8) }

// minute is a record's time to the minute, as 2026-11-13T05:12.
func minute(at string) string { return cut(at, 16) }

func orNo(s string) string {
	if s == "" {
		return "no"
	}
	return s
}

func joinNonEmpty(parts []string, sep string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
