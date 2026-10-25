package billing_test

import (
	"strings"
	"testing"
)

func TestIssueThenStatementShowsBalance(t *testing.T) {
	dir := t.TempDir()
	out, _, code := run(t, dir, "issue", "acme", "2500")
	if code != 0 || !strings.Contains(out, "issued invoice 1 for $25.00") {
		t.Fatalf("got %d %q", code, out)
	}
	out, _, _ = run(t, dir, "statement", "acme")
	if !strings.Contains(out, "balance  $25.00") {
		t.Fatalf("got %q", out)
	}
}

func TestPaymentAboveBalanceIsRefused(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "issue", "acme", "1000")
	_, errOut, code := run(t, dir, "pay", "acme", "5000")
	if code != 1 || !strings.Contains(errOut, "payment exceeds balance owed") {
		t.Fatalf("got %d %q", code, errOut)
	}
}
