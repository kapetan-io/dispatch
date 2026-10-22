package dispatch_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kapetan-io/dispatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ticketHint = "load the dispatch:ticket skill"

func TestTicketHintNamesTheSkillInAManagedRepo(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[repos.dispatch]\npath = \""+clone+"\"\n")

	assert.Contains(t, runTicketHint(t, clone, config), ticketHint)
}

func TestTicketHintNamesTheSkillInAWorktreeOfAManagedRepo(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[repos.dispatch]\npath = \""+clone+"\"\n")
	tree := filepath.Join(t.TempDir(), "tree")
	hintGit(t, clone, "worktree", "add", "-b", "tree", tree)

	assert.Contains(t, runTicketHint(t, tree, config), ticketHint)
}

func TestTicketHintExpandsTildeInRepoPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clone := filepath.Join(home, "src", "repo")
	require.NoError(t, os.MkdirAll(clone, 0o755))
	hintGit(t, clone, "init", "-q")
	config := writeHintConfig(t, "[repos.repo]\npath = \"~/src/repo\"\n")

	assert.Contains(t, runTicketHint(t, clone, config), ticketHint)
}

func TestTicketHintStaysSilentOutsideManagedRepos(t *testing.T) {
	managed := newHintRepo(t)
	other := newHintRepo(t)
	config := writeHintConfig(t, "[plugins.linear]\nteams = [\"ENG\"]\n\n[repos.dispatch]\npath = \""+managed+"\"\n")

	assert.Empty(t, runTicketHint(t, other, config))
	assert.Empty(t, runTicketHint(t, t.TempDir(), config))
}

func TestTicketHintIgnoresPathsListedOutsideTheReposTable(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[plugins.herdr]\nplugin_root = \""+clone+"\"\n\n[plugins.linear]\nother = \""+clone+"\"\n")

	assert.Empty(t, runTicketHint(t, clone, config))
}

func TestTicketHintReadsTheTomlFormsTheDispatcherAccepts(t *testing.T) {
	clone := newHintRepo(t)

	for _, test := range []struct {
		name   string
		config string
	}{
		{"SingleQuotes", "[repos.dispatch]\npath = '" + clone + "'\n"},
		{"CRLF", "[repos.dispatch]\r\npath = \"" + clone + "\"\r\n"},
		{"HeaderComment", "[repos.dispatch] # clones\npath = \"" + clone + "\"\n"},
		{"IndentedHeader", "  [repos.dispatch]\n  path = \"" + clone + "\" # mine\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Contains(t, runTicketHint(t, clone, writeHintConfig(t, test.config)), ticketHint)
		})
	}
}

func TestTicketHintReadsEscapesInQuotedPaths(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	clone := filepath.Join(base, `say "hi"`)
	require.NoError(t, os.MkdirAll(clone, 0o755))
	hintGit(t, clone, "init", "-q")
	escaped := strings.ReplaceAll(clone, `"`, `\"`)
	config := writeHintConfig(t, "[repos.dispatch]\npath = \""+escaped+"\"\n")

	assert.Contains(t, runTicketHint(t, clone, config), ticketHint)
}

func TestTicketHintIgnoresACommentedOutRepo(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[repos.dispatch]\n# path = \""+clone+"\"\n")

	assert.Empty(t, runTicketHint(t, clone, config))
}

func TestTicketHintStaysSilentWithoutAConfig(t *testing.T) {
	clone := newHintRepo(t)

	assert.Empty(t, runTicketHint(t, clone, filepath.Join(t.TempDir(), "missing.toml")))
}

func TestTicketHintStaysSilentWithABrokenConfig(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[repos]\ndispatch = \"~/x\"\n")

	assert.Empty(t, runTicketHint(t, clone, config))
}

func TestTicketHintGivesUpWhenGitHangsPastTheContext(t *testing.T) {
	clone := newHintRepo(t)
	configPath := writeHintConfig(t, "[repos.dispatch]\npath = \""+clone+"\"\n")
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(clone)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := dispatch.Run(ctx, []string{"ticket-hint"}, dispatch.Options{ConfigPath: configPath, Stdout: io.Discard})

	require.NoError(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func runTicketHint(t *testing.T, dir, configPath string) string {
	t.Helper()
	t.Chdir(dir)
	var out bytes.Buffer
	require.NoError(t, dispatch.Run(context.Background(), []string{"ticket-hint"},
		dispatch.Options{ConfigPath: configPath, Stdout: &out}))
	return out.String()
}
