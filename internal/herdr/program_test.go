package herdr_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/harnesstest"
	"github.com/kapetan-io/dispatch/internal/herdr"
)

func TestStartWorkerStampsTheTabEnvInTheOrderClaudeAlwaysGot(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()
	cli := s.cli.WithDispatchBin("/opt/dispatch/bin/dispatch")

	_, err := cli.StartWorker(context.Background(), herdr.Worker{
		Workspace: "w9", Dir: "/t/1", Label: "ENG-1 Fix", Name: "eng-1", PluginRoot: "/plugin", Program: claude,
	})

	require.NoError(t, err)
	tabs := s.callsStarting("tab create")
	require.Len(t, tabs, 1)
	assert.Regexp(t, `^tab create --workspace w9 --cwd /t/1 --label ENG-1 Fix `+
		`--env PATH=/plugin/scripts/shims:.* --env AUTONOMOUS_WORKER=1 --env DISPATCH_PLUGIN_ROOT=/plugin `+
		`--env DISPATCH_BIN=/opt/dispatch/bin/dispatch --no-focus$`, tabs[0])
}

func TestStartWorkerStampsTheProgramEnvSortedByKeyAfterTheStampedEntries(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()
	cli := s.cli.WithDispatchBin("/opt/dispatch/bin/dispatch")
	program := herdr.Program{
		Name: "Fake", AgentKind: "fake", Argv: []string{"--model", "tiny"},
		Env: map[string]string{"ZED": "z", "ALPHA": "a", "MIDDLE": "m"},
	}

	_, err := cli.StartWorker(context.Background(), herdr.Worker{
		Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", PluginRoot: "/plugin", Program: program,
	})

	require.NoError(t, err)
	tabs := s.callsStarting("tab create")
	require.Len(t, tabs, 1)
	assert.Regexp(t, `--env DISPATCH_BIN=/opt/dispatch/bin/dispatch --env ALPHA=a --env MIDDLE=m --env ZED=z --no-focus$`, tabs[0])
}

func TestStartWorkerPutsEveryPathFirstDirectoryBeforeTheInheritedPathInOrder(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()
	program := herdr.Program{
		Name: "Fake", AgentKind: "fake", Argv: []string{"--model", "tiny"},
		PathFirst: []string{"/first", "/second"},
	}

	_, err := s.cli.StartWorker(context.Background(), herdr.Worker{
		Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", PluginRoot: "/plugin", Program: program,
	})

	require.NoError(t, err)
	tabs := s.callsStarting("tab create")
	require.Len(t, tabs, 1)
	assert.Contains(t, tabs[0], "--env PATH=/first:/second:")
}

func TestStartWorkerLeavesPathAloneForAProgramWithNoPathFirst(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	s := newStatefulHerdr(t)
	s.workerStarts()

	_, err := s.cli.StartWorker(context.Background(), herdr.Worker{
		Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", PluginRoot: "/plugin", Program: harnesstest.New().Program(harness.Start{Model: "tiny"}),
	})

	require.NoError(t, err)
	tabs := s.callsStarting("tab create")
	require.Len(t, tabs, 1)
	assert.Contains(t, tabs[0], "--env PATH=/usr/bin:/bin --env AUTONOMOUS_WORKER=1")
	assert.NotContains(t, tabs[0], "DISPATCH_BIN")
}

func TestStartWorkerStartsTheAgentOfTheProgramsKindWithItsArgv(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()

	_, err := s.cli.StartWorker(context.Background(), herdr.Worker{
		Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", PluginRoot: "/plugin", Program: harnesstest.New().Program(harness.Start{Model: "tiny"}),
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"agent start eng-1 --kind fake --pane w9:p1 --timeout 90000 -- --model tiny"}, s.callsStarting("agent start"))
}

func TestStartWorkerKeepsAnAgentOfTheProgramsKindWhoseStartTimedOut(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()
	s.write("start-fails", "")
	s.write("agents.json", `{"result":{"agents":[{"name":"eng-1","pane_id":"w9:p1","agent":"fake","agent_status":"idle"}]}}`)

	started, err := s.cli.StartWorker(context.Background(), herdr.Worker{
		Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", PluginRoot: "/plugin", Program: harnesstest.New().Program(harness.Start{Model: "tiny"}),
	})

	require.NoError(t, err)
	assert.Equal(t, "w9:p1", started.Pane)
	assert.Len(t, s.callsStarting("agent start"), 1)
}

func TestStartWorkerDoesNotTakeAnAgentOfAnotherKindForTheProgramsOwn(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()
	s.write("start-fails", "")
	s.write("agents.json", `{"result":{"agents":[{"name":"eng-1","pane_id":"w9:p1","agent":"claude","agent_status":"idle"}]}}`)

	started, err := s.cli.StartWorker(context.Background(), herdr.Worker{
		Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", PluginRoot: "/plugin", Program: harnesstest.New().Program(harness.Start{Model: "tiny"}),
	})

	require.ErrorContains(t, err, "herdr agent start failed in pane w9:p1")
	assert.Equal(t, "w9:p1", started.Pane)
	assert.Len(t, s.callsStarting("agent start"), 2)
}

func TestKickoffCountsSightingsOfEachDialogApartFromTheOthers(t *testing.T) {
	s := newStatefulHerdr(t)
	trust := "Do you trust this folder?  ❯ No, exit  Yes, proceed"
	s.screens(consentText, trust, consentText, trust, "❯")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1", claude.Startup)

	require.NoError(t, err)
	// Each dialog is on its own second try the second time it shows, however
	// many other dialogs came between.
	assert.Equal(t, []string{
		"pane send-keys w9:p1 Down Enter",
		"pane send-keys w9:p1 Down",
		"pane send-keys w9:p1 Enter",
		"pane send-keys w9:p1 2",
		"pane send-keys w9:p1 Enter",
		"pane send-keys w9:p1 Down",
		"pane send-keys w9:p1 Enter",
		"pane send-keys w9:p1 Enter",
	}, s.callsStarting("pane send-keys"))
}

func TestKickoffReturnsToTheFirstTryAfterTheLastOne(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens(consentText, consentText, consentText, "❯")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1", claude.Startup)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"pane send-keys w9:p1 Down Enter",
		"pane send-keys w9:p1 2",
		"pane send-keys w9:p1 Enter",
		"pane send-keys w9:p1 Down Enter",
		"pane send-keys w9:p1 Enter",
	}, s.callsStarting("pane send-keys"))
}

func TestKickoffAnswersWithTheFirstDialogWhoseEveryPatternShows(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("alpha beta", "ready>")
	startup := herdr.Startup{
		Answers: []herdr.Answer{
			{Shows: []*regexp.Regexp{regexp.MustCompile(`alpha`), regexp.MustCompile(`gamma`)}, Tries: []herdr.Try{{{"Never"}}}},
			{Shows: []*regexp.Regexp{regexp.MustCompile(`alpha`)}, Tries: []herdr.Try{{{"First"}}}},
			{Shows: []*regexp.Regexp{regexp.MustCompile(`beta`)}, Tries: []herdr.Try{{{"Second"}}}},
		},
		Ready: regexp.MustCompile(`>`),
	}

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1", startup)

	require.NoError(t, err)
	// The trailing Enter submits the kickoff.
	assert.Equal(t, []string{"pane send-keys w9:p1 First", "pane send-keys w9:p1 Enter"}, s.callsStarting("pane send-keys"))
}

func TestKickoffSendsEachKeyListOfATryAsOneSendKeysCall(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("alpha", "ready>")
	startup := herdr.Startup{
		Answers: []herdr.Answer{{Shows: []*regexp.Regexp{regexp.MustCompile(`alpha`)}, Tries: []herdr.Try{{{"Up", "Up"}, {"Enter"}}}}},
		Ready:   regexp.MustCompile(`>`),
	}

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1", startup)

	require.NoError(t, err)
	assert.Equal(t, []string{"pane send-keys w9:p1 Up Up", "pane send-keys w9:p1 Enter", "pane send-keys w9:p1 Enter"}, s.callsStarting("pane send-keys"))
}

func TestKickoffNeverAnswersADialogThatHasNoTries(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("alpha", "ready>")
	startup := herdr.Startup{
		Answers: []herdr.Answer{{Shows: []*regexp.Regexp{regexp.MustCompile(`alpha`)}}},
		Ready:   regexp.MustCompile(`>`),
	}

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1", startup)

	require.NoError(t, err)
	assert.Equal(t, []string{"pane send-keys w9:p1 Enter"}, s.callsStarting("pane send-keys"))
	assert.Equal(t, []string{"pane run w9:p1 /dispatch:build ENG-1"}, s.callsStarting("pane run"))
}

func TestKickoffNeverAnswersADialogThatListsNoPatterns(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("ready>")
	startup := herdr.Startup{
		Answers: []herdr.Answer{{Tries: []herdr.Try{{{"Enter"}}}}},
		Ready:   regexp.MustCompile(`>`),
	}

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1", startup)

	require.NoError(t, err)
	assert.Equal(t, []string{"pane send-keys w9:p1 Enter"}, s.callsStarting("pane send-keys"))
}

func TestKickoffGivesUpOnAProgramWithNoReadyScreen(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("❯")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1", herdr.Startup{})

	require.ErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
	assert.Empty(t, s.callsStarting("pane run"))
	assert.Empty(t, s.callsStarting("pane send-keys"))
}

func TestKickoffTreatsAScreenAsReadyWhenTheProgramHasNoUnrunPattern(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("❯ claude --model opus --plugin-dir /plugin")
	startup := herdr.Startup{Ready: regexp.MustCompile(`❯`)}

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1", startup)

	require.NoError(t, err)
	assert.Equal(t, []string{"pane send-keys w9:p1 Enter"}, s.callsStarting("pane send-keys"))
	assert.Equal(t, []string{"pane run w9:p1 /dispatch:build ENG-1"}, s.callsStarting("pane run"))
}

func TestKickoffTypesIntoAProgramWhoseReadyScreenIsNotClaudes(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("starting", "ready>")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1", harnesstest.New().Program(harness.Start{Model: "tiny"}).Startup)

	require.NoError(t, err)
	assert.Equal(t, []string{"pane run w9:p1 /dispatch:build ENG-1"}, s.callsStarting("pane run"))
}
