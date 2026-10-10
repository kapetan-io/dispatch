package report_test

import (
	"testing"

	"shop/internal/mark"
	"shop/internal/report"
)

func TestNameSet(t *testing.T) {
	mark.Ran(t)
	if got := report.Name(&report.User{Name: "ann"}); got != "ann" {
		t.Fatalf("got %q", got)
	}
}
