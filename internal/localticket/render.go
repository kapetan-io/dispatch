package localticket

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/kapetan-io/dispatch/internal/ticket"
)

// commentTime is how show prints a comment's time, in the machine's time zone.
const commentTime = "2006-01-02 15:04"

// oneLine is s with every control character, a line break included, turned into a space. A ticket's
// text can come from a stranger through a rule, and must not print an escape sequence or a line
// that looks like another field.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// block is s with every control character but the line break removed.
func block(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return oneLine(s)
}

func clean(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = oneLine(s)
	}
	return out
}

// Lines is what `dispatch ticket show` prints. The description is indented under the fields, so
// no line of it reads as a comment.
func (d Detail) Lines() []string {
	field := func(label, value string) string { return fmt.Sprintf("%-12s%s", label, value) }
	var attrs []string
	for _, k := range slices.Sorted(maps.Keys(d.Attributes)) {
		attrs = append(attrs, oneLine(k)+"="+oneLine(d.Attributes[k]))
	}
	startPR := ""
	if d.StartPR > 0 {
		startPR = strconv.Itoa(d.StartPR)
	}
	lines := []string{
		oneLine(d.ID + " " + d.Title),
		field("status", orDash(d.Status)),
		field("repo", orDash(d.Repo)),
		field("branch", orDash(d.Branch)),
		field("filed by", orDash(d.FiledBy)),
		field("labels", orDash(strings.Join(clean(d.Labels), ", "))),
		field("attributes", orDash(strings.Join(attrs, ", "))),
		field("parent", orDash(d.Parent)),
		field("blocked by", orDash(strings.Join(clean(d.BlockedBy), ", "))),
		field("start PR", orDash(startPR)),
	}
	if d.Description != "" {
		lines = append(lines, "")
		for _, l := range strings.Split(block(d.Description), "\n") {
			lines = append(lines, "  "+l)
		}
		lines = append(lines, "")
	}
	for _, c := range d.Comments {
		prefix := fmt.Sprintf("%s  %-8s  ", c.At.Local().Format(commentTime), oneLine(c.Author))
		body := strings.Split(block(c.Body), "\n")
		lines = append(lines, prefix+body[0])
		for _, l := range body[1:] {
			lines = append(lines, strings.Repeat(" ", len(prefix))+l)
		}
	}
	return lines
}

// ListLines is what `dispatch ticket list` prints: one line per ticket, each column padded to its widest value plus two spaces.
func ListLines(found []Summary) []string {
	var wid, wst, wrp int
	for _, s := range found {
		wid, wst, wrp = max(wid, len(s.ID)), max(wst, len(oneLine(s.Status))), max(wrp, len(oneLine(s.Repo)))
	}
	out := make([]string, len(found))
	for i, s := range found {
		out[i] = fmt.Sprintf("%-*s%-*s%-*s%s", wid+2, s.ID, wst+2, oneLine(s.Status), wrp+2, oneLine(s.Repo), oneLine(s.Title))
	}
	return out
}

// StatusLines is what `dispatch ticket statuses` prints: each name padded to the longest plus two spaces, then its jobs.
func StatusLines(statuses ticket.Statuses) []string {
	width := 0
	for _, st := range statuses {
		width = max(width, len(st.Name))
	}
	out := make([]string, len(statuses))
	for i, st := range statuses {
		jobs := make([]string, len(st.Jobs))
		for j, job := range st.Jobs {
			jobs[j] = string(job)
		}
		out[i] = strings.TrimRight(fmt.Sprintf("%-*s%s", width+2, st.Name, strings.Join(jobs, ", ")), " ")
	}
	return out
}
