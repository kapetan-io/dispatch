package delivery_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/delivery"
	"github.com/kapetan-io/dispatch/internal/worker"
)

func script(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755))
	return path
}

func TestNudgeTypesIntoThePane(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	post := delivery.Post{Herdr: script(t, "herdr", `echo "$@" >> `+log)}

	err := post.Nudge(context.Background(), worker.Worker{State: worker.Idle, PaneID: "p1"}, "keep going")

	require.NoError(t, err)
	got, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, "agent prompt p1 keep going\n", string(got))
}

func TestNudgeRefusesABlockedWorkerBeforeAnyTransport(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	post := delivery.Post{Herdr: script(t, "herdr", `echo "$@" >> `+log)}

	err := post.Nudge(context.Background(), worker.Worker{State: worker.Blocked, PaneID: "p1"}, "keep going")

	require.ErrorIs(t, err, delivery.ErrBlocked)
	assert.NoFileExists(t, log)
}

func TestNudgeReportsAFailedTransport(t *testing.T) {
	post := delivery.Post{Herdr: script(t, "herdr", `echo no pane >&2; exit 1`)}

	err := post.Nudge(context.Background(), worker.Worker{State: worker.Idle, PaneID: "p1"}, "hi")

	require.ErrorContains(t, err, "no pane")
	assert.NotErrorIs(t, err, delivery.ErrBlocked)
}

func TestNotifyShowsTheAlert(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	post := delivery.Post{Osascript: script(t, "osascript", `for a; do last2="$last"; last="$a"; done; echo "$last2|$last" >> `+log)}

	err := post.Notify(context.Background(), delivery.Event{Text: `ENG-1 needs "you"`})

	require.NoError(t, err)
	got, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, "ENG-1 needs \"you\"|dispatch\n", string(got))
}

func TestNotifyReturnsTheAlertsError(t *testing.T) {
	post := delivery.Post{Osascript: script(t, "osascript", `echo denied >&2; exit 1`)}

	err := post.Notify(context.Background(), delivery.Event{Text: "x"})

	require.ErrorContains(t, err, "while notifying")
	assert.ErrorContains(t, err, "denied")
}
