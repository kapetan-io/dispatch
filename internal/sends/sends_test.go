package sends_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/sends"
)

// Removing a file is not exclusive on every filesystem, so this runs many rounds to catch a claim two callers both win.
func TestOneSendIsClaimedByOneOfManyCallers(t *testing.T) {
	const rounds, callers = 200, 8
	for range rounds {
		state := t.TempDir()
		_, err := sends.Record(state, state, "", "/merge-it 42")
		require.NoError(t, err)
		var won atomic.Int32
		var wg sync.WaitGroup
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if sends.Claim(state, state, "session", "/merge-it 42") {
					won.Add(1)
				}
			}()
		}
		wg.Wait()

		require.Equal(t, int32(1), won.Load())
	}
}

func TestClaimMatchesOnlyTheSameTextAndSession(t *testing.T) {
	state := t.TempDir()
	_, err := sends.Record(state, state, "mine", "/merge-it 42")
	require.NoError(t, err)

	assert.False(t, sends.Claim(state, state, "mine", "/merge-it 43"))
	assert.False(t, sends.Claim(state, state, "other", "/merge-it 42"))
	assert.True(t, sends.Claim(state, state, "mine", "  /merge-it 42\n"))
	assert.False(t, sends.Claim(state, state, "mine", "/merge-it 42"))
}

func TestClaimTakesTheOldestOfTwoIdenticalSendsAndAnEmptySessionMatchesAny(t *testing.T) {
	state := t.TempDir()
	_, err := sends.Record(state, state, "", "/merge-it 42")
	require.NoError(t, err)
	_, err = sends.Record(state, state, "mine", "/merge-it 42")
	require.NoError(t, err)

	assert.True(t, sends.Claim(state, state, "mine", "/merge-it 42"))
	assert.True(t, sends.Claim(state, state, "mine", "/merge-it 42"))
	assert.False(t, sends.Claim(state, state, "mine", "/merge-it 42"))
}

func TestRecordKeepsNoRecordOfTextThatIsNotACommandAndWithdrawDropsOne(t *testing.T) {
	state := t.TempDir()
	id, err := sends.Record(state, state, "", "keep going")
	require.NoError(t, err)
	assert.Empty(t, id)
	id, err = sends.Record(state, state, "", "/merge-it 42")
	require.NoError(t, err)

	sends.Withdraw(state, state, id)

	assert.False(t, sends.Claim(state, state, "mine", "/merge-it 42"))
}

func TestClearDropsEverySendOfTheTree(t *testing.T) {
	state := t.TempDir()
	_, err := sends.Record(state, state, "", "/merge-it 42")
	require.NoError(t, err)

	sends.Clear(state, state)

	assert.False(t, sends.Claim(state, state, "mine", "/merge-it 42"))
}

func TestClearOfOneTreeLeavesTheSendsOfAnotherTree(t *testing.T) {
	state := t.TempDir()
	_, err := sends.Record(state, "/trees/one", "", "/merge-it 42")
	require.NoError(t, err)
	_, err = sends.Record(state, "/trees/two", "", "/merge-it 42")
	require.NoError(t, err)

	sends.Clear(state, "/trees/one")

	assert.False(t, sends.Claim(state, "/trees/one", "mine", "/merge-it 42"))
	assert.True(t, sends.Claim(state, "/trees/two", "mine", "/merge-it 42"))
}

func TestASendIsOnlyClaimedInTheTreeItWasRecordedFor(t *testing.T) {
	state := t.TempDir()
	_, err := sends.Record(state, "/trees/one", "", "/merge-it 42")
	require.NoError(t, err)

	assert.False(t, sends.Claim(state, "/trees/two", "mine", "/merge-it 42"))
	assert.True(t, sends.Claim(state, "/trees/one", "mine", "/merge-it 42"))
}

func TestWithdrawDropsOnlyTheSendItWasGivenWhenTwoAreIdentical(t *testing.T) {
	state := t.TempDir()
	_, err := sends.Record(state, state, "", "/merge-it 42")
	require.NoError(t, err)
	second, err := sends.Record(state, state, "", "/merge-it 42")
	require.NoError(t, err)

	sends.Withdraw(state, state, second)
	sends.Withdraw(state, state, "")

	assert.True(t, sends.Claim(state, state, "mine", "/merge-it 42"))
	assert.False(t, sends.Claim(state, state, "mine", "/merge-it 42"))
}

func TestAClaimedSendStaysClaimedAfterWithdrawAndANewRecordIsClaimableAgain(t *testing.T) {
	state := t.TempDir()
	id, err := sends.Record(state, state, "", "/merge-it 42")
	require.NoError(t, err)
	require.True(t, sends.Claim(state, state, "mine", "/merge-it 42"))

	sends.Withdraw(state, state, id)
	_, err = sends.Record(state, state, "", "/merge-it 42")
	require.NoError(t, err)

	assert.True(t, sends.Claim(state, state, "mine", "/merge-it 42"))
}

// A command is a / at the first byte, so a nudge led by white space is not one and must not leave a record a person's later command would claim.
func TestATextLedByWhiteSpaceLeavesNoSendForAPersonsCommandToClaim(t *testing.T) {
	state := t.TempDir()

	_, err := sends.Record(state, state, "", " /merge-it 42")
	require.NoError(t, err)

	assert.False(t, sends.Claim(state, state, "mine", "/merge-it 42"))
}
