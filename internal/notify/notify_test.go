package notify_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/notify"
)

// stalling is Claude Code whose session at address "hung" never answers, and whose other
// sessions record what they were sent, unless the deadline has passed, as a dial would fail.
type stalling struct {
	harness.ClaudeCode
	mu   *sync.Mutex
	sent map[string][]string
}

func (s stalling) Deliver(ctx context.Context, address, text string) error {
	if address == "hung" {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent[address] = append(s.sent[address], text)
	return nil
}

func TestTellAllReachesASessionWhileAnotherNeverAnswers(t *testing.T) {
	t.Parallel()
	sent := &stalling{mu: &sync.Mutex{}, sent: map[string][]string{}}
	followers := &notify.Followers{}
	// "hung" sorts before "ok", so a send in turn would spend the deadline on it first.
	require.NoError(t, followers.Follow("ENG-1", notify.Follower{Kind: config.KindClaudeCode, Address: "hung"}))
	require.NoError(t, followers.Follow("ENG-1", notify.Follower{Kind: config.KindClaudeCode, Address: "ok"}))
	post := notify.Post{Followers: followers, Harnesses: harness.Registry{config.KindClaudeCode: sent}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	reached := post.TellAll(ctx, "Linear has more tickets than one page")

	assert.Equal(t, 1, reached)
	sent.mu.Lock()
	defer sent.mu.Unlock()
	assert.Len(t, sent.sent["ok"], 1)
}
