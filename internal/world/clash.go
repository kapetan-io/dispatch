package world

import (
	"fmt"

	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/ticket"
)

// clash is the error for a Linear read that returned a ticket, or a blocker of one, whose id starts
// with LOC-, which only a local ticket may have. Ticket systems keep their ids apart by that prefix.
func clash(tickets ...linear.Ticket) error {
	for _, t := range tickets {
		ids := []string{t.Identifier}
		for _, b := range t.Blockers {
			ids = append(ids, b.Identifier)
		}
		for _, id := range ids {
			if ticket.SystemOf(id) == ticket.SystemLocal {
				return fmt.Errorf("Linear returned %s, which is a local ticket id; rename the Linear team", id)
			}
		}
	}
	return nil
}
