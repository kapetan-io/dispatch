package refund_test

import (
	"testing"

	"shop/internal/refund"
)

func TestPercentWhole(t *testing.T) {
	if got := refund.Percent(1000, 10); got != 100 {
		t.Fatalf("got %d", got)
	}
}
