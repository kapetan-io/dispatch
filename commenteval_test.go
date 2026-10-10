package dispatch_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kapetan-io/dispatch/internal/commenteval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const good = "## In plain English\n\nI hit a wall.\n\n```\nexample\n```\n\n## Details\n\nstate\n"

func TestCommentEvalRunAndReport(t *testing.T) {
	dir := t.TempDir()
	arm := filepath.Join(dir, "arm.md")
	require.NoError(t, os.WriteFile(arm, []byte("skill text"), 0o644))
	scenarios := filepath.Join(dir, "scenarios")
	require.NoError(t, os.MkdirAll(scenarios, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(scenarios, "alpha.md"), []byte("You are stuck."), 0o644))
	out := filepath.Join(dir, "out")

	var prompts []string
	opts := commenteval.Options{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
		Ask: func(_ context.Context, _, prompt string) (string, error) {
			prompts = append(prompts, prompt)
			return good, nil
		}}
	code := commenteval.Run(t.Context(), []string{"run", "-arm", arm, "-scenarios", scenarios, "-out", out, "-n", "2", "-parallel", "1"}, opts)
	require.Equal(t, 0, code)
	require.Len(t, prompts, 2)
	assert.Contains(t, prompts[0], "skill text")
	assert.Contains(t, prompts[0], "You are stuck.")

	var stdout bytes.Buffer
	code = commenteval.Run(t.Context(), []string{"report", out}, commenteval.Options{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	require.Equal(t, 0, code)
	assert.Regexp(t, `alpha\s+2\s+2\s+2\s+2`, stdout.String())
}

func TestCommentEvalScore(t *testing.T) {
	for _, test := range []struct {
		name                    string
		comment                 string
		opens, example, details bool
	}{
		{"complete", good, true, true, true},
		{"statusLineFirst", "Moving to Needs Human.\n\n" + good, false, false, false},
		{"noExample", "## In plain English\n\nWords only.\n\n## Details\n\nx", true, false, true},
		{"exampleAfterDetails", "## In plain English\n\nWords.\n\n## Details\n\n```\nx\n```", true, false, true},
		{"noDetails", "## In plain English\n\nWords.\n\n```\nx\n```", true, true, false},
		{"otherHeading", "## Summary\n\n```\nx\n```\n\n## Details", false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "case-1.md"), []byte(test.comment), 0o644))
			var stdout bytes.Buffer
			require.Equal(t, 0, commenteval.Run(t.Context(), []string{"report", dir}, commenteval.Options{Stdout: &stdout, Stderr: &bytes.Buffer{}}))
			want := func(b bool) string {
				if b {
					return "1"
				}
				return "0"
			}
			assert.Regexp(t, `case\s+1\s+`+want(test.opens)+`\s+`+want(test.opens && test.example)+`\s+`+want(test.opens && test.details), stdout.String())
		})
	}
}

func TestCommentEvalUsage(t *testing.T) {
	var stderr bytes.Buffer
	assert.Equal(t, 2, commenteval.Run(t.Context(), nil, commenteval.Options{Stdout: &bytes.Buffer{}, Stderr: &stderr}))
	assert.Equal(t, 1, commenteval.Run(t.Context(), []string{"bogus"}, commenteval.Options{Stdout: &bytes.Buffer{}, Stderr: &stderr}))
	assert.Contains(t, stderr.String(), "unknown command")
}
