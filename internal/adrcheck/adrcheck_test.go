package adrcheck_test

// No exception needed: these tests run adrcheck's own command line through Run, which is that tool's surface.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapetan-io/dispatch/internal/adrcheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const clean = `# 1. Fake external tools at their domain interfaces

Date: 2026-10-12

## Status

Accepted

## Context

- Dispatch drives herdr and treehouse as external programs, and the rules live in a rules directory.
- SHA-256, UTF-8 and ISO-8601 name standards, not tickets. See [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110).
- ADR 0002 and ADR-0002 both name another ADR, and so does [the next one](0002-next.md).

## Decision

We will fake each tool behind ` + "`herdr.Multiplexer`" + ` and set ` + "`[plugins.herdr] host`" + `.

## Consequences

- Each tool's wire format exists once.
`

const next = `# 2. Next

Date: 2026-10-13

## Status

Amends ADR 0001
Superseded in part by ADR-0003

## Decision

We will do the next thing.
`

// repo writes a repo with a docs/adr directory holding files, plus the other repo
// files an ADR might cite.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"internal/rules/state.go": "package rules\n",
		"docs/rules.md":           "# Rules\n",
		"rules/shared.ts":         "export {}\n",
		"go.mod":                  "module x\n",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs/adr"), 0o755))
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, "docs/adr", name), []byte(body), 0o644))
	}
	return root
}

func run(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := adrcheck.Run(t.Context(), append([]string{"-root", root}, args...), adrcheck.Options{Stdout: &stdout, Stderr: &stderr})
	return code, stdout.String(), stderr.String()
}

func TestCleanADRsPass(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": clean, "0002-next.md": next, "README.md": "Cites ENG-1 but is not an ADR.\n"})
	code, stdout, stderr := run(t, root, "-ticket-keys", "ENG,LOC", "-repo", "kapetan-io/dispatch")
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestEachProblemNamesTheADRTheLineAndTheText(t *testing.T) {
	const file = "docs/adr/0001-fake.md"
	for _, test := range []struct {
		name string
		line string
		want string
	}{
		{"linear ticket", "Proposed first in ENG-630.", `:11: cites a ticket: "ENG-630"`},
		{"second team key", "A local ticket, LOC-7, asked.", `:11: cites a ticket: "LOC-7"`},
		{"linear url", "See https://linear.app/x/issue/ENG-153/title.", `:11: cites a ticket: "ENG-153"`},
		{"existing file", "The hold lives in `internal/rules/state.go`.", `:11: references a file or directory in the repo: "internal/rules/state.go"`},
		{"existing directory", "Fakes live in internal/rules/ today.", `:11: references a file or directory in the repo: "internal/rules/"`},
		{"existing root file", "Pinned in go.mod.", `:11: references a file or directory in the repo: "go.mod"`},
		{"missing source file", "Moved to (`internal/email/retry.go`).", `:11: references a file or directory in the repo: "internal/email/retry.go"`},
		{"relative link to a file", "Read [the rules](../rules.md).", `:11: references a file or directory in the repo: "../rules.md"`},
		{"pull request url", "Landed in https://github.com/kapetan-io/dispatch/pull/223.", `:11: links to the repo's pull requests, commits, branches or issues: "https://github.com/kapetan-io/dispatch/pull/223"`},
		{"branch link", "See [the branch](https://github.com/kapetan-io/dispatch/tree/thrawn01/x).", `:11: links to the repo's pull requests, commits, branches or issues: "https://github.com/kapetan-io/dispatch/tree/thrawn01/x"`},
		{"pull request number", "ENG-491's merge retry (#154) stays.", `:11: cites a pull request or issue: "#154"`},
		{"commit", "It now retries at the resync (`e01aa31`).", `:11: cites a commit: "e01aa31"`},
		{"missing ADR link", "Builds on [ADR 9](0009-gone.md).", `:11: links to an ADR file that doesn't exist: "0009-gone.md"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := "# 1. Fake\n\nDate: 2026-10-12\n\n## Status\n\nAccepted\n\n## Context\n\n" + test.line + "\n"
			root := repo(t, map[string]string{"0001-fake.md": body})
			code, stdout, stderr := run(t, root, "-ticket-keys", "ENG,LOC", "-repo", "kapetan-io/dispatch")
			assert.Equal(t, 1, code)
			assert.Contains(t, stdout, file+test.want)
			assert.Contains(t, stderr, "problems")
		})
	}
}

func TestStatusMustBeAnAllowedForm(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		want   string
	}{
		{"amended note", "Accepted, amended", `:7: has a Status outside the allowed forms: "Accepted, amended"`},
		{"prose amendment", "Accepted. Amends ADR 0006, which says dispatch keeps no id record.", `:7: has a Status outside the allowed forms: "Accepted. Amends ADR 0006, which says dispatch keeps no id record."`},
		{"dated", "Accepted 2026-11-04", `:7: has a Status outside the allowed forms: "Accepted 2026-11-04"`},
		{"superseded without number", "Superseded", `:7: has a Status outside the allowed forms: "Superseded"`},
		{"empty", "", `: has an empty Status`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := repo(t, map[string]string{"0001-fake.md": strings.Replace(clean, "\nAccepted\n", "\n"+test.status+"\n", 1), "0002-next.md": next})
			code, stdout, _ := run(t, root)
			assert.Equal(t, 1, code)
			assert.Contains(t, stdout, "docs/adr/0001-fake.md")
			assert.Contains(t, stdout, test.want)
		})
	}
}

func TestEveryAllowedStatusPasses(t *testing.T) {
	for _, status := range []string{
		"Proposed", "Accepted", "Rejected", "Deprecated",
		"Superseded by ADR 0007", "Superseded by ADR-0007",
		"Superseded in part by ADR 12", "Amends ADR 0006", "Amended by ADR-0019",
	} {
		t.Run(status, func(t *testing.T) {
			root := repo(t, map[string]string{"0001-fake.md": strings.Replace(clean, "\nAccepted\n", "\n"+status+"\n", 1), "0002-next.md": next})
			code, stdout, _ := run(t, root)
			assert.Equal(t, 0, code)
			assert.Empty(t, stdout)
		})
	}
}

func TestMissingStatusSectionFails(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": strings.Replace(clean, "## Status\n\nAccepted\n", "", 1), "0002-next.md": next})
	code, stdout, _ := run(t, root)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, `docs/adr/0001-fake.md:1: has no Status section: "# 1. Fake external tools at their domain interfaces"`)
}

func TestSharedNumberFailsBothADRs(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": clean, "0002-next.md": next, "0002-other.md": next})
	code, stdout, _ := run(t, root)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, `docs/adr/0002-next.md:1: shares its number with another ADR: "0002-other.md"`)
	assert.Contains(t, stdout, `docs/adr/0002-other.md:1: shares its number with another ADR: "0002-next.md"`)
	assert.NotContains(t, stdout, "0001-fake.md")
}

func TestTicketsAreCheckedOnlyForGivenKeys(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": strings.Replace(clean, "Each tool's wire format", "ENG-1 and ABC-2 say each tool's wire format", 1), "0002-next.md": next})

	code, stdout, _ := run(t, root, "-ticket-keys", "ABC")
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, `cites a ticket: "ABC-2"`)
	assert.NotContains(t, stdout, "ENG-1")

	code, stdout, _ = run(t, root)
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
}

func TestOtherReposLinksStayAllowed(t *testing.T) {
	root := repo(t, map[string]string{"0001-fake.md": strings.Replace(clean, "Each tool's wire format", "Per https://github.com/golang/go/issues/1 each tool's wire format", 1), "0002-next.md": next})
	code, stdout, _ := run(t, root, "-repo", "kapetan-io/dispatch")
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
}

func TestBadArgumentsExitTwo(t *testing.T) {
	root := repo(t, nil)
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"lower-case key", []string{"-ticket-keys", "eng"}, `ticket key "eng"`},
		{"repo without owner", []string{"-repo", "dispatch"}, `repo "dispatch" is not owner/name`},
		{"stray argument", []string{"docs"}, `unexpected argument "docs"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, _, stderr := run(t, root, test.args...)
			assert.Equal(t, 2, code)
			assert.Contains(t, stderr, test.want)
		})
	}
}

func TestRepoWithoutADRsPasses(t *testing.T) {
	code, stdout, _ := run(t, t.TempDir(), "-ticket-keys", "ENG")
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
}
