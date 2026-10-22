package dispatch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The segment was written by main at commit 6d04c44 when it merged ENG-1, with the notify's two
// receipt lines cut by hand, as a crash between the merge and the notify leaves them. A harness
// run can't stop there itself, and CrashedEarlierRun can't write a receipt's saved rest list.
func TestARestartFinishesAnOrderedListThatACrashCutShort(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.SavedFromMain("main-6d04c44")
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))

	d.Tick()

	var notify []Receipt
	for _, receipt := range d.Receipts("ENG-1") {
		if receipt.Kind == "notify" {
			notify = append(notify, receipt)
		}
	}
	require.Len(t, notify, 1)
	assert.Equal(t, "done", notify[0].State)
	assert.Equal(t, []string{mergedText1}, d.Alerts.Shown())

	d.Restart()
	d.Tick()

	assert.Equal(t, []string{mergedText1}, d.Alerts.Shown())
}
