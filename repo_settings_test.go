package dispatch_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

func runRepoSettings(t *testing.T, dir, configPath string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	var out bytes.Buffer
	err := dispatch.Run(context.Background(), []string{"repo-settings"},
		dispatch.Options{ConfigPath: configPath, Stdout: &out})
	return out.String(), err
}

func TestRepoSettingsPrintsTheTableOfTheRepoAtTheWorkingDirectory(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[repos.dispatch]\npath = \""+clone+"\"\n")

	out, err := runRepoSettings(t, clone, config)

	require.NoError(t, err)
	assert.Equal(t, "[repos.dispatch]\n"+
		"path = \""+clone+"\"\n"+
		"open_pr_as = \"open\"\n"+
		"ignore_comment_authors = []\n"+
		"ignore_optional_checks = false\n"+
		"auto_merge = false\n", out)
}

func TestRepoSettingsPrintsEverySettingAtItsConfiguredValue(t *testing.T) {
	clone := newHintRepo(t)
	other := newHintRepo(t)
	config := writeHintConfig(t, "[repos.dispatch]\npath = \""+other+"\"\n\n"+
		"[repos.work-api]\npath = \""+clone+"\"\nopen_pr_as = \"draft\"\n"+
		"ignore_comment_authors = [\"github-actions[bot]\", \"codeowners-bot\"]\n"+
		"ignore_optional_checks = true\nauto_merge = true\n")

	out, err := runRepoSettings(t, clone, config)

	require.NoError(t, err)
	assert.Equal(t, "[repos.work-api]\n"+
		"path = \""+clone+"\"\n"+
		"open_pr_as = \"draft\"\n"+
		"ignore_comment_authors = [\"github-actions[bot]\", \"codeowners-bot\"]\n"+
		"ignore_optional_checks = true\n"+
		"auto_merge = true\n", out)
}

func TestRepoSettingsWorksInAWorktreeOfTheRepo(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[repos.dispatch]\npath = \""+clone+"\"\nauto_merge = true\n")
	tree := filepath.Join(t.TempDir(), "tree")
	hintGit(t, clone, "worktree", "add", "-b", "tree", tree)

	out, err := runRepoSettings(t, tree, config)

	require.NoError(t, err)
	assert.Contains(t, out, "[repos.dispatch]\n")
	assert.Contains(t, out, "auto_merge = true\n")
}

func TestRepoSettingsWorksInASubdirectoryOfTheRepo(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[repos.dispatch]\npath = \""+clone+"\"\n")
	sub := filepath.Join(clone, "pkg", "deep")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	out, err := runRepoSettings(t, sub, config)

	require.NoError(t, err)
	assert.Contains(t, out, "[repos.dispatch]\n")
}

func TestRepoSettingsOutputLoadsBackThroughTheConfig(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[repos.work-api]\npath = \""+clone+"\"\nopen_pr_as = \"draft\"\n"+
		"ignore_comment_authors = [\"a\", \"b\"]\nignore_optional_checks = true\nauto_merge = true\n")
	out, err := runRepoSettings(t, clone, config)
	require.NoError(t, err)

	loaded, err := loadExample(t, out)
	original, origErr := loadExample(t, mustRead(t, config))

	require.NoError(t, err)
	require.NoError(t, origErr)
	assert.Equal(t, original.Repos, loaded.Repos)
}

func TestRepoSettingsQuotesALabelThatIsNotABareKey(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[repos.\"my.repo\"]\npath = \""+clone+"\"\n")

	out, err := runRepoSettings(t, clone, config)
	require.NoError(t, err)
	loaded, err := loadExample(t, out)

	require.NoError(t, err)
	assert.Contains(t, loaded.Repos, "my.repo")
}

func TestRepoSettingsFailsOutsideAConfiguredRepo(t *testing.T) {
	managed := newHintRepo(t)
	elsewhere := newHintRepo(t)
	config := writeHintConfig(t, "[repos.dispatch]\npath = \""+managed+"\"\n")

	out, err := runRepoSettings(t, elsewhere, config)

	require.ErrorContains(t, err, elsewhere+" is not inside a repo under [repos] in "+config)
	assert.Empty(t, out)

	_, err = runRepoSettings(t, t.TempDir(), config)
	require.ErrorContains(t, err, "is not inside a repo under [repos]")
}

func TestRepoSettingsFailsOnAConfigThatDoesNotLoad(t *testing.T) {
	clone := newHintRepo(t)

	for _, test := range []struct {
		name   string
		config string
	}{
		{"FlatEntry", "[repos]\ndispatch = \"" + clone + "\"\n"},
		{"UnknownSetting", "bogus = 1\n[repos.dispatch]\npath = \"" + clone + "\"\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := runRepoSettings(t, clone, writeHintConfig(t, test.config))

			require.Error(t, err)
			assert.Empty(t, out)
		})
	}

	_, err := runRepoSettings(t, clone, filepath.Join(t.TempDir(), "missing.toml"))
	require.Error(t, err)
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(body)
}

func TestRepoSettingsFailsWhenTwoReposResolveToTheCloneAtTheWorkingDirectory(t *testing.T) {
	clone := newHintRepo(t)
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(clone, link))
	config := writeHintConfig(t, "[repos.alpha]\npath = \""+link+"\"\nauto_merge = true\n[repos.zeta]\npath = \""+clone+"\"\n")

	out, err := runRepoSettings(t, clone, config)

	require.ErrorContains(t, err, "repos.alpha and repos.zeta both resolve to "+clone+": give each repo its own clone")
	assert.Empty(t, out)
}

func TestRepoSettingsPrintsTheApprovalChecksWhenListed(t *testing.T) {
	clone := newHintRepo(t)
	config := writeHintConfig(t, "[repos.dispatch]\npath = \""+clone+"\"\napproval_checks = [\"Mergify Merge Protections\"]\n")

	out, err := runRepoSettings(t, clone, config)

	require.NoError(t, err)
	assert.Contains(t, out, "approval_checks = [\"Mergify Merge Protections\"]\n")
}
