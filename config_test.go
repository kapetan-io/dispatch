package dispatch_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/linear"
)

func TestUnknownConfigKeyFailsEveryCommand(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		config string
		args   []string
		want   string
	}{
		{"TickTopLevel", "max_in_progres = 9\n", []string{"tick"}, `unknown setting max_in_progres`},
		{"Plan", "max_in_progres = 9\n", []string{"plan"}, `unknown setting max_in_progres`},
		{"Run", "max_in_progres = 9\n", []string{"run"}, `unknown setting max_in_progres`},
		{"Tell", "max_in_progres = 9\n", []string{"tell", "ENG-1", "hello"}, `unknown setting max_in_progres`},
		{"BlueprintNeedsHumanTable", "[plugins.linear.needs_human]\nstatus = \"Needs Human\"\n", []string{"tick"}, "unknown settings:\n  plugins.linear.needs_human\n  plugins.linear.needs_human.status"},
		{"StatusesTodoTypo", "[plugins.linear.statuses]\ntodoo = \"Todo\"\n", []string{"tick"}, `unknown setting plugins.linear.statuses.todoo`},
		{"TwoUnknownKeys", "nudge_limt = 1\nworker_modle = \"opus\"\n", []string{"tick"}, "unknown settings:\n  nudge_limt\n  worker_modle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: test.config})

			res := d.Run(test.args...)

			require.ErrorContains(t, res.Err, test.want)
			require.ErrorContains(t, res.Err, d.ConfigPath())
		})
	}
}

func TestUnknownConfigKeyTakesNoAction(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "max_in_progres = 9\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("tick")

	require.Error(t, res.Err)
	assert.Empty(t, d.Workers.Launched())
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestKnownConfigKeysBehaveAsBefore(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n[plugins.linear.statuses]\nin_progress = \"In Progress\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
}

func TestConfigRejectsABadRepoTable(t *testing.T) {
	t.Parallel()
	const path = "~/src/api"
	for _, test := range []struct {
		name    string
		config  string
		wantErr string
	}{
		{"PlainEntry", "[repos]\ndispatch = \"~/x\"\n", `[repos] entry "dispatch" must be a table: write [repos.dispatch] with path = "~/x"`},
		{"TopLevelAutoMerge", "auto_merge = true\n[repos.api]\npath = \"~/src/api\"\n", `auto_merge moved into each repo's table: set auto_merge = true under [repos.<label>]`},
		{"OldAutoMergeList", "auto_merge = [\"kapetan-io/dispatch\"]\n[repos.api]\npath = \"~/src/api\"\n", `auto_merge moved into each repo's table`},
		{"NoPath", "[repos.work-api]\nauto_merge = true\n", `repos.work-api has no path`},
		{"EmptyPath", "[repos.work-api]\npath = \"\"\n", `repos.work-api has no path`},
		{"SamePath", "[repos.a]\npath = \"" + path + "\"\n[repos.b]\npath = \"" + path + "/\"\n", `repos.a and repos.b have the same path`},
		{"BadOpenPRAs", "[repos.work-api]\npath = \"~/x\"\nopen_pr_as = \"ready\"\n", `repos.work-api.open_pr_as must be "open" or "draft", not "ready"`},
		{"UnknownKey", "[repos.work-api]\npath = \"~/x\"\nbogus = 1\n", `unknown setting repos.work-api.bogus`},
		{"WrongType", "[repos.work-api]\npath = \"~/x\"\nauto_merge = \"yes\"\n", `repos.work-api`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: test.config})

			res := d.Run("plan")

			require.ErrorContains(t, res.Err, "while reading config "+d.ConfigPath()+": ")
			require.ErrorContains(t, res.Err, test.wantErr)
			assert.NotContains(t, res.Err.Error(), "toml: line")
		})
	}
}

func TestConfigSamePathIsComparedAfterHomeExpansion(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	d := newDispatch(t, dispatchOptions{
		OwnRepos: true,
		Env:      map[string]string{"HOME": home},
		Config:   "[repos.a]\npath = \"~/src/api\"\n[repos.b]\npath = \"" + home + "/src/api\"\n",
	})

	res := d.Run("plan")

	require.ErrorContains(t, res.Err, "repos.a and repos.b have the same path "+home+"/src/api")
}

func TestConfigLoadsTwoReposWhosePathsAreASymlinkAndItsTarget(t *testing.T) {
	t.Parallel()
	clone := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(clone, link))
	d := newDispatch(t, dispatchOptions{
		OwnRepos: true,
		Config:   "[repos.alpha]\npath = \"" + link + "\"\n[repos.zeta]\npath = \"" + clone + "\"\n",
	})

	res := d.Run("tick")

	require.NoError(t, res.Err)
}

func TestConfigNamesTheKeyOfAWrongTypedSetting(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.api]\npath = \"~/x\"\nauto_merge = 1\n"})

	res := d.Run("plan")

	require.ErrorContains(t, res.Err, "repos.api.auto_merge: incompatible types")
}

func TestOldConfigLayoutFailsTheStartNamingItsFirstKey(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		config string
		want   string
	}{
		{"IntervalMinutes", "interval_minutes = 5\nmax_in_progress = 4\n", "unknown settings:\n  interval_minutes\n  max_in_progress"},
		{"LinearTable", "[linear]\nteams = [\"ENG\"]\n", "unknown settings:\n  linear\n  linear.teams"},
		{"ModelsMoved", "worker_model = \"opus\"\n", `unknown setting worker_model`},
		{"DoneRenamed", "[plugins.linear.statuses]\ndone = [\"Done\"]\n", `unknown setting plugins.linear.statuses.done`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: test.config})

			res := d.Run("tick")

			require.ErrorContains(t, res.Err, test.want)
		})
	}
}

func TestRulesTableTakesAnyKey(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nbug_model = \"opus\"\nreviewers = [\"a\", \"b\"]\n[rules.slack]\nchannel = \"#eng\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
}

func TestConfigRejectsValuesThatDecodeButMeanNothing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		config string
		want   string
	}{
		{"BadDuration", "[dispatch]\nresync = \"soon\"\n", `"soon" is not a duration`},
		{"NoRotation", "[history]\nrotate = \"0s\"\n", `history.rotate and history.keep must be above zero`},
		{"HTTPWithoutBase", "[http.sentry]\ntoken_command = \"op read x\"\n", `http.sentry has no base_url`},
		{"ModelNotAString", "[rules]\nworker_model = 3\n", `rules.worker_model must be a string`},
		{"LimitNotANumber", "[rules]\nnudge_limit = \"two\"\n", `rules.nudge_limit must be a whole number`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: test.config})

			res := d.Run("tick")

			require.ErrorContains(t, res.Err, test.want)
		})
	}
}

func TestConfigDaysAreAccepted(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[history]\nkeep = \"7d\"\nrotate = \"1d12h\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
}

func TestRepoSettingsPrintsTheTableOfTheRepoAtTheWorkingDirectory(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.dispatch]\npath = \"" + clone + "\"\n"})

	res := d.RunIn(clone, "repo-settings")

	require.NoError(t, res.Err)
	assert.Equal(t, "[repos.dispatch]\n"+
		"path = \""+clone+"\"\n"+
		"open_pr_as = \"open\"\n"+
		"ignore_comment_authors = []\n"+
		"ignore_optional_checks = false\n"+
		"approval_checks = []\n"+
		"auto_merge = false\n", res.Stdout)
}

func TestRepoSettingsPrintsEverySettingAtItsConfiguredValue(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	other := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.dispatch]\npath = \"" + other + "\"\n\n" +
		"[repos.work-api]\npath = \"" + clone + "\"\nopen_pr_as = \"draft\"\n" +
		"ignore_comment_authors = [\"github-actions[bot]\", \"codeowners-bot\"]\n" +
		"ignore_optional_checks = true\nauto_merge = true\n"})

	res := d.RunIn(clone, "repo-settings")

	require.NoError(t, res.Err)
	assert.Equal(t, "[repos.work-api]\n"+
		"path = \""+clone+"\"\n"+
		"open_pr_as = \"draft\"\n"+
		"ignore_comment_authors = [\"github-actions[bot]\", \"codeowners-bot\"]\n"+
		"ignore_optional_checks = true\n"+
		"approval_checks = []\n"+
		"auto_merge = true\n", res.Stdout)
}

func TestRepoSettingsWorksInAWorktreeOfTheRepo(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.dispatch]\npath = \"" + clone + "\"\nauto_merge = true\n"})
	tree := filepath.Join(t.TempDir(), "tree")
	hintGit(t, clone, "worktree", "add", "-b", "tree", tree)

	res := d.RunIn(tree, "repo-settings")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "[repos.dispatch]\n")
	assert.Contains(t, res.Stdout, "auto_merge = true\n")
}

func TestRepoSettingsWorksInASubdirectoryOfTheRepo(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	sub := filepath.Join(clone, "pkg", "deep")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.work-api]\npath = \"" + clone + "\"\nauto_merge = true\n"})

	res := d.RunIn(sub, "repo-settings")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "[repos.work-api]\n")
	assert.Contains(t, res.Stdout, "auto_merge = true\n")
}

func TestRepoSettingsOutputLoadsBackThroughTheConfig(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	original := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.work-api]\npath = \"" + clone + "\"\nopen_pr_as = \"draft\"\n" +
		"ignore_comment_authors = [\"a\", \"b\"]\nignore_optional_checks = true\nauto_merge = true\n"})
	printed := original.RunIn(clone, "repo-settings")
	require.NoError(t, printed.Err)
	loaded := newDispatch(t, dispatchOptions{OwnRepos: true, Config: printed.Stdout})

	res := loaded.RunIn(clone, "repo-settings")

	require.NoError(t, res.Err)
	assert.Equal(t, printed.Stdout, res.Stdout)
}

func TestRepoSettingsQuotesALabelThatIsNotABareKey(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	original := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.\"my.repo\"]\npath = \"" + clone + "\"\n"})
	printed := original.RunIn(clone, "repo-settings")
	require.NoError(t, printed.Err)
	loaded := newDispatch(t, dispatchOptions{OwnRepos: true, Config: printed.Stdout})

	res := loaded.RunIn(clone, "repo-settings")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "[repos.\"my.repo\"]\n")
}

func TestRepoSettingsFailsOutsideAConfiguredRepo(t *testing.T) {
	t.Parallel()
	managed := newHintRepo(t)
	elsewhere := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.dispatch]\npath = \"" + managed + "\"\n"})

	res := d.RunIn(elsewhere, "repo-settings")

	require.ErrorContains(t, res.Err, elsewhere+" is not inside a repo under [repos] in "+d.ConfigPath())
	assert.Empty(t, res.Stdout)

	res = d.RunIn(t.TempDir(), "repo-settings")
	require.ErrorContains(t, res.Err, "is not inside a repo under [repos]")
}

func TestRepoSettingsFailsOnAConfigThatDoesNotLoad(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)

	for _, test := range []struct {
		name   string
		config string
	}{
		{"FlatEntry", "[repos]\ndispatch = \"" + clone + "\"\n"},
		{"UnknownSetting", "bogus = 1\n[repos.dispatch]\npath = \"" + clone + "\"\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: test.config})

			res := d.RunIn(clone, "repo-settings")

			require.Error(t, res.Err)
			assert.Empty(t, res.Stdout)
		})
	}

	d := newDispatch(t, dispatchOptions{})
	res := d.runInWithoutConfig(clone, "repo-settings")
	require.Error(t, res.Err)
}

func TestRepoSettingsFailsWhenTwoReposResolveToTheCloneAtTheWorkingDirectory(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(clone, link))
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.alpha]\npath = \"" + link + "\"\nauto_merge = true\n[repos.zeta]\npath = \"" + clone + "\"\n"})

	res := d.RunIn(clone, "repo-settings")

	require.ErrorContains(t, res.Err, "repos.alpha and repos.zeta both resolve to "+clone+": give each repo its own clone")
	assert.Empty(t, res.Stdout)
}

func TestRepoSettingsPrintsTheApprovalChecksWhenListed(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.dispatch]\npath = \"" + clone + "\"\napproval_checks = [\"Mergify Merge Protections\"]\n"})

	res := d.RunIn(clone, "repo-settings")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "approval_checks = [\"Mergify Merge Protections\"]\n")
}

const ticketHint = "load the dispatch:ticket skill"

func TestTicketHintNamesTheSkillInAManagedRepo(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.dispatch]\npath = \"" + clone + "\"\n"})

	assert.Contains(t, d.hintFrom(clone), ticketHint)
}

func TestTicketHintNamesTheSkillInAWorktreeOfAManagedRepo(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.dispatch]\npath = \"" + clone + "\"\n"})
	tree := filepath.Join(t.TempDir(), "tree")
	hintGit(t, clone, "worktree", "add", "-b", "tree", tree)

	assert.Contains(t, d.hintFrom(tree), ticketHint)
}

func TestTicketHintExpandsTildeInRepoPaths(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clone := filepath.Join(home, "src", "repo")
	require.NoError(t, os.MkdirAll(clone, 0o755))
	hintGit(t, clone, "init", "-q")
	d := newDispatch(t, dispatchOptions{
		OwnRepos: true,
		Env:      map[string]string{"HOME": home},
		Config:   "[repos.repo]\npath = \"~/src/repo\"\n",
	})

	assert.Contains(t, d.hintFrom(clone), ticketHint)
}

func TestTicketHintStaysSilentOutsideManagedRepos(t *testing.T) {
	t.Parallel()
	managed := newHintRepo(t)
	other := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[plugins.linear]\nteams = [\"ENG\"]\n\n[repos.dispatch]\npath = \"" + managed + "\"\n"})

	assert.Empty(t, d.hintFrom(other))
	assert.Empty(t, d.hintFrom(t.TempDir()))
}

func TestTicketHintIgnoresPathsListedOutsideTheReposTable(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[plugins.herdr]\nplugin_root = \"" + clone + "\"\n\n[plugins.linear]\nother = \"" + clone + "\"\n"})

	assert.Empty(t, d.hintFrom(clone))
}

func TestTicketHintReadsTheTomlFormsTheDispatcherAccepts(t *testing.T) {
	t.Parallel()
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
			d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: test.config})

			assert.Contains(t, d.hintFrom(clone), ticketHint)
		})
	}
}

func TestTicketHintReadsEscapesInQuotedPaths(t *testing.T) {
	t.Parallel()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	clone := filepath.Join(base, `say "hi"`)
	require.NoError(t, os.MkdirAll(clone, 0o755))
	hintGit(t, clone, "init", "-q")
	escaped := strings.ReplaceAll(clone, `"`, `\"`)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.dispatch]\npath = \"" + escaped + "\"\n"})

	assert.Contains(t, d.hintFrom(clone), ticketHint)
}

func TestTicketHintIgnoresACommentedOutRepo(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos.dispatch]\n# path = \"" + clone + "\"\n"})

	assert.Empty(t, d.hintFrom(clone))
}

func TestTicketHintStaysSilentWithoutAConfig(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{})

	res := d.runInWithoutConfig(clone, "ticket-hint")

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stdout)
}

func TestTicketHintStaysSilentWithABrokenConfig(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: "[repos]\ndispatch = \"~/x\"\n"})

	assert.Empty(t, d.hintFrom(clone))
}

func TestTicketHintGivesUpWhenGitHangsPastTheContext(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755))
	d := newDispatch(t, dispatchOptions{
		OwnRepos: true,
		Config:   "[repos.dispatch]\npath = \"" + clone + "\"\n",
		Env:      map[string]string{"PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH")},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	res := d.runIn(ctx, clone, dispatch.Options{}, "ticket-hint")

	require.NoError(t, res.Err)
	assert.Less(t, time.Since(start), 5*time.Second)
}

// hintFrom runs `dispatch ticket-hint` from dir and returns what it printed.
func (d *Dispatch) hintFrom(dir string) string {
	d.t.Helper()
	res := d.RunIn(dir, "ticket-hint")
	require.NoError(d.t, res.Err)
	return res.Stdout
}

// runInWithoutConfig is RunIn on a machine that has no config.toml.
func (d *Dispatch) runInWithoutConfig(dir string, args ...string) Result {
	d.t.Helper()
	opts := d.options(dispatch.Options{Dir: dir})
	opts.ConfigPath = filepath.Join(d.dir, "missing.toml")
	return d.run(opts, args...)
}

// keyScript writes an executable that appends a line to a counter file on each
// run and prints body, and returns the shell command that runs it.
func keyScript(t *testing.T, dir, body string) (command, counter string) {
	t.Helper()
	counter = filepath.Join(dir, "runs")
	script := filepath.Join(dir, "key.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho run >> "+counter+"\n"+body+"\n"), 0o755))
	return script, counter
}

func runCount(t *testing.T, counter string) int {
	t.Helper()
	raw, err := os.ReadFile(counter)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return len(raw) / len("run\n")
}

// Calls counts the requests Linear answered.
func (l *Linear) Calls() int { return l.d.linear.calls() }

// runOver runs a command over Options the harness doesn't set itself, under a context the test
// can cancel.
func (d *Dispatch) runOver(ctx context.Context, extra dispatch.Options, args ...string) Result {
	d.t.Helper()
	return d.result(d.runWith(ctx, extra, args...))
}

func TestApiKeyCommandSuppliesTheLinearKey(t *testing.T) {
	t.Parallel()
	command, _ := keyScript(t, t.TempDir(), "echo test-key")
	d := newDispatch(t, dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
}

func TestApiKeyCommandWinsOverTheKeychain(t *testing.T) {
	t.Parallel()
	command, counter := keyScript(t, t.TempDir(), "echo test-key")
	d := newDispatch(t, dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\napi_key_keychain = \"no-such-service\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Equal(t, 1, runCount(t, counter))
}

func TestApiKeyCommandYieldsToTheEnvironmentKey(t *testing.T) {
	t.Parallel()
	command, counter := keyScript(t, t.TempDir(), "echo other-key")
	d := newDispatch(t, dispatchOptions{
		Key:    configuredKey,
		Env:    map[string]string{"LINEAR_API_KEY": "test-key"},
		Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n",
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Equal(t, 0, runCount(t, counter))
}

func TestApiKeyCommandFailureNamesCommandAndStatus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"ExitStatus", "echo secret-value\nexit 3", "exit status 3"},
		{"PrintsNothing", "true", "printed nothing (exit status 0)"},
		{"PrintsBlank", "echo '   '", "printed nothing (exit status 0)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, _ := keyScript(t, t.TempDir(), test.body)
			d := newDispatch(t, dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n"})

			res := d.Run("tick")

			require.ErrorContains(t, res.Err, command)
			require.ErrorContains(t, res.Err, test.want)
			assert.NotContains(t, res.Err.Error(), "secret-value")
			assert.Empty(t, d.Linear.Moves())
			assert.Empty(t, d.Linear.Comments())
		})
	}
}

func TestApiKeyCommandRunsOncePerRunProcess(t *testing.T) {
	t.Parallel()
	command, counter := keyScript(t, t.TempDir(), "echo test-key")
	d := newDispatch(t, dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()
	callsAfter1 := d.Linear.Calls()
	d.Tick()
	d.Advance(5 * time.Minute)

	require.Greater(t, d.Linear.Calls(), callsAfter1)
	assert.Equal(t, 1, runCount(t, counter))
}

func TestLinearViewerPrintsTheKeyOwner(t *testing.T) {
	t.Parallel()
	command, _ := keyScript(t, t.TempDir(), "echo test-key")
	d := newDispatch(t, dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n"})

	res := d.Run("linear-viewer")

	require.NoError(t, res.Err)
	assert.JSONEq(t, `{"id":"viewer-1","name":"Ada Lovelace"}`, res.Stdout)
}

func TestLinearViewerFailsWithoutAKey(t *testing.T) {
	t.Parallel()
	command, _ := keyScript(t, t.TempDir(), "exit 1")
	d := newDispatch(t, dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n"})

	res := d.Run("linear-viewer")

	require.ErrorContains(t, res.Err, "api_key_command")
	assert.Empty(t, res.Stdout)
}

func TestLinearViewerOffMacOSNamesTheKeySourcesWithoutAKeychain(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "darwin" {
		t.Skip("macOS looks the key up in the keychain")
	}
	d := newDispatch(t, dispatchOptions{Key: configuredKey})

	res := d.Run("linear-viewer")

	require.ErrorContains(t, res.Err, "LINEAR_API_KEY")
	require.ErrorContains(t, res.Err, "api_key_command")
	assert.NotContains(t, res.Err.Error(), "keychain has no")
}

func TestApiKeyCommandRunsAgainOnTheNextStartAfterLinearRejectsTheKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	command, counter := keyScript(t, dir, `if [ "$(wc -l < `+filepath.Join(dir, "runs")+`)" -le 1 ]; then echo bad; else echo test-key; fi`)
	d := newDispatch(t, dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	// Linear answers 401 to the first key, so only the rerun's key can reach it.
	res := d.Run("run")
	require.ErrorIs(t, res.Err, linear.ErrUnauthorized)
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
	assert.Equal(t, 2, runCount(t, counter))
}

func TestApiKeyCommandStopsWhenRunIsCancelled(t *testing.T) {
	t.Parallel()
	// The sleep is a grandchild of the shell that holds the command's stdout.
	command, counter := keyScript(t, t.TempDir(), "sleep 30")
	d := newDispatch(t, dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n"})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	done := make(chan Result, 1)
	go func() { done <- d.runOver(ctx, dispatch.Options{}, "run") }()
	require.Eventually(t, func() bool { return runCount(t, counter) == 1 }, 5*time.Second, 10*time.Millisecond)
	stop()

	select {
	case res := <-done:
		require.NoError(t, res.Err)
	case <-time.After(3 * time.Second):
		t.Fatal("dispatch run did not return within 3s of cancel")
	}
}

func TestApiKeyCommandThatBlocksTimesOutAndTheNextStartRetries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	command, counter := keyScript(t, dir, `if [ "$(wc -l < `+filepath.Join(dir, "runs")+`)" -le 1 ]; then sleep 30; fi; echo test-key`)
	d := newDispatch(t, dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	timeout := dispatch.Options{KeyCommandTimeout: time.Second}

	ticked := later(func() Result { return d.runOver(context.Background(), timeout, "tick") })
	require.Eventually(t, func() bool { return runCount(t, counter) == 1 }, 20*time.Second, time.Millisecond)
	assert.Equal(t, time.Second, d.Deadline("api_key_command"))
	d.Expire("api_key_command")
	res := ticked()

	require.ErrorContains(t, res.Err, command)
	require.ErrorContains(t, res.Err, "timed out after 1s")
	res = d.runOver(context.Background(), timeout, "tick")
	require.NoError(t, res.Err)
	assert.Len(t, d.Workers.Launched(), 1)
	assert.Equal(t, 2, runCount(t, counter))
}

const referenceMarker = "# every setting at its default"

var tomlBlock = regexp.MustCompile("(?s)```toml\n(.*?)```")

// docExamples returns every toml block in the docs, keyed by file and index.
func docExamples(t *testing.T) map[string]string {
	t.Helper()
	files := []string{"README.md"}
	more, err := filepath.Glob("docs/*.md")
	require.NoError(t, err)
	files = append(files, more...)

	examples := map[string]string{}
	for _, file := range files {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		for i, m := range tomlBlock.FindAllStringSubmatch(string(body), -1) {
			examples[file+"#"+strconv.Itoa(i+1)] = m[1]
		}
	}
	return examples
}

func TestDocConfigExamplesLoad(t *testing.T) {
	t.Parallel()
	examples := docExamples(t)
	require.NotEmpty(t, examples)

	for name, body := range examples {
		t.Run(name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{OwnRepos: strings.Contains(body, "[repos"), Config: body})
			// The reference block names ~/Development/dispatch for rules, plugin root and repo.
			checkout, err := filepath.Abs(".")
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Join(d.Home, "Development"), 0o755))
			require.NoError(t, os.Symlink(checkout, filepath.Join(d.Home, "Development", "dispatch")))

			res := d.Run("tick")

			require.NoError(t, res.Err)
		})
	}
}

// No command prints the effective config, so this compares the documented defaults to config.Load
// and the struct's keys directly. It guards the docs against drift from the code.
func TestDocConfigReferenceListsEveryKeyAtItsDefault(t *testing.T) {
	t.Parallel()
	var reference string
	for _, body := range docExamples(t) {
		if strings.HasPrefix(body, referenceMarker) {
			reference = body
		}
	}
	require.NotEmpty(t, reference)

	got, err := loadExample(t, reference)
	require.NoError(t, err)
	want, err := loadExample(t, "")
	require.NoError(t, err)
	// Repos have no default, so the reference shows one repo at its defaults.
	assert.Equal(t, config.Repo{
		Path:                 environment.Process().ExpandHome("~/Development/dispatch"),
		OpenPRAs:             "open",
		IgnoreCommentAuthors: []string{},
		ApprovalChecks:       []string{},
	}, got.Repos["dispatch"])
	got.Repos, want.Repos = nil, nil
	assert.Equal(t, want, got)

	var decoded config.Config
	meta, err := toml.Decode(reference, &decoded)
	require.NoError(t, err)
	documented := map[string]bool{}
	for _, key := range meta.Keys() {
		documented[key.String()] = true
	}
	for _, key := range settingKeys(reflect.TypeOf(config.Config{}), "") {
		assert.Contains(t, documented, key)
	}
	body, err := os.ReadFile("docs/config.md")
	require.NoError(t, err)
	for _, key := range settingKeys(reflect.TypeOf(config.Repo{}), "repos.dispatch.") {
		assert.Contains(t, documented, key)
		assert.Contains(t, string(body), "| `"+strings.TrimPrefix(key, "repos.dispatch.")+"` |")
	}
}

// settingKeys lists the dotted toml name of every leaf setting in typ.
func settingKeys(typ reflect.Type, prefix string) []string {
	var keys []string
	for i := range typ.NumField() {
		field := typ.Field(i)
		tag, _, _ := strings.Cut(field.Tag.Get("toml"), ",")
		name := prefix + tag
		if field.Type.Kind() == reflect.Struct {
			keys = append(keys, settingKeys(field.Type, name+".")...)
			continue
		}
		if field.Type.Kind() != reflect.Map {
			keys = append(keys, name)
		}
	}
	return keys
}

// The glossary is prose no command prints, so this guards the terms the code uses against being
// dropped from it.
func TestContextDefinesTheRepoSettingsTerms(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("docs/CONTEXT.md")
	require.NoError(t, err)

	for _, term := range []string{"Repo settings", "Listed author", "Optional check", "PR phase", "Review question"} {
		assert.Contains(t, string(body), "**"+term+"**:")
	}
}

// The glossary is prose no command prints, so this guards the terms the rules use against being
// dropped from it.
func TestContextDefinesTheRuleTerms(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("docs/CONTEXT.md")
	require.NoError(t, err)

	for _, term := range []string{"World", "Fact", "Unknown", "Cycle", "Once key"} {
		assert.Contains(t, string(body), "**"+term+"**:")
	}
}

// The install command lives in the guide and the plugin manifests, files no command compares, so
// this guards the guide against a renamed plugin or marketplace.
func TestGuideInstallCommandsMatchTheMarketplace(t *testing.T) {
	t.Parallel()
	var manifest, catalog struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	for file, into := range map[string]any{
		".claude-plugin/plugin.json":      &manifest,
		".claude-plugin/marketplace.json": &catalog,
	} {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, into))
	}

	require.Len(t, catalog.Plugins, 1)
	assert.Equal(t, manifest.Name, catalog.Plugins[0].Name)
	assert.Equal(t, "./", catalog.Plugins[0].Source)

	guide, err := os.ReadFile("docs/guide.md")
	require.NoError(t, err)
	assert.Contains(t, string(guide), "claude plugin install "+catalog.Plugins[0].Name+"@"+catalog.Name)
}

func TestApiKeyCommandRunsInTheWorkingDirectoryWithTheEnvironmentItWasGiven(t *testing.T) {
	t.Parallel()
	work, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	seen := filepath.Join(t.TempDir(), "seen")
	command, _ := keyScript(t, t.TempDir(), "pwd -P > "+seen+"\necho \"$MARKER\" >> "+seen+"\necho test-key")
	d := newDispatch(t, dispatchOptions{
		Key:    configuredKey,
		Dir:    work,
		Env:    map[string]string{"MARKER": "from-options"},
		Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n",
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	got, err := os.ReadFile(seen)
	require.NoError(t, err)
	assert.Equal(t, work+"\nfrom-options\n", string(got))
}
