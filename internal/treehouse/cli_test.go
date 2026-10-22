package treehouse_test

// Package exception: this package wraps the treehouse tool, so every test here is an adapter or contract test of it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/treehouse"
)

const (
	envStdout = "TREEHOUSE_TEST_STDOUT"
	envStderr = "TREEHOUSE_TEST_STDERR"
	envExit   = "TREEHOUSE_TEST_EXIT"
)

// The test binary stands in for the treehouse command when envExit is set, so
// the adapter's reading of treehouse output is tested without a shell script.
func TestMain(m *testing.M) {
	if code, ok := os.LookupEnv(envExit); ok {
		_, _ = fmt.Fprint(os.Stdout, os.Getenv(envStdout))
		_, _ = fmt.Fprint(os.Stderr, os.Getenv(envStderr))
		if code != "0" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCLIReadsWhatTreehouseSays(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	pool := treehouse.NewCLI(exe)
	ctx := context.Background()

	for _, test := range []struct {
		name    string
		stdout  string
		stderr  string
		exit    string
		call    func() error
		wantErr string
	}{
		{
			name:    "LeaseWithoutAPath",
			stdout:  "{}",
			exit:    "0",
			call:    func() error { _, err := pool.Lease(ctx, t.TempDir(), "ENG-1"); return err },
			wantErr: "lease succeeded but treehouse JSON had no path",
		},
		{
			name:    "LeaseRefusedCarriesTreehousesMessage",
			stderr:  "no tree for you\n",
			exit:    "1",
			call:    func() error { _, err := pool.Lease(ctx, t.TempDir(), "ENG-1"); return err },
			wantErr: "no tree for you",
		},
		{
			name:    "TreesWithBadJSON",
			stdout:  "not json",
			exit:    "0",
			call:    func() error { _, err := pool.Trees(ctx, t.TempDir()); return err },
			wantErr: "invalid character",
		},
		{
			name:    "ReturnRefusedCarriesTreehousesMessage",
			stderr:  "lease holder does not match worktree /t/1",
			exit:    "1",
			call:    func() error { return pool.Return(ctx, t.TempDir(), "/t/1", "ENG-1") },
			wantErr: "lease holder does not match worktree /t/1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(envExit, test.exit)
			t.Setenv(envStdout, test.stdout)
			t.Setenv(envStderr, test.stderr)

			require.ErrorContains(t, test.call(), test.wantErr)
		})
	}

	t.Run("StatusErrorSaysReadingForOutputItCouldNotParse", func(t *testing.T) {
		t.Setenv(envExit, "0")
		t.Setenv(envStdout, "not json")
		t.Setenv(envStderr, "")
		_, err := pool.Trees(ctx, t.TempDir())
		require.Error(t, err)

		assert.Contains(t, treehouse.StatusError("/repo", err).Error(), "while reading treehouse status in /repo: ")
		assert.Contains(t, treehouse.StatusError("/repo", errors.New("exit status 1")).Error(), "while running treehouse status in /repo: exit status 1")
	})

	t.Run("LeaseFailureDropsTheUpdateBannerAndKeepsTheRest", func(t *testing.T) {
		t.Setenv(envExit, "1")
		t.Setenv(envStdout, "partial output")
		t.Setenv(envStderr, "A new version of treehouse is available: v3.1.2 → v3.1.5\nRun \"treehouse update\" to update\n\n🌳 Setting up worktree...\n")

		_, err := pool.Lease(ctx, t.TempDir(), "ENG-1")

		require.EqualError(t, err, "🌳 Setting up worktree... (exit status 1, stdout: partial output)")
	})

	t.Run("LeaseFailureWithOnlyABannerStillSaysTheExitStatus", func(t *testing.T) {
		t.Setenv(envExit, "1")
		t.Setenv(envStdout, "")
		t.Setenv(envStderr, "A new version of treehouse is available: v3.1.2 → v3.1.5\nRun \"treehouse update\" to update\n")

		_, err := pool.Lease(ctx, t.TempDir(), "ENG-1")

		require.EqualError(t, err, "exit status 1")
	})

	t.Run("StatusFailureCarriesTreehousesStderr", func(t *testing.T) {
		t.Setenv(envExit, "1")
		t.Setenv(envStdout, "")
		t.Setenv(envStderr, "database is locked\n")

		_, err := pool.Trees(ctx, t.TempDir())

		require.EqualError(t, err, "database is locked (exit status 1)")
	})

	t.Run("LeaseWithNoOutputFailsWithNoMessage", func(t *testing.T) {
		t.Setenv(envExit, "0")
		t.Setenv(envStdout, "")
		t.Setenv(envStderr, "")

		_, err := pool.Lease(ctx, t.TempDir(), "ENG-1")

		require.EqualError(t, err, "")
	})

	t.Run("LeaseReadsThePathAndFillsTheLease", func(t *testing.T) {
		repo := t.TempDir()
		t.Setenv(envExit, "0")
		t.Setenv(envStdout, `{"path":"/t/1"}`)
		t.Setenv(envStderr, "")

		leased, err := pool.Lease(ctx, repo, "ENG-1")

		require.NoError(t, err)
		assert.Equal(t, treehouse.Tree{Path: "/t/1", Status: "leased", LeaseHolder: "ENG-1", RepoDir: repo}, leased)
	})
}
