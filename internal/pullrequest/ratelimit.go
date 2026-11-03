package pullrequest

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// RateLimitError is GitHub refusing a read because the account's request budget is spent.
// Reset is when GitHub says the budget refills, and is zero when GitHub gives no time, as with
// a secondary (per-minute) limit.
type RateLimitError struct {
	Reset time.Time
	Err   error
}

func (e *RateLimitError) Error() string { return e.Err.Error() }
func (e *RateLimitError) Unwrap() error { return e.Err }

// ResetText prints a reset time in UTC, so a message reads the same in every timezone.
func ResetText(reset time.Time) string {
	return reset.UTC().Format(time.RFC3339)
}

// rateLimited is true when gh says GitHub refused for a spent budget. gh prints "API rate limit
// already exceeded" on stderr and a secondary limit says "secondary rate limit"; the GraphQL
// body carries "type":"RATE_LIMIT". Stdout is searched only for that exact form, because it
// holds PR titles and comments when the refusal is partial.
func rateLimited(stdout, stderr string) bool {
	return strings.Contains(stdout, `"type":"RATE_LIMIT"`) || strings.Contains(strings.ToLower(stderr), "rate limit")
}

// resetOf asks GitHub when the GraphQL budget refills. The rate_limit endpoint does not count
// against the budget. It returns the zero time when GitHub gives none, as for a secondary limit
// or when the question itself fails.
func (c *CLI) resetOf(ctx context.Context, dir, stderr string) time.Time {
	if strings.Contains(strings.ToLower(stderr), "secondary") {
		return time.Time{}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := c.env.Command(ctx, c.bin, "api", "rate_limit", "--jq", ".resources.graphql.reset")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	b, err := cmd.Output()
	if err != nil {
		return time.Time{}
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0).UTC()
}
