package agentfailure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kapetan-io/dispatch/internal/transcript"
)

const (
	// exampleHead and exampleTail are how much of an example's start and end a row keeps.
	exampleHead, exampleTail = 400, 300
	// signatureLength caps a normalized error line.
	signatureLength = 200
)

// placeholders replace the parts of an error line that differ between two runs of the same
// failure, so the two share a signature. Order matters, since each rule sees what the earlier
// ones left: a subagent id (a and 16 hex digits) that holds a digit is a <sha> before the <id>
// rule sees it. The rules and their order are the pattern miner's, so the tables match its own.
var placeholders = []struct {
	match   *regexp.Regexp
	replace func(string) string
}{
	{regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), fixed("<uuid>")},
	{regexp.MustCompile(`https?://\S+`), fixed("<url>")},
	{regexp.MustCompile(`(?:~|\.{1,2})?(?:/[\p{L}\p{N}_.@+%=:-]+)+/?`), fixed("<path>")},
	{regexp.MustCompile(`\b[\p{L}\p{N}_.-]+\.(?:go|py|ts|js|md|json|yaml|yml|toml|txt|sh|sql|jsonl)\b`), fixed("<file>")},
	// A hex word counts as a sha only when it holds both a digit and a letter, so a plain number
	// or word survives to the later rules.
	{regexp.MustCompile(`\b[0-9a-f]{7,64}\b`), func(s string) string {
		if strings.ContainsAny(s, "0123456789") && strings.ContainsAny(s, "abcdef") {
			return "<sha>"
		}
		return s
	}},
	{regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b`), fixed("<hex>")},
	{regexp.MustCompile(`\b(?:toolu|agent|msg|req)_[\p{L}\p{N}_]+\b`), fixed("<id>")},
	{regexp.MustCompile(`\ba[0-9a-f]{16}\b`), fixed("<id>")},
	{regexp.MustCompile(`\p{Nd}+(?:\.\p{Nd}+)*`), fixed("<n>")},
}

func fixed(s string) func(string) string { return func(string) string { return s } }

// normalize is an error line with its paths, numbers and ids replaced by placeholders.
func normalize(text string) string {
	for _, p := range placeholders {
		text = p.match.ReplaceAllStringFunc(text, p.replace)
	}
	return cut(strings.Join(strings.Fields(shortenRuns(text)), " "), signatureLength)
}

// shortenRuns turns each run of three or more of = # * - into two, so rulers of any length
// read the same.
func shortenRuns(text string) string {
	var out strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); {
		j := i + 1
		if strings.ContainsRune("=#*-", runes[i]) {
			for j < len(runes) && runes[j] == runes[i] {
				j++
			}
		}
		n := min(j-i, 2)
		for range n {
			out.WriteRune(runes[i])
		}
		i = j
	}
	return out.String()
}

// trim keeps the start and the end of a long example.
func trim(text string) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) <= exampleHead+exampleTail {
		return string(r)
	}
	return string(r[:exampleHead]) + " … " + string(r[len(r)-exampleTail:])
}

var (
	diagnostic = regexp.MustCompile(`^(\(eval\):\d+: .*|(?:zsh|bash|sh): .*|(?:fatal|error|Error|panic|FAIL)\b.*)$`)
	exitCode   = regexp.MustCompile(`^Exit code (\d+)$`)
	errorTags  = regexp.MustCompile(`</?tool_use_error>`)
	// lineBreaks are the characters Python's str.splitlines breaks on. A progress meter ends its
	// updates with a bare \r, and the diagnostic after it starts a line of its own.
	lineBreaks = regexp.MustCompile(`\r\n|[\n\r\v\f\x1c\x1d\x1e\x{85}\x{2028}\x{2029}]`)
)

// errorSignature is how a failure's result reads in its signature: "Exit code N" with the first
// shell or program diagnostic line after it, or else the result's first line.
func errorSignature(head, text string) string {
	var lines []string
	for _, l := range lineBreaks.Split(errorTags.ReplaceAllString(text, ""), -1) {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	m := exitCode.FindStringSubmatch(lines[0])
	if m == nil {
		return normalize(lines[0])
	}
	prog := strings.SplitN(head, " ", 2)[0]
	for _, line := range lines[1:] {
		if diagnostic.MatchString(line) || (prog != "" && strings.HasPrefix(line, prog+": ")) {
			return "Exit code " + m[1] + ": " + normalize(line)
		}
	}
	return "Exit code " + m[1]
}

// denials are how Claude Code words a denied tool call in the call's result.
var denials = []struct {
	kind  string
	match *regexp.Regexp
}{
	{"rule", regexp.MustCompile(`(?s)^Permission to use (\S+) (?:with command .*?)?has been denied`)},
	{"safety-check", regexp.MustCompile(`^Permission for this command was denied by a built-in Claude Code safety check`)},
	{"classifier", regexp.MustCompile(`^Permission for this action was denied by the Claude Code auto mode classifier\. Reason: \[([^\]]*)\]`)},
	{"classifier-no-verdict", regexp.MustCompile(`^The server-side auto mode classifier gave no verdict`)},
	{"user-rejected", regexp.MustCompile(`^The user doesn't want to proceed with this tool use`)},
}

// denialKind is the kind of denial a result reports, or empty when it reports none.
func denialKind(r transcript.ToolResult) string {
	for _, d := range denials {
		if m := d.match.FindStringSubmatch(r.Text); m != nil {
			if d.kind == "classifier" {
				return "classifier: " + m[1]
			}
			return d.kind
		}
	}
	if r.DenialKind != "" {
		return "toolDenialKind=" + r.DenialKind
	}
	return ""
}

// DenyRules is the Bash command prefixes the user's Claude Code settings deny, such as "git
// push" for Bash(git push:*).
type DenyRules []string

var bashRule = regexp.MustCompile(`^Bash\((.+?)(?::\*| \*)?\)$`)

// ReadDenyRules reads the Bash deny rules from settings.json and settings.local.json in Claude
// Code's config directory. A file that is missing or not JSON adds none.
func ReadDenyRules(configDir string) DenyRules {
	var rules DenyRules
	for _, name := range []string{"settings.json", "settings.local.json"} {
		data, err := os.ReadFile(filepath.Join(configDir, name))
		if err != nil {
			continue
		}
		var settings struct {
			Permissions struct {
				Deny []string `json:"deny"`
			} `json:"permissions"`
		}
		if json.Unmarshal(data, &settings) != nil {
			continue
		}
		for _, rule := range settings.Permissions.Deny {
			if m := bashRule.FindStringSubmatch(rule); m != nil && strings.TrimSpace(m[1]) != "" {
				rules = append(rules, m[1])
			}
		}
	}
	return rules
}

// matching is the deny rules a Bash command's simple commands start with, each once.
func (d DenyRules) matching(cmd string) []string {
	var hits []string
	for _, c := range splitCommands(cmd) {
		toks := strings.Fields(c.text)
		for len(toks) > 0 && (assignment.MatchString(toks[0]) || wrappers[toks[0]]) {
			toks = toks[1:]
		}
		text := strings.Join(toks, " ")
		for _, p := range d {
			if (text == p || strings.HasPrefix(text, p+" ")) && !slices.Contains(hits, p) {
				hits = append(hits, p)
			}
		}
	}
	return hits
}

// deniedCommands is the simple commands of a Bash command that a deny rule matches.
func (d DenyRules) deniedCommands(cmd string) []string {
	var out []string
	for _, c := range splitCommands(cmd) {
		if len(d.matching(c.text)) > 0 {
			out = append(out, c.text)
		}
	}
	return out
}

// subjectKeys name what a non-Bash tool call acts on, in the order a subject prefers them.
var subjectKeys = []string{"file_path", "notebook_path", "path", "pattern", "url", "query"}

// subject is what a tool call acted on: a Bash call's command, key=value for the first subject
// key the input has, or the input as JSON.
func subject(call transcript.ToolCall) string {
	var input map[string]json.RawMessage
	_ = json.Unmarshal(call.Input, &input)
	if call.Name == "Bash" {
		return transcript.String(input["command"])
	}
	for _, key := range subjectKeys {
		if v, ok := input[key]; ok {
			return key + "=" + transcript.String(v)
		}
	}
	return cut(compactJSON(call.Input), 600)
}

// compactJSON writes a JSON value the way Python's json.dumps(ensure_ascii=False) does, which is
// how the pattern miner wrote a call's input in its examples: a space after each comma and colon,
// no escapes beyond what JSON needs, and the keys in the order they were written.
func compactJSON(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out strings.Builder
	if writeJSON(dec, &out) != nil {
		return string(raw)
	}
	return out.String()
}

func writeJSON(dec *json.Decoder, out *strings.Builder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case json.Delim:
		closing := "}"
		if v == '[' {
			closing = "]"
		}
		out.WriteString(string(v))
		for first := true; dec.More(); first = false {
			if !first {
				out.WriteString(", ")
			}
			if v == '{' {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				writeString(out, key.(string))
				out.WriteString(": ")
			}
			if err := writeJSON(dec, out); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		out.WriteString(closing)
	case string:
		writeString(out, v)
	case json.Number:
		out.WriteString(v.String())
	case bool:
		fmt.Fprint(out, v)
	case nil:
		out.WriteString("null")
	}
	return nil
}

func writeString(out *strings.Builder, s string) {
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
}

// repo names the repo a session ran in from its directory: the repo of a treehouse tree, the
// directory under ~/Development, the directory under home, or "(temp dir)".
func repo(home, cwd string) string {
	if cwd == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(cwd, filepath.Join(home, ".treehouse")+"/"); ok {
		if m := treehouseTree.FindStringSubmatch(rest); m != nil {
			return m[1]
		}
	}
	for _, root := range []string{filepath.Join(home, "Development") + "/", home + "/"} {
		if rest, ok := strings.CutPrefix(cwd, root); ok && rest != "" {
			return strings.SplitN(rest, "/", 2)[0]
		}
	}
	for _, tmp := range []string{"/private/tmp", "/tmp", "/private/var", "/var/folders"} {
		if strings.HasPrefix(cwd, tmp) {
			return "(temp dir)"
		}
	}
	if base := filepath.Base(strings.TrimRight(cwd, "/")); base != "/" && base != "." {
		return base
	}
	return cwd
}

// treehouseTree is a tree's path under the treehouse root: <pool>/<number>/<repo>.
var treehouseTree = regexp.MustCompile(`^[^/]+/\d+/([^/]+)`)
