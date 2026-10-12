package dispatch_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTickMarksAQuietDesignWorkerAfterAnHourIdle(t *testing.T) {
	for _, label := range []string{"design", "design-review"} {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), label))
			h.worker("ENG-1", "idle")

			h.tick()
			h.advance(59 * time.Minute)
			h.tick()
			assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())

			h.advance(time.Minute)
			h.tick()
			assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|💤 ENG-1"}, h.renames())
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.notifications())
		})
	}
}

func TestTickClearsTheQuietMarkWhenTheDesignWorkerWorksAgain(t *testing.T) {
	h := newHarness(t)
	h.tickets(designTicket("ENG-1", "In Progress"))
	h.worker("ENG-1", "idle")
	h.tick()
	h.advance(time.Hour)
	h.tick()

	h.setWorkerStatus("ENG-1", "working")
	h.tick()

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|💤 ENG-1", "w-ENG-1|🟢 ENG-1"}, h.renames())
}

func TestTickRestartsTheQuietHourWhenTheDesignWorkerIdlesAgain(t *testing.T) {
	h := newHarness(t)
	h.tickets(designTicket("ENG-1", "In Progress"))
	h.worker("ENG-1", "idle")
	h.tick()
	h.advance(30 * time.Minute)
	h.setWorkerStatus("ENG-1", "working")
	h.tick()
	h.setWorkerStatus("ENG-1", "idle")
	h.tick()

	h.advance(45 * time.Minute)
	h.tick()

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())
}

func TestTickKeepsABlockedDesignWorkerGreen(t *testing.T) {
	h := newHarness(t)
	h.tickets(designTicket("ENG-1", "In Progress"))
	h.worker("ENG-1", "blocked")
	h.tick()

	h.advance(3 * time.Hour)
	h.tick()

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())
}

func TestTickKeepsAnIdleBuildWorkerGreen(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tick()

	h.advance(3 * time.Hour)
	h.tick()

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())
}
