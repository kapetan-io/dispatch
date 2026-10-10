package ledger_test

import (
	"testing"

	"shop/internal/ledger"
	"shop/internal/mark"
)

func TestTotalEmpty(t *testing.T) {
	mark.Ran(t)
	if got := ledger.Total(nil); got != 0 {
		t.Fatalf("got %d", got)
	}
}
