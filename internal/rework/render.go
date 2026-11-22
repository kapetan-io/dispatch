package rework

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kapetan-io/dispatch/internal/agentfailure"
)

// WriteMarkdown writes the tables, numbered 3a, 3b and 4 after the agent failure tables 1 to 2b.
// The fix chain table shows only the rows that pass the rule, and its footer counts every row.
func (r Tables) WriteMarkdown(w io.Writer) error {
	var b strings.Builder
	var repos [][]string
	for _, row := range r.Repos {
		repos = append(repos, []string{row.Repo, fmt.Sprint(row.MergedPRs), row.Checkout})
	}
	agentfailure.Section(&b, fmt.Sprintf("3a. Repos with merged PRs (%d)", len(repos)), []string{"repo", "merged_prs", "checkout"}, repos)

	var chains [][]string
	missing := 0
	for _, c := range r.FixChains {
		if c.Bug == nil {
			missing++
		}
		if !c.PassesRule {
			continue
		}
		chains = append(chains, []string{c.LaterPR, c.LaterMerged, c.EarlierPR, c.EarlierMerged, fmt.Sprint(c.Files), fmt.Sprint(c.Lines),
			optionalFloat(c.LaterDelShare), optionalFloat(c.EarlierAddShare), fmt.Sprint(c.Revert), c.Ticket, optionalBool(c.Bug),
			c.LaterSession, c.EarlierSession, c.FileList})
	}
	agentfailure.Section(&b, fmt.Sprintf("3b. Fix chains (earlier PR merged within %d days) (%d of %d pass the rule)", int(FixWindow.Hours()/24), len(chains), len(r.FixChains)),
		[]string{"later_pr", "later_merged", "earlier_pr", "earlier_merged", "files", "lines", "later_del_share",
			"earlier_add_share", "revert", "ticket", "bug", "later_session", "earlier_session", "file_list"}, chains)
	fmt.Fprintf(&b, "%d of %d rows shown; --json prints every row with passes_rule. A row passes when revert is true, or bug is true and later_del_share >= %g. "+
		"The %g threshold is unvalidated: it was fitted on 20 hand-checked rows with 2 real fixes. bug is whether the later PR's ticket carries the Bug label",
		len(chains), len(r.FixChains), RuleShare, RuleShare)
	if missing > 0 {
		fmt.Fprintf(&b, "; %d rows have no label data, so bug is empty there", missing)
	}
	b.WriteString(".\n\n")

	var tests [][]string
	for _, t := range r.ChangedTests {
		tests = append(tests, []string{t.PR, t.Merged, t.File, t.Function, t.Change, fmt.Sprint(t.OldLinesTouched), t.LaterTouches, t.Session, t.LaterSessions})
	}
	agentfailure.Section(&b, fmt.Sprintf("4. Changed existing Go tests (%d)", len(tests)), []string{"pr", "merged", "file", "function", "change",
		"old_lines_touched", "later_pr_touching_it", "session", "later_pr_sessions"}, tests)

	var notes [][]string
	for _, n := range r.Notes {
		notes = append(notes, []string{n})
	}
	agentfailure.Section(&b, fmt.Sprintf("Notes (%d)", len(notes)), []string{"note"}, notes)
	_, err := io.WriteString(w, b.String())
	return err
}

func optionalFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func optionalBool(v *bool) string {
	if v == nil {
		return ""
	}
	return strconv.FormatBool(*v)
}
