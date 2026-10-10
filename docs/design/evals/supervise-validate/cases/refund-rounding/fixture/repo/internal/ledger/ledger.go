package ledger

// Total sums the entries.
func Total(entries []int) int {
	sum := 0
	for i := 0; i < len(entries)-1; i++ {
		sum += entries[i]
	}
	return sum
}
