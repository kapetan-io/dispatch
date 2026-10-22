package dispatch_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/linear"
)

// setChildren gives Linear the children a parent's child list returns.
func (d *Dispatch) setChildren(children ...fakeTicket) { d.linear.setChildren(children...) }

// failChildren makes Linear refuse every child list read.
func (d *Dispatch) failChildren() { d.linear.failChildren() }

// failMoves makes Linear refuse every status change.
func (d *Dispatch) failMoves() { d.linear.failMoves() }

// childReads lists the parents each child list read of Linear asked for.
func (d *Dispatch) childReads() [][]string { return d.linear.childReadParents() }

// plan is what `dispatch plan` prints.
func (d *Dispatch) plan() string {
	d.t.Helper()
	res := d.Run("cycle")
	require.NoError(d.t, res.Err)
	return res.Stdout
}

// unreadRepo adds a repo whose clone moved, so treehouse cannot read its pool.
func (d *Dispatch) unreadRepo(label string) {
	d.addRepo(label, filepath.Join(d.dir, "moved"))
}

// realLauncher makes a launch create a herdr workspace, so a launched worker counts against the cap.
func (d *Dispatch) realLauncher() { parentsRealLauncher(d) }

// herdrDown makes herdr refuse every read, or answer again when on is false.
func (d *Dispatch) herdrDown(on bool) {
	var err error
	if on {
		err = errors.New("server not running")
	}
	d.tools.Herdr.Fail(herdrtest.OpRead, err)
}

func TestTickPromotesAnUnblockedChildAndHoldsTheBlockedOne(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(
		child("ENG-2", "ENG-1", "Todo"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Todo"}),
	)

	d.Tick()

	assert.Equal(t, []string{"ENG-2 -> In Progress"}, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
	// Surface exception: the child-list reads are Linear traffic that no command prints.
	for _, read := range d.childReads() {
		assert.Equal(t, []string{"uuid-ENG-1"}, read)
	}
	// Linear shows the promoted child In Progress at the re-read, so it launches in the same tick.
	require.Len(t, d.Workers.Launched(), 1)
	assert.Contains(t, d.Workers.Launched()[0], " ENG-2 ")
}

func TestTickLaunchesAPromotedChildOnceLinearShowsItInProgress(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(child("ENG-2", "ENG-1", "Todo"))
	d.Tick()

	// A second tick promotes and launches nothing again.
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
	assert.Len(t, d.Linear.Moves(), 1)
}

func TestTickPromotesABlockedChildOnTheFirstTickAfterItsBlockerIsDone(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		blocker string
		want    bool
	}{
		{name: "BlockerInProgress", blocker: "In Progress"},
		{name: "BlockerInReview", blocker: "In Review"},
		{name: "BlockerNeedsHuman", blocker: "Needs Human"},
		{name: "BlockerDone", blocker: "Done", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(parent("ENG-1", "In Progress"))
			d.setChildren(
				child("ENG-2", "ENG-1", test.blocker),
				child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", test.blocker}),
			)

			d.Tick()

			assert.Empty(t, d.Linear.Comments())
			if !test.want {
				assert.Empty(t, d.Linear.Moves())
				return
			}
			assert.Equal(t, []string{"ENG-3 -> In Progress"}, d.Linear.Moves())
		})
	}
}

func TestTickEscalatesAChildWhoseBlockerWasAbandoned(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"Canceled", "Duplicate"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(parent("ENG-1", "In Progress"))
			d.setChildren(
				child("ENG-2", "ENG-1", status),
				child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", status}),
			)

			d.Tick()

			assert.Equal(t, []string{"ENG-3 -> Needs Human"}, d.Linear.Moves())
			require.Len(t, d.Linear.Comments(), 1)
			assert.Contains(t, d.Linear.Comments()[0], "ENG-3: ")
			assert.Contains(t, d.Linear.Comments()[0], "ENG-2 is "+status)
			assert.Contains(t, d.Linear.Comments()[0], "Remove the blocker relation")
			assert.Empty(t, d.Workers.Launched())
		})
	}
}

func TestTickEscalatesAChildOnlyOnceAndAgainWhenMovedBackToTodo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(
		child("ENG-2", "ENG-1", "Canceled"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Canceled"}),
	)
	d.Tick()
	require.Len(t, d.Linear.Moves(), 1)
	require.Len(t, d.Linear.Comments(), 1)

	// Needs Human leaves the Todo set, so the next tick stays quiet.
	d.setChildren(child("ENG-2", "ENG-1", "Canceled"), child("ENG-3", "ENG-1", "Needs Human", [2]string{"ENG-2", "Canceled"}))
	d.Tick()
	assert.Len(t, d.Linear.Moves(), 1)
	assert.Len(t, d.Linear.Comments(), 1)

	// The user moves it back to Todo with the blocker still attached, a minute later.
	d.Advance(time.Minute)
	d.setChildren(child("ENG-2", "ENG-1", "Canceled"), child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Canceled"}))
	d.Tick()
	assert.Len(t, d.Linear.Moves(), 2)
	assert.Len(t, d.Linear.Comments(), 2)
}

func TestTickKeepsWaitingSilentlyOnAnOpenBlocker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(
		child("ENG-2", "ENG-1", "In Progress"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "In Progress"}),
	)

	d.Tick()

	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickEscalatesAChildWithAnAbandonedBlockerBesideAnOpenOne(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(
		child("ENG-2", "ENG-1", "In Progress"),
		child("ENG-4", "ENG-1", "Canceled"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "In Progress"}, [2]string{"ENG-4", "Canceled"}),
	)

	d.Tick()

	assert.Equal(t, []string{"ENG-3 -> Needs Human"}, d.Linear.Moves())
	require.Len(t, d.Linear.Comments(), 1)
	assert.Contains(t, d.Linear.Comments()[0], "its blocker ENG-4 is Canceled")
}

func TestTickEscalatesOnlyChildrenOfYoursThatCarryTheAILabel(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	blocker := [2]string{"ENG-9", "Canceled"}
	someoneElses := child("ENG-2", "ENG-1", "Todo", blocker)
	someoneElses.Assignee = "someone-else"
	unlabeled := child("ENG-3", "ENG-1", "Todo", blocker)
	unlabeled.Labels = []linear.Label{{Name: "dispatch", Group: "repo"}}
	d.Workers.Running("ENG-4", "")
	d.Linear.Finished(child("ENG-4", "ENG-1", "Todo", blocker))
	d.setChildren(someoneElses, unlabeled, child("ENG-4", "ENG-1", "Todo", blocker))

	d.Tick()

	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickEscalatesAnAbandonedBlockerEvenAtTheWorkerCap(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-5", "In Progress", "dispatch"))
	d.setChildren(child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Canceled"}))

	d.Tick()

	assert.Equal(t, []string{"ENG-3 -> Needs Human"}, d.Linear.Moves())
	assert.Len(t, d.Linear.Comments(), 1)
}

func TestTickHoldsAChildWhoseBlockersWereCutShort(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	cut := child("ENG-2", "ENG-1", "Todo")
	cut.BlockersTruncated = true
	d.setChildren(cut)

	d.Tick()

	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickPromotesTheFirstPieceOnceDesignIsOffAndAReviewChildIsDone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(withLabel(child("ENG-2", "ENG-1", "Done"), "design-review"), child("ENG-3", "ENG-1", "Todo"))

	d.Tick()

	assert.Equal(t, []string{"ENG-3 -> In Progress"}, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickClosesAParentOnceItsDoneReviewChildAndBuildChildrenAreFinished(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(withLabel(child("ENG-2", "ENG-1", "Done"), "design-review"), child("ENG-3", "ENG-1", "Done"))

	d.Tick()

	assert.Equal(t, []string{"ENG-1 -> Done"}, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickDoesNotCountATicketWithTruncatedLabelsAgainstTheCap(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	cut := ticket("ENG-3", "In Progress", "dispatch")
	cut.LabelsTruncated = true
	d.Linear.Tickets(parent("ENG-1", "In Progress"), cut)
	d.setChildren(child("ENG-2", "ENG-1", "Todo"))

	d.Tick()

	// The cut-short ticket goes to Needs Human and holds no slot, so the child is still promoted.
	assert.ElementsMatch(t, []string{"ENG-3 -> Needs Human", "ENG-2 -> In Progress"}, d.Linear.Moves())
	require.Len(t, d.Linear.Comments(), 1)
	assert.Contains(t, d.Linear.Comments()[0], "ENG-3: ")
}

func TestTickClosesAParentWhoseChildrenAreAllFinished(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(
		child("ENG-2", "ENG-1", "Done"),
		child("ENG-3", "ENG-1", "Canceled"),
		child("ENG-4", "ENG-1", "Duplicate"),
	)

	d.Tick()

	assert.Equal(t, []string{"ENG-1 -> Done"}, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickKeepsAParentOpenWhileAnyChildIsUnfinished(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"Backlog", "Todo", "In Progress", "In Review", "Needs Human"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(parent("ENG-1", "In Progress"))
			// Todo and Backlog children are held back by a blocker that never finishes.
			d.setChildren(
				child("ENG-2", "ENG-1", "Done"),
				child("ENG-3", "ENG-1", status, [2]string{"ENG-9", "Backlog"}),
			)

			d.Tick()

			assert.Empty(t, d.Linear.Moves())
			// An In Progress child with no worker gets the blocked comment and nothing else.
			if status != "In Progress" {
				assert.Empty(t, d.Linear.Comments())
				return
			}
			require.Len(t, d.Linear.Comments(), 1)
			assert.Contains(t, d.Linear.Comments()[0], "ENG-3: ")
		})
	}
}

func TestTickCountsEveryChildBeforeClosingAParent(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	// A child without the ai label or your assignment is still the parent's child.
	stranger := child("ENG-3", "ENG-1", "Todo")
	stranger.Assignee = "someone-else"
	d.setChildren(child("ENG-2", "ENG-1", "Done"), stranger)

	d.Tick()

	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickNeverClosesAParentWhoseChildrenWereNotReturned(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(child("ENG-2", "ENG-1", "Done"))
	d.failChildren()

	d.Tick()

	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickPromotesOnlyChildrenOfYoursThatCarryTheAILabel(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	someoneElses := child("ENG-2", "ENG-1", "Todo")
	someoneElses.Assignee = "someone-else"
	unlabeled := child("ENG-3", "ENG-1", "Todo")
	unlabeled.Labels = []linear.Label{{Name: "dispatch", Group: "repo"}}
	d.setChildren(someoneElses, unlabeled, child("ENG-4", "ENG-1", "Backlog"), child("ENG-5", "ENG-1", "Todo"))

	d.Tick()

	assert.Equal(t, []string{"ENG-5 -> In Progress"}, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickNeverPromotesPastTheWorkerCap(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 2\n"})
	d.realLauncher()
	d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	d.Workers.Running("ENG-9", "working")
	d.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Todo"))

	d.Tick()

	assert.Regexp(t, planRow("waiting", "ENG-3"), d.plan())
	assert.Equal(t, []string{"ENG-2 -> In Progress"}, d.Linear.Moves())
}

func TestTickPromotesAWaitingChildOnceASlotFrees(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	d.Workers.Running("ENG-9", "working")
	d.setChildren(child("ENG-2", "ENG-1", "Todo"))

	d.Tick()
	assert.Regexp(t, planRow("waiting", "ENG-2"), d.plan())
	require.Empty(t, d.Linear.Moves())

	// ENG-9 merged.
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.Tick()

	assert.Equal(t, []string{"ENG-2 -> In Progress"}, d.Linear.Moves())
}

func TestTickCountsInProgressTicketsStillWaitingForAWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	// ENG-9 is In Progress but has no worker yet, since its launch waits.
	d.realLauncher()
	d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	d.setChildren(child("ENG-2", "ENG-1", "Todo"))

	d.Tick()

	assert.Empty(t, d.Linear.Moves())
}

func TestTickDoesNotCountParentsAgainstTheWorkerCap(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(parent("ENG-1", "In Progress"), parent("ENG-8", "In Progress"))
	d.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-9", "ENG-8", "Backlog"))

	d.Tick()

	assert.Len(t, d.Linear.Moves(), 1)
	assert.Empty(t, d.Linear.Comments())
}

func TestTickNeverPromotesAChildThatHoldsALease(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	// ENG-2 was paused with its tree kept. Linear knows it only by identifier.
	d.Workers.Running("ENG-2", "")
	d.Linear.Finished(child("ENG-2", "ENG-1", "Todo"))
	d.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Todo"))

	d.Tick()

	assert.Equal(t, []string{"ENG-3 -> In Progress"}, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickStopsAChildYouPausedWithoutPromotingItAgain(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.Workers.Running("ENG-2", "working")
	d.Linear.Finished(child("ENG-2", "ENG-1", "Todo"))
	d.setChildren(child("ENG-2", "ENG-1", "Todo"))

	d.Tick()

	assert.Equal(t, []string{"w-ENG-2"}, d.Workers.Closed())
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickNeverGivesAParentAWorker(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"In Progress", "In Review"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(parent("ENG-1", status))
			d.setChildren(child("ENG-2", "ENG-1", "Todo", [2]string{"ENG-9", "Todo"}))

			d.Tick()

			assert.Empty(t, d.Workers.Launched())
			assert.Empty(t, d.Workers.Prompts())
		})
	}
}

func TestTickNotifiesOnceWhenAParentEntersNeedsHuman(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "Needs Human"))

	d.Tick()
	d.Tick()

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
	// Surface exception: the child-list reads are Linear traffic that no command prints.
	assert.Empty(t, d.childReads())
}

func TestTickLeavesDesignParentsAlone(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"design", "design-review"} {
		t.Run(label, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(withLabel(parent("ENG-1", "In Progress"), label))
			d.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Done"))

			d.Tick()

			assert.Empty(t, d.Linear.Moves())
			assert.Empty(t, d.Linear.Comments())
			// Surface exception: the child-list reads are Linear traffic that no command prints.
			assert.Empty(t, d.childReads())
		})
	}
}

func TestTickReadsNoChildrenWhenNoParentIsInProgress(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), parent("ENG-2", "In Review"))

	d.Tick()

	// Surface exception: the child-list reads are Linear traffic that no command prints.
	assert.Empty(t, d.childReads())
}

func TestTickKeepsLaunchingWhenTheChildrenCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-2", "In Progress", "dispatch"))
	d.setChildren(child("ENG-3", "ENG-1", "Backlog"))
	d.failChildren()

	d.Tick()

	plan := d.plan()
	assert.Len(t, d.Workers.Launched(), 1)
	assert.Regexp(t, planRow("held", "ENG-1"), plan)
	assert.Contains(t, plan, "the children of ENG-1 were not read")
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickSurvivesAFailedPromotion(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	d.setChildren(child("ENG-2", "ENG-1", "Todo"))
	d.failMoves()

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "ENG-2 move failed")
	require.Len(t, d.Workers.Launched(), 1)
	assert.Contains(t, d.Workers.Launched()[0], " ENG-9 ")
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickHoldsAChildWhoseRepoPoolCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.unreadRepo("tiger")
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	tiger := child("ENG-2", "ENG-1", "Todo")
	tiger.Labels = []linear.Label{{Name: "ai"}, {Name: "tiger", Group: "repo"}}
	d.setChildren(tiger)

	d.Tick()

	// Whether the child holds a lease in that pool is unknown, so it waits.
	plan := d.plan()
	assert.Regexp(t, planRow("held", "ENG-2"), plan)
	assert.Contains(t, plan, "treehouse pool tiger unread")
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickDoesNotEscalateAChildWhoseRepoPoolCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.unreadRepo("tiger")
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	tiger := child("ENG-2", "ENG-1", "Todo", [2]string{"ENG-9", "Canceled"})
	tiger.Labels = []linear.Label{{Name: "ai"}, {Name: "tiger", Group: "repo"}}
	d.setChildren(tiger)

	d.Tick()

	// A paused child may hold a lease in the unread pool, and escalating it would unpause it.
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickDoesNotEscalateAChildWhoseBlockerIsDoneButNotBlockerDone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[plugins.linear.statuses]\nblocker_done = \"Released\"\n"})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(
		child("ENG-2", "ENG-1", "Done"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Done"}),
	)

	d.Tick()

	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickSharesTheWorkerCapAcrossParents(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.realLauncher()
	d.Linear.Tickets(parent("ENG-1", "In Progress"), parent("ENG-4", "In Progress"))
	d.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-5", "ENG-4", "Todo"))

	d.Tick()

	assert.Regexp(t, planRow("waiting", "ENG-5"), d.plan())
	assert.Equal(t, []string{"ENG-2 -> In Progress"}, d.Linear.Moves())
}

func TestTickClosesOneParentAndPromotesForAnotherInTheSameTick(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"), parent("ENG-4", "In Progress"))
	d.setChildren(child("ENG-2", "ENG-1", "Done"), child("ENG-5", "ENG-4", "Todo"))

	d.Tick()

	assert.ElementsMatch(t, []string{"ENG-1 -> Done", "ENG-5 -> In Progress"}, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickNeverGivesAParentAWorkerOrANudge(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"Todo", "Backlog", "Needs Human"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(parent("ENG-1", status))

			d.Tick()

			assert.Empty(t, d.Workers.Launched())
			assert.Empty(t, d.Workers.Prompts())
			assert.Empty(t, d.Linear.Moves())
			assert.Empty(t, d.Linear.Comments())
		})
	}

	t.Run("IdleWorkerOnAnInProgressParent", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{})
		d.Linear.Tickets(parent("ENG-1", "In Progress"))
		d.Workers.Running("ENG-1", "idle")
		d.Workers.Running("ENG-2", "working")
		d.setChildren(child("ENG-2", "ENG-1", "In Progress"))

		d.Tick()

		assert.Empty(t, d.Workers.Launched())
		assert.Empty(t, d.Workers.Prompts())
	})
}

func TestTickPromotesNothingForAParentThatIsNotInProgress(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"Needs Human", "In Review", "Todo"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(parent("ENG-1", status))
			d.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Done"))

			d.Tick()

			assert.Empty(t, d.Linear.Moves())
			assert.Empty(t, d.Linear.Comments())
			// Surface exception: the child-list reads are Linear traffic that no command prints.
			assert.Empty(t, d.childReads())
		})
	}
}

func TestTickPromotesTheOldestChildFirstWhenOneSlotIsFree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.realLauncher()
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	older, newer := child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Todo")
	older.CreatedAt = "2026-10-01T09:00:00.000Z"
	newer.CreatedAt = "2026-10-02T09:00:00.000Z"
	// Linear may return either order.
	d.setChildren(newer, older)

	d.Tick()

	assert.Regexp(t, planRow("waiting", "ENG-3"), d.plan())
	assert.Equal(t, []string{"ENG-2 -> In Progress"}, d.Linear.Moves())
}

func TestTickNeverPromotesAChildOutsideTheConfiguredTeams(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	other := child("OTH-2", "ENG-1", "Todo")
	other.Team = "OTH"
	d.setChildren(other)

	d.Tick()

	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickPromotesAChildWithNoRepoLabelAndEscalatesItOnceLinearShowsItInProgress(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	lost := child("ENG-2", "ENG-1", "Todo")
	lost.Labels = []linear.Label{{Name: "ai"}}
	d.setChildren(lost)

	d.Tick()

	// The promotion holds its slot until Linear shows the child In Progress, then the user hears why it can't start.
	assert.Equal(t, []string{"ENG-2 -> In Progress", "ENG-2 -> Needs Human"}, d.Linear.Moves())
	require.Len(t, d.Linear.Comments(), 1)
	assert.Contains(t, d.Linear.Comments()[0], "it has no repo label")
}

func TestTickReleasesAChildWhenItsBlockerIsDoneWhateverCloseParentIs(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[plugins.linear.statuses]\nclose_parent = \"Closed\"\n"})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(
		child("ENG-2", "ENG-1", "Done"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Done"}),
	)

	d.Tick()

	assert.Equal(t, []string{"ENG-3 -> In Progress"}, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickReleasesAChildOnTheConfiguredBlockerStatus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		blocker string
		want    bool
	}{
		{name: "ConfiguredStatus", blocker: "Released", want: true},
		{name: "DefaultStatus", blocker: "Done", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: "[plugins.linear.statuses]\nblocker_done = \"Released\"\n"})
			d.tools.Linear.AddState("Released", "completed")
			d.Linear.Tickets(parent("ENG-1", "In Progress"))
			d.setChildren(
				child("ENG-2", "ENG-1", test.blocker),
				child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", test.blocker}),
			)

			d.Tick()

			assert.Equal(t, test.want, len(d.Linear.Moves()) == 1)
		})
	}
}

func TestTickAdmitsOnlyTheOldestWaitingTicketToOneFreeSlot(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.realLauncher()
	newest, middle, oldest := ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"), ticket("ENG-3", "In Progress", "dispatch")
	newest.CreatedAt = "2026-10-03T09:00:00.000Z"
	middle.CreatedAt = "2026-10-02T09:00:00.000Z"
	oldest.CreatedAt = "2026-10-01T09:00:00.000Z"
	d.Linear.Tickets(newest, middle, oldest)

	d.Tick()

	var launched []string
	for _, id := range []string{"ENG-1", "ENG-2", "ENG-3"} {
		for _, receipt := range d.Receipts(id) {
			if receipt.Kind == "launch" {
				launched = append(launched, receipt.Subject)
			}
		}
	}
	assert.Equal(t, []string{"ENG-3"}, launched)
	plan := d.plan()
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan)
	assert.Regexp(t, planRow("waiting", "ENG-1"), plan)
}

func TestTickCountsATicketWhoseWorkerIsUnknownAgainstTheCap(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-5", "In Progress", "dispatch"))
	d.setChildren(child("ENG-2", "ENG-1", "Todo"))
	d.herdrDown(true)

	d.Tick()

	// ENG-5 may already have a worker, so it holds the only slot and the promotion waits.
	assert.Regexp(t, planRow("waiting", "ENG-2"), d.plan())
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Workers.Launched())
}

func TestTickGivesOneFreeSlotToEitherAPromotionOrALaunchNeverBoth(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.realLauncher()
	d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-5", "In Progress", "dispatch"))
	d.setChildren(child("ENG-2", "ENG-1", "Todo"))

	d.Tick()

	// A promotion holds its slot until Linear shows the child In Progress and its worker is up,
	// so the launch that wanted the same slot in the same cycle waits.
	// Surface exception: herdr's started workspaces are the fake's state, not command output.
	assert.Len(t, d.tools.Herdr.Started(), 1)
	assert.Contains(t, d.plan(), "waiting")
}

func TestTickNeverStartsAWorkerWhileHerdrIsUnread(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.herdrDown(true)

	d.Tick()

	assert.Regexp(t, planRow("held", "ENG-1"), d.plan())
	assert.Empty(t, d.Workers.Launched())

	d.herdrDown(false)
	d.Tick()
	assert.Len(t, d.Workers.Launched(), 1)
}
