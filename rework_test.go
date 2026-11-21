package dispatch_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shop is the dispatch repo's clone made a git repo whose origin is github.com/acme/shop. A bare
// repo stands in for GitHub's copy, so a fetch reaches it and never the network. PRs merge in the
// clone, which already holds every commit the report reads.
type shop struct {
	t      *testing.T
	dir    string
	github *GitHub
	number int
	// bare is the bare repo that stands in for GitHub's copy.
	bare string
}

const shopURL = "https://github.com/acme/shop"

func newShop(t *testing.T, d *Dispatch) *shop {
	t.Helper()
	copyPlainRepo(t, d.Repo)
	bare := filepath.Join(t.TempDir(), "shop.git")
	s := &shop{t: t, dir: d.Repo, github: d.GitHub, bare: bare}
	s.git("init", "-q", "--bare", bare)
	s.git("remote", "add", "origin", shopURL+".git")
	s.git("config", "url."+bare+".insteadOf", shopURL+".git")
	s.git("push", "-q", "origin", "main")
	return s
}

func (s *shop) git(args ...string) string {
	s.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", s.dir, "-c", "user.name=t", "-c", "user.email=t@t",
		"-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(s.t, err, string(out))
	return strings.TrimSpace(string(out))
}

// write writes files into the work tree and commits them on the current branch. An empty file
// content deletes the file.
func (s *shop) write(message string, files map[string]string) {
	s.t.Helper()
	for name, content := range files {
		path := filepath.Join(s.dir, name)
		if content == "" {
			require.NoError(s.t, os.Remove(path))
			continue
		}
		require.NoError(s.t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(s.t, os.WriteFile(path, []byte(content), 0o644))
	}
	s.git("add", "-A")
	s.git("commit", "-q", "-m", message)
}

// shopPR is a PR to make on the shop.
type shopPR struct {
	branch, title, body string
	files               map[string]string
	at                  string
}

// merge makes a PR: a branch off main with one commit that writes files, merged into main with a
// merge commit, and recorded on GitHub as the gh user's PR merged at its time. It
// returns the PR's number.
func (s *shop) merge(pr shopPR) int {
	s.t.Helper()
	s.number++
	base := s.git("rev-parse", "main")
	s.git("checkout", "-q", "-b", pr.branch)
	s.write(pr.title, pr.files)
	head := s.git("rev-parse", "HEAD")
	s.git("checkout", "-q", "main")
	s.git("merge", "-q", "--no-ff", pr.branch, "-m", fmt.Sprintf("Merge pull request #%d", s.number))
	at, err := time.Parse(time.RFC3339, pr.at)
	require.NoError(s.t, err)
	s.github.OwnPullRequestMerged(ownPR{repo: "acme/shop", number: s.number, branch: pr.branch, title: pr.title,
		body: pr.body, base: base, head: head, merge: s.git("rev-parse", "HEAD"), at: at})
	return s.number
}

// numbered is n lines "<word>1" to "<word>n".
func numbered(word string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s%d\n", word, i)
	}
	return b.String()
}

// replaced is text with lines from to to (counting from 1) replaced by "<word><i>".
func replaced(text, word string, from, to int) string {
	lines := strings.Split(text, "\n")
	for i := from; i <= to; i++ {
		lines[i-1] = fmt.Sprintf("%s%d", word, i)
	}
	return strings.Join(lines, "\n")
}

type reworkJSON struct {
	Repos []struct {
		Repo      string `json:"repo"`
		MergedPRs int    `json:"merged_prs"`
		Checkout  string `json:"checkout"`
	} `json:"fix_chain_repos"`
	FixChains []struct {
		LaterDelShare   *float64 `json:"later_del_share"`
		EarlierAddShare *float64 `json:"earlier_add_share"`
		Revert          bool     `json:"revert"`
		Ticket          string   `json:"ticket"`
		Bug             *bool    `json:"bug"`
		PassesRule      bool     `json:"passes_rule"`
		LaterPR         string   `json:"later_pr"`
		LaterMerged     string   `json:"later_merged"`
		EarlierPR       string   `json:"earlier_pr"`
		EarlierMerged   string   `json:"earlier_merged"`
		Files           int      `json:"files"`
		FileList        string   `json:"file_list"`
		Lines           int      `json:"lines"`
		LaterSession    string   `json:"later_session"`
		EarlierSession  string   `json:"earlier_session"`
	} `json:"fix_chains"`
	ChangedTests []struct {
		PR              string `json:"pr"`
		Merged          string `json:"merged"`
		File            string `json:"file"`
		Function        string `json:"function"`
		Change          string `json:"change"`
		OldLinesTouched int    `json:"old_lines_touched"`
		LaterTouches    string `json:"later_pr_touching_it"`
		Session         string `json:"session"`
		LaterSessions   string `json:"later_pr_sessions"`
	} `json:"changed_existing_tests"`
	Notes []string `json:"notes"`
}

func reworkOf(t *testing.T, d *Dispatch, args ...string) reworkJSON {
	t.Helper()
	res := d.Run(append([]string{"collect-agent-failures", "--json"}, args...)...)
	require.NoError(t, res.Err, res.Stderr)
	var out reworkJSON
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &out))
	return out
}

func TestCollectAgentFailuresReportsAFixChainOfABugTicket(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	cart := numbered("c", 10)
	// ENG-1 merged before the 7-day window, and ENG-2, a Bug ticket, rewrote 6 of its 10 lines.
	s.merge(shopPR{branch: "thrawn01/eng-1-add-the-cart", title: "Add the cart", body: "Fixes ENG-1",
		files: map[string]string{"cart.go": cart}, at: "2026-10-01T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-fix-the-cart", title: "Fix the cart", body: "Fixes ENG-2",
		files: map[string]string{"cart.go": replaced(cart, "x", 3, 8)}, at: "2026-10-08T10:00:00Z"})
	d.Linear.Tickets(withLabel(ticket("ENG-2", "Done", ""), "Bug"))

	out := reworkOf(t, d)

	require.Len(t, out.Repos, 1)
	assert.Equal(t, "acme/shop", out.Repos[0].Repo)
	assert.Equal(t, 1, out.Repos[0].MergedPRs)
	require.Len(t, out.FixChains, 1)
	c := out.FixChains[0]
	assert.Equal(t, "shop#2", c.LaterPR)
	assert.Equal(t, "2026-10-08T10:00", c.LaterMerged)
	assert.Equal(t, "shop#1", c.EarlierPR)
	assert.Equal(t, "2026-10-01T10:00", c.EarlierMerged)
	assert.Equal(t, 6, c.Lines)
	assert.Equal(t, new(1.0), c.LaterDelShare)
	assert.Equal(t, new(0.6), c.EarlierAddShare)
	assert.Equal(t, "ENG-2", c.Ticket)
	assert.Equal(t, new(true), c.Bug)
	assert.False(t, c.Revert)
	assert.True(t, c.PassesRule)
	assert.Equal(t, 1, c.Files)
	assert.Equal(t, "cart.go", c.FileList)
	assert.Empty(t, out.Notes)

	res := d.Run("collect-agent-failures")
	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "## 3a. Repos with merged PRs (1)\n")
	assert.Contains(t, res.Stdout, "| acme/shop | 1 | "+d.Repo+" |")
	assert.Contains(t, res.Stdout, "## 3b. Fix chains (earlier PR merged within 30 days) (1 of 1 pass the rule)\n")
	assert.Contains(t, res.Stdout, "| shop#2 | 2026-10-08T10:00 | shop#1 | 2026-10-01T10:00 | 1 | 6 | 1 | 0.6 | false | ENG-2 | true | not proven | not proven | cart.go |")
	assert.Contains(t, res.Stdout, "1 of 1 rows shown; --json prints every row with passes_rule.")
	assert.Contains(t, res.Stdout, "The 0.5 threshold is unvalidated")
	assert.Contains(t, res.Stdout, "## Notes (0)\n")
}

func TestCollectAgentFailuresPrintsOnlyTheFixChainsThatPassTheRule(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	// Lines on main that no PR wrote count toward no fix chain.
	s.write("Seed", map[string]string{"seed.go": numbered("s", 3)})
	s.merge(shopPR{branch: "thrawn01/eng-1-start", title: "Start", body: "Fixes ENG-1",
		files: map[string]string{"a.go": numbered("a", 4), "b.go": numbered("b", 4), "c.go": numbered("c", 4),
			"go.sum": numbered("g", 4), "a_test.go": numbered("t", 4)},
		at: "2026-10-03T10:00:00Z"})
	// A Feature ticket rewrote all of a.go, and its go.sum and test lines count for nothing.
	s.merge(shopPR{branch: "thrawn01/eng-2-feature", title: "Grow a", body: "Fixes ENG-2",
		files: map[string]string{"a.go": numbered("x", 4), "go.sum": numbered("y", 4), "a_test.go": numbered("z", 4)},
		at:    "2026-10-04T10:00:00Z"})
	// A revert passes with no ticket.
	s.merge(shopPR{branch: "revert-1-start", title: `Revert "Start"`, body: "This reverts commit abc.",
		files: map[string]string{"b.go": numbered("r", 2)}, at: "2026-10-05T10:00:00Z"})
	// A Bug ticket whose deleted lines are mostly the seed's, not the earlier PR's.
	s.merge(shopPR{branch: "thrawn01/eng-5-bug", title: "Fix c", body: "Fixes ENG-5",
		files: map[string]string{"c.go": replaced(numbered("c", 4), "w", 1, 1), "seed.go": numbered("v", 3)},
		at:    "2026-10-06T10:00:00Z"})
	d.Linear.Tickets(withLabel(ticket("ENG-2", "Done", ""), "Feature"), withLabel(ticket("ENG-5", "Done", ""), "Bug"))

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 3)
	revert := out.FixChains[0]
	assert.Equal(t, "shop#3", revert.LaterPR)
	assert.True(t, revert.Revert)
	assert.Empty(t, revert.Ticket)
	assert.Nil(t, revert.Bug)
	assert.True(t, revert.PassesRule)
	feature := out.FixChains[1]
	assert.Equal(t, "shop#2", feature.LaterPR)
	assert.Equal(t, 4, feature.Lines)
	assert.Equal(t, "a.go", feature.FileList)
	assert.Equal(t, new(1.0), feature.LaterDelShare)
	assert.Equal(t, new(false), feature.Bug)
	assert.False(t, feature.PassesRule)
	bug := out.FixChains[2]
	assert.Equal(t, "shop#4", bug.LaterPR)
	assert.Equal(t, 1, bug.Lines)
	assert.Equal(t, new(0.25), bug.LaterDelShare)
	assert.Equal(t, new(true), bug.Bug)
	assert.False(t, bug.PassesRule)

	res := d.Run("collect-agent-failures")
	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "(1 of 3 pass the rule)")
	assert.Contains(t, res.Stdout, "| shop#3 |")
	assert.NotContains(t, res.Stdout, "| shop#2 |")
	assert.NotContains(t, res.Stdout, "| shop#4 |")
	assert.Contains(t, res.Stdout, "1 of 3 rows shown;")
	assert.Contains(t, res.Stdout, "; 1 rows have no label data, so bug is empty there.")
}

func TestCollectAgentFailuresLeavesOutAnEarlierPROlderThan30Days(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-old", title: "Old", files: map[string]string{"a.go": numbered("a", 4)}, at: "2026-09-07T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-new", title: "New", files: map[string]string{"a.go": numbered("b", 4)}, at: "2026-10-08T10:00:00Z"})

	out := reworkOf(t, d)

	assert.Empty(t, out.FixChains)
}

const cartTests = `package cart

func TestAdd(t *testing.T) {
	add(1)
}

func TestRemove(t *testing.T) {
	remove(1)
}
`

func TestCollectAgentFailuresReportsChangedExistingTests(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-cart", title: "Cart",
		files: map[string]string{"cart/cart_test.go": cartTests, "cart/cart.go": "package cart\n\nfunc add() {}\n"},
		at:    "2026-10-03T10:00:00Z"})
	// ENG-2 changes TestAdd, deletes TestRemove and changes the code ENG-1 wrote.
	s.merge(shopPR{branch: "thrawn01/eng-2-cart", title: "Cart again",
		files: map[string]string{
			"cart/cart_test.go": "package cart\n\nfunc TestAdd(t *testing.T) {\n\tadd(2)\n}\n",
			"cart/cart.go":      "package cart\n\nfunc add() { _ = 2 }\n",
		},
		at: "2026-10-04T10:00:00Z"})
	// ENG-3 changes TestAdd again and the code ENG-2 wrote beside it.
	s.merge(shopPR{branch: "thrawn01/eng-3-cart", title: "Cart once more",
		files: map[string]string{
			"cart/cart_test.go": "package cart\n\nfunc TestAdd(t *testing.T) {\n\tadd(3)\n}\n",
			"cart/cart.go":      "package cart\n\nfunc add() { _ = 3 }\n",
		},
		at: "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.ChangedTests, 3)
	add := out.ChangedTests[0]
	assert.Equal(t, "shop#2", add.PR)
	assert.Equal(t, "2026-10-04T10:00", add.Merged)
	assert.Equal(t, "cart/cart_test.go", add.File)
	assert.Equal(t, "TestAdd", add.Function)
	assert.Equal(t, "modified", add.Change)
	// Git diffs the change as one hunk, old lines 4 to 8, which holds 2 lines of each function.
	assert.Equal(t, 2, add.OldLinesTouched)
	assert.Equal(t, "shop#3 (code), shop#3 (test)", add.LaterTouches)
	assert.Equal(t, "not proven", add.Session)
	assert.Equal(t, "shop#3: not proven", add.LaterSessions)
	remove := out.ChangedTests[1]
	assert.Equal(t, "shop#2", remove.PR)
	assert.Equal(t, "TestRemove", remove.Function)
	assert.Equal(t, "deleted", remove.Change)
	assert.Equal(t, 2, remove.OldLinesTouched)
	assert.Equal(t, "shop#3 (code)", remove.LaterTouches)
	again := out.ChangedTests[2]
	assert.Equal(t, "shop#3", again.PR)
	assert.Equal(t, "TestAdd", again.Function)
	assert.Equal(t, "no", again.LaterTouches)
	assert.Empty(t, again.LaterSessions)

	res := d.Run("collect-agent-failures")
	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "## 4. Changed existing Go tests (3)\n")
	assert.Contains(t, res.Stdout, "| shop#2 | 2026-10-04T10:00 | cart/cart_test.go | TestRemove | deleted | 2 | shop#3 (code) | not proven | shop#3: not proven |")
}

// branchRecord is a user record of a session that ran on branch.
func branchRecord(at, branch string) map[string]any {
	return map[string]any{"type": "user", "timestamp": at, "gitBranch": branch, "message": map[string]any{"content": "go on"}}
}

func TestCollectAgentFailuresLinksEachPRToTheSessionThatMadeIt(t *testing.T) {
	t.Parallel()
	d, project := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"a.go": numbered("a", 4)}, at: "2026-09-25T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2", files: map[string]string{"a.go": numbered("b", 4)}, at: "2026-10-05T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-3-a", title: "A3", files: map[string]string{"a.go": numbered("c", 4)}, at: "2026-10-06T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-4-a", title: "A4", files: map[string]string{"a.go": numbered("d", 4)}, at: "2026-10-07T10:00:00Z"})
	// The oldest record on disk is from 2026-10-01, after shop#1 merged.
	writeTranscript(t, filepath.Join(project, "00000000-old.jsonl"), assistantText("2026-10-01T00:00:00.000Z", "hello"))
	// A subagent of session aaaaaaaa opened shop#2.
	writeTranscript(t, filepath.Join(project, "aaaaaaaa-1111", "subagents", "agent-a1.jsonl"),
		bash("2026-10-04T10:00:00.000Z", "/src", "pr1", `gh pr create --title "A2" --body "Fixes ENG-2"`),
		toolResult("2026-10-04T10:00:01.000Z", "pr1", shopURL+"/pull/2\n", false),
	)
	// Session cccccccc opened shop#21, whose URL starts with shop#2's.
	writeTranscript(t, filepath.Join(project, "cccccccc-3333.jsonl"),
		bash("2026-10-04T11:00:00.000Z", "/src", "pr21", "gh pr create --fill"),
		toolResult("2026-10-04T11:00:01.000Z", "pr21", shopURL+"/pull/21\n", false),
		// A gh pr create that failed proves nothing.
		bash("2026-10-04T11:00:02.000Z", "/src", "pr4", "gh pr create --fill"),
		toolResult("2026-10-04T11:00:03.000Z", "pr4", "Exit code 1\n"+shopURL+"/pull/4 already exists", true),
	)
	// Only session bbbbbbbb ran on shop#3's branch, and two sessions ran on shop#4's.
	writeTranscript(t, filepath.Join(project, "bbbbbbbb-2222.jsonl"),
		branchRecord("2026-10-06T09:00:00.000Z", "thrawn01/eng-3-a"), branchRecord("2026-10-06T09:00:00.000Z", "thrawn01/eng-4-a"))
	writeTranscript(t, filepath.Join(project, "dddddddd-4444.jsonl"), branchRecord("2026-10-07T09:00:00.000Z", "thrawn01/eng-4-a"))

	out := reworkOf(t, d)

	sessions := map[string][2]string{}
	for _, c := range out.FixChains {
		sessions[c.LaterPR+" over "+c.EarlierPR] = [2]string{c.LaterSession, c.EarlierSession}
	}
	assert.Equal(t, map[string][2]string{
		"shop#2 over shop#1": {"aaaaaaaa/agent-a1 (gh pr create)", "not on disk"},
		"shop#3 over shop#2": {"bbbbbbbb (branch)", "aaaaaaaa/agent-a1 (gh pr create)"},
		"shop#4 over shop#3": {"not proven", "bbbbbbbb (branch)"},
	}, sessions)
}

func TestCollectAgentFailuresNotesBugLabelsItCannotRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Key: noKey, Env: map[string]string{"CLAUDE_CONFIG_DIR": ""}})
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"a.go": numbered("a", 4)}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2", body: "Fixes ENG-2", files: map[string]string{"a.go": numbered("b", 4)}, at: "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, "ENG-2", out.FixChains[0].Ticket)
	assert.Nil(t, out.FixChains[0].Bug)
	assert.False(t, out.FixChains[0].PassesRule)
	require.Len(t, out.Notes, 1)
	assert.Contains(t, out.Notes[0], "bug labels not read: no Linear API key")
}

func TestCollectAgentFailuresNotesARepoThatIsNotOnGitHub(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	copyPlainRepo(t, d.Repo)
	hintGit(t, d.Repo, "remote", "add", "origin", "/srv/git/shop.git")

	out := reworkOf(t, d)

	assert.Empty(t, out.Repos)
	assert.Equal(t, []string{"dispatch: remote origin of " + d.Repo + " is not a github.com repo"}, out.Notes)
}

func TestCollectAgentFailuresReadsTheBugLabelInAnyCase(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", body: "Fixes ENG-1",
		files: map[string]string{"a.go": numbered("a", 4)}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "Fix a", body: "Fixes ENG-2",
		files: map[string]string{"a.go": numbered("b", 4)}, at: "2026-10-05T10:00:00Z"})
	d.Linear.Tickets(withLabel(ticket("ENG-2", "Done", ""), "bug"))

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, new(true), out.FixChains[0].Bug)
	assert.True(t, out.FixChains[0].PassesRule)
}

func TestCollectAgentFailuresPassesABugTicketWhoseLaterDelShareIsExactlyHalf(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.write("Seed", map[string]string{"seed.go": numbered("s", 2)})
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", body: "Fixes ENG-1",
		files: map[string]string{"a.go": numbered("a", 2)}, at: "2026-10-03T10:00:00Z"})
	// One of the two lines ENG-2 changes is ENG-1's, the other the seed's.
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "Fix a", body: "Fixes ENG-2",
		files: map[string]string{"a.go": replaced(numbered("a", 2), "x", 1, 1), "seed.go": replaced(numbered("s", 2), "v", 1, 1)},
		at:    "2026-10-05T10:00:00Z"})
	d.Linear.Tickets(withLabel(ticket("ENG-2", "Done", ""), "Bug"))

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, 1, out.FixChains[0].Lines)
	assert.Equal(t, new(0.5), out.FixChains[0].LaterDelShare)
	assert.True(t, out.FixChains[0].PassesRule)

	res := d.Run("collect-agent-failures")
	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "| shop#2 |")
}

func TestCollectAgentFailuresLeavesLockfilesAndGeneratedGoOutOfFixChains(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	skipped := []string{"package-lock.json", "web/pnpm-lock.yaml", "Cargo.lock", "yarn.lock", "api/api.pb.go",
		"api/api.pb.gw.go", "mock_gen.go", "k8s/zz_generated.deepcopy.go", "vendor/x/x.go"}
	first := map[string]string{"a.go": numbered("a", 4)}
	second := map[string]string{"a.go": numbered("b", 4)}
	for _, name := range skipped {
		first[name] = numbered("g", 4)
		second[name] = numbered("h", 4)
	}
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: first, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2", files: second, at: "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, "a.go", out.FixChains[0].FileList)
	assert.Equal(t, 1, out.FixChains[0].Files)
	assert.Equal(t, 4, out.FixChains[0].Lines)
	assert.Equal(t, new(1.0), out.FixChains[0].LaterDelShare)
	assert.Equal(t, new(1.0), out.FixChains[0].EarlierAddShare)
}

// Go marks a generated file with a "Code generated ... DO NOT EDIT." line, whatever its name
// (https://go.dev/s/generatedcode).
func TestCollectAgentFailuresLeavesGoMarkedGeneratedOutOfFixChains(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	const header = "// Code generated by sqlc. DO NOT EDIT.\n"
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A",
		files: map[string]string{"a.go": numbered("a", 4), "db/models.go": header + numbered("g", 4)}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2",
		files: map[string]string{"a.go": numbered("b", 4), "db/models.go": header + numbered("h", 4)}, at: "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, "a.go", out.FixChains[0].FileList)
	assert.Equal(t, 4, out.FixChains[0].Lines)
	assert.Equal(t, new(1.0), out.FixChains[0].LaterDelShare)
	assert.Equal(t, new(1.0), out.FixChains[0].EarlierAddShare)
}

func TestCollectAgentFailuresBlamesTheLinesAtTheLaterPRsMergeBase(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	// Lines 1 to 5 of a.go are the seed's and 6 to 10 shop#1's.
	s.write("Seed", map[string]string{"a.go": numbered("s", 5)})
	full := numbered("s", 5) + numbered("a", 5)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"a.go": full}, at: "2026-10-03T10:00:00Z"})
	// shop#3 branches off here and changes lines 6 and 7, shop#1's.
	s.git("checkout", "-q", "-b", "thrawn01/eng-3-a")
	s.write("Change a", map[string]string{"a.go": replaced(full, "b", 6, 7)})
	head := s.git("rev-parse", "HEAD")
	s.git("checkout", "-q", "main")
	// shop#2 merges first and puts 5 lines on top, so at main lines 6 and 7 are the seed's.
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "Top", files: map[string]string{"a.go": numbered("c", 5) + full}, at: "2026-10-04T10:00:00Z"})
	s.number++
	base := s.git("rev-parse", "main")
	s.git("merge", "-q", "--no-ff", "thrawn01/eng-3-a", "-m", "Merge pull request #3")
	s.github.OwnPullRequestMerged(ownPR{repo: "acme/shop", number: 3, branch: "thrawn01/eng-3-a", title: "Change a",
		base: base, head: head, merge: s.git("rev-parse", "HEAD"), at: time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)})

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, "shop#3", out.FixChains[0].LaterPR)
	assert.Equal(t, "shop#1", out.FixChains[0].EarlierPR)
	assert.Equal(t, 2, out.FixChains[0].Lines)
	assert.Equal(t, new(1.0), out.FixChains[0].LaterDelShare)
}

func TestCollectAgentFailuresTakesNoURLFromACommandOtherThanGHPRCreate(t *testing.T) {
	t.Parallel()
	d, project := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"a.go": numbered("a", 4)}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2", files: map[string]string{"a.go": numbered("b", 4)}, at: "2026-10-05T10:00:00Z"})
	writeTranscript(t, filepath.Join(project, "eeeeeeee-5555.jsonl"),
		bash("2026-10-01T10:00:00.000Z", "/src", "view", "gh pr view 2 --json url"),
		toolResult("2026-10-01T10:00:01.000Z", "view", shopURL+"/pull/2\n", false),
	)

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, "not proven", out.FixChains[0].LaterSession)
}

func TestCollectAgentFailuresLinksEachChangedTestsPRToItsSession(t *testing.T) {
	t.Parallel()
	d, project := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-cart", title: "Cart",
		files: map[string]string{"cart/cart_test.go": cartTests}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-cart", title: "Cart again",
		files: map[string]string{"cart/cart_test.go": strings.Replace(cartTests, "add(1)", "add(2)", 1)}, at: "2026-10-04T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-3-cart", title: "Cart once more",
		files: map[string]string{"cart/cart_test.go": strings.Replace(cartTests, "add(1)", "add(3)", 1)}, at: "2026-10-05T10:00:00Z"})
	writeTranscript(t, filepath.Join(project, "aaaaaaaa-1111.jsonl"),
		bash("2026-10-01T10:00:00.000Z", "/src", "pr2", "gh pr create --fill"),
		toolResult("2026-10-01T10:00:01.000Z", "pr2", shopURL+"/pull/2\n", false),
	)
	writeTranscript(t, filepath.Join(project, "bbbbbbbb-2222.jsonl"), branchRecord("2026-10-05T09:00:00.000Z", "thrawn01/eng-3-cart"))

	out := reworkOf(t, d)

	require.Len(t, out.ChangedTests, 2)
	assert.Equal(t, "shop#2", out.ChangedTests[0].PR)
	assert.Equal(t, "aaaaaaaa (gh pr create)", out.ChangedTests[0].Session)
	assert.Equal(t, "shop#3 (test)", out.ChangedTests[0].LaterTouches)
	assert.Equal(t, "shop#3: bbbbbbbb (branch)", out.ChangedTests[0].LaterSessions)
	assert.Equal(t, "shop#3", out.ChangedTests[1].PR)
	assert.Equal(t, "bbbbbbbb (branch)", out.ChangedTests[1].Session)

	res := d.Run("collect-agent-failures")
	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "| shop#2 | 2026-10-04T10:00 | cart/cart_test.go | TestAdd | modified | 1 | shop#3 (test) | aaaaaaaa (gh pr create) | shop#3: bbbbbbbb (branch) |")
}

func TestCollectAgentFailuresLeavesOutATestThePRAdded(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-cart", title: "Cart",
		files: map[string]string{"cart/cart_test.go": cartTests}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-cart", title: "More tests",
		files: map[string]string{"cart/cart_test.go": cartTests + "\nfunc TestEmpty(t *testing.T) {\n\tadd(0)\n}\n"}, at: "2026-10-04T10:00:00Z"})

	out := reworkOf(t, d)

	assert.Empty(t, out.ChangedTests)
}

// state is what a report must leave as it was in a clone: the checkout, the work tree, the
// index, local branches and tags, and the stash.
func (s *shop) state() []string {
	return []string{s.git("rev-parse", "HEAD"), s.git("symbolic-ref", "HEAD"), s.git("status", "--porcelain"),
		s.git("for-each-ref", "refs/heads", "refs/tags"), s.git("stash", "list")}
}

func TestCollectAgentFailuresFetchesTheCloneAndChangesNothingElseInIt(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	// The PRs merge in another clone, so the report's clone holds their commits only after a fetch.
	other := &shop{t: t, dir: filepath.Join(t.TempDir(), "other"), github: d.GitHub}
	s.git("clone", "-q", "-b", "main", s.bare, other.dir)
	other.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"a.go": numbered("a", 4)}, at: "2026-10-03T10:00:00Z"})
	other.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2", files: map[string]string{"a.go": numbered("b", 4)}, at: "2026-10-05T10:00:00Z"})
	other.git("push", "-q", "origin", "main")
	// The report's clone is on a branch of its own with uncommitted work.
	s.git("checkout", "-q", "-b", "wip")
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, "staged.txt"), []byte("staged\n"), 0o644))
	s.git("add", "staged.txt")
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, "untracked.txt"), []byte("untracked\n"), 0o644))
	before := s.state()

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, "shop#2", out.FixChains[0].LaterPR)
	assert.Equal(t, "shop#1", out.FixChains[0].EarlierPR)
	assert.Equal(t, before, s.state())
	assert.Equal(t, other.git("rev-parse", "main"), s.git("rev-parse", "origin/main"))
}

func TestCollectAgentFailuresFollowsPathsGitQuotes(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	// Git ends a path with a space with a tab, and C-quotes a path with a quote or a backslash.
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A",
		files: map[string]string{"pkg/my file.go": numbered("a", 2), `pkg/q"uote.go`: numbered("b", 2), `pkg/back\slash.go`: numbered("c", 2)},
		at:    "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2",
		files: map[string]string{"pkg/my file.go": numbered("x", 2), `pkg/q"uote.go`: numbered("y", 2), `pkg/back\slash.go`: numbered("z", 2)},
		at:    "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, `pkg/back\slash.go, pkg/my file.go, pkg/q"uote.go`, out.FixChains[0].FileList)
	assert.Equal(t, 6, out.FixChains[0].Lines)
	assert.Equal(t, new(1.0), out.FixChains[0].LaterDelShare)
	assert.Equal(t, new(1.0), out.FixChains[0].EarlierAddShare)
}

func TestCollectAgentFailuresLeavesARenamedTestFileOutOfTheShares(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A",
		files: map[string]string{"a.go": numbered("a", 4), "pkg/a_test.go": numbered("t", 10), "pkg/testdata/f.txt": numbered("f", 10)},
		at:    "2026-10-03T10:00:00Z"})
	// The later PR renames the test file and the testdata directory and edits a line in each.
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2",
		files: map[string]string{"a.go": numbered("b", 4), "pkg/a_test.go": "", "pkg/b_test.go": replaced(numbered("t", 10), "u", 1, 1),
			"pkg/testdata/f.txt": "", "pkg/fixtures/f.txt": replaced(numbered("f", 10), "g", 1, 1)},
		at: "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, "a.go", out.FixChains[0].FileList)
	assert.Equal(t, new(1.0), out.FixChains[0].LaterDelShare)
}

const oneLineTests = `package cart

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func helper() int {
	return 1
}

func TestNoop(t *testing.T) {}
`

func TestCollectAgentFailuresReadsATestFunctionOnOneLine(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"cart_test.go": oneLineTests}, at: "2026-10-03T10:00:00Z"})
	// The later PR changes the helper and deletes TestNoop. TestMain is untouched.
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2",
		files: map[string]string{"cart_test.go": "package cart\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n\nfunc helper() int {\n\treturn 2\n}\n"},
		at:    "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.ChangedTests, 1)
	assert.Equal(t, "TestNoop", out.ChangedTests[0].Function)
	assert.Equal(t, "deleted", out.ChangedTests[0].Change)
	assert.Equal(t, 1, out.ChangedTests[0].OldLinesTouched)
}

func TestCollectAgentFailuresCreditsAPRToTheAgentThatMadeIt(t *testing.T) {
	t.Parallel()
	d, project := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"a.go": numbered("a", 4)}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2", files: map[string]string{"a.go": numbered("b", 4)}, at: "2026-10-05T10:00:00Z"})
	create := bash("2026-10-04T10:00:00.000Z", "/src", "pr2", "gh pr create --fill")
	created := toolResult("2026-10-04T10:00:01.000Z", "pr2", shopURL+"/pull/2\n", false)
	writeTranscript(t, filepath.Join(project, "aaaaaaaa-1111.jsonl"), create, created)
	// A subagent's transcript repeats the records of the session that started it.
	writeTranscript(t, filepath.Join(project, "aaaaaaaa-1111", "subagents", "agent-a1.jsonl"), create, created)

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, "aaaaaaaa (gh pr create)", out.FixChains[0].LaterSession)
}

func TestCollectAgentFailuresReadsOnlyARevertTitleAsARevert(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"a.go": numbered("a", 4)}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "Reverting to the old cache", files: map[string]string{"a.go": numbered("b", 4)}, at: "2026-10-05T10:00:00Z"})
	s.merge(shopPR{branch: "revert-2", title: `Revert "Reverting to the old cache"`, files: map[string]string{"a.go": numbered("c", 4)}, at: "2026-10-06T10:00:00Z"})

	out := reworkOf(t, d)

	reverts := map[string]bool{}
	for _, c := range out.FixChains {
		reverts[c.LaterPR] = c.Revert
	}
	assert.Equal(t, map[string]bool{"shop#2": false, "shop#3": true}, reverts)
}

func TestCollectAgentFailuresNotesTicketsItCannotNameWithoutLinearTeams(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[plugins.linear]\nteams = []\n", Env: map[string]string{"CLAUDE_CONFIG_DIR": ""}})
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"a.go": numbered("a", 4)}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2", body: "Fixes ENG-2", files: map[string]string{"a.go": numbered("b", 4)}, at: "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Empty(t, out.FixChains[0].Ticket)
	assert.Equal(t, []string{"tickets not read: the config lists no Linear teams"}, out.Notes)
}

// rawStringTest holds Go source in a raw string, whose lines start with "}" and "func ".
const rawStringTest = "package cart\n\nfunc TestParse(t *testing.T) {\n\tsrc := `package y\n}\nfunc TestFake() {}\n`\n\tcheck(t, src)\n\tcheck(t, src)\n}\n"

func TestCollectAgentFailuresReadsATestThatHoldsGoSourceInARawString(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"cart_test.go": rawStringTest}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2",
		files: map[string]string{"cart_test.go": strings.Replace(rawStringTest, "\tcheck(t, src)\n}", "\tcheck(t, nil)\n}", 1)},
		at:    "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.ChangedTests, 1)
	assert.Equal(t, "TestParse", out.ChangedTests[0].Function)
	assert.Equal(t, "modified", out.ChangedTests[0].Change)
	assert.Equal(t, 1, out.ChangedTests[0].OldLinesTouched)
}

func TestCollectAgentFailuresLeavesAFileTheLaterPRMadeGeneratedOutOfBothSidesOfTheShare(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"api.go": numbered("a", 10), "other.go": numbered("o", 4)}, at: "2026-10-03T10:00:00Z"})
	// The later PR replaces api.go with generated output and rewrites other.go.
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2",
		files: map[string]string{"api.go": "// Code generated by oapi. DO NOT EDIT.\n" + numbered("g", 3), "other.go": numbered("p", 4)},
		at:    "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, "other.go", out.FixChains[0].FileList)
	assert.Equal(t, 4, out.FixChains[0].Lines)
	assert.Equal(t, new(1.0), out.FixChains[0].LaterDelShare)
}

// helperTests has a helper whose name starts with Test and a //line comment, as generated tests carry.
const helperTests = "package cart\n\nfunc Testable() int {\n\treturn 1\n}\n\n//line gen.go:100\nfunc TestCart(t *testing.T) {\n\tcheck(t, 1)\n}\n"

func TestCollectAgentFailuresReadsTestNamesAndLinesAsGoTestDoes(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"cart_test.go": helperTests}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2",
		files: map[string]string{"cart_test.go": strings.NewReplacer("return 1", "return 2", "check(t, 1)", "check(t, 2)").Replace(helperTests)},
		at:    "2026-10-05T10:00:00Z"})

	out := reworkOf(t, d)

	require.Len(t, out.ChangedTests, 1)
	assert.Equal(t, "TestCart", out.ChangedTests[0].Function)
	assert.Equal(t, 1, out.ChangedTests[0].OldLinesTouched)
}

func TestCollectAgentFailuresReadsChangedLinesWhateverTheClonesGitConfig(t *testing.T) {
	t.Parallel()
	d, _ := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"a.go": numbered("a", 5)}, at: "2026-10-02T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2", files: map[string]string{"a.go": numbered("b", 5)}, at: "2026-10-03T10:00:00Z"})
	// The later PR changes lines 1 and 5 only.
	s.merge(shopPR{branch: "thrawn01/eng-3-a", title: "A3", files: map[string]string{"a.go": replaced(replaced(numbered("b", 5), "c", 1, 1), "c", 5, 5)}, at: "2026-10-05T10:00:00Z"})
	// Settings that would join the two hunks and credit #2's lines to #1.
	s.git("config", "diff.interHunkContext", "5")
	revs := filepath.Join(t.TempDir(), "ignore-revs")
	require.NoError(t, os.WriteFile(revs, []byte(s.git("rev-parse", "thrawn01/eng-2-a")+"\n"), 0o644))
	s.git("config", "blame.ignoreRevsFile", revs)

	out := reworkOf(t, d)

	got := map[string]int{}
	for _, c := range out.FixChains {
		got[c.LaterPR+" over "+c.EarlierPR] = c.Lines
	}
	assert.Equal(t, map[string]int{"shop#2 over shop#1": 5, "shop#3 over shop#2": 2}, got)
}

func TestCollectAgentFailuresTakesNoURLFromTextThatOnlyNamesGHPRCreate(t *testing.T) {
	t.Parallel()
	d, project := transcripts(t)
	s := newShop(t, d)
	s.merge(shopPR{branch: "thrawn01/eng-1-a", title: "A", files: map[string]string{"a.go": numbered("a", 4)}, at: "2026-10-03T10:00:00Z"})
	s.merge(shopPR{branch: "thrawn01/eng-2-a", title: "A2", files: map[string]string{"a.go": numbered("b", 4)}, at: "2026-10-05T10:00:00Z"})
	writeTranscript(t, filepath.Join(project, "eeeeeeee-5555.jsonl"),
		bash("2026-10-01T10:00:00.000Z", "/src", "print", "printf 'run gh pr create, got %s\\n' "+shopURL+"/pull/2"),
		toolResult("2026-10-01T10:00:01.000Z", "print", "run gh pr create, got "+shopURL+"/pull/2\n", false),
		bash("2026-10-01T10:01:00.000Z", "/src", "made", "cd /src && GH_TOKEN=x gh pr create --fill"),
		toolResult("2026-10-01T10:01:01.000Z", "made", shopURL+"/pull/1\n", false),
	)

	out := reworkOf(t, d)

	require.Len(t, out.FixChains, 1)
	assert.Equal(t, "not proven", out.FixChains[0].LaterSession)
	assert.Equal(t, "eeeeeeee (gh pr create)", out.FixChains[0].EarlierSession)
}
