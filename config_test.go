package dispatch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnknownConfigKeyFailsEveryCommand(t *testing.T) {
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
			h := newHarness(t)
			h.config(test.config)

			_, err := h.run(test.args...)

			require.ErrorContains(t, err, test.want)
			require.ErrorContains(t, err, filepath.Join(h.dir, "config.toml"))
		})
	}
}

func TestUnknownConfigKeyTakesNoAction(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progres = 9\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick")

	require.Error(t, err)
	assert.Empty(t, h.launches())
	assert.Empty(t, h.linear.mutations())
}

func TestKnownConfigKeysBehaveAsBefore(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n[plugins.linear.statuses]\nin_progress = \"In Progress\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.launches(), 1)
}

func TestConfigRejectsABadRepoTable(t *testing.T) {
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
			file := writeHintConfig(t, test.config)

			err := dispatch.Run(t.Context(), []string{"plan"}, dispatch.Options{ConfigPath: file})

			require.ErrorContains(t, err, "while reading config "+file+": ")
			require.ErrorContains(t, err, test.wantErr)
			assert.NotContains(t, err.Error(), "toml: line")
		})
	}
}

func TestConfigSamePathIsComparedAfterHomeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	file := writeHintConfig(t, "[repos.a]\npath = \"~/src/api\"\n[repos.b]\npath = \""+home+"/src/api\"\n")

	err := dispatch.Run(t.Context(), []string{"plan"}, dispatch.Options{ConfigPath: file})

	require.ErrorContains(t, err, "repos.a and repos.b have the same path "+home+"/src/api")
}

func TestConfigLoadsTwoReposWhosePathsAreASymlinkAndItsTarget(t *testing.T) {
	clone := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(clone, link))
	file := writeHintConfig(t, "[repos.alpha]\npath = \""+link+"\"\n[repos.zeta]\npath = \""+clone+"\"\n")

	loaded, err := config.Load(file)

	require.NoError(t, err)
	assert.Equal(t, link, loaded.Repos["alpha"].Path)
	assert.Equal(t, clone, loaded.Repos["zeta"].Path)
}

func TestConfigNamesTheKeyOfAWrongTypedSetting(t *testing.T) {
	file := writeHintConfig(t, "[repos.api]\npath = \"~/x\"\nauto_merge = 1\n")

	err := dispatch.Run(t.Context(), []string{"plan"}, dispatch.Options{ConfigPath: file})

	require.ErrorContains(t, err, "repos.api.auto_merge: incompatible types")
}

func TestOldConfigLayoutFailsTheStartNamingItsFirstKey(t *testing.T) {
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
			h := newHarness(t)
			h.config(test.config)

			_, err := h.run("tick")

			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestRulesTableTakesAnyKey(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nbug_model = \"opus\"\nreviewers = [\"a\", \"b\"]\n[rules.slack]\nchannel = \"#eng\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.launches(), 1)
}

func TestConfigRejectsValuesThatDecodeButMeanNothing(t *testing.T) {
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
			h := newHarness(t)
			h.config(test.config)

			_, err := h.run("tick")

			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestConfigDaysAreAccepted(t *testing.T) {
	h := newHarness(t)
	h.config("[history]\nkeep = \"7d\"\nrotate = \"1d12h\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.launches(), 1)
}
