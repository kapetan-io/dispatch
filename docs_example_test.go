package dispatch_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The long-loop rule in docs/rules.md is what readers copy, so it must not hold for tickets it has no opinion on.
func TestTheTurnsExampleInTheRulesGuideIsNotHeldForOtherTickets(t *testing.T) {
	guide, err := os.ReadFile("docs/rules.md")
	require.NoError(t, err)
	example := regexp.MustCompile("(?s)```ts\nwhenever\\(\\{\n  name: \"long-loop-in-review\".*?\n\\}\\);\n```").Find(guide)
	require.NotEmpty(t, example)
	h := newHarness(t)
	dir := oneRule(t, string(example[len("```ts\n"):len(example)-len("\n```")]))
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.NotRegexp(t, `held`, out)
	assert.Regexp(t, `long-loop-in-review +whenever +linear\.ticket +wants nothing now`, out)
}
