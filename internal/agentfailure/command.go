package agentfailure

import (
	"regexp"
	"strings"
)

// command is one simple command of a Bash string, with the separator before it: '|' for a
// pipeline stage, ';' otherwise.
type command struct {
	sep  rune
	text string
}

// splitCommands is the top-level simple commands of a Bash string. It splits on unquoted ;,
// newline, &&, || and |, after dropping heredoc bodies, and leaves out comments.
func splitCommands(cmd string) []command {
	runes := []rune(dropHeredocBodies(cmd))
	var out []command
	var cur strings.Builder
	var quote rune
	sep := ';'
	at := func(i int, s string) bool { return strings.HasPrefix(string(runes[i:min(i+2, len(runes))]), s) }
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
			} else if ch == '\\' && quote == '"' {
				cur.WriteRune(ch)
				i++
				if i >= len(runes) {
					continue
				}
				ch = runes[i]
			}
			cur.WriteRune(ch)
		case ch == '\'' || ch == '"':
			quote = ch
			cur.WriteRune(ch)
		case ch == '\\':
			cur.WriteString(string(runes[i:min(i+2, len(runes))]))
			i++
		case at(i, "&&") || at(i, "||") || ch == ';' || ch == '\n' || ch == '|':
			out = append(out, command{sep: sep, text: strings.TrimSpace(cur.String())})
			cur.Reset()
			double := at(i, "&&") || at(i, "||")
			sep = ';'
			if ch == '|' && !double {
				sep = '|'
			}
			if double {
				i++
			}
		default:
			cur.WriteRune(ch)
		}
	}
	out = append(out, command{sep: sep, text: strings.TrimSpace(cur.String())})
	kept := out[:0]
	for _, c := range out {
		if c.text != "" && !strings.HasPrefix(c.text, "#") {
			kept = append(kept, c)
		}
	}
	return kept
}

// dropHeredocBodies keeps the first line of each heredoc, the one that starts it, and drops the
// body and the closing word.
func dropHeredocBodies(cmd string) string {
	var out strings.Builder
	for i := 0; i < len(cmd); {
		j := strings.Index(cmd[i:], "<<")
		if j < 0 {
			out.WriteString(cmd[i:])
			break
		}
		start := i + j
		if end, ok := heredocEnd(cmd, start); ok {
			out.WriteString(cmd[i:start])
			header := cmd[start:end]
			if nl := strings.IndexByte(header, '\n'); nl >= 0 {
				header = header[:nl]
			}
			out.WriteString(header)
			i = end
			continue
		}
		out.WriteString(cmd[i : start+1])
		i = start + 1
	}
	return out.String()
}

// heredocEnd finds where a heredoc that starts at start ends: past its closing word and any blank
// lines after it. It follows the pattern miner's heredoc pattern, quirks included: the closing word
// may be indented without <<-, it is looked for only after one body line, so an empty heredoc is
// not recognized, and << inside <<< or $((1<<2)) can start one.
func heredocEnd(cmd string, start int) (int, bool) {
	i := start + 2
	if i < len(cmd) && cmd[i] == '-' {
		i++
	}
	for i < len(cmd) && isSpace(cmd[i]) {
		i++
	}
	var quote byte
	if i < len(cmd) && (cmd[i] == '\'' || cmd[i] == '"') {
		quote = cmd[i]
		i++
	}
	w := i
	for w < len(cmd) && isWordByte(cmd[w]) {
		w++
	}
	if w == i {
		return 0, false
	}
	word := cmd[i:w]
	i = w
	if quote != 0 {
		if i >= len(cmd) || cmd[i] != quote {
			return 0, false
		}
		i++
	}
	nl := strings.IndexByte(cmd[i:], '\n')
	if nl < 0 {
		return 0, false
	}
	for k := i + nl + 1; k < len(cmd); k++ {
		if cmd[k] != '\n' {
			continue
		}
		p := k + 1
		for p < len(cmd) && isSpace(cmd[p]) {
			p++
		}
		if !strings.HasPrefix(cmd[p:], word) {
			continue
		}
		p += len(word)
		q := p
		lastNL := -1
		for q < len(cmd) && isSpace(cmd[q]) {
			if cmd[q] == '\n' {
				lastNL = q
			}
			q++
		}
		switch {
		case q == len(cmd):
			return q, true
		case lastNL >= 0:
			return lastNL, true
		}
	}
	return 0, false
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

func isWordByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b >= 0x80
}

var (
	// wrappers run the program named after them. The shell's own words that open a block (if,
	// while, for) are not stripped, so "if git diff --quiet; then" names "if", as the pattern
	// miner's tables do.
	wrappers = set("env", "time", "timeout", "nohup", "sudo", "command", "xargs")
	// subcommandTools name what they do with their first argument, or first two for gh,
	// dispatch and herdr. tiger is Tiger Cloud's CLI.
	subcommandTools = set("git", "gh", "go", "make", "npm", "pnpm", "npx", "docker", "cargo", "kubectl", "uv", "brew", "task", "dispatch", "herdr", "tiger")
	// setup is the commands that prepare for the one that does the work, and the words that close
	// a block.
	setup = set("cd", "export", "set", "source", ".", "pushd", "popd", "true", "fi", "done", "}", ")", "esac")

	assignment = regexp.MustCompile(`^[\p{L}\p{N}_]+=`)
)

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// program is the program a simple command runs, with the subcommand for git, gh, go and the
// like, as in "git push" or "gh pr view". A program run through a variable, such as
// "${DISPATCH_BIN:-dispatch}" ask, reads as "$", as it does in the pattern miner's tables. Flag
// values count as subcommands, so git -c a=b commit names "git a=b".
func program(text string) string {
	toks := strings.Fields(strings.NewReplacer("(", " ", "{", " ").Replace(text))
	for len(toks) > 0 && (assignment.MatchString(toks[0]) || wrappers[toks[0]] || toks[0] == "then" || toks[0] == "do" || toks[0] == "else" || toks[0] == "!") {
		toks = toks[1:]
	}
	if len(toks) == 0 {
		return ""
	}
	name := strings.Trim(toks[0], `'"`)
	head := []string{name[strings.LastIndex(name, "/")+1:]}
	if subcommandTools[head[0]] {
		args := toks[1:]
		if head[0] == "git" && len(args) > 0 && args[0] == "-C" {
			args = args[min(2, len(args)):]
		}
		want := 1
		if head[0] == "gh" || head[0] == "dispatch" || head[0] == "herdr" {
			want = 2
		}
		for _, a := range args {
			if len(head) > want {
				break
			}
			if !strings.HasPrefix(a, "-") {
				head = append(head, a)
			}
		}
	}
	return cut(strings.Join(head, " "), 60)
}

// commandHead is the first program a Bash command runs past setup such as cd and export. With
// last, it is the last command in the list instead, by the program of its first pipeline stage,
// since that command's exit status is the one the shell reports.
func commandHead(cmd string, last bool) string {
	cmds := splitCommands(cmd)
	pick := ""
	for _, c := range cmds {
		if c.sep == ';' && !setup[strings.SplitN(program(c.text), " ", 2)[0]] {
			pick = c.text
			if !last {
				break
			}
		}
	}
	if pick != "" {
		return program(pick)
	}
	if len(cmds) > 0 {
		return program(cmds[0].text)
	}
	return ""
}

// cut keeps the first n characters of text.
func cut(text string, n int) string {
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return string(r[:n])
}
