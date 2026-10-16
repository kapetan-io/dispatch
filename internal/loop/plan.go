package loop

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/ledger"
)

// ErrNoHistory means there is no history to read a plan from.
var ErrNoHistory = errors.New("there is no history to read")

// PlanRow is one line of dispatch plan.
type PlanRow struct {
	State, Subject, Action, Rule, Detail string
}

// Plan lists what the latest cycle in dir's newest segment wants, waits for and
// holds. A wanted action the ledger has since recorded is covered.
func Plan(dir string) ([]PlanRow, error) {
	seg, ok, err := history.Latest(dir)
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
	var rows []PlanRow
	if st.Cycle == nil {
		return rows, nil
	}
	c := st.Cycle
	for _, e := range c.Wants {
		key := ledger.Key(e.Kind, e.Subject, e.Once)
		if rc, ok := byKey(st.Ledger, key); ok && rc.Outcome != ledger.Failed {
			rows = append(rows, PlanRow{"covered", e.Subject, e.Kind, e.Rule, coveredBy(rc) + " once " + FormatOnce(e.Once)})
			continue
		}
		rows = append(rows, PlanRow{"act", e.Subject, e.Kind, e.Rule, "once " + FormatOnce(e.Once)})
	}
	for _, e := range c.Waiting {
		rows = append(rows, PlanRow{"waiting", e.Subject, e.Kind, e.Rule, e.Reason})
	}
	for _, e := range c.Held {
		rows = append(rows, PlanRow{"held", e.Subject, "-", e.Rule, e.Reason})
	}
	for _, e := range c.Covered {
		rows = append(rows, PlanRow{"covered", e.Subject, e.Kind, e.Rule, e.Reason + " once " + FormatOnce(e.Once)})
	}
	order := map[string]int{"act": 0, "waiting": 1, "held": 2, "covered": 3}
	slices.SortStableFunc(rows, func(a, b PlanRow) int { return order[a.State] - order[b.State] })
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

// FormatOnce writes a once key as "[launch - 3]", a missing value as "-".
func FormatOnce(o ledger.Once) string {
	parts := make([]string, len(o))
	for i, v := range o {
		switch v := v.(type) {
		case nil:
			parts[i] = "-"
		case float64:
			if v == float64(int64(v)) {
				parts[i] = fmt.Sprint(int64(v))
			} else {
				parts[i] = fmt.Sprint(v)
			}
		default:
			parts[i] = fmt.Sprint(v)
		}
	}
	return "[" + strings.Join(parts, " ") + "]"
}
