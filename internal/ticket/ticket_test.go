package ticket_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/ticket"
)

func TestSystemOf(t *testing.T) {
	for _, test := range []struct{ id, want string }{
		{"LOC-7", ticket.SystemLocal},
		{"ENG-7", ticket.SystemLinear},
		{"LOCAL-7", ticket.SystemLinear},
	} {
		assert.Equal(t, test.want, ticket.SystemOf(test.id))
	}
}

func TestLocalNumber(t *testing.T) {
	for _, test := range []struct {
		id   string
		want uint64
		ok   bool
	}{
		{"LOC-7", 7, true},
		{"LOC-0", 0, false},
		{"LOC-07", 0, false},
		{"LOC-+7", 0, false},
		{"LOC-", 0, false},
		{"ENG-7", 0, false},
	} {
		n, ok := ticket.LocalNumber(test.id)
		assert.Equal(t, test.ok, ok)
		assert.Equal(t, test.want, n)
	}
}

func TestStatusesCheck(t *testing.T) {
	require.NoError(t, ticket.DefaultStatuses().Check())

	for _, test := range []struct {
		name     string
		statuses ticket.Statuses
		want     string
	}{
		{"JobTwice", append(ticket.DefaultStatuses(), ticket.Status{Name: "Doing", Jobs: []ticket.Job{ticket.InProgress}}), "in_progress belongs to 2 statuses (In Progress, Doing)"},
		{"JobMissing", ticket.DefaultStatuses()[:5], "no status does the job close_parent"},
		{"NameTwice", append(ticket.DefaultStatuses(), ticket.Status{Name: "done"}), `two statuses are named "done"`},
		{"JobUnknown", append(ticket.DefaultStatuses(), ticket.Status{Name: "X", Jobs: []ticket.Job{"fly"}}), `"fly"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, test.statuses.Check(), test.want)
		})
	}
}

func TestStatusesFindIgnoresCase(t *testing.T) {
	st, ok := ticket.DefaultStatuses().Find("in REVIEW")
	require.True(t, ok)
	assert.Equal(t, "In Review", st.Name)
}
