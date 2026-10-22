//go:build slow_tests

package history_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/httpservice"
)

// Package exception: a 65 MiB cycle line is too slow to drive through the CLI on every run.
func TestASegmentWithAHugeResponseInOneCycleLineReadsBack(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	w, err := history.Start(dir, at, history.Header{Why: "start", At: at.UnixMilli()}, history.Snapshot{At: at.UnixMilli()})
	require.NoError(t, err)
	// Over the 64 MiB a line could once hold, since a cycle line carries every response a then waited on.
	body, err := json.Marshal(strings.Repeat("x", 65<<20))
	require.NoError(t, err)
	require.NoError(t, w.Append(history.Cycle{
		Line: history.LineCycle, Now: at.UnixMilli(),
		HTTP: []httpservice.Exchange{{Service: "sentry", Path: "/huge", Status: 200, Body: body}},
	}))
	require.NoError(t, w.Close(true))

	seg, ok, err := history.Latest(dir)

	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, seg.Clean)
	assert.Len(t, seg.Lines, 1)
}
