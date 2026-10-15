package dispatch_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestA3Debug(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE", moreThreads: true})
	_, err := h.run("tick")
	require.NoError(t, err)
	fmt.Println("ERR1:", h.loopErr(), "OUT:", h.loopOut())
	h2 := newHarness(t)
	h2.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h2.worker("ENG-1", "idle")
	h2.tools.PullRequests.FailRead(errors.New("HTTP 401: Bad credentials"))
	_, err = h2.run("tick")
	require.NoError(t, err)
	fmt.Println("ERR2:", h2.loopErr(), "OUT:", h2.loopOut())
}
