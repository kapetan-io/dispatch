package history

import (
	"slices"

	"github.com/kapetan-io/dispatch/internal/ledger"
)

// CycleRow is one line of dispatch cycle.
type CycleRow struct {
	State, Subject, Action, Rule, Detail string
}

// LatestCycle lists what the latest cycle in dir's newest segment wants, waits for and
// holds. A wanted action the ledger has since recorded is covered.
func LatestCycle(dir string) ([]CycleRow, error) {
	seg, ok, err := Latest(dir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoHistory
	}
	st, err := seg.Load()
	if err != nil {
		return nil, err
	}
	var rows []CycleRow
	if st.Cycle == nil {
		return rows, nil
	}
	c := st.Cycle
	for _, e := range c.Wants {
		key := ledger.Key(e.Kind, e.Subject, e.Once)
		if rc, ok := byKey(st.Ledger, key); ok && rc.Outcome != ledger.Failed {
			rows = append(rows, CycleRow{"covered", e.Subject, e.Kind, e.Rule, CoveredBy(rc) + " once " + e.Once.String()})
			continue
		}
		rows = append(rows, CycleRow{"act", e.Subject, e.Kind, e.Rule, "once " + e.Once.String()})
	}
	for _, e := range c.Waiting {
		rows = append(rows, CycleRow{"waiting", e.Subject, e.Kind, e.Rule, e.Reason})
	}
	for _, e := range c.Held {
		rows = append(rows, CycleRow{"held", e.Subject, "-", e.Rule, e.Reason})
	}
	for _, e := range c.Covered {
		rows = append(rows, CycleRow{"covered", e.Subject, e.Kind, e.Rule, e.Reason + " once " + e.Once.String()})
	}
	order := map[string]int{"act": 0, "waiting": 1, "held": 2, "covered": 3}
	slices.SortStableFunc(rows, func(a, b CycleRow) int { return order[a.State] - order[b.State] })
	return rows, nil
}

func byKey(l *ledger.Ledger, key string) (ledger.Receipt, bool) {
	for i := len(l.Entries) - 1; i >= 0; i-- {
		if l.Entries[i].Key() == key {
			return l.Entries[i], true
		}
	}
	return ledger.Receipt{}, false
}
