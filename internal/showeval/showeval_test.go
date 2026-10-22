package showeval_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/showeval"
)

const skill = "## Principles\n\n4. Four.\n\n5. **Show it.** New wording.\n\n6. Six.\n"

func TestRunPrintsCountsPerArm(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "arms"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "context.md"), []byte("CONTEXT"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte("PROMPT"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "arms", "current.md"), []byte("5. **Old.** Old wording.\n"), 0o644))
	skillPath := filepath.Join(dir, "SKILL.md")
	require.NoError(t, os.WriteFile(skillPath, []byte(skill), 0o644))

	var stdout, stderr bytes.Buffer
	code := showeval.Run(context.Background(), []string{"-dir", dir, "-skill", skillPath, "-n", "2"}, showeval.Options{
		Stdout: &stdout,
		Stderr: &stderr,
		Ask: func(_ context.Context, _, systemFile, prompt string) (string, error) {
			system, err := os.ReadFile(systemFile)
			require.NoError(t, err)
			assert.Equal(t, "PROMPT", prompt)
			assert.Contains(t, string(system), "CONTEXT")
			if strings.Contains(string(system), "Old wording") {
				return "one two three\n", nil
			}
			return "one two\n```go\nx := 1\n```\nthree four\n", nil
		},
	})

	require.Equal(t, 0, code, stderr.String())
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"current", "0", "of", "2", "3"}, strings.Fields(lines[1]))
	assert.Equal(t, []string{"skill", "2", "of", "2", "4"}, strings.Fields(lines[2]))
}

func TestRunFailsWhenSkillHasNoRuleFive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "arms"), 0o755))
	for _, name := range []string{"context.md", "prompt.txt", "arms/current.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}
	skillPath := filepath.Join(dir, "SKILL.md")
	require.NoError(t, os.WriteFile(skillPath, []byte("no rules\n"), 0o644))

	var stderr bytes.Buffer
	code := showeval.Run(context.Background(), []string{"-dir", dir, "-skill", skillPath}, showeval.Options{Stdout: &bytes.Buffer{}, Stderr: &stderr})

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "no line starting with")
}

func TestCountSkipsFencedCode(t *testing.T) {
	blocks, prose := showeval.Count("a b\n```\nx y z\n```\nc\n```go\nq\n```\n")
	assert.Equal(t, 2, blocks)
	assert.Equal(t, 3, prose)
}
