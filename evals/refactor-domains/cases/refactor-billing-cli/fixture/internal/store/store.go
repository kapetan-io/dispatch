package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"billing/internal/util"
)

// Invoice has no json tags, so its Go field names are the saved names.
type Invoice struct {
	Number     int
	CustomerID string
	Cents      int
}

type Payment struct {
	CustomerID string
	Cents      int
}

// Manager stores invoices and payments, numbers invoices and renders statements.
type Manager struct{ dir string }

func NewManager(dir string) *Manager { return &Manager{dir: dir} }

func (m *Manager) IssueInvoice(customer string, cents int) (int, error) {
	num, err := util.NextNumber(filepath.Join(m.dir, "invoice-counter"))
	if err != nil {
		return 0, err
	}
	inv := Invoice{Number: num, CustomerID: customer, Cents: cents}
	return num, util.SaveJSON(filepath.Join(m.dir, "invoices", fmt.Sprintf("%d.json", num)), inv)
}

func (m *Manager) RecordPayment(customer string, cents int) error {
	if m.Balance(customer) < cents {
		return fmt.Errorf("payment exceeds balance owed")
	}
	n := len(m.payments(customer)) + 1
	return util.SaveJSON(filepath.Join(m.dir, "payments", fmt.Sprintf("%s-%d.json", customer, n)), Payment{customer, cents})
}

func (m *Manager) invoices(customer string) []Invoice {
	var out []Invoice
	files, _ := filepath.Glob(filepath.Join(m.dir, "invoices", "*.json"))
	for _, f := range files {
		var inv Invoice
		if util.LoadJSON(f, &inv) == nil && inv.CustomerID == customer {
			out = append(out, inv)
		}
	}
	return out
}

func (m *Manager) payments(customer string) []Payment {
	var out []Payment
	files, _ := filepath.Glob(filepath.Join(m.dir, "payments", customer+"-*.json"))
	for _, f := range files {
		var p Payment
		if util.LoadJSON(f, &p) == nil {
			out = append(out, p)
		}
	}
	return out
}

func (m *Manager) Balance(customer string) int {
	total := 0
	for _, inv := range m.invoices(customer) {
		total += inv.Cents
	}
	for _, p := range m.payments(customer) {
		total -= p.Cents
	}
	return total
}

func (m *Manager) RenderStatement(customer string) (string, error) {
	if _, err := os.Stat(filepath.Join(m.dir, "invoices")); err != nil {
		return "", fmt.Errorf("no invoices for %s", customer)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Statement for %s\n", customer)
	for _, inv := range m.invoices(customer) {
		fmt.Fprintf(&b, "invoice %d  %s\n", inv.Number, util.FormatMoney(inv.Cents))
	}
	for _, p := range m.payments(customer) {
		fmt.Fprintf(&b, "payment  -%s\n", util.FormatMoney(p.Cents))
	}
	fmt.Fprintf(&b, "balance  %s\n", util.FormatMoney(m.Balance(customer)))
	return b.String(), nil
}
