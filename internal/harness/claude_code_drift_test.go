//go:build drift

package harness_test

// Package exception: drift test that checks the flags dispatch passes against the installed claude.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/harness"
)

// TestClaudeCodeFlagsAreFlagsTheInstalledClaudeListsInItsHelp fails when claude is missing.
// Run with: go test -tags drift ./internal/harness/
func TestClaudeCodeFlagsAreFlagsTheInstalledClaudeListsInItsHelp(t *testing.T) {
	var claude string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		// The shim only re-adds flags, and it asks dispatch for the real claude.
		if strings.HasSuffix(filepath.ToSlash(dir), "scripts/shims") {
			continue
		}
		if path, err := exec.LookPath(filepath.Join(dir, "claude")); err == nil {
			claude = path
			break
		}
	}
	require.NotEmpty(t, claude)
	help, err := exec.Command(claude, "--help").Output()
	require.NoError(t, err)

	for _, flag := range harness.ClaudeCodeFlags("/plugin") {
		assert.Contains(t, string(help), flag.Names[0])
	}
	assert.Contains(t, string(help), "--model")
}
