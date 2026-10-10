package refund

// Percent returns pct percent of cents.
func Percent(cents, pct int) int {
	return cents * pct / 100
}
