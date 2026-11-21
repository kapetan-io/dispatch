// Package rework reports merged PRs that reworked earlier work: fix chains, where a later PR
// changed lines an earlier PR wrote, and the existing Go tests a PR changed, each with the Claude
// Code session that made the PR. It calls no chain a regression: the Markdown table filters on a
// stated rule (see RuleShare), and the JSON keeps every row.
//
// GitHub is read through gh. Each repo's local clone is fetched into and otherwise only read.
package rework

import (
	"cmp"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/gitremote"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/transcript"
)

const (
	// FixWindow is how long after an earlier PR merged a later PR can merge and still form a fix
	// chain with it.
	FixWindow = 30 * 24 * time.Hour
	// RuleShare is the share of the later PR's deleted lines the earlier PR must have written for
	// a Bug ticket's row to pass the rule. It was fitted on 20 hand-checked rows with 2 real fixes.
	RuleShare = 0.5
	// lookups is how many gh calls run at once.
	lookups = 8
)

var (
	testPath      = regexp.MustCompile(`(_test\.go$|(^|/)(tests?|testdata|__tests__|e2e)/|\.(test|spec)\.\w+$|(^|/)test_[^/]*\.py$|_test\.py$)`)
	generatedPath = regexp.MustCompile(`(\.lock$|(^|/)go\.sum$|(^|/)(package-lock\.json|pnpm-lock\.yaml|npm-shrinkwrap\.json)$|\.pb(\.gw)?\.go$|_gen\.go$|(^|/)zz_generated[^/]*$|\.min\.(js|css)$|(^|/)vendor/)`)
	// revertTitle is the title GitHub gives a revert, "Revert "<title>"", and not "Reverting...".
	revertTitle = regexp.MustCompile(`^Revert\b`)
)

// Repo is a local clone the report reads, and the remote whose GitHub repo it stands for.
type Repo struct {
	Label  string
	Path   string
	Remote string
}

// TicketLabels reads the label names of tickets. A ticket it cannot see is left out of the answer.
type TicketLabels interface {
	Labels(ctx context.Context, tickets []string) (map[string][]string, error)
}

// Inputs is what one report reads.
type Inputs struct {
	Repos        []Repo
	GitHub       pullrequest.Repository
	TicketLabels TicketLabels
	// Sessions is the transcripts, which prove which session made a PR.
	Sessions transcript.Sessions
	// Teams are the ticket prefixes a PR's body or branch can name, such as ENG.
	Teams    []string
	Env      environment.Environment
	From, To time.Time
}

// RepoRow is a repo with PRs that merged in the window.
type RepoRow struct {
	Repo      string `json:"repo"`
	MergedPRs int    `json:"merged_prs"`
	Checkout  string `json:"checkout"`
}

// FixChain is a later merged PR that changed lines an earlier merged PR wrote, within FixWindow.
type FixChain struct {
	// LaterDelShare is the share of the later PR's deleted and changed lines the earlier PR wrote,
	// and EarlierAddShare the share of the earlier PR's added lines the later PR changed. Lines in
	// tests and generated files count in neither. Each is nil when its PR's commits are missing.
	LaterDelShare   *float64 `json:"later_del_share"`
	EarlierAddShare *float64 `json:"earlier_add_share"`
	Revert          bool     `json:"revert"`
	// Ticket is the later PR's ticket, from "Fixes <ticket>" in its body or else its branch.
	Ticket string `json:"ticket"`
	// Bug is whether that ticket carries the Bug label, nil when its labels could not be read.
	Bug *bool `json:"bug"`
	// PassesRule is the rule the Markdown table filters on: Revert, or Bug with LaterDelShare at
	// least RuleShare.
	PassesRule    bool   `json:"passes_rule"`
	LaterPR       string `json:"later_pr"`
	LaterMerged   string `json:"later_merged"`
	EarlierPR     string `json:"earlier_pr"`
	EarlierMerged string `json:"earlier_merged"`
	Files         int    `json:"files"`
	FileList      string `json:"file_list"`
	// Lines is how many lines the later PR changed that the earlier PR wrote.
	Lines          int    `json:"lines"`
	LaterSession   string `json:"later_session"`
	EarlierSession string `json:"earlier_session"`
}

// ChangedTest is a Go test function that existed before a merged PR and that the PR modified or
// deleted.
type ChangedTest struct {
	PR       string `json:"pr"`
	Merged   string `json:"merged"`
	File     string `json:"file"`
	Function string `json:"function"`
	// Change is "modified" or "deleted".
	Change          string `json:"change"`
	OldLinesTouched int    `json:"old_lines_touched"`
	// LaterTouches lists each later merged PR that touched the test, as "<pr> (test)" when it
	// changed the test again or "<pr> (code)" when it changed code this PR wrote in the test's
	// directory, and is "no" when none did.
	LaterTouches  string `json:"later_pr_touching_it"`
	Session       string `json:"session"`
	LaterSessions string `json:"later_pr_sessions"`
}

// Tables is the report's tables.
type Tables struct {
	Repos        []RepoRow     `json:"fix_chain_repos"`
	FixChains    []FixChain    `json:"fix_chains"`
	ChangedTests []ChangedTest `json:"changed_existing_tests"`
	// Notes is what the report could not read, such as a repo with no clone or a gh call that failed.
	Notes []string `json:"notes"`
}

// NewTables is tables with no rows, which print as empty lists.
func NewTables() Tables {
	return Tables{Repos: []RepoRow{}, FixChains: []FixChain{}, ChangedTests: []ChangedTest{}, Notes: []string{}}
}

// merged is a PR that merged in the window, in the clone of its repo.
type merged struct {
	pullrequest.MergedChange
	clone     *clone
	mergeBase string
}

func (m *merged) ref() string { return ref(m.clone.slug, m.Number) }

func ref(slug string, number int) string {
	return fmt.Sprintf("%s#%d", slug[strings.LastIndex(slug, "/")+1:], number)
}

// blamed is lines a later PR changed, in one file, that one commit wrote.
type blamed struct {
	later *merged
	file  string
	sha   string
	lines int
}

// commit is a commit of one GitHub repo.
type commit struct{ slug, sha string }

// prKey is a PR of one GitHub repo.
type prKey struct {
	slug   string
	number int
}

func (b blamed) commit() commit { return commit{b.later.clone.slug, b.sha} }

// Collect reads the merged PRs of every repo in the window and builds the report.
func Collect(ctx context.Context, in Inputs) Tables {
	r := NewTables()
	note := func(format string, args ...any) { r.Notes = append(r.Notes, fmt.Sprintf(format, args...)) }
	repos, prs := mergedInWindow(ctx, in, note)
	r.Repos = repos
	blames, tests := blameChanges(ctx, prs, in.Sessions, note)
	writers := commitWriters(ctx, in.GitHub, blames, note)
	r.FixChains = fixChains(ctx, in, blames, writers, note)
	r.ChangedTests = laterTouches(tests, blames, writers, in.Sessions)
	return r
}

// mergedInWindow lists the merged PRs of each repo, once per GitHub repo, fetches their commits
// and finds where each one's diff starts. It returns a row for each repo with any.
func mergedInWindow(ctx context.Context, in Inputs, note func(string, ...any)) ([]RepoRow, []*merged) {
	type listed struct {
		clone *clone
		prs   []pullrequest.MergedChange
	}
	var repos []listed
	seen := map[string]bool{}
	for _, repo := range in.Repos {
		slug, err := gitremote.Read(ctx, in.Env, repo.Path, repo.Remote)
		if err != nil {
			note("%s: %v", repo.Label, err)
			continue
		}
		if slug == "" {
			note("%s: remote %s of %s is not a github.com repo", repo.Label, repo.Remote, repo.Path)
			continue
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		prs, err := in.GitHub.OwnMerged(ctx, slug, in.From, in.To)
		if err != nil {
			note("%s: %v", slug, err)
			continue
		}
		if len(prs) > 0 {
			repos = append(repos, listed{&clone{env: in.Env, path: repo.Path, remote: repo.Remote, slug: slug}, prs})
		}
	}
	slices.SortFunc(repos, func(a, b listed) int { return cmp.Compare(a.clone.slug, b.clone.slug) })

	rows := []RepoRow{}
	var out []*merged
	for _, repo := range repos {
		rows = append(rows, RepoRow{Repo: repo.clone.slug, MergedPRs: len(repo.prs), Checkout: repo.clone.path})
		var shas []string
		for _, pr := range repo.prs {
			shas = append(shas, pr.Base, pr.Head)
		}
		_, _ = repo.clone.git(ctx, "fetch", "-q", repo.clone.remote)
		repo.clone.fetchMissing(ctx, shas)
		for _, pr := range repo.prs {
			m := &merged{MergedChange: pr, clone: repo.clone}
			m.mergeBase = repo.clone.mergeBase(ctx, pr.Base, pr.Head)
			if m.mergeBase == "" {
				note("%s: base or head commit not available locally", m.ref())
			}
			out = append(out, m)
		}
	}
	return rows, out
}

// blameChanges blames, at each PR's merge base, the lines the PR changed in files whose lines
// count toward fix chains, and finds the existing Go tests the PR changed.
func blameChanges(ctx context.Context, prs []*merged, sessions transcript.Sessions, note func(string, ...any)) ([]blamed, []changedTest) {
	var blames []blamed
	var tests []changedTest
	for _, p := range prs {
		if p.mergeBase == "" {
			continue
		}
		files, err := p.clone.changes(ctx, p.mergeBase, p.Head)
		if err != nil {
			note("%s: %v", p.ref(), err)
			continue
		}
		// lineCounts leaves out a file marked generated at either end, so the blame does too.
		atBase, atHead := p.clone.markedGenerated(ctx, p.mergeBase), p.clone.markedGenerated(ctx, p.Head)
		for _, f := range files {
			if strings.HasSuffix(f.old, "_test.go") {
				tests = append(tests, changedTests(ctx, p, f, sessions)...)
			}
			if !counted(f.old, atBase) || atHead[f.new] {
				continue
			}
			for sha, n := range p.clone.blame(ctx, p.mergeBase, f.old, f.hunks) {
				blames = append(blames, blamed{later: p, file: f.old, sha: sha, lines: n})
			}
		}
	}
	return blames, tests
}

// commitWriters finds the merged PR that brought each blamed commit in, the PR that wrote its
// lines. A commit no merged PR holds has no entry.
func commitWriters(ctx context.Context, gh pullrequest.Repository, blames []blamed, note func(string, ...any)) map[commit]pullrequest.MergedChange {
	var commits []commit
	seen := map[commit]bool{}
	for _, b := range blames {
		if c := b.commit(); !seen[c] {
			seen[c] = true
			commits = append(commits, c)
		}
	}
	writers := map[commit]pullrequest.MergedChange{}
	var mu sync.Mutex
	var failed []string
	var wg sync.WaitGroup
	slots := make(chan struct{}, lookups)
	for _, c := range commits {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			pr, ok, err := gh.MergedHolding(ctx, c.slug, c.sha)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				failed = append(failed, fmt.Sprintf("%s %s: %v", c.slug, c.sha, err))
			case ok:
				writers[c] = pr
			}
		}()
	}
	wg.Wait()
	if len(failed) > 0 {
		slices.Sort(failed)
		note("%d commit lookups failed, so their lines count toward no fix chain; the first: %s", len(failed), failed[0])
	}
	return writers
}

// earlierWriter is the PR that wrote the lines b blamed, false when it is unknown or is the later
// PR itself.
func earlierWriter(writers map[commit]pullrequest.MergedChange, b blamed) (pullrequest.MergedChange, bool) {
	earlier, ok := writers[b.commit()]
	return earlier, ok && earlier.Number != b.later.Number
}

// pair is the lines one later PR changed that one earlier PR wrote.
type pair struct {
	later   *merged
	earlier pullrequest.MergedChange
	files   map[string]bool
	lines   int
}

// pairs groups the blamed lines by the later PR and the earlier PR that wrote them, keeping the
// pairs whose earlier PR merged within FixWindow before the later one.
func pairs(blames []blamed, writers map[commit]pullrequest.MergedChange) []*pair {
	type key struct {
		later   prKey
		earlier int
	}
	byKey := map[key]*pair{}
	var out []*pair
	for _, b := range blames {
		earlier, ok := earlierWriter(writers, b)
		if !ok {
			continue
		}
		if gap := b.later.MergedAt.Sub(earlier.MergedAt); gap < 0 || gap > FixWindow {
			continue
		}
		k := key{prKey{b.later.clone.slug, b.later.Number}, earlier.Number}
		p := byKey[k]
		if p == nil {
			p = &pair{later: b.later, earlier: earlier, files: map[string]bool{}}
			byKey[k] = p
			out = append(out, p)
		}
		p.files[b.file] = true
		p.lines += b.lines
	}
	return out
}

type lineCount struct {
	added, deleted int
	ok             bool
}

// lineCounter counts the added and deleted lines of each PR once.
type lineCounter map[prKey]lineCount

// of counts the lines of the PR from base to head. An earlier PR can have merged before the
// window, so the clone may still lack its commits, and they are fetched here.
func (l lineCounter) of(ctx context.Context, c *clone, number int, base, head string) lineCount {
	k := prKey{c.slug, number}
	if n, ok := l[k]; ok {
		return n
	}
	var n lineCount
	mb := c.mergeBase(ctx, base, head)
	if mb == "" {
		c.fetchMissing(ctx, []string{base, head})
		mb = c.mergeBase(ctx, base, head)
	}
	if mb != "" {
		if a, d, err := c.lineCounts(ctx, mb, head); err == nil {
			n = lineCount{a, d, true}
		}
	}
	l[k] = n
	return n
}

// fixChains builds a row for each pair, with the later PR's ticket and its Bug label, sorted with
// the rows that pass the rule first.
func fixChains(ctx context.Context, in Inputs, blames []blamed, writers map[commit]pullrequest.MergedChange, note func(string, ...any)) []FixChain {
	counts := lineCounter{}
	chains := []FixChain{}
	for _, p := range pairs(blames, writers) {
		c := p.later.clone
		later := counts.of(ctx, c, p.later.Number, p.later.Base, p.later.Head)
		earlier := counts.of(ctx, c, p.earlier.Number, p.earlier.Base, p.earlier.Head)
		row := FixChain{
			Revert:         revertTitle.MatchString(p.later.Title) || strings.Contains(p.later.Body, "This reverts commit"),
			Ticket:         ticketOf(in.Teams, p.later.Body, p.later.Branch),
			LaterPR:        p.later.ref(),
			LaterMerged:    minute(p.later.MergedAt),
			EarlierPR:      ref(c.slug, p.earlier.Number),
			EarlierMerged:  minute(p.earlier.MergedAt),
			Files:          len(p.files),
			FileList:       cut(strings.Join(slices.Sorted(maps.Keys(p.files)), ", "), 150),
			Lines:          p.lines,
			LaterSession:   sessionOf(in.Sessions, p.later.MergedChange),
			EarlierSession: sessionOf(in.Sessions, p.earlier),
		}
		if later.ok && later.deleted > 0 {
			row.LaterDelShare = new(share(p.lines, later.deleted))
		}
		if earlier.ok && earlier.added > 0 {
			row.EarlierAddShare = new(share(p.lines, earlier.added))
		}
		chains = append(chains, row)
	}
	if len(chains) > 0 && len(in.Teams) == 0 {
		note("tickets not read: the config lists no Linear teams")
	}
	markBugs(ctx, in.TicketLabels, chains, note)
	applyRule(chains)
	return chains
}

// markBugs sets Bug on each row whose ticket's labels it can read, in one read.
func markBugs(ctx context.Context, labels TicketLabels, chains []FixChain, note func(string, ...any)) {
	tickets := map[string]bool{}
	for _, c := range chains {
		if c.Ticket != "" {
			tickets[c.Ticket] = true
		}
	}
	if len(tickets) == 0 || labels == nil {
		return
	}
	read, err := labels.Labels(ctx, slices.Sorted(maps.Keys(tickets)))
	if err != nil {
		note("bug labels not read: %v", err)
	}
	for i := range chains {
		if names, ok := read[chains[i].Ticket]; ok {
			chains[i].Bug = new(slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, "bug") }))
		}
	}
}

// applyRule sets PassesRule on each row and sorts the rows that pass first, then by lines.
func applyRule(chains []FixChain) {
	for i := range chains {
		c := &chains[i]
		c.PassesRule = c.Revert || (c.Bug != nil && *c.Bug && c.LaterDelShare != nil && *c.LaterDelShare >= RuleShare)
	}
	slices.SortStableFunc(chains, func(a, b FixChain) int {
		if a.PassesRule != b.PassesRule {
			if a.PassesRule {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(b.Lines, a.Lines), cmp.Compare(a.LaterPR, b.LaterPR), cmp.Compare(a.EarlierPR, b.EarlierPR))
	})
}

// ticketOf is the ticket a PR's body names after "Fixes", or else the one its branch names, from
// one of teams.
func ticketOf(teams []string, body, branch string) string {
	if len(teams) == 0 {
		return ""
	}
	quoted := make([]string, len(teams))
	for i, t := range teams {
		quoted[i] = regexp.QuoteMeta(t)
	}
	id := `((?:` + strings.Join(quoted, "|") + `)-\d+)`
	if m := regexp.MustCompile(`(?i)\bFixes\s+` + id).FindStringSubmatch(body); m != nil {
		return strings.ToUpper(m[1])
	}
	if m := regexp.MustCompile(`(?i)\b` + id).FindStringSubmatch(branch); m != nil {
		return strings.ToUpper(m[1])
	}
	return ""
}

// share is part over whole, rounded to 3 places.
func share(part, whole int) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(float64(part)/float64(whole), 'f', 3, 64), 64)
	return v
}

func minute(t time.Time) string { return t.UTC().Format("2006-01-02T15:04") }

func cut(text string, n int) string {
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return string(r[:n])
}

// goTestName is go test's rule for a test's name: the prefix, then nothing or a character that is
// not a lowercase letter, so a helper named Testable is not a test.
var goTestName = regexp.MustCompile(`^(?:Test|Benchmark|Fuzz|Example)(?:$|[^a-z])`)

// span is the lines start to end of a Go test function.
type span struct {
	name       string
	start, end int
}

// testSpans is each top-level Go test function in src, from its func line to its closing brace,
// as lines of src itself, not those a //line comment names, since diff hunks count src's own.
// A file that does not parse falls back to scanSpans.
func testSpans(src string) []span {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return scanSpans(src)
	}
	var spans []span
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !goTestName.MatchString(fn.Name.Name) {
			continue
		}
		spans = append(spans, span{name: fn.Name.Name, start: fset.PositionFor(fn.Pos(), false).Line, end: fset.PositionFor(fn.End(), false).Line})
	}
	return spans
}

var goTestFunc = regexp.MustCompile(`^func ((?:Test|Benchmark|Fuzz|Example)(?:[^a-z(]\w*)?)\(`)

// scanSpans reads test functions line by line, from a func line to the first line after it that
// starts with "}", or the func line alone when it closes the function on that line. It misreads a
// raw string holding Go source, so it serves only a file go/parser rejects.
func scanSpans(src string) []span {
	var spans []span
	var cur *span
	for i, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "func ") {
			// A func line while a test is open means the test had no "}" starting a line, which
			// gofmt never leaves, so the open test is dropped rather than given a guessed end.
			cur = nil
			if m := goTestFunc.FindStringSubmatch(line); m != nil {
				cur = &span{name: m[1], start: i + 1}
				if strings.HasSuffix(strings.TrimSpace(line), "}") {
					cur.end = i + 1
					spans = append(spans, *cur)
					cur = nil
				}
			}
		} else if cur != nil && strings.HasPrefix(line, "}") {
			cur.end = i + 1
			spans = append(spans, *cur)
			cur = nil
		}
	}
	return spans
}

// changedTest is a changed test's row while laterTouches finds the PRs that touched it again.
type changedTest struct {
	row ChangedTest
	pr  *merged
	// newFile is the file the PR left the test in, empty when it deleted the test.
	newFile string
}

// changedTests is the test functions of a _test.go file, as it was at the PR's merge base, that
// the PR's hunks touch.
func changedTests(ctx context.Context, p *merged, f fileChange, sessions transcript.Sessions) []changedTest {
	src, ok := p.clone.show(ctx, p.mergeBase, f.old)
	if !ok {
		return nil
	}
	kept := map[string]bool{}
	if f.new != "" {
		if after, ok := p.clone.show(ctx, p.Head, f.new); ok {
			for _, s := range testSpans(after) {
				kept[s.name] = true
			}
		}
	}
	var rows []changedTest
	for _, s := range testSpans(src) {
		touched := 0
		for _, h := range f.hunks {
			if h.count > 0 {
				touched += max(0, min(s.end, h.start+h.count-1)-max(s.start, h.start)+1)
			} else if s.start <= h.start && h.start < s.end {
				touched++
			}
		}
		if touched == 0 {
			continue
		}
		t := changedTest{pr: p, newFile: f.new, row: ChangedTest{PR: p.ref(), Merged: minute(p.MergedAt), File: f.old,
			Function: s.name, Change: "modified", OldLinesTouched: touched, Session: sessionOf(sessions, p.MergedChange)}}
		if !kept[s.name] {
			t.row.Change, t.newFile = "deleted", ""
		}
		rows = append(rows, t)
	}
	return rows
}

// laterTouches finishes each changed test's row with the later merged PRs that touched it again:
// one that changed the same function in the file this PR left ("test"), and one that changed code
// this PR wrote in the test's directory, by blame ("code").
func laterTouches(tests []changedTest, blames []blamed, writers map[commit]pullrequest.MergedChange, sessions transcript.Sessions) []ChangedTest {
	type testKey struct{ slug, file, function string }
	byTest := map[testKey][]changedTest{}
	for _, t := range tests {
		k := testKey{t.pr.clone.slug, t.row.File, t.row.Function}
		byTest[k] = append(byTest[k], t)
	}
	type codeKey struct {
		earlier prKey
		dir     string
	}
	code := map[codeKey]map[*merged]bool{}
	for _, b := range blames {
		earlier, ok := earlierWriter(writers, b)
		if !ok {
			continue
		}
		k := codeKey{prKey{b.later.clone.slug, earlier.Number}, path.Dir(b.file)}
		if code[k] == nil {
			code[k] = map[*merged]bool{}
		}
		code[k][b.later] = true
	}
	type hit struct {
		pr   *merged
		text string
	}
	rows := []ChangedTest{}
	for _, t := range tests {
		var hits []hit
		if t.newFile != "" {
			for _, other := range byTest[testKey{t.pr.clone.slug, t.newFile, t.row.Function}] {
				if other.pr.MergedAt.After(t.pr.MergedAt) {
					hits = append(hits, hit{other.pr, other.row.PR + " (test)"})
				}
			}
		}
		for later := range code[codeKey{prKey{t.pr.clone.slug, t.pr.Number}, path.Dir(t.row.File)}] {
			hits = append(hits, hit{later, later.ref() + " (code)"})
		}
		slices.SortFunc(hits, func(a, b hit) int { return cmp.Or(a.pr.MergedAt.Compare(b.pr.MergedAt), cmp.Compare(a.text, b.text)) })
		hits = slices.CompactFunc(hits, func(a, b hit) bool { return a.text == b.text })
		row := t.row
		row.LaterTouches = "no"
		if len(hits) > 0 {
			var texts, links []string
			for _, h := range hits {
				texts = append(texts, h.text)
				links = append(links, h.pr.ref()+": "+sessionOf(sessions, h.pr.MergedChange))
			}
			row.LaterTouches = strings.Join(texts, ", ")
			row.LaterSessions = strings.Join(slices.Compact(links), ", ")
		}
		rows = append(rows, row)
	}
	return rows
}
