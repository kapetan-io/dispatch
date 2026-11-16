package dispatch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An instruction is a verb in its plain form that opens a sentence or a clause, such as "move the
// ticket to Needs Human" or "and move it back to In Progress". A mention, such as "dispatch moves a
// ticket to Needs Human" or "a ticket dispatch moved to Needs Human", names someone else doing it.
const clauseStart = `(?i)(?:^|[,:;]\s*|\b(?:and|then|or)\s+)`

var (
	toNeedsHuman = regexp.MustCompile(clauseStart + `(?:(?:move|send|put|set|change|escalate)\b[^.(]{0,80}?\b(?:in)?to needs human|mark (?:the|this|its) ticket (?:as )?needs human)|\bticket move \S+ "needs human"`)
	backToWork   = regexp.MustCompile(clauseStart + `(?:move|return)\s+(?:the ticket|this ticket|it)\s+(?:back to in (?:progress|review)|to in progress again)`)
	listNumber   = regexp.MustCompile(`^\d+\.\s+`)
)

// managerSkills run in the sessions that follow workers, not in a worker, and move a ticket
// dispatch escalated back to In Progress.
var managerSkills = map[string]bool{"manage": true, "autobots": true, "dispatch": true}

// TestNoShippedSkillOrRuleTellsAWorkerToMoveItsTicketToNeedsHuman fails when any shipped skill,
// skill reference or rule tells its reader to move a ticket to Needs Human, or a worker's skill tells
// it to move the ticket back after the answer. A worker that needs the user runs `dispatch ask`. The
// failed-restack steps in rules/stacking.ts (RESTACK) are the one exception.
func TestNoShippedSkillOrRuleTellsAWorkerToMoveItsTicketToNeedsHuman(t *testing.T) {
	skills, err := filepath.Glob("skills/*/SKILL.md")
	require.NoError(t, err)
	require.NotEmpty(t, skills)
	references, err := filepath.Glob("skills/*/references/*.md")
	require.NoError(t, err)
	for _, path := range append(skills, references...) {
		t.Run(path, func(t *testing.T) {
			text := readShipped(t, path)
			assert.Empty(t, instructions(toNeedsHuman, text))
			if !managerSkills[strings.Split(path, "/")[1]] {
				assert.Empty(t, instructions(backToWork, text))
			}
		})
	}

	rules, err := filepath.Glob("rules/*.ts")
	require.NoError(t, err)
	require.NotEmpty(t, rules)
	for _, path := range rules {
		if strings.HasSuffix(path, ".test.ts") {
			continue
		}
		t.Run(path, func(t *testing.T) {
			text := readShipped(t, path)
			if path == "rules/stacking.ts" {
				text = withoutRestack(t, text)
			}
			assert.Empty(t, instructions(toNeedsHuman, text))
		})
	}
}

// TestManageAndAutobotsMoveAnEscalatedTicketBackToWork fails when the skills that follow workers
// lose the instruction to move a ticket dispatch escalated to Needs Human back to work, since no
// worker moves it back any more.
func TestManageAndAutobotsMoveAnEscalatedTicketBackToWork(t *testing.T) {
	moveBack := regexp.MustCompile(clauseStart + `move\s+(?:the ticket|the child|it)\s+back to in (?:progress|review)`)
	for _, path := range []string{"skills/manage/SKILL.md", "skills/autobots/SKILL.md"} {
		t.Run(path, func(t *testing.T) {
			assert.NotEmpty(t, instructions(moveBack, readShipped(t, path)))
		})
	}
}

// TestTheNeedsHumanCheckTellsAnInstructionFromAMention pins what the check above counts, so a
// change to its patterns cannot quietly pass every file.
func TestTheNeedsHumanCheckTellsAnInstructionFromAMention(t *testing.T) {
	for _, test := range []struct {
		text        string
		instruction bool
	}{
		{text: "Ask in chat and move the ticket to Needs Human, so dispatch notifies the sessions following the ticket.", instruction: true},
		{text: "If it doesn't, quote the deliverable next to the intent in chat and move the ticket to Needs Human.", instruction: true},
		{text: "Post what you found in chat, with the evidence, and move it to Needs Human.", instruction: true},
		{text: "Escalate to Needs Human only when no code change satisfies both the test and the ticket.", instruction: true},
		{text: "1. If it did not, move this ticket to Needs Human with a comment saying so, and stop.", instruction: true},
		{text: "Write the user's answer on the ticket, move the ticket back to In Progress, then resume the steps.", instruction: true},
		{text: "Needs Human remains the status dispatch moves a ticket to when it escalates by itself.", instruction: false},
		{text: "A ticket dispatch moved to Needs Human, with the reason.", instruction: false},
		{text: "After nudge_limit nudges with no push, moves the ticket to Needs Human.", instruction: false},
		{text: "Check the status once, about 30 seconds later, and move it back if Linear reverted it.", instruction: false},
		{text: "A worker that moved its ticket to Needs Human moves it back itself.", instruction: false},
		{text: "When unclear, move the ticket into `Needs Human`.", instruction: true},
		{text: "Then mark the ticket **Needs Human** and stop.", instruction: true},
		{text: `Run "${DISPATCH_BIN:-dispatch}" ticket move LOC-<n> "Needs Human".`, instruction: true},
		{text: "Once the user answers, move the ticket back to In Review.", instruction: true},
		{text: "Then move the ticket to In Review.", instruction: false},
		{text: "Drop or re-explain the finding, and move the ticket back if it is in Needs Human.", instruction: false},
		{text: "Until then, don't change the PR body, or move the ticket because of it (an escalation to Needs Human below is not that).", instruction: false},
	} {
		t.Run(test.text, func(t *testing.T) {
			found := len(instructions(toNeedsHuman, test.text)) + len(instructions(backToWork, test.text))
			assert.Equal(t, test.instruction, found > 0)
		})
	}
}

func readShipped(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// instructions returns each sentence of text that pattern matches. A sentence ends at a full stop
// followed by a space or at a line end, so a list item or a template line is a sentence of its own.
func instructions(pattern *regexp.Regexp, text string) []string {
	var found []string
	for _, line := range strings.Split(text, "\n") {
		// Markdown list and bold markers would hide the start of a sentence.
		line = strings.TrimLeft(line, " -*>`\t")
		line = listNumber.ReplaceAllString(line, "")
		for _, sentence := range strings.Split(line, ". ") {
			// Statuses are often written in backticks or bold, which would split "to Needs Human".
			if pattern.MatchString(strings.NewReplacer("`", "", "**", "").Replace(sentence)) {
				found = append(found, sentence)
			}
		}
	}
	return found
}

// withoutRestack drops the failed-restack instructions, the RESTACK text in rules/stacking.ts, so
// any step in them may send the ticket to Needs Human.
func withoutRestack(t *testing.T, text string) string {
	t.Helper()
	start := strings.Index(text, "const RESTACK = ")
	require.GreaterOrEqual(t, start, 0)
	end := strings.Index(text[start:], "\n};\n")
	require.GreaterOrEqual(t, end, 0)
	return text[:start] + text[start+end:]
}
