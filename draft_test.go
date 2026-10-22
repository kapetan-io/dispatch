package dispatch_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func draftPR(head string) prState {
	pr := cleanPR(head)
	pr.draft = true
	return pr
}

func TestTickNotifiesOncePerHeadWhenADraftIsReady(t *testing.T) {
	h := draftHarness(t)
	h.pr("ENG-1", draftPR("a1"))

	h.tick()

	assert.Equal(t, []string{draftReadyText1}, h.notifications())

	h.tick()
	h.tick()
	assert.Len(t, h.notifications(), 1)

	h.pr("ENG-1", draftPR("a2"))
	h.tick()
	assert.Equal(t, []string{draftReadyText1, draftReadyText1}, h.notifications())
}

func TestTickNotifiesAgainWhenTheUserOpensAReadyDraft(t *testing.T) {
	h := draftHarness(t)
	h.pr("ENG-1", draftPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)

	// Opening the draft leaves the head where it was.
	h.pr("ENG-1", cleanPR("a1"))
	h.tick()

	assert.Equal(t, []string{draftReadyText1, reviewsHandled1}, h.notifications())

	h.tick()
	assert.Len(t, h.notifications(), 2)

	// Moving the PR back to draft at the same head says nothing more.
	h.pr("ENG-1", draftPR("a1"))
	h.tick()
	assert.Len(t, h.notifications(), 2)
}

func TestTickStaysSilentForADraftThatIsNotReady(t *testing.T) {
	h := draftHarness(t)
	pr := draftPR("a1")
	pr.unresolved = 1
	h.pr("ENG-1", pr)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.notifications())
}

func TestTickKeepsTheDraftNotificationAcrossALeasedTicketsAbsence(t *testing.T) {
	h := draftHarness(t)
	h.pr("ENG-1", draftPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.notifications(), 1)

	// Out of the Linear read while its worker tree is leased.
	h.tickets()
	_, err = h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
}

func TestTickNeverMergesADraft(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings string
		text     string
	}{
		{name: "OpenRepo", settings: autoMergeSetting, text: readyText1},
		{name: "DraftRepo", settings: autoMergeSetting + draftSetting, text: draftReadyText1},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.repoSettings("dispatch", test.settings)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			pr := approvedPR("a1")
			pr.draft = true
			h.pr("ENG-1", pr)

			h.tick()
			h.advance(5 * time.Minute)
			h.tick()

			assert.Empty(t, h.merges())
			assert.Equal(t, []string{test.text}, h.notifications())
			assert.Empty(t, h.receipts("merge", ""))
		})
	}
}

func TestTickKeepsTodaysNotificationInAnOpenRepoForADraft(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", draftPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)

	h.pr("ENG-1", cleanPR("a1"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{readyText1}, h.notifications())
}

func TestTickMergesAnApprovedOpenPRInADraftRepoWithAutoMerge(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting+draftSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{mergeCall1}, h.merges())
}

func TestTickNotifiesReviewsHandledOncePerHeadForAnOpenPRInADraftRepo(t *testing.T) {
	h := draftHarness(t)
	h.pr("ENG-1", cleanPR("a1"))

	h.tick()

	assert.Equal(t, []string{reviewsHandled1}, h.notifications())

	h.tick()
	assert.Len(t, h.notifications(), 1)

	h.pr("ENG-1", cleanPR("a2"))
	h.tick()
	assert.Equal(t, []string{reviewsHandled1, reviewsHandled1}, h.notifications())
}

func TestTickMergesADraftOnceTheUserOpensItAtTheSameHead(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting+draftSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	pr := approvedPR("a1")
	pr.draft = true
	h.pr("ENG-1", pr)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Empty(t, h.merges())

	h.pr("ENG-1", approvedPR("a1"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{mergeCall1}, h.merges())
}

func TestTickRetriesADraftNotificationThatFailed(t *testing.T) {
	h := draftHarness(t)
	h.pr("ENG-1", draftPR("a1"))
	h.tools.Alerts.Fail(errors.New("exit status 1"))

	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-1")
	assert.Empty(t, h.notifications())

	h.tools.Alerts.Fail(nil)
	h.tick()

	assert.Equal(t, []string{draftReadyText1}, h.notifications())
}

func TestTickAppliesOpenPRAsOnlyToItsOwnRepo(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", filepath.Join(h.dir, "tiger"))
	h.repoSettings("tiger", draftSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", draftPR("a1"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{readyText1}, h.notifications())
}
