// Package adrcheck fails when an ADR carries something a later reader can't use: a
// ticket id, a reference to a file or directory in the repo, a link to the repo's
// pull requests, commits, branches or issues, a Status outside the allowed forms, a
// number another ADR already has, or a link to an ADR file that doesn't exist.
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
	"strings"
)

// Options carries the command's working directory and output streams.
type Options struct {
	// Dir is the directory a relative -root resolves against. Empty means the process's.
	Dir    string
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
// returns 0 when there is none, 1 when there is one, and 2 when it could not check.
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
		fmt.Fprintf(opts.Stderr, "adrcheck: %d problems\n", len(problems))
		return 1
	}
	return 0
}

const adrDir = "docs/adr"

func run(args []string, opts Options) ([]Problem, error) {
	fs := flag.NewFlagSet("adrcheck", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	root := fs.String("root", ".", "the repo whose docs/adr is checked")
	keys := fs.String("ticket-keys", "", "ticket team keys, comma or space separated (ENG,LOC)")
	repo := fs.String("repo", "", "the repo as owner/name, whose pull request, commit, branch and issue links are refused")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	dir := *root
	if !filepath.IsAbs(dir) && opts.Dir != "" {
		dir = filepath.Join(opts.Dir, dir)
	}
	c, err := newChecker(dir, *keys, *repo)
	if err != nil {
		return nil, err
	}
	return c.checkAll()
}

type checker struct {
	root   string
	ticket *regexp.Regexp // nil when no ticket keys were given
	repo   *regexp.Regexp // nil when no repo was given
}

var (
	keyPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9]*$`)
	repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

func newChecker(root, keys, repo string) (*checker, error) {
	c := &checker{root: root}
	var names []string
	for _, k := range strings.FieldsFunc(keys, func(r rune) bool { return r == ',' || r == ' ' }) {
		if !keyPattern.MatchString(k) {
			return nil, fmt.Errorf("ticket key %q is not upper-case letters and digits", k)
		}
		names = append(names, k)
	}
	if len(names) > 0 {
		c.ticket = regexp.MustCompile(`\b(?:` + strings.Join(names, "|") + `)-\d+\b`)
	}
	if repo != "" {
		if !repoPattern.MatchString(repo) {
			return nil, fmt.Errorf("repo %q is not owner/name", repo)
		}
		c.repo = regexp.MustCompile(`(?i)\bgithub\.com/` + regexp.QuoteMeta(repo) + `(?:[/#?]|$|[^A-Za-z0-9_.-])`)
	}
	return c, nil
}

var adrName = regexp.MustCompile(`^(\d+)-.*\.md$`)

func (c *checker) checkAll() ([]Problem, error) {
	entries, err := os.ReadDir(filepath.Join(c.root, adrDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var problems []Problem
	byNumber := map[string][]string{}
	for _, e := range entries {
		m := adrName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			continue
		}
		byNumber[strings.TrimLeft(m[1], "0")] = append(byNumber[strings.TrimLeft(m[1], "0")], e.Name())
		body, err := os.ReadFile(filepath.Join(c.root, adrDir, e.Name()))
		if err != nil {
			return nil, err
		}
		problems = append(problems, c.check(adrDir+"/"+e.Name(), string(body))...)
	}
	for _, names := range byNumber {
		for _, name := range names {
			for _, other := range names {
				if other != name {
					problems = append(problems, Problem{File: adrDir + "/" + name, Line: 1, Reason: "shares its number with another ADR", Text: other})
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
	statusHeading = regexp.MustCompile(`(?i)^##\s+status\s*$`)
	anyHeading    = regexp.MustCompile(`^#`)
	allowedStatus = regexp.MustCompile(`^(?:Proposed|Accepted|Rejected|Deprecated|(?:Superseded by|Superseded in part by|Amends|Amended by) ADR[ -]\d+)$`)
	mdLink        = regexp.MustCompile(`\[([^\]]*)\]\(\s*<?([^)\s>]+)>?[^)]*\)`)
	urlPattern    = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s)>\]]*[^\s)>\].,;:!?'"]`)
	issueRef      = regexp.MustCompile(`(?:^|[\s(\[])(#\d+)\b`)
	hexRun        = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	hasDigit      = regexp.MustCompile(`[0-9]`)
	hasHexLetter  = regexp.MustCompile(`[a-f]`)
)

// sourceExtensions marks a path with a directory separator as a file reference even
// when the file is not in the repo yet, or no longer is.
var sourceExtensions = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".py": true, ".rb": true, ".rs": true, ".java": true, ".kt": true, ".kts": true, ".swift": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true, ".cs": true, ".php": true,
	".sh": true, ".bash": true, ".sql": true, ".proto": true, ".scala": true, ".ex": true, ".exs": true,
}

func (c *checker) check(file, body string) []Problem {
	var problems []Problem
	add := func(line int, reason, text string) {
		problems = append(problems, Problem{File: file, Line: line, Reason: reason, Text: text})
	}
	lines := strings.Split(body, "\n")
	inStatus, sawStatus, statusLines := false, false, 0
	for i, line := range lines {
		n := i + 1
		trimmed := strings.TrimSpace(line)
		if statusHeading.MatchString(trimmed) {
			inStatus, sawStatus = true, true
			continue
		}
		if anyHeading.MatchString(trimmed) {
			if inStatus && statusLines == 0 {
				add(n, "has an empty Status", trimmed)
			}
			inStatus = false
		}
		if inStatus && trimmed != "" {
			statusLines++
			if !allowedStatus.MatchString(trimmed) {
				add(n, "has a Status outside the allowed forms", trimmed)
			}
		}
		if c.ticket != nil {
			for _, m := range c.ticket.FindAllString(line, -1) {
				add(n, "cites a ticket", m)
			}
		}
		for _, p := range c.lineReferences(file, line) {
			add(n, p[0], p[1])
		}
	}
	if !sawStatus {
		add(1, "has no Status section", strings.TrimSpace(lines[0]))
	} else if inStatus && statusLines == 0 {
		add(len(lines), "has an empty Status", "")
	}
	return problems
}

// lineReferences returns a reason and the matched text for each link or reference
// in line that points into the repo.
func (c *checker) lineReferences(file, line string) [][2]string {
	var found [][2]string
	// Markdown link targets are checked as targets, and the rest of the line without them.
	rest := mdLink.ReplaceAllStringFunc(line, func(link string) string {
		m := mdLink.FindStringSubmatch(link)
		if r := c.linkTarget(file, m[2]); r != "" {
			found = append(found, [2]string{r, m[2]})
		}
		return " " + m[1] + " "
	})
	rest = urlPattern.ReplaceAllStringFunc(rest, func(url string) string {
		if c.repo != nil && c.repo.MatchString(url) {
			found = append(found, [2]string{"links to the repo's pull requests, commits, branches or issues", url})
		}
		return " "
	})
	for _, m := range issueRef.FindAllStringSubmatch(rest, -1) {
		found = append(found, [2]string{"cites a pull request or issue", m[1]})
	}
	for _, m := range hexRun.FindAllString(rest, -1) {
		if hasDigit.MatchString(m) && hasHexLetter.MatchString(m) {
			found = append(found, [2]string{"cites a commit", m})
		}
	}
	for _, token := range strings.Fields(rest) {
		if path := c.repoPath(token); path != "" {
			found = append(found, [2]string{"references a file or directory in the repo", path})
		}
	}
	return found
}

// linkTarget returns why a markdown link target is refused, or "" when it is allowed.
func (c *checker) linkTarget(file, target string) string {
	if urlPattern.MatchString(target) {
		if c.repo != nil && c.repo.MatchString(target) {
			return "links to the repo's pull requests, commits, branches or issues"
		}
		return ""
	}
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
		return ""
	}
	path, _, _ := strings.Cut(target, "#")
	resolved := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(file), path)))
	if filepath.ToSlash(filepath.Dir(resolved)) == adrDir && adrName.MatchString(filepath.Base(resolved)) {
		if _, err := os.Stat(filepath.Join(c.root, resolved)); err != nil {
			return "links to an ADR file that doesn't exist"
		}
		return ""
	}
	return "references a file or directory in the repo"
}

// repoPath returns the path a prose token names in the repo, or "". A token names
// one when it resolves to an existing file or directory other than an ADR, or when
// it has a directory separator and a source-file extension. A bare word such as
// "rules" is never a path, even when a directory has that name.
func (c *checker) repoPath(token string) string {
	token = strings.TrimLeft(token, "`\"'([{<*_")
	token = strings.TrimRight(token, "`\"')]}>*_,;:.!?")
	if token == "" || strings.ContainsAny(token, "<>$~*") || strings.HasPrefix(token, "/") {
		return ""
	}
	if !strings.ContainsAny(token, "/.") {
		return ""
	}
	path := strings.TrimPrefix(token, "./")
	if !filepath.IsLocal(path) {
		return ""
	}
	if filepath.ToSlash(filepath.Dir(filepath.Clean(path))) == adrDir && adrName.MatchString(filepath.Base(path)) {
		return ""
	}
	if _, err := os.Stat(filepath.Join(c.root, path)); err == nil {
		return token
	}
	if strings.Contains(path, "/") && sourceExtensions[filepath.Ext(path)] {
		return token
	}
	return ""
}
