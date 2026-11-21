// Package adrcheck fails when an ADR carries something a later reader can't use: a
// ticket id, a reference to a file or directory in the repo, a link into the repo's
// pull requests, commits, branches or issues, a missing Status or one outside the
// allowed forms, a number another ADR already has, or a link to an ADR that doesn't
// exist.
package adrcheck

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Options carries the command's output streams.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
}

// Problem is one thing an ADR must not carry, at the line it appears on.
type Problem struct {
	File   string
	Line   int
	Reason string
	Text   string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s:%d: %s: %q", p.File, p.Line, p.Reason, p.Text)
}

// Run checks every ADR under <root>/docs/adr and prints one line per problem. It
// returns 0 when there is none, 1 when there is at least one, and 2 when it could not check.
func Run(ctx context.Context, args []string, opts Options) int {
	problems, err := run(args, opts)
	if err != nil {
		fmt.Fprintln(opts.Stderr, "adrcheck:", err)
		return 2
	}
	for _, p := range problems {
		fmt.Fprintln(opts.Stdout, p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(opts.Stderr, "adrcheck: %d ADR problem(s)\n", len(problems))
		return 1
	}
	return 0
}

const adrDir = "docs/adr"

// The reasons a PR author reads next to each problem.
const (
	citesTicket      = "cites a ticket"
	citesRepoPath    = "references a file or directory in the repo"
	citesRepoHistory = "cites the repo's pull requests, commits, branches or issues"
	citesMissingADR  = "links to an ADR that doesn't exist"
	sharesNumber     = "shares its number with another ADR"
	badStatus        = "has a Status outside the allowed forms (Proposed, Accepted, Rejected, Deprecated, Superseded by ADR N, Superseded in part by ADR N, Amends ADR N, Amended by ADR N)"
	emptyStatus      = "has an empty Status"
	missingStatus    = "has no Status section"
	statusMissingADR = "has a Status naming an ADR that doesn't exist"
)

func run(args []string, opts Options) ([]Problem, error) {
	fs := flag.NewFlagSet("adrcheck", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	root := fs.String("root", ".", "the repo whose docs/adr is checked")
	keys := fs.String("ticket-keys", "", "ticket team keys, comma or space separated (ENG,LOC)")
	repo := fs.String("repo", "", "the repo as owner/name, whose pull requests, commits, branches and issues an ADR must not cite")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	rules, err := newChecker(*root, *keys, *repo)
	if err != nil {
		return nil, err
	}
	return rules.checkAll()
}

// checker holds what the checks need to know about the repo. checkAll fills
// numbers before it checks any ADR.
type checker struct {
	root    string
	ticket  *regexp.Regexp // nil when no ticket keys were given
	history []*regexp.Regexp
	numbers map[int][]string // ADR file names by number
}

var (
	keyPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

func newChecker(root, keys, repo string) (*checker, error) {
	r := &checker{root: root, history: []*regexp.Regexp{
		// #123 is how GitHub writes a pull request or issue in prose.
		regexp.MustCompile("(?:^|[\\s(\\[`])(#\\d+)\\b"),
		regexp.MustCompile(`\b(PR ?\d+|PR#\d+|GH-\d+)\b`),
		// A commit id is cited in backticks or after the word commit, and has both
		// digits and letters. Elsewhere a hex run is as likely a key name (ed25519),
		// and in backticks a run of digits is a number.
		regexp.MustCompile("`([0-9a-f]*[a-f][0-9a-f]*)`"),
		regexp.MustCompile(`(?i)\bcommits? ([0-9a-f]*[a-f][0-9a-f]*)\b`),
	}}
	var names []string
	for _, k := range strings.FieldsFunc(keys, func(r rune) bool { return r == ',' || r == ' ' }) {
		if !keyPattern.MatchString(k) {
			return nil, fmt.Errorf("ticket key %q is not letters and digits", k)
		}
		names = append(names, regexp.QuoteMeta(k))
	}
	if len(names) > 0 {
		r.ticket = regexp.MustCompile(`(?i)\b(?:` + strings.Join(names, "|") + `)-\d+\b`)
	}
	if repo != "" {
		if !repoPattern.MatchString(repo) {
			return nil, fmt.Errorf("repo %q is not owner/name", repo)
		}
		name := regexp.QuoteMeta(repo)
		r.history = append(r.history,
			// A link to the repo's home page cites no history, so the path must go on.
			regexp.MustCompile(`(?i)((?:https?://)?(?:www\.)?github\.com/`+name+`/(?:[^\s)>\]#?][^\s)>\]]*[^\s)>\].,;:!?'"]|[^\s)>\]#?.,;:!'"]))`),
			regexp.MustCompile(`(?i)((?:https?://)?raw\.githubusercontent\.com/`+name+`/[^\s)>\]]*[^\s)>\].,;:!?'"])`),
			regexp.MustCompile(`(?i)\b(`+name+`#\d+)\b`),
		)
	}
	return r, nil
}

var adrName = regexp.MustCompile(`^(\d+)-.*\.md$`)

func (r *checker) checkAll() ([]Problem, error) {
	entries, err := os.ReadDir(filepath.Join(r.root, adrDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.numbers = map[int][]string{}
	var names []string
	for _, e := range entries {
		m := adrName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			continue
		}
		number, _ := strconv.Atoi(m[1])
		r.numbers[number] = append(r.numbers[number], e.Name())
		names = append(names, e.Name())
	}
	var problems []Problem
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(r.root, adrDir, name))
		if err != nil {
			return nil, err
		}
		problems = append(problems, r.check(adrDir+"/"+name, string(body))...)
	}
	for _, shared := range r.numbers {
		for _, name := range shared {
			for _, other := range shared {
				if other != name {
					problems = append(problems, Problem{File: adrDir + "/" + name, Line: 1, Reason: sharesNumber, Text: other})
				}
			}
		}
	}
	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].File != problems[j].File {
			return problems[i].File < problems[j].File
		}
		return problems[i].Line < problems[j].Line
	})
	return problems, nil
}

var (
	statusHeading  = regexp.MustCompile(`(?i)^##\s+status\s*$`)
	allowedStatus  = regexp.MustCompile(`^(?:Proposed|Accepted|Rejected|Deprecated|(?:Superseded by|Superseded in part by|Amends|Amended by) ADR[ -](\d+))$`)
	mdLink         = regexp.MustCompile(`\[([^\]]*)\]\(\s*<?([^)\s>]+)>?[^)]*\)`)
	linkDefinition = regexp.MustCompile(`^\s*\[[^\]^][^\]]*\]:\s*<?(\S+?)>?(?:\s|$)`)
	urlPattern     = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s)>\]]*[^\s)>\].,;:!?'"]`)
	// A trailing :42, :42:7 or #anchor names a place in the file.
	lineSuffix = regexp.MustCompile(`(?::\d+)+$|#.*$`)
)

// sourceExtensions marks a path with a directory separator as a file reference even
// when the file is not in the repo yet, or no longer is.
var sourceExtensions = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".py": true, ".rb": true, ".rs": true, ".java": true, ".kt": true, ".kts": true, ".swift": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true, ".cs": true, ".php": true,
	".sh": true, ".bash": true, ".sql": true, ".proto": true, ".scala": true, ".ex": true, ".exs": true,
}

func (r *checker) check(file, body string) []Problem {
	var problems []Problem
	seen := map[Problem]bool{}
	add := func(line int, reason, text string) {
		p := Problem{File: file, Line: line, Reason: reason, Text: text}
		if !seen[p] {
			seen[p] = true
			problems = append(problems, p)
		}
	}
	lines := strings.Split(body, "\n")
	statusAt, statusLines := 0, 0
	inStatus := false
	for i, line := range lines {
		n := i + 1
		trimmed := strings.TrimSpace(line)
		if statusHeading.MatchString(trimmed) {
			statusAt, inStatus = n, true
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			inStatus = false
		}
		if inStatus && trimmed != "" {
			statusLines++
			m := allowedStatus.FindStringSubmatch(trimmed)
			switch {
			case m == nil:
				add(n, badStatus, trimmed)
			case m[1] != "" && !r.adrExists(m[1]):
				add(n, statusMissingADR, trimmed)
			}
		}
		if r.ticket != nil {
			for _, m := range r.ticket.FindAllString(line, -1) {
				add(n, citesTicket, m)
			}
		}
		r.references(file, line, func(reason, text string) { add(n, reason, text) })
	}
	switch {
	case statusAt == 0:
		add(1, missingStatus, strings.TrimSpace(lines[0]))
	case statusLines == 0:
		add(statusAt, emptyStatus, strings.TrimSpace(lines[statusAt-1]))
	}
	return problems
}

func (r *checker) adrExists(number string) bool {
	n, _ := strconv.Atoi(number)
	return len(r.numbers[n]) > 0
}

// references reports each link, path or history citation in line that points into the repo.
func (r *checker) references(file, line string, add func(reason, text string)) {
	if m := linkDefinition.FindStringSubmatch(line); m != nil {
		if reason := r.linkTarget(file, m[1], false); reason != "" {
			add(reason, m[1])
		}
		return
	}
	// A link's target is judged as a target, and its text with the rest of the line.
	var rest strings.Builder
	last := 0
	for _, m := range mdLink.FindAllStringSubmatchIndex(line, -1) {
		text, target := line[m[2]:m[3]], line[m[4]:m[5]]
		image := m[0] > 0 && line[m[0]-1] == '!'
		if reason := r.linkTarget(file, target, image); reason != "" {
			add(reason, target)
		}
		rest.WriteString(line[last:m[0]] + " " + text + " ")
		last = m[1]
	}
	rest.WriteString(line[last:])
	prose := rest.String()
	for _, pattern := range r.history {
		for _, m := range pattern.FindAllStringSubmatch(prose, -1) {
			if isHex(m[1]) && (len(m[1]) < 7 || len(m[1]) > 40 || !strings.ContainsAny(m[1], "0123456789")) {
				continue
			}
			add(citesRepoHistory, m[1])
		}
	}
	for _, token := range strings.Fields(urlPattern.ReplaceAllString(prose, " ")) {
		if path := r.repoPath(file, token); path != "" {
			add(citesRepoPath, path)
		}
	}
}

// linkTarget returns why a markdown link target is refused, or "" when it is allowed.
// An image beside the ADRs is part of the ADR, so it is allowed.
func (r *checker) linkTarget(file, target string, image bool) string {
	if urlPattern.MatchString(target) || strings.HasPrefix(target, "mailto:") {
		for _, pattern := range r.history {
			if pattern.MatchString(target) {
				return citesRepoHistory
			}
		}
		return ""
	}
	if strings.HasPrefix(target, "#") {
		return ""
	}
	path, _, _ := strings.Cut(target, "#")
	resolved := resolve(file, path, true)
	if isADRPath(resolved) {
		if _, err := os.Stat(filepath.Join(r.root, resolved)); err != nil {
			return citesMissingADR
		}
		return ""
	}
	if resolved == adrDir || image && strings.HasPrefix(resolved, adrDir+"/") {
		return ""
	}
	return citesRepoPath
}

// repoPath returns the path a prose token names in the repo, or "". A token names
// one when it resolves to an existing file or directory other than an ADR, or when
// it has a directory separator and a source-file extension. A bare word such as
// "rules" is never a path, even when a directory has that name.
func (r *checker) repoPath(file, token string) string {
	token = strings.TrimLeft(token, "`\"'([{*_")
	token = strings.TrimRight(token, "`\"')]}*_,;:.!?")
	if token == "" || strings.ContainsAny(token, "<>$~") || !strings.ContainsAny(token, "/.") {
		return ""
	}
	path := lineSuffix.ReplaceAllString(token, "")
	resolved := resolve(file, path, false)
	if strings.Trim(path, "./") == "" || resolved == "" || isADRPath(resolved) || resolved == adrDir {
		return ""
	}
	if !strings.Contains(path, "*") {
		if _, err := os.Stat(filepath.Join(r.root, resolved)); err == nil {
			return token
		}
	}
	if strings.Contains(path, "/") && !strings.HasPrefix(path, "/") && sourceExtensions[filepath.Ext(path)] {
		return token
	}
	return ""
}

// resolve turns a path an ADR names into a repo-relative one, or "" when it leaves
// the repo. A path starting with / is from the repo root. A link's other targets are
// relative to the ADR, as markdown reads them. In prose only a path starting with ../,
// or a bare ADR file name, is relative to the ADR; any other, ./cmd/x included,
// names a repo file from the root, as a command line in prose does.
func resolve(file, path string, link bool) string {
	var joined string
	switch {
	case strings.HasPrefix(path, "/"):
		joined = strings.TrimPrefix(path, "/")
	case link, strings.HasPrefix(path, "../"), !strings.Contains(path, "/") && isADRName(path):
		joined = filepath.Join(filepath.Dir(file), path)
	default:
		joined = path
	}
	joined = filepath.ToSlash(filepath.Clean(joined))
	if !filepath.IsLocal(joined) {
		return ""
	}
	return joined
}

func isADRName(name string) bool { return adrName.MatchString(name) }

func isADRPath(path string) bool {
	return filepath.ToSlash(filepath.Dir(path)) == adrDir && isADRName(filepath.Base(path))
}

func isHex(s string) bool {
	return strings.Trim(s, "0123456789abcdefABCDEF") == ""
}
