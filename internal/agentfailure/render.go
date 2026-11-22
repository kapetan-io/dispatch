package agentfailure

import (
	"fmt"
	"io"
	"strings"
)

// WriteMarkdown writes the report as Markdown tables under a heading that names the window. The
// tables keep the pattern miner's titles and numbers (1, 1b, 2a, 2b), which its later tables
// continue from 3.
func (r Report) WriteMarkdown(w io.Writer, from, to string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent failures from %s to %s\n\n", from, to)
	fmt.Fprintf(&b, "Read %d transcript files.", r.TranscriptFiles)
	if len(r.SkippedSessions) > 0 {
		fmt.Fprintf(&b, " Skipped sessions that ran the pattern miner: %s.", strings.Join(r.SkippedSessions, ", "))
	}
	b.WriteString("\n\n")

	failureCols := []string{"signature", "count", "sessions", "repos", "first_seen", "last_seen", "example"}
	Section(&b, fmt.Sprintf("1. Tool failures (%d)", len(r.ToolFailures)), failureCols, failureCells(r.ToolFailures))
	fmt.Fprintf(&b, "Dropped %d signatures seen once.\n\n", r.DroppedSingletons.Signatures)
	Section(&b, fmt.Sprintf("1b. Permission denials (%d)", len(r.PermissionDenials)), failureCols, failureCells(r.PermissionDenials))

	var denials [][]string
	for _, d := range r.SubagentDenials {
		denials = append(denials, []string{d.Session, d.Agent, d.Description, d.Time, d.Kind, d.Denied,
			d.NextActionOnSameFile, d.ReportNamesFile, d.ReportDenialWords, d.ReportSource})
	}
	Section(&b, fmt.Sprintf("2a. Subagent permission denials (%d)", len(denials)), []string{"session", "agent", "description", "time", "kind",
		"denied", "next_action_on_same_file", "report_names_file", "report_denial_words", "report_source"}, denials)

	var edits [][]string
	for _, e := range r.RepeatedEdits {
		edits = append(edits, []string{e.Session, e.Agent, e.Repo, e.File, fmt.Sprint(e.Edits), e.First, e.Last})
	}
	Section(&b, fmt.Sprintf("2b. Files edited %d+ times by one agent or session (%d)", MinimumEdits, len(edits)),
		[]string{"session", "agent", "repo", "file", "edits", "first", "last"}, edits)

	_, err := io.WriteString(w, b.String())
	return err
}

func failureCells(rows []Failure) [][]string {
	var cells [][]string
	for _, f := range rows {
		cells = append(cells, []string{f.Signature, fmt.Sprint(f.Count), fmt.Sprint(f.Sessions), f.Repos, f.FirstSeen, f.LastSeen, f.Example})
	}
	return cells
}

var cellEscapes = strings.NewReplacer("|", `\|`, "\n", "⏎ ")

// Section writes a Markdown table under a "## heading" line, or _none_ when rows is empty. Cells
// have their pipes escaped and newlines shown as ⏎.
func Section(b *strings.Builder, heading string, cols []string, rows [][]string) {
	fmt.Fprintf(b, "## %s\n\n", heading)
	if len(rows) == 0 {
		b.WriteString("_none_\n\n")
		return
	}
	b.WriteString("| " + strings.Join(cols, " | ") + " |\n|" + strings.Repeat("---|", len(cols)) + "\n")
	for _, row := range rows {
		for i := range row {
			row[i] = cellEscapes.Replace(row[i])
		}
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
	b.WriteString("\n")
}
