package ledger_test

import (
	"testing"

	"shop/internal/ledger"
)

func TestTotalEmpty(t *testing.T) {
	if got := ledger.Total(nil); got != 0 {
		t.Fatalf("got %d", got)
	}
}
